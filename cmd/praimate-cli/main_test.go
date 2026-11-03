package main

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sPROFFEs/PrAImate/internal/core"
)

func TestNativeCLIHelpAndValidationDoNotOpenStorage(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PRAIMATE_HOME", dir)
	for _, args := range [][]string{{"--version"}, {"--help"}} {
		if err := run(context.Background(), args); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"--tools", "unsafe"}, {"--format", "broken"}, {"--mcp-config", "untrusted.json"}, {"--chat", "id", "--continue"}} {
		if err := run(context.Background(), args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatal("help/invalid options touched storage")
	}
}

func TestNativeCLIOpensSameEncryptedCoreAndPreservesInput(t *testing.T) {
	t.Setenv("PRAIMATE_HOME", t.TempDir())
	input := bufio.NewReader(strings.NewReader("correct horse battery staple\nuser prompt\n"))
	c, closeCore, err := openCore(input, true, false)
	if err != nil {
		t.Fatal(err)
	}
	chat, err := c.CreateChat(context.Background(), core.CreateChatRequest{CLIAgent: "praimate-cli", WorkspacePath: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	remaining, err := input.ReadString('\n')
	if err != nil || remaining != "user prompt\n" {
		t.Fatalf("password consumed prompt: %q %v", remaining, err)
	}
	closeCore()
	c, closeCore, err = openCore(bufio.NewReader(strings.NewReader("correct horse battery staple\n")), true, false)
	if err != nil {
		t.Fatal(err)
	}
	defer closeCore()
	if _, err := c.GetChat(context.Background(), chat.ID); err != nil {
		t.Fatal("terminal did not reopen core history:", err)
	}
	if _, _, err := openCore(bufio.NewReader(strings.NewReader("wrong password\n")), true, false); err == nil {
		t.Fatal("accepted wrong database password")
	}
}

func TestNativeCLIModelSelectionSwitchesAssignedHost(t *testing.T) {
	t.Setenv("PRAIMATE_HOME", t.TempDir())
	c, closeCore, err := openCore(bufio.NewReader(strings.NewReader("test-password\n")), true, false)
	if err != nil {
		t.Fatal(err)
	}
	defer closeCore()
	ctx := context.Background()
	for _, host := range []core.LocalHost{
		{ID: "default", Name: "Local", Endpoint: "http://127.0.0.1:11434", IsDefault: true, NativeModels: []string{"shared"}},
		{ID: "gpu", Name: "GPU", Endpoint: "http://127.0.0.1:8000", ContextTokens: 16384, NativeModels: []string{"shared", "large"}},
	} {
		if _, err := c.SaveLocalHost(ctx, host); err != nil {
			t.Fatal(err)
		}
	}
	chat, err := c.CreateChat(ctx, core.CreateChatRequest{CLIAgent: "praimate-cli", WorkspacePath: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	models, err := nativeModelChoices(ctx, c, "")
	if err != nil || !strings.Contains(strings.Join(models, " "), "gpu::large") {
		t.Fatalf("assigned models: %v, %v", models, err)
	}
	if err := command(ctx, c, chat.ID, "/model shared", &strings.Builder{}); err == nil {
		t.Fatal("ambiguous model accepted without a host")
	}
	if err := command(ctx, c, chat.ID, "/model gpu::large", &strings.Builder{}); err != nil {
		t.Fatal(err)
	}
	updated, err := c.GetChat(ctx, chat.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Settings.Model != "large" || updated.Settings.Local == nil || updated.Settings.Local.Endpoint != "http://127.0.0.1:8000" || updated.Settings.Local.ContextTokens != 16384 {
		t.Fatalf("model selection did not switch host: %+v", updated.Settings)
	}
}

func TestNativeCLIContextCommandPersistsAndValidatesLimits(t *testing.T) {
	t.Setenv("PRAIMATE_HOME", t.TempDir())
	c, closeCore, err := openCore(bufio.NewReader(strings.NewReader("test-password\n")), true, false)
	if err != nil {
		t.Fatal(err)
	}
	defer closeCore()
	ctx := context.Background()
	_, err = c.SaveLocalHost(ctx, core.LocalHost{ID: "gpu", Name: "GPU", Endpoint: "http://local.test/v1", ContextTokens: 16384})
	if err != nil {
		t.Fatal(err)
	}
	chat, err := c.CreateChat(ctx, core.CreateChatRequest{CLIAgent: "praimate-cli", Settings: core.ChatSettings{Local: &core.ChatLocalEndpoint{Endpoint: "http://local.test/v1", Model: "test-model"}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"/context nope", "/context 1024", "/context 8192 8192", "/context 8192 7900", "/context 100 200 300"} {
		if err := command(ctx, c, chat.ID, bad, &strings.Builder{}); err == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
	for _, tc := range []struct {
		cmd    string
		window int
		mode   string
	}{{"/context 8192 2048", 8192, "fixed"}, {"/context auto", 16384, "automatic"}} {
		var output strings.Builder
		if err := command(ctx, c, chat.ID, tc.cmd, &output); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(output.String(), "Output mode: "+tc.mode) {
			t.Fatalf("unclear output mode: %s", output.String())
		}
		status, err := c.NativeContext(ctx, chat.ID)
		if err != nil || status.Window != tc.window {
			t.Fatalf("status=%+v err=%v", status, err)
		}
	}
}

func TestNativeCLIMCPAndSkillStatus(t *testing.T) {
	t.Setenv("PRAIMATE_HOME", t.TempDir())
	c, closeCore, err := openCore(bufio.NewReader(strings.NewReader("test-password\n")), true, false)
	if err != nil {
		t.Fatal(err)
	}
	defer closeCore()
	ctx := context.Background()
	chat, err := c.CreateChat(ctx, core.CreateChatRequest{CLIAgent: "praimate-cli", WorkspacePath: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	if err := command(ctx, c, chat.ID, "/mcp", &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "No registered MCP servers") || !strings.Contains(output.String(), "skill tools: inactive") {
		t.Fatalf("empty MCP status is unclear: %s", output.String())
	}
	if _, err := c.ConnectMCP(ctx, core.ConnectMCPRequest{ID: "local-docs", Name: "Local Docs", Transport: core.MCPTransportHTTP, URL: "http://127.0.0.1:9999"}); err != nil {
		t.Fatal(err)
	}
	if err := c.UpdateChatSettings(ctx, chat.ID, func(s *core.ChatSettings) { s.MCPServers = []string{"local-docs"} }); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	if err := command(ctx, c, chat.ID, "/mcp", &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "local-docs (Local Docs) [selected]") {
		t.Fatalf("selected MCP missing: %s", output.String())
	}
	output.Reset()
	if err := command(ctx, c, chat.ID, "/skills", &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Available approved versions:") {
		t.Fatalf("skill catalogue status missing: %s", output.String())
	}
}

func TestChatSystemPromptDoesNotLeakBetweenSessions(t *testing.T) {
	t.Setenv("PRAIMATE_HOME", t.TempDir())
	c, closeCore, err := openCore(bufio.NewReader(strings.NewReader("test-password\n")), true, false)
	if err != nil {
		t.Fatal(err)
	}
	defer closeCore()
	ctx := context.Background()
	first, err := c.CreateChat(ctx, core.CreateChatRequest{ID: "terminal-first", CLIAgent: "praimate-cli", WorkspacePath: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.CreateChat(ctx, core.CreateChatRequest{ID: "terminal-second", CLIAgent: "praimate-cli", WorkspacePath: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.AddMessage(ctx, first.ID, "system", "first only", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := c.AddMessage(ctx, second.ID, "system", "second only", nil); err != nil {
		t.Fatal(err)
	}
	firstSystem, _, _, err := chatSystemPrompt(ctx, c, first)
	if err != nil {
		t.Fatal(err)
	}
	secondSystem, _, _, err := chatSystemPrompt(ctx, c, second)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(firstSystem, "first only") || strings.Contains(secondSystem, "first only") || !strings.Contains(secondSystem, "second only") {
		t.Fatalf("session system context leaked: first=%q second=%q", firstSystem, secondSystem)
	}
}

func TestNativeCLIPinnedFilesAndSystemPrompt(t *testing.T) {
	t.Setenv("PRAIMATE_HOME", t.TempDir())
	c, closeCore, err := openCore(bufio.NewReader(strings.NewReader("test-password\n")), true, false)
	if err != nil {
		t.Fatal(err)
	}
	defer closeCore()
	ctx := context.Background()
	ws := t.TempDir()
	rulesFile := filepath.Join(ws, "ARCH.md")
	if err := os.WriteFile(rulesFile, []byte("# Architecture Rules\nKeep components decoupled.\n"), 0644); err != nil {
		t.Fatal(err)
	}
	chat, err := c.CreateChat(ctx, core.CreateChatRequest{CLIAgent: "praimate-cli", WorkspacePath: ws})
	if err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	// 1. /pinned initially empty
	if err := command(ctx, c, chat.ID, "/pinned", &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "No pinned files") {
		t.Fatalf("expected empty pinned files, got: %s", out.String())
	}

	// 2. /pin ARCH.md
	out.Reset()
	if err := command(ctx, c, chat.ID, "/pin ARCH.md", &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Pinned: ARCH.md") {
		t.Fatalf("unexpected pin output: %s", out.String())
	}

	// 3. /pinned now shows ARCH.md
	out.Reset()
	if err := command(ctx, c, chat.ID, "/pinned", &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "1. ARCH.md") {
		t.Fatalf("expected 1. ARCH.md, got: %s", out.String())
	}

	// 4. Verify chatSystemPrompt includes pinned content
	refetched, err := c.GetChat(ctx, chat.ID)
	if err != nil {
		t.Fatal(err)
	}
	system, _, _, err := chatSystemPrompt(ctx, c, refetched)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(system, "[Pinned Context: ARCH.md]") || !strings.Contains(system, "Keep components decoupled.") {
		t.Fatalf("chatSystemPrompt missing pinned content: %s", system)
	}

	// 5. /unpin 1
	out.Reset()
	if err := command(ctx, c, chat.ID, "/unpin 1", &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Unpinned: ARCH.md") {
		t.Fatalf("unexpected unpin output: %s", out.String())
	}

	// 6. Verify unpinned from chatSystemPrompt
	refetched, err = c.GetChat(ctx, chat.ID)
	if err != nil {
		t.Fatal(err)
	}
	system, _, _, err = chatSystemPrompt(ctx, c, refetched)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(system, "[Pinned Context: ARCH.md]") {
		t.Fatalf("unpinned content still in system prompt: %s", system)
	}
}

func TestNativeCLIExportCommand(t *testing.T) {
	t.Setenv("PRAIMATE_HOME", t.TempDir())
	c, closeCore, err := openCore(bufio.NewReader(strings.NewReader("test-password\n")), true, false)
	if err != nil {
		t.Fatal(err)
	}
	defer closeCore()
	ctx := context.Background()
	ws := t.TempDir()
	chat, err := c.CreateChat(ctx, core.CreateChatRequest{CLIAgent: "praimate-cli", WorkspacePath: ws, Title: "Export Test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.AddMessage(ctx, chat.ID, "user", "What is 2+2?", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := c.AddMessage(ctx, chat.ID, "assistant", "4", nil); err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	exportTarget := filepath.Join(ws, "transcript.md")
	if err := command(ctx, c, chat.ID, "/export "+exportTarget, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Session exported to:") {
		t.Fatalf("unexpected export output: %s", out.String())
	}

	data, err := os.ReadFile(exportTarget)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "# PrAImate Chat Export") ||
		!strings.Contains(content, "What is 2+2?") ||
		!strings.Contains(content, "### Assistant") ||
		!strings.Contains(content, "4") {
		t.Fatalf("invalid export content:\n%s", content)
	}
}

func TestNativeCLIDiffCommand(t *testing.T) {
	t.Setenv("PRAIMATE_HOME", t.TempDir())
	c, closeCore, err := openCore(bufio.NewReader(strings.NewReader("test-password\n")), true, false)
	if err != nil {
		t.Fatal(err)
	}
	defer closeCore()
	ctx := context.Background()
	ws := t.TempDir()
	chat, err := c.CreateChat(ctx, core.CreateChatRequest{CLIAgent: "praimate-cli", WorkspacePath: ws})
	if err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	err = command(ctx, c, chat.ID, "/diff", &out)
	if err == nil && !strings.Contains(out.String(), "Working tree clean") {
		t.Fatalf("expected git error or clean in non-git dir, got err=%v out=%s", err, out.String())
	}
}

func TestNativeCLIShellCommand(t *testing.T) {
	t.Setenv("PRAIMATE_HOME", t.TempDir())
	c, closeCore, err := openCore(bufio.NewReader(strings.NewReader("test-password\n")), true, false)
	if err != nil {
		t.Fatal(err)
	}
	defer closeCore()
	ctx := context.Background()
	ws := t.TempDir()
	chat, err := c.CreateChat(ctx, core.CreateChatRequest{CLIAgent: "praimate-cli", WorkspacePath: ws})
	if err != nil {
		t.Fatal(err)
	}

	turn, err := c.RunChatCommand(ctx, chat.ID, "echo hello-praimate")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(turn.Reply, "hello-praimate") {
		t.Fatalf("unexpected shell output: %s", turn.Reply)
	}
	messages, err := c.ListMessages(ctx, chat.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) < 2 || messages[len(messages)-1].Role != "command" {
		t.Fatalf("shell turn was not persisted as command: %+v", messages)
	}
}

func TestNativeCLIRepoMapCommandAndSystemPrompt(t *testing.T) {
	t.Setenv("PRAIMATE_HOME", t.TempDir())
	c, closeCore, err := openCore(bufio.NewReader(strings.NewReader("test-password\n")), true, false)
	if err != nil {
		t.Fatal(err)
	}
	defer closeCore()
	ctx := context.Background()
	ws := t.TempDir()
	sourceFile := filepath.Join(ws, "main.go")
	if err := os.WriteFile(sourceFile, []byte("package main\n\nfunc EntryPoint() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	chat, err := c.CreateChat(ctx, core.CreateChatRequest{CLIAgent: "praimate-cli", WorkspacePath: ws})
	if err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	// 1. /repomap initially shows repo map but disabled in system prompt
	if err := command(ctx, c, chat.ID, "/repomap", &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "AMBIENT REPO MAP") || !strings.Contains(out.String(), "main.go (EntryPoint)") {
		t.Fatalf("unexpected repomap output: %s", out.String())
	}

	// 2. /repomap on enables it
	out.Reset()
	if err := command(ctx, c, chat.ID, "/repomap on", &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "enabled for this chat") {
		t.Fatalf("unexpected repomap on output: %s", out.String())
	}

	// 3. chatSystemPrompt now contains the repo map
	refetched, err := c.GetChat(ctx, chat.ID)
	if err != nil {
		t.Fatal(err)
	}
	system, _, _, err := chatSystemPrompt(ctx, c, refetched)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(system, "[Ambient Repo Map:") || !strings.Contains(system, "main.go (EntryPoint)") {
		t.Fatalf("system prompt missing repo map: %s", system)
	}

	// 4. /repomap off disables it
	out.Reset()
	if err := command(ctx, c, chat.ID, "/repomap off", &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "disabled") {
		t.Fatalf("unexpected repomap off output: %s", out.String())
	}
	refetched, err = c.GetChat(ctx, chat.ID)
	if err != nil {
		t.Fatal(err)
	}
	system, _, _, err = chatSystemPrompt(ctx, c, refetched)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(system, "[Ambient Repo Map:") {
		t.Fatalf("system prompt still contains disabled repo map: %s", system)
	}
}
