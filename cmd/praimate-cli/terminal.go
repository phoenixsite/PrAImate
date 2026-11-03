package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/term"
)

var errInputCanceled = errors.New("input cancelled")

const interactiveHelp = `Commands:
  /help                 Show this help
  /status              Session settings
  /context [auto|N [M]] Context usage; auto-detect or set window N / output M
  /models, /model [ID]  List assigned models / choose one (HOST_ID::MODEL selects a host)
  /tools [LEVEL]        Choose safe | ask | edits | full
  /attach PATH          Queue one file (spaces and quoted paths supported)
  /attachments          List files queued for the next message
  /detach N|all         Remove a queued file (1-based) or all files
  /pin PATH             Pin a workspace file permanently into system context
  /pinned               List currently pinned context files
  /unpin PATH|all       Unpin a pinned file or all pinned files
  /diff [staged]        Show working tree diff (or staged changes)
  /export [PATH]        Export full chat transcript to a Markdown file
  /repomap [on|off]     Show or toggle ambient repository map
  /mcp, /skills         Show configured core tools and skills
  /sessions             Choose a native core chat
  /compact              Compact model history (lossy; drops old images)
  /clear                Start a new chat, keeping the previous transcript
  /exit                 Exit (also Ctrl+D on an empty prompt)
  ! <COMMAND>           Run a shell command directly in the workspace
Keys: arrows/history, Home/End, Tab command and argument completion.
Selectors: type a number; n/p changes page; Enter cancels.
Multiline: enclose in """ ... """ or end a line with \. Bracketed paste
keeps newlines; press Enter after pasting. Pasted slash commands are sent as text.
Ctrl+C cancels the current turn or clears the current input.
Images go to the selected endpoint; they are not text-redacted.`

var slashCommands = []string{"/help", "/status", "/context", "/models", "/model", "/tools", "/attach", "/attachments", "/detach", "/pin", "/pinned", "/unpin", "/diff", "/export", "/repomap", "/mcp", "/skills", "/sessions", "/compact", "/clear", "/exit"}

func completeCommand(line string, pos int, key rune) (string, int, bool) {
	if key != '\t' || pos != len(line) || !strings.HasPrefix(line, "/") || strings.ContainsAny(line, " \t\n") {
		return "", 0, false
	}
	var matches []string
	for _, command := range slashCommands {
		if strings.HasPrefix(command, line) {
			matches = append(matches, command)
		}
	}
	if len(matches) == 0 {
		return line, pos, true
	}
	prefix := matches[0]
	for _, match := range matches[1:] {
		for !strings.HasPrefix(match, prefix) {
			prefix = prefix[:len(prefix)-1]
		}
	}
	if len(matches) == 1 {
		prefix += " "
	}
	return prefix, len(prefix), true
}

func completeInteractive(line string, pos int, key rune, models, attachments []string, cwd string) (string, int, bool) {
	if key != '\t' || pos != len(line) {
		return "", 0, false
	}
	if !strings.ContainsAny(line, " \t") {
		return completeCommand(line, pos, key)
	}
	name, prefix, _ := strings.Cut(line, " ")
	var choices []string
	switch name {
	case "/context":
		choices = []string{"auto"}
	case "/tools":
		choices = []string{"safe", "ask", "edits", "full"}
	case "/model":
		choices = models
	case "/diff":
		choices = []string{"staged", "--cached"}
	case "/repomap":
		choices = []string{"on", "off", "refresh"}
	case "/detach", "/unpin":
		choices = []string{"all"}
		for i := range attachments {
			choices = append(choices, strconv.Itoa(i+1))
		}
	case "/attach", "/pin":
		return completeAttachmentPath(line, prefix, cwd)
	default:
		return line, pos, true
	}
	return completeArgument(name+" ", prefix, choices)
}

func completeArgument(command, prefix string, choices []string) (string, int, bool) {
	var matches []string
	for _, choice := range choices {
		if strings.HasPrefix(choice, prefix) && cleanTerminalText(choice) == choice && !strings.ContainsAny(choice, "\r\n") {
			matches = append(matches, choice)
		}
	}
	if len(matches) == 0 {
		return command + prefix, len(command) + len(prefix), true
	}
	common := matches[0]
	for _, match := range matches[1:] {
		for !strings.HasPrefix(match, common) {
			common = common[:len(common)-1]
		}
	}
	if len(matches) == 1 {
		common += " "
	}
	return command + common, len(command) + len(common), true
}

