package core

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// A workpath template must convert into an agent whose instructions
// compose the persona+mission+playbook+rules and whose knowledge base
// carries knowledge/, tools/ and agents/ content.
func TestImportWorkpathTemplate(t *testing.T) {
	withTempConfigDir(t)
	ctx := context.Background()
	c, _ := New(Options{Store: openTempStore(t)})

	tpl := t.TempDir()
	writeFile(t, tpl, "workpath.json", `{"description":"Reverse engineering with Ghidra"}`)
	writeFile(t, tpl, "personality.md", "<!--\nformat docs here\n-->\nYou are a careful RE analyst.")
	writeFile(t, tpl, "mission.md", "# reverse\nRecover behavior from binaries.")
	writeFile(t, tpl, "playbook.md", "Stage 1: triage. Stage 2: decompile.")
	writeFile(t, tpl, "rules.md", "Never execute unknown binaries.")
	writeFile(t, tpl, "knowledge/notes.md", "Ghidra decompiler tips.")
	writeFile(t, tpl, "tools/triage.sh", "#!/bin/sh\necho triage\n")
	writeFile(t, tpl, "agents/operator.md", "Ghidra operator sub-agent.")
	// Executable bit must survive on tools/.
	_ = os.Chmod(filepath.Join(tpl, "tools", "triage.sh"), 0o755)

	agent, err := c.ImportWorkpathTemplate(ctx, tpl, "", nil)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if agent.Knowledge != "raw" {
		t.Errorf("knowledge mode = %q", agent.Knowledge)
	}
	if agent.Description != "Reverse engineering with Ghidra" {
		t.Errorf("description = %q", agent.Description)
	}
	// Persona comment block must be stripped; all sections present.
	ins := agent.Instructions
	if strings.Contains(ins, "format docs here") {
		t.Error("persona format-comment not stripped")
	}
	for _, want := range []string{"careful RE analyst", "Recover behavior", "Stage 1: triage", "Never execute", "tools/", "subagents/"} {
		if !strings.Contains(ins, want) {
			t.Errorf("instructions missing %q:\n%s", want, ins)
		}
	}

	// Knowledge tree: notes.md at root, tools/triage.sh, subagents/operator.md.
	files, _ := ListAgentKnowledge(agent.ID)
	got := strings.Join(files, ",")
	for _, want := range []string{"notes.md", "tools/triage.sh", "subagents/operator.md"} {
		if !strings.Contains(got, want) {
			t.Errorf("knowledge missing %q; got %v", want, files)
		}
	}
	// Executable bit preserved on platforms supporting POSIX file mode bits.
	if runtime.GOOS != "windows" {
		dir, _ := AgentKnowledgeDir(agent.ID)
		if fi, err := os.Stat(filepath.Join(dir, "tools", "triage.sh")); err != nil || fi.Mode().Perm()&0o100 == 0 {
			t.Errorf("tools/triage.sh lost its executable bit (mode=%v err=%v)", fi.Mode(), err)
		}
	}

	// Non-template dir rejected.
	if _, err := c.ImportWorkpathTemplate(ctx, t.TempDir(), "", nil); err == nil {
		t.Error("empty dir should not import as a template")
	}
}

func writeFile(t *testing.T, base, rel, content string) {
	t.Helper()
	p := filepath.Join(base, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Nested-layout templates (real files under <name>/workpath/) must use
// the OUTER dir name as the agent id, not "workpath".
func TestImportWorkpathTemplate_NestedLayout(t *testing.T) {
	withTempConfigDir(t)
	ctx := context.Background()
	c, _ := New(Options{Store: openTempStore(t)})

	mk := func(name string) string {
		root := filepath.Join(t.TempDir(), name)
		writeFile(t, root, "workpath/workpath.json", `{"description":"`+name+` dev"}`)
		writeFile(t, root, "workpath/mission.md", "# "+name+"\nWork on "+name+".")
		return root
	}
	a, err := c.ImportWorkpathTemplate(ctx, mk("clade-dev"), "", nil)
	if err != nil {
		t.Fatalf("clade-dev: %v", err)
	}
	b, err := c.ImportWorkpathTemplate(ctx, mk("cve-parser-dev"), "", nil)
	if err != nil {
		t.Fatalf("cve-parser-dev: %v", err)
	}
	if a.ID != "clade-dev" || b.ID != "cve-parser-dev" {
		t.Fatalf("nested ids collided: %q, %q", a.ID, b.ID)
	}
	if !strings.Contains(a.Instructions, "Work on clade-dev") {
		t.Errorf("nested mission not read: %q", a.Instructions)
	}
}
