// praimate-cli is a terminal frontend for the PrAImate core. It deliberately
// has no separate tool executor, permission engine, MCP loader or session store.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sPROFFEs/PrAImate/internal/core"
	"github.com/sPROFFEs/PrAImate/internal/store"
	"github.com/sPROFFEs/PrAImate/internal/version"
	"golang.org/x/term"
)

type options struct {
	chat, agent, model, endpoint, tools, format, system, cwd, workflow, inputs, mcp string
	contextTokens, outputTokens                                                     int
	resume, passwordStdin, version, repoMap                                         bool
	attachments                                                                     attachmentFlags
	showReasoning                                                                   bool
}

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(safeTerminalWriter{os.Stderr}, "praimate-cli:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) > 0 && args[0] == "run" {
		args = args[1:]
	}
	var o options
	fs := flag.NewFlagSet("praimate-cli", flag.ContinueOnError)
	fs.StringVar(&o.chat, "chat", "", "Resume a PrAImate chat ID (shared with Desktop/Studio)")
	fs.StringVar(&o.chat, "session", "", "Alias for --chat; uses a core chat ID")
	fs.StringVar(&o.agent, "agent", "", "PrAImate agent ID; includes capabilities, knowledge, and skills")
	fs.StringVar(&o.model, "model", "", "Endpoint model ID (or PRAIMATE_MODEL/OPENAI_MODEL)")
	fs.StringVar(&o.endpoint, "endpoint", "", "OpenAI-compatible base URL; defaults to the core local route")
	fs.StringVar(&o.tools, "tools", "safe", "safe | ask | edits | full (safe by default)")
	fs.StringVar(&o.mcp, "mcp", "", "Comma-separated registered core MCP IDs; no workspace discovery")
	fs.StringVar(&o.format, "format", "text", "text | json (JSONL core stream events)")
	fs.StringVar(&o.cwd, "cwd", "", "Workspace (defaults to current directory)")
	fs.StringVar(&o.system, "system-prompt", "", "Additional system instructions for a new chat")
	fs.StringVar(&o.workflow, "workflow", "", "Execute a named workflow of --agent through the core")
	fs.StringVar(&o.inputs, "inputs", "{}", "Workflow inputs as a JSON object")
	fs.Var(&o.attachments, "attach", "Attach a file to the next chat message (repeatable; images need a vision model)")
	fs.BoolVar(&o.showReasoning, "show-reasoning", false, "Display streamed model reasoning in text mode")
	fs.IntVar(&o.contextTokens, "context-tokens", 0, "Context window: 0 adapts automatically using backend metadata and host hints")
	fs.IntVar(&o.outputTokens, "output-tokens", 0, "Output limit: 0 adapts automatically to the backend (includes reasoning)")
	fs.BoolVar(&o.resume, "continue", false, "Resume the latest native chat in this workspace")
	fs.BoolVar(&o.passwordStdin, "db-password-stdin", false, "Read database password from the first stdin line")
	fs.BoolVar(&o.repoMap, "repomap", false, "Include ambient repository map in chat context")
	fs.BoolVar(&o.version, "version", false, "Print version")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "PrAImate native CLI — shared core runtime\nUsage: praimate-cli [run] [flags] [prompt]\nKeys come from the core vault or OPENAI_API_KEY.\nInteractive: /help, /status, /context, /attach PATH, /model [ID], /tools [LEVEL], /sessions, /compact, /exit\nTab completes commands and arguments; /model, /tools and /sessions offer selectors.")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if o.version {
		fmt.Println("PrAImate CLI", version.Current)
		return nil
	}
	if o.format != "text" && o.format != "json" {
		return errors.New("--format must be text or json")
	}
	level, err := toolLevel(o.tools)
	if err != nil {
		return err
	}
	if o.chat != "" && o.resume {
		return errors.New("choose --chat or --continue, not both")
	}
	if o.workflow != "" && (o.chat != "" || o.resume || o.agent == "") {
		return errors.New("--workflow requires --agent and a new run")
	}
	if o.workflow != "" && len(o.attachments) > 0 {
		return errors.New("--attach is for chat messages; use workflow inputs for a workflow run")
	}
	if o.cwd == "" {
		o.cwd, err = os.Getwd()
		if err != nil {
			return err
		}
	}
	o.cwd, err = filepath.Abs(o.cwd)
	if err != nil {
		return err
	}
	o.cwd, err = filepath.EvalSymlinks(o.cwd)
	if err != nil {
		return err
	}
	input := bufio.NewReader(os.Stdin)
	interactive := term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stderr.Fd())) && o.format == "text"
	if !interactive {
		var stop context.CancelFunc
		ctx, stop = signal.NotifyContext(ctx, os.Interrupt)
		defer stop()
	}
	output, statusOutput := io.Writer(os.Stdout), io.Writer(os.Stderr)
	if o.format == "text" {
		output, statusOutput = safeTerminalWriter{os.Stdout}, safeTerminalWriter{os.Stderr}
	}
	c, closeCore, err := openCore(input, o.passwordStdin, interactive)
	if err != nil {
		return err
	}
	defer closeCore()
	var editor *terminalEditor
	if interactive {
		restoreConsole, err := enableConsole()
		if err != nil {
			return fmt.Errorf("enable terminal editing: %w", err)
		}
		defer restoreConsole()
		editor = &terminalEditor{input: newTerminalInput(input)}
		defer close(editor.input.done)
	}
	core.RegisterAllCLIAdapters()
	if _, err := c.SeedBuiltins(ctx); err != nil {
		return err
	}
	ctx = core.WithNativeAPIKey(ctx, o.endpoint, os.Getenv("OPENAI_API_KEY"))
	var cancelMu sync.Mutex
	var cancelTurn context.CancelFunc
	setTurnCancel := func(cancel context.CancelFunc) { cancelMu.Lock(); cancelTurn = cancel; cancelMu.Unlock() }
	c.SetApprovalProvider(func(string) *core.ApprovalConfig {
		return &core.ApprovalConfig{Request: func(ctx context.Context, name string, details map[string]any) (bool, error) {
			if !interactive {
				return false, nil
			}
			if err := ctx.Err(); err != nil {
				return false, err
			}
			raw, _ := json.Marshal(details)
			fmt.Fprintf(statusOutput, "\nApproval required: %s\n%s\n", name, raw)
			line, pasted, err := editor.read(ctx, "Allow? Type y to approve [y/N] > ", true)
			if err != nil {
				if errors.Is(err, errInputCanceled) || errors.Is(err, io.EOF) {
					cancelMu.Lock()
					if cancelTurn != nil {
						cancelTurn()
					}
					cancelMu.Unlock()
					return false, context.Canceled
				}
				return false, err
			}
			if pasted {
				fmt.Fprintln(statusOutput, "Pasted approvals are not accepted; denied.")
				return false, nil
			}
			return strings.EqualFold(strings.TrimSpace(line), "y"), ctx.Err()
		}}
	})
	visited := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { visited[f.Name] = true })
	var chat *core.Chat
	if o.resume {
		chats, e := c.ListChats(ctx, 1000)
		if e != nil {
			return e
		}
		for _, candidate := range chats {
			root, _ := filepath.EvalSymlinks(candidate.WorkspacePath)
			if candidate.CLIAgent == "praimate-cli" && root == o.cwd {
				o.chat = candidate.ID
				break
			}
		}
		if o.chat == "" {
			return errors.New("no native chat found in this workspace")
		}
	}
	if o.chat != "" {
		chat, err = c.GetChat(ctx, o.chat)
		if err != nil {
			return err
		}
		if chat.CLIAgent != "praimate-cli" {
			return errors.New("selected chat does not use praimate-cli")
		}
		if o.agent != "" && o.agent != chat.AgentID {
			return errors.New("--agent cannot change a resumed chat")
		}
		if visited["cwd"] {
			root, _ := filepath.EvalSymlinks(chat.WorkspacePath)
			if root != o.cwd {
				return errors.New("--cwd differs from resumed chat workspace")
			}
		}
		o.cwd = chat.WorkspacePath
		if o.system != "" {
			return errors.New("--system-prompt requires a new chat")
		}
	} else {
		if o.agent != "" {
			chat, err = c.StartInteractiveChat(ctx, o.agent, "praimate-cli", o.cwd)
		} else {
			chat, err = c.StartCleanChat(ctx, "praimate-cli", o.model, o.cwd)
		}
		if err != nil {
			return err
		}
	}
	if err := c.UpdateChatSettings(ctx, chat.ID, func(s *core.ChatSettings) {
		if o.chat == "" || visited["tools"] {
			s.Tools = level
			s.ToolsConfigured = true
		}
		if visited["model"] {
			s.Model = o.model
			if s.Local != nil {
				s.Local.Model = o.model
			}
		}
		if visited["endpoint"] || visited["context-tokens"] || visited["output-tokens"] {
			if s.Local == nil {
				s.Local = &core.ChatLocalEndpoint{Model: o.model}
			}
			if visited["endpoint"] {
				s.Local.Endpoint = o.endpoint
			}
			if visited["context-tokens"] {
				s.Local.ContextTokens = o.contextTokens
			}
			if visited["output-tokens"] {
				s.Local.OutputTokens = o.outputTokens
			}
		}
		if visited["repomap"] {
			s.RepoMap = o.repoMap
		}
		if visited["mcp"] {
			s.MCPServers = nil
			for _, id := range strings.Split(o.mcp, ",") {
				if id = strings.TrimSpace(id); id != "" {
					s.MCPServers = append(s.MCPServers, id)
				}
			}
			s.MCPConfigured = true
		}
	}); err != nil {
		return err
	}
	chat, err = c.GetChat(ctx, chat.ID)
	if err != nil {
		return err
	}
	if o.system != "" {
		if _, err := c.AddMessage(ctx, chat.ID, "system", o.system, nil); err != nil {
			return err
		}
	}
	system, customSystem, agent, err := chatSystemPrompt(ctx, c, chat)
	if err != nil {
		return err
	}
	var pending []string
	for _, path := range o.attachments {
		pending, err = queueAttachment(pending, chat.WorkspacePath, path)
		if err != nil {
			return err
		}
	}
	var eventMu sync.Mutex
	width := 80
	if interactive {
		if columns, _, sizeErr := term.GetSize(int(os.Stderr.Fd())); sizeErr == nil {
			width = columns
		}
	}
	colored := interactive && term.IsTerminal(int(os.Stdout.Fd())) && os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb"
	view := sessionView{out: os.Stderr, color: colored, width: width}
	renderer := newTextRenderer(os.Stdout, colored, width)
	var renderedText, thinking, reasoningOpen bool
	var toolCount int
	var lastUsage *core.NativeUsage
	emit := func(event core.StreamEvent) {
		eventMu.Lock()
		defer eventMu.Unlock()
		if o.format == "json" {
			if event.Type != "usage" && event.Type != "context" {
				event.Raw = nil
			}
			_ = json.NewEncoder(os.Stdout).Encode(event)
			return
		}
		if interactive && event.Type != "text" {
			renderer.Flush()
		}
		if reasoningOpen && event.Type != "reasoning" {
			fmt.Fprintln(statusOutput)
			reasoningOpen = false
		}
		switch event.Type {
		case "text":
			if interactive && !renderedText {
				fmt.Fprintln(statusOutput, "\n  ASSISTANT\n  ─────────")
				renderedText = true
			}
			if interactive {
				renderer.WriteChunk(event.Text)
			} else {
				fmt.Fprint(output, event.Text)
			}
		case "reasoning":
			text := event.Text
			if text == "" {
				text = event.Detail
			}
			if text == "" {
				return
			}
			if o.showReasoning {
				if !reasoningOpen {
					fmt.Fprint(statusOutput, "\n  THINKING  ")
					reasoningOpen = true
				}
				fmt.Fprint(statusOutput, text)
			} else if interactive && !thinking {
				fmt.Fprintln(statusOutput, "  … thinking")
				thinking = true
			}
		case "tool_start":
			toolCount++
			fmt.Fprintf(statusOutput, "\n  TOOL  %s  %s\n", event.Tool, event.Detail)
		case "tool_end":
			state := "✓ done"
			if !event.OK {
				state = "✗ failed"
			}
			fmt.Fprintf(statusOutput, "  %s  %s\n", state, event.Tool)
		case "context_compacted", "context_recovery":
			fmt.Fprintf(statusOutput, "  CONTEXT  %s\n", event.Detail)
		case "usage":
			if interactive {
				raw, _ := json.Marshal(event.Raw)
				var status core.NativeContextStatus
				if json.Unmarshal(raw, &status) == nil {
					lastUsage = status.LastUsage
				}
			} else {
				fmt.Fprintf(statusOutput, "  USAGE  %s\n", event.Detail)
			}
		}
	}
	if o.workflow != "" {
		workflowCtx, stop := signal.NotifyContext(ctx, os.Interrupt)
		defer stop()
		setTurnCancel(stop)
		defer setTurnCancel(nil)
		defer c.DeleteChat(context.WithoutCancel(ctx), chat.ID)
		if agent.FindWorkflow(o.workflow) == nil {
			return fmt.Errorf("unknown workflow %q", o.workflow)
		}
		var inputs map[string]string
		if err := json.Unmarshal([]byte(o.inputs), &inputs); err != nil {
			return fmt.Errorf("workflow inputs: %w", err)
		}
		result := c.RunWorkflow(workflowCtx, core.RunOptions{Agent: agent, WorkflowName: o.workflow, Inputs: inputs, CLI: "praimate-cli", Cwd: o.cwd, Model: chat.Settings.Model, Tools: chat.Settings.Tools, ChatSettings: chat.Settings, Persist: true, SystemContext: o.system, OnEvent: func(e core.WorkflowRunEvent) {
			emit(core.StreamEvent{Type: e.Type, Text: e.Text, Tool: e.Tool, Detail: e.Detail, ID: e.ID, OK: e.OK})
		}})
		if interactive {
			renderer.Flush()
		}
		if o.format == "json" {
			_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"type": "workflow_result", "chatID": result.ChatID, "outcome": result.Outcome})
		} else {
			fmt.Fprintln(os.Stderr, "Workflow chat:", result.ChatID)
		}
		if result.Err != nil {
			return result.Err
		}
		return nil
	}
	if o.format == "json" {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]string{"type": "session", "chatID": chat.ID})
	} else {
		if interactive {
			view.banner(version.Current, chat.WorkspacePath, chat.ID)
			if err := view.restoreHistory(ctx, c, chat.ID); err != nil {
				return err
			}
		} else {
			fmt.Fprintf(statusOutput, "PrAImate chat: %s\n", chat.ID)
		}
	}
	prompt := strings.Join(fs.Args(), " ")
	if prompt == "" && !interactive {
		raw, e := io.ReadAll(io.LimitReader(input, (1<<20)+1))
		if e != nil {
			return e
		}
		if len(raw) > 1<<20 {
			return errors.New("stdin prompt exceeds 1 MiB")
		}
		prompt = strings.TrimSpace(string(raw))
	}
	send := func(message string) error {
		turnCtx, stop := signal.NotifyContext(ctx, os.Interrupt)
		defer stop()
		setTurnCancel(stop)
		defer setTurnCancel(nil)
		files := pending
		if message == "/compact" {
			files = nil
		} else {
			pending = nil
		}
		eventMu.Lock()
		renderedText, thinking, reasoningOpen, toolCount = false, false, false, 0
		lastUsage = nil
		renderer = newTextRenderer(os.Stdout, colored, width)
		eventMu.Unlock()
		start := time.Now()
		if interactive {
			fmt.Fprintf(statusOutput, "\n  WORKING  %d attachment(s) · Ctrl+C to cancel\n", len(files))
		}
		_, e := c.ContinueChatStream(turnCtx, chat.ID, message, chat.WorkspacePath, system, files, emit)
		if e == nil {
			e = turnCtx.Err()
		}
		if o.format == "text" {
			if interactive {
				eventMu.Lock()
				renderer.Flush()
				if reasoningOpen {
					fmt.Fprintln(statusOutput)
					reasoningOpen = false
				}
				eventMu.Unlock()
			}
			fmt.Fprintln(output)
			if interactive {
				eventMu.Lock()
				view.finish(time.Since(start), toolCount, lastUsage, e)
				eventMu.Unlock()
			}
		}
		return e
	}
	if prompt != "" || (!interactive && len(pending) > 0) {
		return send(prompt)
	}
	if !interactive {
		return errors.New("a prompt is required in non-interactive mode")
	}
	var cachedModels []string
	var modelsChatID, modelsEndpoint string
	modelsLoaded := false
	modelsForChat := func() ([]string, error) {
		endpoint := ""
		if chat.Settings.Local != nil {
			endpoint = chat.Settings.Local.Endpoint
		}
		if modelsLoaded && modelsChatID == chat.ID && modelsEndpoint == endpoint {
			return cachedModels, nil
		}
		models, err := nativeModelChoices(ctx, c, endpoint)
		if err != nil {
			return nil, err
		}
		cachedModels, modelsChatID, modelsEndpoint, modelsLoaded = models, chat.ID, endpoint, true
		return models, nil
	}
	editor.complete = func(line string, pos int, key rune) (string, int, bool) {
		var models []string
		if key == '\t' && strings.HasPrefix(line, "/model ") {
			models, _ = modelsForChat()
		}
		return completeInteractive(line, pos, key, models, pending, chat.WorkspacePath)
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if current, err := c.GetChat(ctx, chat.ID); err == nil {
			status, _ := c.NativeContext(ctx, chat.ID)
			model, level := current.Settings.Model, current.Settings.Tools
			if status != nil {
				model = status.Model
			}
			if !current.Settings.ToolsConfigured && current.AgentID != "" {
				level = "agent permissions"
			}
			view.session(model, level, status, len(pending), len(chat.Settings.PinnedFiles))
		}
		label := "praimate> "
		if len(pending) > 0 {
			label = fmt.Sprintf("praimate [%d attached]> ", len(pending))
		}
		line, pasted, e := editor.read(ctx, label, false)
		if e != nil {
			if errors.Is(e, errInputCanceled) {
				fmt.Fprintln(statusOutput, "Input cleared.")
				continue
			}
			if errors.Is(e, io.EOF) {
				return nil
			}
			return e
		}
		line = strings.TrimSpace(line)
		if line == "" && len(pending) == 0 {
			continue
		}
		if !pasted && strings.HasPrefix(line, "!") {
			cmdStr := strings.TrimSpace(strings.TrimPrefix(line, "!"))
			if cmdStr == "" {
				fmt.Fprintln(statusOutput, "Usage: ! <command>")
				continue
			}
			turn, cmdErr := c.RunChatCommand(ctx, chat.ID, cmdStr)
			if cmdErr != nil {
				fmt.Fprintf(statusOutput, "Command error: %v\n", cmdErr)
			} else {
				fmt.Fprintln(output, turn.Reply)
			}
			continue
		}
		if !pasted && (line == "/exit" || line == "/quit") {
			return nil
		}
		if !pasted && line == "/clear" {
			current, e := c.GetChat(ctx, chat.ID)
			if e != nil {
				return e
			}
			next, e := c.CreateChat(ctx, core.CreateChatRequest{Title: "Native terminal", CLIAgent: "praimate-cli", AgentID: current.AgentID, WorkspacePath: current.WorkspacePath, Settings: current.Settings})
			if e != nil {
				return e
			}
			chat = next
			pending = nil
			if customSystem != "" {
				if _, e := c.AddMessage(ctx, chat.ID, "system", customSystem, nil); e != nil {
					return e
				}
			}
			system, customSystem, agent, e = chatSystemPrompt(ctx, c, chat)
			if e != nil {
				return e
			}
			fmt.Fprintln(statusOutput, "New chat:", chat.ID)
			continue
		}
		if !pasted && strings.HasPrefix(line, "/") && line != "/compact" {
			name, arg, _ := strings.Cut(line, " ")
			arg = strings.TrimSpace(arg)
			var commandErr error
			switch name {
			case "/models":
				modelsLoaded = false // an explicit list refreshes the endpoint catalogue
				var models []string
				models, commandErr = modelsForChat()
				if commandErr == nil {
					for i, model := range models {
						fmt.Fprintf(statusOutput, "  %d. %s\n", i+1, model)
					}
					fmt.Fprintln(statusOutput, "Use /model to choose interactively, or /model ID.")
				}
			case "/model":
				if arg == "" {
					var models []string
					models, commandErr = modelsForChat()
					if commandErr == nil {
						selected := -1
						assignments, _ := c.NativeModelAssignments(ctx)
						for i, model := range models {
							if model == chat.Settings.Model {
								selected = i
								break
							}
							if chat.Settings.Local != nil {
								for _, assignment := range assignments {
									if model == assignment.HostID+"::"+assignment.Model && chat.Settings.Model == assignment.Model && chat.Settings.Local.Endpoint == assignment.Endpoint {
										selected = i
										break
									}
								}
							}
						}
						selected, commandErr = editor.choose(ctx, "Models", models, selected)
						if commandErr == nil && selected >= 0 {
							commandErr = command(ctx, c, chat.ID, "/model "+models[selected], statusOutput)
							if commandErr == nil {
								fmt.Fprintln(statusOutput, "Model:", models[selected])
							}
						}
					}
				} else {
					commandErr = command(ctx, c, chat.ID, line, statusOutput)
				}
			case "/tools":
				if arg == "" {
					levels := []string{"safe", "ask", "edits", "full"}
					current := 0
					for i, level := range levels {
						if level == chat.Settings.Tools {
							current = i
						}
					}
					selected, chooseErr := editor.choose(ctx, "Tool permissions", levels, current)
					commandErr = chooseErr
					if commandErr == nil && selected >= 0 {
						commandErr = command(ctx, c, chat.ID, "/tools "+levels[selected], statusOutput)
						if commandErr == nil {
							fmt.Fprintln(statusOutput, "Tools:", levels[selected])
						}
					}
				} else {
					commandErr = command(ctx, c, chat.ID, line, statusOutput)
				}
			case "/sessions":
				var chats []core.Chat
				chats, commandErr = c.ListChats(ctx, 1000)
				if commandErr == nil {
					var sessions []core.Chat
					var labels []string
					selected := -1
					for _, candidate := range chats {
						if candidate.CLIAgent != "praimate-cli" {
							continue
						}
						if candidate.ID == chat.ID {
							selected = len(sessions)
						}
						sessions = append(sessions, candidate)
						labels = append(labels, candidate.Title+" · "+candidate.ID+" · "+candidate.WorkspacePath)
					}
					var choice int
					choice, commandErr = editor.choose(ctx, "Native chats", labels, selected)
					if commandErr == nil && choice >= 0 {
						next := sessions[choice]
						var nextSystem, nextCustom string
						var nextAgent *core.Agent
						nextSystem, nextCustom, nextAgent, commandErr = chatSystemPrompt(ctx, c, &next)
						if commandErr == nil {
							chat, system, customSystem, agent = &next, nextSystem, nextCustom, nextAgent
							if choice != selected {
								pending = nil
								fmt.Fprintln(statusOutput, "Switched to chat:", chat.ID, "(queued attachments cleared)")
							}
							commandErr = view.restoreHistory(ctx, c, chat.ID)
						}
					}
				}
			case "/attach":
				pending, commandErr = queueAttachment(pending, chat.WorkspacePath, arg)
			case "/detach":
				if arg == "" {
					labels := append([]string(nil), pending...)
					labels = append(labels, "all attachments")
					selected, chooseErr := editor.choose(ctx, "Detach file", labels, -1)
					commandErr = chooseErr
					if commandErr == nil && selected >= 0 {
						if selected == len(pending) {
							pending = nil
						} else {
							pending, commandErr = detachAttachment(pending, fmt.Sprint(selected+1))
						}
					}
				} else {
					pending, commandErr = detachAttachment(pending, arg)
				}
			case "/attachments":
				if len(pending) == 0 {
					fmt.Fprintln(statusOutput, "No queued attachments.")
				}
				for i, path := range pending {
					fmt.Fprintf(statusOutput, "%d. %s\n", i+1, path)
				}
			default:
				commandCtx, stop := signal.NotifyContext(ctx, os.Interrupt)
				commandErr = command(commandCtx, c, chat.ID, line, statusOutput)
				stop()
			}
			if commandErr != nil {
				fmt.Fprintln(statusOutput, commandErr)
			} else if name == "/model" || name == "/tools" || name == "/pin" || name == "/unpin" || name == "/repomap" {
				chat, commandErr = c.GetChat(ctx, chat.ID)
				if commandErr != nil {
					fmt.Fprintln(statusOutput, commandErr)
				} else if name == "/pin" || name == "/unpin" || name == "/repomap" {
					system, customSystem, agent, commandErr = chatSystemPrompt(ctx, c, chat)
					if commandErr != nil {
						fmt.Fprintln(statusOutput, commandErr)
					}
				}
			}
			continue
		}
		if pasted && line == "/compact" {
			line = "Pasted text:\n" + line
		} // Core also recognizes /compact.
		if err := send(line); err != nil {
			if errors.Is(err, context.Canceled) {
				fmt.Fprintln(statusOutput, "Turn cancelled. Chat retained; inspect tool state before retrying.")
			} else {
				fmt.Fprintln(statusOutput, err)
			}
		}
	}
}

