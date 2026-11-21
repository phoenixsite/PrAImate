package studio

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestRecentProjectsRejectCorruptionAndKeepOriginal(t *testing.T) {
	t.Setenv("PRAIMATE_HOME", t.TempDir())
	dir, err := StudioDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "recent_projects.json")
	original := []byte(`{"unfinished":`)
	if err := os.WriteFile(file, original, 0600); err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(nil)
	if _, err := mgr.ListRecentProjects(); err == nil {
		t.Fatal("corrupt history must not be presented as an empty list")
	}
	if err := mgr.SaveRecentProject(t.TempDir()); err == nil {
		t.Fatal("saving must not silently replace corrupt history")
	}
	got, err := os.ReadFile(file)
	if err != nil || string(got) != string(original) {
		t.Fatalf("original history changed: %q, %v", got, err)
	}
}

func TestRecentProjectsKeepDirectoriesOnly(t *testing.T) {
	t.Setenv("PRAIMATE_HOME", t.TempDir())
	mgr := NewManager(nil)
	dir := t.TempDir()
	file := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(file, []byte("not a workspace"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := mgr.SaveRecentProject(file); err != nil {
		t.Fatal(err)
	}
	if err := mgr.SaveRecentProject(dir); err != nil {
		t.Fatal(err)
	}
	projects, err := mgr.ListRecentProjects()
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 || projects[0] != dir {
		t.Fatalf("invalid workspace listed: %v", projects)
	}
}

func TestRecentProjectsConcurrentManagersPreserveUpdates(t *testing.T) {
	t.Setenv("PRAIMATE_HOME", t.TempDir())
	root := t.TempDir()
	const count = 16
	var wg sync.WaitGroup
	for i := range count {
		dir := filepath.Join(root, fmt.Sprintf("project-%d", i))
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := NewManager(nil).SaveRecentProject(dir); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	projects, err := NewManager(nil).ListRecentProjects()
	if err != nil || len(projects) != count {
		t.Fatalf("lost updates: %d/%d projects, %v", len(projects), count, err)
	}
}

func TestRecentProjectsReadLegacyPathList(t *testing.T) {
	t.Setenv("PRAIMATE_HOME", t.TempDir())
	dir, err := StudioDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	project := t.TempDir()
	legacy := fmt.Sprintf("[%q]", project)
	if err := os.WriteFile(filepath.Join(dir, "recent_projects.json"), []byte(legacy), 0600); err != nil {
		t.Fatal(err)
	}
	entries, err := NewManager(nil).ListRecentProjectEntries()
	if err != nil || len(entries) != 1 || entries[0].Path != project || entries[0].Configured {
		t.Fatalf("legacy history not read as unconfigured entry: %+v, %v", entries, err)
	}
}

func TestRecentProjectsRememberLaunchConfiguration(t *testing.T) {
	t.Setenv("PRAIMATE_HOME", t.TempDir())
	mgr := NewManager(nil)
	first, second := t.TempDir(), t.TempDir()
	want := RecentProject{Path: first, CLI: "claude", Model: "sonnet", AgentID: "reviewer", Tools: ""}
	if err := mgr.SaveRecentProjectConfig(want); err != nil {
		t.Fatal(err)
	}
	if err := mgr.SaveRecentProjectConfig(RecentProject{Path: second, CLI: "codex", Tools: "full"}); err != nil {
		t.Fatal(err)
	}
	// A path-only registration (Studio extension RPC) must move the project
	// first without discarding the configuration it was opened with.
	if err := mgr.SaveRecentProject(first); err != nil {
		t.Fatal(err)
	}
	entries, err := mgr.ListRecentProjectEntries()
	if err != nil || len(entries) != 2 {
		t.Fatalf("unexpected entries: %+v, %v", entries, err)
	}
	want.Configured = true
	if entries[0] != want {
		t.Fatalf("configuration lost: got %+v want %+v", entries[0], want)
	}
	if entries[1].Path != second || entries[1].CLI != "codex" || entries[1].Tools != "full" || !entries[1].Configured {
		t.Fatalf("second entry changed: %+v", entries[1])
	}
	// Reopening with a new configuration replaces the saved one.
	if err := mgr.SaveRecentProjectConfig(RecentProject{Path: second, CLI: "claude", Tools: "edits"}); err != nil {
		t.Fatal(err)
	}
	entries, _ = mgr.ListRecentProjectEntries()
	if entries[0].Path != second || entries[0].CLI != "claude" || entries[0].Tools != "edits" {
		t.Fatalf("configuration not replaced: %+v", entries[0])
	}
}

