package core

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGenerateRepoMap(t *testing.T) {
	dir := t.TempDir()

	// Create test Go file
	goCode := `package testpkg

type MyService struct{}

func StartService() error { return nil }
func internalHelper() {}
`
	if err := os.WriteFile(filepath.Join(dir, "service.go"), []byte(goCode), 0644); err != nil {
		t.Fatal(err)
	}

	// Create test Python file
	pyCode := `class Worker:
    def process(self):
        pass

def standalone_task():
    pass
`
	if err := os.WriteFile(filepath.Join(dir, "worker.py"), []byte(pyCode), 0644); err != nil {
		t.Fatal(err)
	}

	// Create test TypeScript file
	tsCode := `export interface Config {
    port: number;
}

export function initializeApp() {}
`
	if err := os.WriteFile(filepath.Join(dir, "app.ts"), []byte(tsCode), 0644); err != nil {
		t.Fatal(err)
	}

	repoMap := GenerateRepoMap(dir, 500)
	if !strings.Contains(repoMap, "[Ambient Repo Map:") {
		t.Fatalf("missing repo map header: %s", repoMap)
	}
	if !strings.Contains(repoMap, "service.go (StartService)") {
		t.Fatalf("missing Go symbols: %s", repoMap)
	}
	if !strings.Contains(repoMap, "worker.py") || !strings.Contains(repoMap, "Worker") {
		t.Fatalf("missing Python symbols: %s", repoMap)
	}
	if !strings.Contains(repoMap, "app.ts") || !strings.Contains(repoMap, "Config") {
		t.Fatalf("missing TS symbols: %s", repoMap)
	}

	// Test caching and invalidation
	InvalidateRepoMap(dir)
	reloaded := GenerateRepoMap(dir, 500)
	if reloaded != repoMap {
		t.Fatalf("cache invalidation returned different map: %s vs %s", reloaded, repoMap)
	}
}

func TestRepoMapRespectsEachBudget(t *testing.T) {
	dir := t.TempDir()
	for i := range 30 {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("service_%02d.go", i)), []byte("package app\nfunc ExportedService() {}\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	large := GenerateRepoMap(dir, 2000)
	for _, budget := range []int{100, 1, 2000} {
		got := GenerateRepoMap(dir, budget)
		if n := nativeTextTokens(got); n > budget {
			t.Fatalf("cached map exceeds requested budget: %d > %d", n, budget)
		}
		if budget == 2000 && got != large {
			t.Fatal("small-budget request poisoned the cache for a later larger request")
		}
	}
}

func TestRepoMapRefreshesNestedFiles(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "src")
	if err := os.Mkdir(sub, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(sub, "service.go")
	if err := os.WriteFile(path, []byte("package app\nfunc Before() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := GenerateRepoMap(dir, 500); !strings.Contains(got, "Before") {
		t.Fatalf("missing initial symbol: %s", got)
	}
	if err := os.WriteFile(path, []byte("package app\nfunc Updated() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, time.Now().Add(time.Second), time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if got := GenerateRepoMap(dir, 500); !strings.Contains(got, "Updated") || strings.Contains(got, "Before") {
		t.Fatalf("nested edit was hidden by the cache: %s", got)
	}
	if err := os.WriteFile(filepath.Join(sub, "added.py"), []byte("def added(): pass\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := GenerateRepoMap(dir, 500); !strings.Contains(got, "added.py") {
		t.Fatalf("nested addition was hidden by the cache: %s", got)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if got := GenerateRepoMap(dir, 500); strings.Contains(got, "service.go") {
		t.Fatalf("deleted file remains in the cache: %s", got)
	}
}

func TestRepoMapHiddenWorkspaceAndSymlinks(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".worktree")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "app.go"), []byte("package app\nfunc Public() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := GenerateRepoMap(dir, 500); !strings.Contains(got, "app.go (Public)") {
		t.Fatalf("hidden workspace itself was skipped: %s", got)
	}
	outside := filepath.Join(t.TempDir(), "outside.go")
	if err := os.WriteFile(outside, []byte("package private\nfunc OutsideWorkspace() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "linked.go")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	InvalidateRepoMap(dir)
	if got := GenerateRepoMap(dir, 500); strings.Contains(got, "OutsideWorkspace") || strings.Contains(got, "linked.go") {
		t.Fatalf("ambient map followed a symlink outside the workspace: %s", got)
	}
}
