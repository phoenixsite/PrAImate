package launcher

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// helper: a chat freshly cloned from the bundled "reversing" template,
// with the given settings stamped on. Returned as a Workspace (via the
// AsWorkspace bridge) so the decorate tests can stay on the existing
// PrepareSandbox interface.
func freshWorkspace(t *testing.T, settings WorkspaceSettings) Workspace {
	t.Helper()
	src := samplesDir(t)
	if _, err := os.Stat(src); err != nil {
		t.Skipf("no samples at %s", src)
	}
	root := t.TempDir()
	t.Cleanup(func() {
		_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err == nil && info != nil && info.Mode().IsRegular() {
				_ = os.Chmod(path, info.Mode().Perm()|0600)
			}
			return nil
		})
	})
	if _, err := SeedSamples(root, []string{src}); err != nil {
		t.Fatal(err)
	}
	tpl, err := LoadTemplate(root, "reversing")
	if err != nil || tpl == nil {
		t.Fatalf("LoadTemplate(reversing): %v %v", err, tpl)
	}
	chat, err := CreateChat(root, *tpl, "decorate-test", AgentClaude)
	if err != nil {
		t.Fatalf("CreateChat: %v", err)
	}
	chat.Settings = settings
	if err := SaveChatSettings(chat); err != nil {
		t.Fatal(err)
	}
	return chat.AsWorkspace()
}

func TestDecorate_LanguageDirectivePrependedToSKILLmd(t *testing.T) {
	ws := freshWorkspace(t, WorkspaceSettings{Language: "Italian"})
	claude := Agent{
		ID: AgentClaude, Label: "Claude", Binary: "claude",
		WpcTarget: "claude", Available: true,
	}
	if err := PrepareSandbox(ws, claude); err != nil {
		t.Fatalf("PrepareSandbox: %v", err)
	}
	path := filepath.Join(ws.SandboxDir, ".claude", "skills", "reversing", "SKILL.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "respond in Italian") {
		t.Errorf("SKILL.md missing language directive:\n%s", raw)
	}
	// Frontmatter still intact.
	if !strings.HasPrefix(string(raw), "---\n") {
		t.Error("frontmatter clobbered")
	}
}

func TestDecorate_LanguageDirectiveInAGENTSmd(t *testing.T) {
	ws := freshWorkspace(t, WorkspaceSettings{Language: "Japanese"})
	codex := Agent{
		ID: AgentCodex, Label: "Codex", Binary: "codex",
		WpcTarget: "codex", Available: true,
	}
	if err := PrepareSandbox(ws, codex); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(ws.SandboxDir, "AGENTS.md"))
	if !strings.Contains(string(raw), "respond in Japanese") {
		t.Errorf("AGENTS.md missing language directive:\n%s", raw)
	}
}