func toolLevel(value string) (string, error) {
	switch value {
	case "safe", "":
		return "", nil
	case "ask", "edits", "full":
		return value, nil
	default:
		return "", fmt.Errorf("invalid tools level %q", value)
	}
}

func chatSystemPrompt(ctx context.Context, c *core.Core, chat *core.Chat) (string, string, *core.Agent, error) {
	var agent *core.Agent
	var system string
	if chat.AgentID != "" {
		var err error
		agent, err = c.GetAgent(ctx, chat.AgentID)
		if err != nil {
			return "", "", nil, err
		}
		system = core.AgentSystemPrompt(agent)
	}
	messages, err := c.ListMessages(ctx, chat.ID, 0)
	if err != nil {
		return "", "", nil, err
	}
	var custom []string
	for _, message := range messages {
		if message.Role == "system" {
			custom = append(custom, message.Content)
		}
	}
	customSystem := strings.Join(custom, "\n")
	if customSystem != "" {
		system += "\n" + customSystem
	}
	if chat.Settings.SkillsV2 == nil {
		system = core.ResolveSkillsPrefix(chat.Settings.Skills) + "\n" + system
	}
	for _, rel := range chat.Settings.PinnedFiles {
		fullPath := rel
		if !filepath.IsAbs(fullPath) {
			fullPath = filepath.Join(chat.WorkspacePath, fullPath)
		}
		if content, err := os.ReadFile(fullPath); err == nil {
			const maxPinnedSize = 64 * 1024
			if len(content) > maxPinnedSize {
				content = append(content[:maxPinnedSize], []byte("\n... [Pinned file truncated]")...)
			}
			system += fmt.Sprintf("\n\n---\n[Pinned Context: %s]\n%s\n", rel, string(content))
		}
	}
	if chat.Settings.RepoMap {
		if rMap := c.WorkspaceRepoMap(chat.WorkspacePath, 800); rMap != "" {
			system += "\n\n---\n" + rMap + "\n"
		}
	}
	return strings.TrimSpace(system), customSystem, agent, nil
}