func completeAttachmentPath(line, prefix, cwd string) (string, int, bool) {
	quoted := strings.HasPrefix(prefix, `"`)
	path := strings.TrimPrefix(prefix, `"`)
	dir, base := filepath.Split(path)
	root := dir
	if !filepath.IsAbs(root) {
		root = filepath.Join(cwd, root)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return line, len(line), true
	}
	var matches []string
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), base) || cleanTerminalText(entry.Name()) != entry.Name() || strings.ContainsAny(entry.Name(), "\r\n\"") {
			continue
		}
		candidate := dir + entry.Name()
		if entry.IsDir() {
			candidate += string(filepath.Separator)
		}
		matches = append(matches, candidate)
	}
	if len(matches) == 0 {
		return line, len(line), true
	}
	common := matches[0]
	for _, match := range matches[1:] {
		for !strings.HasPrefix(match, common) {
			common = common[:len(common)-1]
		}
	}
	if quoted {
		common = `"` + common
	} else if strings.Contains(common, " ") {
		common = `"` + common
		quoted = true
	}
	if len(matches) == 1 && !strings.HasSuffix(common, string(filepath.Separator)) {
		if quoted {
			common += `"`
		}
		common += " "
	}
	result := "/attach " + common
	return result, len(result), true
}

// Prompts live only in memory; approvals are never added to history.
type terminalHistory []string

func (h *terminalHistory) Add(string)      {} // ReadLine adds fragments of a paste; record only completed prompts ourselves.
func (h *terminalHistory) Len() int        { return len(*h) }
func (h *terminalHistory) At(i int) string { return (*h)[len(*h)-1-i] }
func (h *terminalHistory) remember(line string) {
	if strings.TrimSpace(line) == "" || (len(*h) > 0 && h.At(0) == line) {
		return
	}
	*h = append(*h, line)
	if len(*h) > 100 {
		*h = append(terminalHistory(nil), (*h)[len(*h)-100:]...)
	}
}

type inputByte struct {
	value byte
	err   error
}

// One reader owns stdin for the entire interactive session. Cancelling an
// approval never leaves an abandoned goroutine competing with the next prompt.
// Input typed while the model is busy is discarded, not queued as an approval.
type terminalInput struct {
	mu     sync.Mutex
	active *promptInput
	done   chan struct{}
	ended  chan struct{}
	err    error
}

func newTerminalInput(reader *bufio.Reader) *terminalInput {
	in := &terminalInput{done: make(chan struct{}), ended: make(chan struct{})}
	go func() {
		defer close(in.ended)
		for {
			b, err := reader.ReadByte()
			in.mu.Lock()
			active := in.active
			if err != nil {
				in.err = err
			}
			in.mu.Unlock()
			if active != nil {
				select {
				case active.keys <- inputByte{b, err}:
				case <-active.done:
				case <-in.done:
					return
				}
			}
			if err != nil {
				return
			}
			select {
			case <-in.done:
				return
			default:
			}
		}
	}()
	return in
}

type promptInput struct {
	owner    *terminalInput
	ctx      context.Context
	keys     chan inputByte
	done     chan struct{}
	last     byte
	sequence string
	pasted   bool
}

func (in *terminalInput) begin(ctx context.Context) *promptInput {
	reader := &promptInput{owner: in, ctx: ctx, keys: make(chan inputByte, 4096), done: make(chan struct{})}
	in.mu.Lock()
	in.active = reader
	in.mu.Unlock()
	return reader
}
func (r *promptInput) close() {
	r.owner.mu.Lock()
	if r.owner.active == r {
		r.owner.active = nil
	}
	close(r.done)
	r.owner.mu.Unlock()
}
func (r *promptInput) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	var key inputByte
	select {
	case key = <-r.keys:
	case <-r.ctx.Done():
		return 0, r.ctx.Err()
	case <-r.owner.ended:
		select {
		case key = <-r.keys:
		default:
			r.owner.mu.Lock()
			err := r.owner.err
			r.owner.mu.Unlock()
			return 0, err
		}
	}
	if key.err != nil {
		return 0, key.err
	}
	r.last, p[0] = key.value, key.value
	r.sequence += string(key.value)
	if len(r.sequence) > 6 {
		r.sequence = r.sequence[len(r.sequence)-6:]
	}
	if r.sequence == "\x1b[200~" {
		r.pasted = true
	}
	return 1, nil
}

type terminalEditor struct {
	input    *terminalInput
	history  terminalHistory
	readMu   sync.Mutex
	complete func(string, int, rune) (string, int, bool)
}

func (e *terminalEditor) read(ctx context.Context, prompt string, approval bool) (string, bool, error) {
	e.readMu.Lock()
	defer e.readMu.Unlock()
	state, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return "", false, err
	}
	defer term.Restore(int(os.Stdin.Fd()), state)
	r := e.input.begin(ctx)
	defer r.close()
	editor := term.NewTerminal(struct {
		io.Reader
		io.Writer
	}{r, os.Stderr}, prompt)
	if width, height, err := term.GetSize(int(os.Stderr.Fd())); err == nil {
		_ = editor.SetSize(width, height)
	}
	if !approval {
		editor.History = &e.history
		editor.AutoCompleteCallback = completeCommand
		if e.complete != nil {
			editor.AutoCompleteCallback = e.complete
		}
	}
	editor.SetBracketedPasteMode(true)
	defer editor.SetBracketedPasteMode(false)
	line, pasted, err := readEditedInput(editor, r, approval)
	if err != nil {
		fmt.Fprint(os.Stderr, "\r\n")
		return "", pasted, err
	}
	if !approval {
		e.history.remember(line)
	}
	return line, pasted, nil
}