func TestDecorate_MemoryStagedAndSyncedBack(t *testing.T) {
	ws := freshWorkspace(t, WorkspaceSettings{MemoryEnabled: true})
	claude := Agent{ID: AgentClaude, Binary: "claude", WpcTarget: "claude", Available: true}
	if err := PrepareSandbox(ws, claude); err != nil {
		t.Fatal(err)
	}
	// MEMORY.md initialized at the workspace root and copied to sandbox.
	if _, err := os.Stat(filepath.Join(ws.Root, "MEMORY.md")); err != nil {
		t.Errorf("workspace MEMORY.md missing: %v", err)
	}
	sandboxMem := filepath.Join(ws.SandboxDir, "MEMORY.md")
	if _, err := os.Stat(sandboxMem); err != nil {
		t.Errorf("sandbox MEMORY.md missing: %v", err)
	}

	// Simulate agent editing the sandbox memory.
	time.Sleep(20 * time.Millisecond) // ensure mtime difference
	if err := os.WriteFile(sandboxMem, []byte("# Memory\n\n- agent wrote this\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SyncMemoryBack(ws); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(ws.Root, "MEMORY.md"))
	if !strings.Contains(string(raw), "agent wrote this") {
		t.Errorf("memory not synced back:\n%s", raw)
	}
}

func TestDecorate_ChatLogDirCreatedPerLaunch(t *testing.T) {
	ws := freshWorkspace(t, WorkspaceSettings{})
	codex := Agent{ID: AgentCodex, Binary: "codex", WpcTarget: "codex", Available: true}
	if err := PrepareSandbox(ws, codex); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(ws.ChatsDir)
	if err != nil {
		t.Fatalf("ChatsDir not created: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no chat session dir created")
	}
	// session.json valid.
	sess := filepath.Join(ws.ChatsDir, entries[0].Name(), "session.json")
	raw, err := os.ReadFile(sess)
	if err != nil {
		t.Fatalf("session.json missing: %v", err)
	}
	var rec SessionRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatalf("session.json not valid: %v", err)
	}
	if rec.Agent != "codex" || rec.WpcTarget != "codex" {
		t.Errorf("rec = %+v", rec)
	}
}

func TestDecorate_OnlineSkillsClonedIntoClaudeSkillsDir(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	// Build a local git "remote" repo.
	src := filepath.Join(t.TempDir(), "demoskill")
	t.Cleanup(func() {
		_ = filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
			if err == nil && info != nil && info.Mode().IsRegular() {
				_ = os.Chmod(path, info.Mode().Perm()|0600)
			}
			return nil
		})
	})
	_ = os.MkdirAll(src, 0o755)
	_ = os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("# demoskill\n"), 0o644)
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"add", "."},
		{"-c", "user.email=t@e", "-c", "user.name=t", "commit", "-qm", "init"},
	} {
		c := exec.Command("git", args...)
		c.Dir = src
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	ws := freshWorkspace(t, WorkspaceSettings{OnlineSkills: []string{src}})
	claude := Agent{ID: AgentClaude, Binary: "claude", WpcTarget: "claude", Available: true}
	if err := PrepareSandbox(ws, claude); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(ws.SandboxDir, ".claude", "skills", "demoskill", "SKILL.md")
	if _, err := os.Stat(want); err != nil {
		t.Errorf("expected cloned skill at %s: %v", want, err)
	}
}

func TestDecorate_MemoryDirectiveInjectedIntoAgentInstructions(t *testing.T) {
	// User report: MEMORY.md was staged in the sandbox but the agent
	// never knew about it. After this fix the compiled SKILL.md /
	// AGENTS.md / GEMINI.md each carry an explicit "Persistent memory"
	// section pointing the agent at MEMORY.md.
	cases := []struct {
		name        string
		agent       Agent
		wantSection string // path under sandbox containing the directive
	}{
		{
			name: "claude",
			agent: Agent{
				ID: AgentClaude, Binary: "claude", WpcTarget: "claude", Available: true,
			},
			wantSection: filepath.Join(".claude", "skills", "reversing", "SKILL.md"),
		},
		{
			name: "codex",
			agent: Agent{
				ID: AgentCodex, Binary: "codex", WpcTarget: "codex", Available: true,
			},
			wantSection: "AGENTS.md",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ws := freshWorkspace(t, WorkspaceSettings{MemoryEnabled: true})
			if err := PrepareSandbox(ws, tc.agent); err != nil {
				t.Fatalf("PrepareSandbox: %v", err)
			}
			body, err := os.ReadFile(filepath.Join(ws.SandboxDir, tc.wantSection))
			if err != nil {
				t.Fatalf("read %s: %v", tc.wantSection, err)
			}
			if !strings.Contains(string(body), "Persistent memory — required workflow") {
				t.Errorf("expected memory directive in %s; got:\n%s", tc.wantSection, body)
			}
			if !strings.Contains(string(body), "MEMORY.md") {
				t.Errorf("memory directive must mention MEMORY.md literally")
			}
		})
	}
}

func TestDecorate_MemoryDirectiveIdempotent(t *testing.T) {
	ws := freshWorkspace(t, WorkspaceSettings{MemoryEnabled: true})
	agent := Agent{ID: AgentClaude, Binary: "claude", WpcTarget: "claude", Available: true}
	if err := PrepareSandbox(ws, agent); err != nil {
		t.Fatal(err)
	}
	if err := PrepareSandbox(ws, agent); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(filepath.Join(ws.SandboxDir, ".claude", "skills", "reversing", "SKILL.md"))
	if count := strings.Count(string(body), "## Persistent memory — required workflow"); count != 1 {
		t.Errorf("memory directive appended %d times, want exactly 1", count)
	}
}