func openCore(input *bufio.Reader, passwordStdin, interactive bool) (*core.Core, func(), error) {
	path, err := store.DefaultDBPath()
	if err != nil {
		return nil, nil, err
	}
	var st *store.Store
	if passwordStdin {
		password, e := input.ReadString('\n')
		if e != nil {
			return nil, nil, errors.New("database password must be the first newline-terminated stdin line")
		}
		setup, e := store.PasswordSetupRequired(path)
		if e != nil {
			return nil, nil, e
		}
		if setup {
			st, err = store.InitializeWithPassword(path, strings.TrimRight(password, "\r\n"))
		} else {
			st, err = store.OpenWithPassword(path, strings.TrimRight(password, "\r\n"))
		}
	} else {
		st, err = store.Open(path)
		if interactive && (errors.Is(err, store.ErrPasswordRequired) || errors.Is(err, store.ErrPasswordSetupRequired)) {
			setup := errors.Is(err, store.ErrPasswordSetupRequired)
			fmt.Fprint(os.Stderr, "PrAImate database password: ")
			password, e := term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Fprintln(os.Stderr)
			if e != nil {
				return nil, nil, e
			}
			if setup {
				fmt.Fprint(os.Stderr, "Confirm new database password: ")
				confirmation, e := term.ReadPassword(int(os.Stdin.Fd()))
				fmt.Fprintln(os.Stderr)
				if e != nil {
					return nil, nil, e
				}
				if string(password) != string(confirmation) {
					return nil, nil, errors.New("passwords do not match")
				}
			}
			if setup {
				st, err = store.InitializeWithPassword(path, string(password))
			} else {
				st, err = store.OpenWithPassword(path, string(password))
			}
			for i := range password {
				password[i] = 0
			}
		}
	}
	if err != nil {
		return nil, nil, fmt.Errorf("unlock core storage (use Desktop password, remembered OS credential, or --db-password-stdin): %w", err)
	}
	c, err := core.New(core.Options{Store: st})
	if err != nil {
		_ = st.Close()
		return nil, nil, err
	}
	return c, func() { _ = st.Close() }, nil
}

