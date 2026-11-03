package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