func TestDecorate_MemoryStagedFileGetsSessionMarker(t *testing.T) {
	// Each launch must append a "## YYYY-MM-DD HH:MM — Session opened"
	// marker to MEMORY.md in the sandbox, so the file grows visibly
	// even if the agent itself writes nothing.
	ws := freshWorkspace(t, WorkspaceSettings{MemoryEnabled: true})
	agent := Agent{ID: AgentClaude, Binary: "claude", WpcTarget: "claude", Available: true}
	if err := PrepareSandbox(ws, agent); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(ws.SandboxDir, "MEMORY.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "— Session opened") {
		t.Errorf("expected session marker in MEMORY.md; got:\n%s", body)
	}
	// And re-running PrepareSandbox appends a second marker (multiple
	// launches → multiple markers).
	if err := PrepareSandbox(ws, agent); err != nil {
		t.Fatal(err)
	}
	body2, _ := os.ReadFile(filepath.Join(ws.SandboxDir, "MEMORY.md"))
	if got := strings.Count(string(body2), "— Session opened"); got != 2 {
		t.Errorf("expected 2 markers after 2 launches, got %d", got)
	}
}

func TestDecorate_PersonalityPrependedAfterFrontmatter(t *testing.T) {
	ws := freshWorkspace(t, WorkspaceSettings{})
	// Write a personality file the loader will pick up.
	persona := "You are a brutally honest senior architect. Do not soften the truth."
	if err := os.WriteFile(filepath.Join(ws.WorkpathDir, "personality.md"), []byte(persona), 0o644); err != nil {
		t.Fatal(err)
	}
	agent := Agent{ID: AgentClaude, Binary: "claude", WpcTarget: "claude", Available: true}
	if err := PrepareSandbox(ws, agent); err != nil {
		t.Fatal(err)
	}
	skill := filepath.Join(ws.SandboxDir, ".claude", "skills", "reversing", "SKILL.md")
	body, err := os.ReadFile(skill)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "## Persona") {
		t.Errorf("expected ## Persona section; got:\n%s", body)
	}
	if !strings.Contains(string(body), persona) {
		t.Errorf("persona body not injected verbatim")
	}
	// Must come AFTER the YAML frontmatter so the Claude skill loader
	// still parses correctly.
	frontEnd := strings.Index(string(body), "\n---\n")
	personaIdx := strings.Index(string(body), "## Persona")
	if personaIdx < frontEnd {
		t.Errorf("persona placed before frontmatter end (clobbers YAML)")
	}
}

func TestDecorate_PersonalityCommentsOnlyIsNoOp(t *testing.T) {
	// The auto-scaffolded placeholder is HTML-comments only — the
	// decorator must NOT inject "## Persona" in that case.
	ws := freshWorkspace(t, WorkspaceSettings{})
	commentOnly := "<!-- placeholder -->\n\n<!-- another -->\n"
	if err := os.WriteFile(filepath.Join(ws.WorkpathDir, "personality.md"), []byte(commentOnly), 0o644); err != nil {
		t.Fatal(err)
	}
	agent := Agent{ID: AgentClaude, Binary: "claude", WpcTarget: "claude", Available: true}
	if err := PrepareSandbox(ws, agent); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(filepath.Join(ws.SandboxDir, ".claude", "skills", "reversing", "SKILL.md"))
	if strings.Contains(string(body), "## Persona") {
		t.Errorf("comments-only personality.md should NOT inject a Persona section:\n%s", body)
	}
}

func TestStripHTMLComments(t *testing.T) {
	cases := map[string]string{
		"hello":                        "hello",
		"<!-- x -->hello":              "hello",
		"a <!-- x --> b <!-- y --> c":  "a  b  c",
		"<!-- unclosed":                "",
		"line1\n<!-- block -->\nline2": "line1\n\nline2",
	}
	for in, want := range cases {
		if got := stripHTMLComments(in); got != want {
			t.Errorf("stripHTMLComments(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDecorate_FailedOnlineSkillIsNonFatal(t *testing.T) {
	ws := freshWorkspace(t, WorkspaceSettings{OnlineSkills: []string{"/nonexistent/repo"}})
	claude := Agent{ID: AgentClaude, Binary: "claude", WpcTarget: "claude", Available: true}
	if err := PrepareSandbox(ws, claude); err != nil {
		// A failed online-skill clone must not break the launch.
		t.Fatalf("PrepareSandbox should not fail on bad skill URL, got: %v", err)
	}
	if len(LastDecorationNotes) == 0 {
		t.Error("expected LastDecorationNotes to record the failure")
	}
}

// Use context to silence unused-import warnings when subtests get skipped.
var _ = context.Background