// choose is a small paged selector. It uses the normal edited input path,
// so approvals and menu choices do not pollute prompt history.
func (e *terminalEditor) choose(ctx context.Context, title string, options []string, current int) (int, error) {
	if len(options) == 0 {
		fmt.Fprintln(os.Stderr, "No options available.")
		return -1, nil
	}
	const pageSize = 10
	page := 0
	labelWidth := 72
	if columns, _, err := term.GetSize(int(os.Stderr.Fd())); err == nil {
		labelWidth = max(20, columns-8)
	}
	if current >= 0 {
		page = current / pageSize
	}
	for {
		start := page * pageSize
		end := min(start+pageSize, len(options))
		fmt.Fprintf(os.Stderr, "\n%s (%d/%d)\n", title, page+1, (len(options)+pageSize-1)/pageSize)
		for i := start; i < end; i++ {
			marker := " "
			if i == current {
				marker = "*"
			}
			label := strings.NewReplacer("\r", " ", "\n", " ").Replace(options[i])
			fmt.Fprintf(safeTerminalWriter{os.Stderr}, " %s %2d  %s\n", marker, i-start+1, truncateRunes(label, labelWidth))
		}
		line, pasted, err := e.read(ctx, "Select # · n/p page · Enter cancel > ", true)
		if errors.Is(err, errInputCanceled) || errors.Is(err, io.EOF) {
			return -1, nil
		}
		if err != nil {
			return -1, err
		}
		if pasted || strings.TrimSpace(line) == "" || strings.EqualFold(line, "q") {
			return -1, nil
		}
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "n":
			if end < len(options) {
				page++
			}
			continue
		case "p":
			if page > 0 {
				page--
			}
			continue
		}
		index, err := strconv.Atoi(strings.TrimSpace(line))
		if err == nil && index >= 1 && index <= end-start {
			return start + index - 1, nil
		}
		fmt.Fprintln(os.Stderr, "Choose a shown number, n/p, or Enter to cancel.")
	}
}

func readEditedInput(editor *term.Terminal, reader *promptInput, approval bool) (string, bool, error) {
	var lines []string
	size := 0
	inBlock := false
	for {
		line, err := editor.ReadLine()
		if errors.Is(err, io.EOF) && reader.last == 3 {
			return "", reader.pasted, errInputCanceled
		}
		if err != nil && !errors.Is(err, term.ErrPasteIndicator) {
			return "", reader.pasted, err
		}
		if len([]rune(line)) >= 4096 {
			return "", reader.pasted, errors.New("an edited line must be shorter than 4096 characters; use multiline input, --attach, or piped stdin for larger content")
		}
		size += len(line) + 1
		if size > 1<<20 {
			return "", reader.pasted, errors.New("prompt exceeds 1 MiB")
		}
		if errors.Is(err, term.ErrPasteIndicator) {
			lines = append(lines, line)
			editor.SetPrompt("... ")
			continue
		}
		if !approval && !reader.pasted && inBlock {
			if strings.HasSuffix(line, `"""`) {
				content := strings.TrimSuffix(line, `"""`)
				if content != "" {
					lines = append(lines, content)
				}
				return strings.Join(lines, "\n"), true, nil
			}
			lines = append(lines, line)
			editor.SetPrompt("... ")
			continue
		}
		if !approval && !reader.pasted && strings.HasPrefix(line, `"""`) {
			trimmed := strings.TrimPrefix(line, `"""`)
			if strings.HasSuffix(trimmed, `"""`) && len(trimmed) >= 3 {
				return strings.TrimSuffix(trimmed, `"""`), false, nil
			}
			if trimmed != "" {
				lines = append(lines, trimmed)
			}
			inBlock = true
			editor.SetPrompt("... ")
			continue
		}
		if !approval && !reader.pasted && strings.HasSuffix(line, "\\") {
			lines = append(lines, strings.TrimSuffix(line, "\\"))
			editor.SetPrompt("... ")
			continue
		}
		lines = append(lines, line)
		return strings.Join(lines, "\n"), reader.pasted || len(lines) > 1, nil
	}
}

// Model/tool output is data, not terminal escape sequences (OSC clipboard,
// hyperlinks, cursor movement, etc.). JSONL remains unmodified machine data.
type safeTerminalWriter struct{ target io.Writer }

func cleanTerminalText(value string) string {
	return strings.Map(func(r rune) rune {
		if (r < 32 && r != '\n' && r != '\t') || (r >= 0x7f && r <= 0x9f) {
			return -1
		}
		return r
	}, value)
}

func (w safeTerminalWriter) Write(p []byte) (int, error) {
	text := cleanTerminalText(string(p))
	_, err := io.WriteString(w.target, text)
	if err != nil {
		return 0, err
	}
	return len(p), nil
}