func command(ctx context.Context, c *core.Core, id, line string, output io.Writer) error {
	chat, err := c.GetChat(ctx, id)
	if err != nil {
		return err
	}
	name, arg, _ := strings.Cut(line, " ")
	arg = strings.TrimSpace(arg)
	switch name {
	case "/help":
		fmt.Fprintln(output, interactiveHelp)
	case "/status":
		fmt.Fprintln(output, "\nSTATUS")
		level := chat.Settings.Tools
		if level == "" {
			level = "safe"
		}
		if !chat.Settings.ToolsConfigured && chat.AgentID != "" {
			level = "inherited from agent/core"
		}
		fmt.Fprintf(output, "Chat: %s\nAgent: %s\nTools: %s\nWorkspace: %s\n", chat.ID, chat.AgentID, level, chat.WorkspacePath)
		status, err := c.NativeContext(ctx, id)
		if err != nil {
			fmt.Fprintln(output, "Model configuration:", err)
			return nil
		}
		fmt.Fprintf(output, "Model: %s · context %d · output reserve %d\n", status.Model, status.Window, status.OutputReserve)
	case "/context":
		if arg != "" {
			fields := strings.Fields(arg)
			window, reserve := 0, 0
			if arg != "auto" {
				if len(fields) > 2 {
					return errors.New("usage: /context auto | WINDOW [OUTPUT_LIMIT]")
				}
				window, err = strconv.Atoi(fields[0])
				if err != nil {
					return errors.New("context window must be an integer, or auto")
				}
				if len(fields) == 2 {
					reserve, err = strconv.Atoi(fields[1])
					if err != nil {
						return errors.New("output limit must be an integer")
					}
				}
			}
			if err := c.SetNativeChatLimits(ctx, id, window, reserve); err != nil {
				return err
			}
		}
		fmt.Fprintln(output, "\nCONTEXT")
		status, err := c.NativeContext(ctx, id)
		if err != nil {
			return err
		}
		if status.WindowKnown {
			fmt.Fprintf(output, "Window: %d tokens\n", status.Window)
		} else {
			fmt.Fprintf(output, "Backend window: unknown · planning threshold: %d tokens (adaptive, not an input limit)\n", status.Window)
		}
		fmt.Fprintf(output, "Input budget: %d · output reserve: %d · safety: %d\nLast request estimate: ~%d tokens · calibration: %.2fx · compactions: %d\n", status.InputLimit, status.OutputReserve, status.SafetyReserve, status.EstimatedInput, status.Calibration, status.Compactions)
		fmt.Fprintf(output, "Source: %s\nChange this chat: /context auto or /context WINDOW [OUTPUT_LIMIT]\n", status.Source)
		if status.OutputAutomatic {
			fmt.Fprintln(output, "Output mode: automatic — adapts to backend context and reasoning; with an unknown window the backend selects the initial generation limit.")
		} else {
			fmt.Fprintln(output, "Output mode: fixed by this chat (includes reasoning).")
		}
		if status.LastOutputLimit > 0 {
			fmt.Fprintf(output, "Last request output limit: %d tokens\n", status.LastOutputLimit)
		}
		if status.LastUsage != nil {
			fmt.Fprintf(output, "Last endpoint usage: %d input / %d output tokens\n", status.LastUsage.PromptTokens, status.LastUsage.CompletionTokens)
		} else {
			fmt.Fprintln(output, "No endpoint-reported usage yet. Estimates are not exact tokenizer counts.")
		}
	case "/models":
		fmt.Fprintln(output, "\nMODELS")
		endpoint := ""
		if chat.Settings.Local != nil {
			endpoint = chat.Settings.Local.Endpoint
		}
		models, err := nativeModelChoices(ctx, c, endpoint)
		if err != nil {
			return err
		}
		fmt.Fprintln(output, strings.Join(models, "\n"))
	case "/model":
		if arg == "" {
			return errors.New("usage: /model MODEL_ID or HOST_ID::MODEL_ID")
		}
		assignments, err := c.NativeModelAssignments(ctx)
		if err != nil {
			return err
		}
		var selected *core.NativeModelAssignment
		for i := range assignments {
			candidate := &assignments[i]
			if arg == candidate.HostID+"::"+candidate.Model {
				selected = candidate
				break
			}
			if arg == candidate.Model {
				if selected != nil {
					return fmt.Errorf("model %q is assigned to multiple hosts; use HOST_ID::MODEL_ID", arg)
				}
				selected = candidate
			}
		}
		if strings.Contains(arg, "::") && selected == nil {
			return fmt.Errorf("assigned model %q not found; use /models", arg)
		}
		return c.UpdateChatSettings(ctx, id, func(s *core.ChatSettings) {
			if selected != nil {
				s.Model = selected.Model
				s.Local = &core.ChatLocalEndpoint{
					Endpoint: selected.Endpoint, Model: selected.Model,
					ContextTokens: selected.ContextTokens, OutputTokens: selected.OutputTokens,
				}
			} else {
				s.Model = arg
				if s.Local != nil {
					s.Local.Model = arg
				}
			}
		})
	case "/tools":
		level, err := toolLevel(arg)
		if err != nil {
			return err
		}
		return c.UpdateChatSettings(ctx, id, func(s *core.ChatSettings) { s.Tools = level; s.ToolsConfigured = true })
	case "/mcp":
		fmt.Fprintln(output, "\nMCP SERVERS")
		servers, err := c.ListMCPServers(ctx, false)
		if err != nil {
			return err
		}
		if len(servers) == 0 {
			fmt.Fprintln(output, "No registered MCP servers.")
		}
		for _, server := range servers {
			status := "available"
			if !server.Enabled {
				status = "disabled"
			}
			for _, selected := range chat.Settings.MCPServers {
				if selected == server.ID {
					if server.Enabled {
						status = "selected"
					} else {
						status = "selected, disabled"
					}
					break
				}
			}
			fmt.Fprintf(output, "%s (%s) [%s]\n", server.ID, server.Name, status)
		}
		if chat.Settings.SkillsV2 != nil {
			fmt.Fprintln(output, "Internal native skill tools: skill_load, skill_read (see /skills)")
		} else {
			fmt.Fprintln(output, "Internal native skill tools: inactive (no versioned skill selection; see /skills)")
		}
	case "/skills":
		fmt.Fprintln(output, "\nSKILLS")
		raw, _ := json.MarshalIndent(struct {
			Legacy    []string `json:"legacy"`
			Selection any      `json:"selection"`
			Receipt   any      `json:"receipt"`
		}{chat.Settings.Skills, chat.Settings.SkillsV2, chat.Settings.SkillRuntime}, "", "  ")
		fmt.Fprintln(output, string(raw))
		available, err := core.InstalledSkillSummaries(ctx)
		if err != nil {
			return err
		}
		fmt.Fprintln(output, "Available approved versions:")
		count := 0
		for _, skill := range available {
			if skill.Approved {
				fmt.Fprintf(output, "%s (%s)\n", skill.Name, skill.Ref)
				count++
			}
		}
		if count == 0 {
			fmt.Fprintln(output, "None installed and approved.")
		}
	case "/pinned":
		if len(chat.Settings.PinnedFiles) == 0 {
			fmt.Fprintln(output, "No pinned files in this session.")
			return nil
		}
		fmt.Fprintln(output, "\nPINNED FILES")
		for i, p := range chat.Settings.PinnedFiles {
			fmt.Fprintf(output, "  %d. %s\n", i+1, p)
		}
	case "/pin":
		if arg == "" {
			return errors.New("usage: /pin PATH")
		}
		cleanPath := strings.Trim(arg, `"'`)
		fullPath := cleanPath
		if !filepath.IsAbs(fullPath) {
			fullPath = filepath.Join(chat.WorkspacePath, cleanPath)
		}
		fi, err := os.Stat(fullPath)
		if err != nil {
			return fmt.Errorf("pin: %w", err)
		}
		if fi.IsDir() {
			return fmt.Errorf("pin: %s is a directory", cleanPath)
		}
		rel, err := filepath.Rel(chat.WorkspacePath, fullPath)
		if err != nil || strings.HasPrefix(rel, "..") {
			rel = cleanPath
		}
		for _, existing := range chat.Settings.PinnedFiles {
			if existing == rel {
				fmt.Fprintf(output, "File already pinned: %s\n", rel)
				return nil
			}
		}
		err = c.UpdateChatSettings(ctx, id, func(s *core.ChatSettings) {
			s.PinnedFiles = append(s.PinnedFiles, rel)
		})
		if err != nil {
			return err
		}
		fmt.Fprintf(output, "Pinned: %s (%d bytes)\n", rel, fi.Size())
	case "/unpin":
		if arg == "" {
			return errors.New("usage: /unpin PATH | N | all")
		}
		if len(chat.Settings.PinnedFiles) == 0 {
			fmt.Fprintln(output, "No pinned files.")
			return nil
		}
		if arg == "all" {
			err := c.UpdateChatSettings(ctx, id, func(s *core.ChatSettings) {
				s.PinnedFiles = nil
			})
			if err != nil {
				return err
			}
			fmt.Fprintln(output, "Unpinned all files.")
			return nil
		}
		idx, convErr := strconv.Atoi(arg)
		target := arg
		if convErr == nil {
			if idx < 1 || idx > len(chat.Settings.PinnedFiles) {
				return fmt.Errorf("invalid pinned index %d (1-%d)", idx, len(chat.Settings.PinnedFiles))
			}
			target = chat.Settings.PinnedFiles[idx-1]
		}
		found := false
		err = c.UpdateChatSettings(ctx, id, func(s *core.ChatSettings) {
			var updated []string
			for _, p := range s.PinnedFiles {
				if p == target && !found {
					found = true
					continue
				}
				updated = append(updated, p)
			}
			s.PinnedFiles = updated
		})
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("pinned file %q not found", arg)
		}
		fmt.Fprintf(output, "Unpinned: %s\n", target)
	case "/diff":
		cwd := chat.WorkspacePath
		if cwd == "" || cwd == "." {
			cwd, _ = os.Getwd()
		}
		gitArgs := []string{"--no-pager", "-c", "core.fsmonitor=false", "-C", cwd, "diff"}
		if arg == "staged" || arg == "--cached" {
			gitArgs = append(gitArgs, "--cached")
		} else if arg != "" {
			gitArgs = append(gitArgs, arg)
		}
		cmd := exec.CommandContext(ctx, "git", gitArgs...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("git diff: %v (%s)", err, strings.TrimSpace(string(out)))
		}
		res := strings.TrimSpace(string(out))
		if res == "" {
			if arg == "staged" || arg == "--cached" {
				fmt.Fprintln(output, "No staged changes.")
			} else {
				fmt.Fprintln(output, "Working tree clean (no changes).")
			}
			return nil
		}
		fmt.Fprintln(output, res)
	case "/export":
		exportPath := arg
		if exportPath == "" {
			prefix := chat.ID
			if len(prefix) > 8 {
				prefix = prefix[:8]
			}
			exportPath = fmt.Sprintf("praimate-chat-%s.md", prefix)
		}
		exportPath = strings.Trim(exportPath, `"'`)
		if !filepath.IsAbs(exportPath) {
			exportPath = filepath.Join(chat.WorkspacePath, exportPath)
		}
		if err := os.MkdirAll(filepath.Dir(exportPath), 0755); err != nil {
			return fmt.Errorf("export: %w", err)
		}
		messages, err := c.ListMessages(ctx, id, 0)
		if err != nil {
			return fmt.Errorf("export: list messages: %w", err)
		}
		var b strings.Builder
		fmt.Fprintf(&b, "# PrAImate Chat Export\n\n")
		fmt.Fprintf(&b, "- **Chat ID:** `%s`\n", chat.ID)
		fmt.Fprintf(&b, "- **Title:** %s\n", chat.Title)
		fmt.Fprintf(&b, "- **Workspace:** `%s`\n", chat.WorkspacePath)
		fmt.Fprintf(&b, "- **Model:** `%s`\n", chat.Settings.Model)
		fmt.Fprintf(&b, "- **Exported At:** %s\n\n---\n\n", time.Now().Format(time.RFC3339))
		for _, m := range messages {
			roleTitle := strings.ToUpper(m.Role)
			switch m.Role {
			case "user":
				roleTitle = "User"
			case "assistant":
				roleTitle = "Assistant"
			case "tool":
				roleTitle = "Tool Result"
			case "system":
				roleTitle = "System"
			case "command":
				roleTitle = "Command Output"
			}
			fmt.Fprintf(&b, "### %s (%s)\n\n", roleTitle, m.TS.Format("2006-01-02 15:04:05"))
			fmt.Fprintf(&b, "%s\n\n", strings.TrimSpace(m.Content))
		}
		if err := os.WriteFile(exportPath, []byte(b.String()), 0600); err != nil {
			return fmt.Errorf("export: %w", err)
		}
		fmt.Fprintf(output, "Session exported to: %s (%d messages)\n", exportPath, len(messages))
	case "/repomap":
		if arg == "on" {
			err := c.UpdateChatSettings(ctx, id, func(s *core.ChatSettings) { s.RepoMap = true })
			if err != nil {
				return err
			}
			fmt.Fprintln(output, "Ambient Repo Map enabled for this chat.")
			return nil
		} else if arg == "off" {
			err := c.UpdateChatSettings(ctx, id, func(s *core.ChatSettings) { s.RepoMap = false })
			if err != nil {
				return err
			}
			fmt.Fprintln(output, "Ambient Repo Map disabled.")
			return nil
		} else if arg == "refresh" {
			core.InvalidateRepoMap(chat.WorkspacePath)
		}
		rMap := c.WorkspaceRepoMap(chat.WorkspacePath, 800)
		if rMap == "" {
			fmt.Fprintln(output, "Repo Map: empty (no supported source files found).")
		} else {
			status := "disabled in system prompt; use /repomap on to include in context"
			if chat.Settings.RepoMap {
				status = "active in system prompt"
			}
			fmt.Fprintf(output, "\nAMBIENT REPO MAP (%s)\n%s\n", status, rMap)
		}
	case "/sessions":
		chats, err := c.ListChats(ctx, 50)
		if err != nil {
			return err
		}
		for _, ch := range chats {
			if ch.CLIAgent == "praimate-cli" {
				fmt.Fprintf(output, "%s  %s  %s\n", ch.ID, ch.Title, ch.WorkspacePath)
			}
		}
	case "/clear":
		return errors.New("history is retained in the core; exit and start without --chat for a clean session, or use /compact to reset model context")
	default:
		return fmt.Errorf("unknown command %q; use /help", name)
	}
	return nil
}

func nativeModelChoices(ctx context.Context, c *core.Core, endpoint string) ([]string, error) {
	assignments, err := c.NativeModelAssignments(ctx)
	if err != nil {
		return nil, err
	}
	if len(assignments) == 0 {
		return c.NativeModels(ctx, endpoint)
	}
	choices := make([]string, 0, len(assignments))
	for _, assignment := range assignments {
		choices = append(choices, assignment.HostID+"::"+assignment.Model)
	}
	return choices, nil
}
