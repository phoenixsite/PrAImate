package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/sPROFFEs/PrAImate/internal/core"
	workerruntime "github.com/sPROFFEs/PrAImate/internal/runtime"
)

func TestReplacementSupportsProjectTextAndMetadata(t *testing.T) {
	for _, name := range []string{"README.md", "package.json", "config.yaml", "config.toml", "AndroidManifest.xml", "build.gradle.kts", "Dockerfile", "icon.svg", ".gitignore", ".gitattributes", ".dockerignore", ".editorconfig"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, name)
			if err := os.WriteFile(path, []byte("before\n"), 0640); err != nil {
				t.Fatal(err)
			}
			// Creation respects umask; set the mode whose preservation is tested.
			if err := os.Chmod(path, 0640); err != nil {
				t.Fatal(err)
			}
			if err := replaceSource(root, name, "before", "after"); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != "after\n" {
				t.Fatalf("edit failed: %q %v", data, err)
			}
			info, _ := os.Stat(path)
			if runtime.GOOS != "windows" && info.Mode().Perm() != 0640 {
				t.Fatalf("mode changed: %v", info.Mode())
			}
		})
	}
	root := t.TempDir()
	content := strings.Repeat("line\n", 8000) + "unique target\n"
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	if err := replaceSource(root, "README.md", "unique target", "updated target"); err != nil {
		t.Fatalf("bounded edit of a large text file: %v", err)
	}
}

func TestReplacementValidationCanBeCorrectedWithoutRestart(t *testing.T) {
	cfg := testConfig(t)
	cfg.Profiles[0].AllowEdits = true
	if err := os.WriteFile(filepath.Join(cfg.Workspace, "main.go"), []byte("package main\n"), 0600); err != nil {
		t.Fatal(err)
	}
	worker := &fakeWorker{responses: []string{
		`{"action":"replace","path":"main.go","oldText":"stale source","newText":"package broken"}`,
		`{"action":"replace","path":"main.go","oldText":"package main","newText":"package fixed"}`,
		`{"action":"final","content":"corrected the edit"}`,
	}}
	runner := Runner{Resolve: func(Profile) (workerruntime.Runtime, error) { return worker, nil }}
	result, err := runner.Run(context.Background(), cfg, "Edit the file")
	if err != nil || result != "corrected the edit" {
		t.Fatalf("result=%q err=%v", result, err)
	}
	if len(worker.requests) != 3 || !strings.Contains(worker.requests[1].Task, "main.go") || !strings.Contains(worker.requests[1].Task, "No edit was applied") {
		t.Fatal("model did not receive actionable failure evidence")
	}
}

func TestReplacementRejectsBinaryAndSensitiveFiles(t *testing.T) {
	for _, tc := range []struct{ path, content string }{{"image.bin", "before\x00binary"}, {"image.dat", "before\xffbinary"}, {".env", "before"}, {"credentials.json", "before"}, {"private.pem", "before"}, {".git/config", "before"}} {
		t.Run(tc.path, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, tc.path)
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(tc.content), 0600); err != nil {
				t.Fatal(err)
			}
			if err := replaceSource(root, tc.path, "before", "after"); err == nil {
				t.Fatal("protected or binary file edited")
			}
			data, _ := os.ReadFile(path)
			if string(data) != tc.content {
				t.Fatal("failed edit changed the file")
			}
		})
	}
}

func TestReplacementRepeatedValidationFailureStopsWithPath(t *testing.T) {
	cfg := testConfig(t)
	cfg.Profiles[0].AllowEdits = true
	path := filepath.Join(cfg.Workspace, "README.md")
	if err := os.WriteFile(path, []byte("unchanged\n"), 0600); err != nil {
		t.Fatal(err)
	}
	response := `{"action":"replace","path":"README.md","oldText":"missing","newText":"changed"}`
	worker := &fakeWorker{responses: []string{response, response}}
	var events []Event
	runner := Runner{Resolve: func(Profile) (workerruntime.Runtime, error) { return worker, nil }, Emit: func(e Event) { events = append(events, e) }}
	_, err := runner.Run(context.Background(), cfg, "Edit the document")
	if !errors.Is(err, errWorkerEditInput) || !strings.Contains(err.Error(), "README.md") || len(worker.requests) != 2 || canAutomaticallyRetry(context.Background(), err.Error()) {
		t.Fatalf("unsafe or unhelpful retry: calls=%d error=%v", len(worker.requests), err)
	}
	starts, failures := 0, 0
	for _, event := range events {
		if event.Kind == "tool_start" && strings.Contains(event.Text, "README.md") {
			starts++
		}
		if event.Kind == "error" && strings.Contains(event.Text, "README.md") {
			failures++
		}
	}
	if starts != 2 || failures != 2 {
		t.Fatalf("operation diagnostics: starts=%d failures=%d", starts, failures)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "unchanged\n" {
		t.Fatal("failed replacement changed the file")
	}
}

func TestReplacementBoundsAndWorkspaceContainment(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "README.md")
	for _, content := range []string{strings.Repeat("x", maxWorkerEditBytes+1), "before before", "before"} {
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		newText := "after"
		if content == "before" {
			newText += "\x00"
		}
		if err := replaceSource(root, "README.md", "before", newText); !errors.Is(err, errWorkerEditInput) {
			t.Fatalf("invalid text accepted: %v", err)
		}
		data, _ := os.ReadFile(path)
		if string(data) != content {
			t.Fatal("invalid replacement changed the file")
		}
	}
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("before"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []string{outside, "../outside.md"} {
		if err := replaceSource(root, candidate, "before", "after"); err == nil {
			t.Fatal("outside workspace path accepted")
		}
	}
	if err := os.Symlink(outside, filepath.Join(root, "link.md")); err == nil {
		if err := replaceSource(root, "link.md", "before", "after"); err == nil {
			t.Fatal("outside workspace symlink accepted")
		}
	}
	data, _ := os.ReadFile(outside)
	if string(data) != "before" {
		t.Fatal("outside workspace file changed")
	}
}

func TestReplacementRejectsFIFOWithoutBlocking(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX named pipe check")
	}
	mkfifo, err := exec.LookPath("mkfifo")
	if err != nil {
		t.Skip("mkfifo is not available")
	}
	root := t.TempDir()
	path := filepath.Join(root, "pipe.txt")
	if err := exec.Command(mkfifo, path).Run(); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { result <- replaceSource(root, "pipe.txt", "before", "after") }()
	select {
	case err := <-result:
		if !errors.Is(err, errWorkerEditInput) {
			t.Fatalf("nonregular file accepted: %v", err)
		}
	case <-time.After(time.Second):
		// Release a blocked reader before reporting the regression.
		writer, _ := os.OpenFile(path, os.O_RDWR, 0600)
		if writer != nil {
			_ = writer.Close()
		}
		t.Fatal("opening a FIFO blocked replacement validation")
	}
}

type retainedReplacementAdapter struct{ dagAdapter }

func (a *retainedReplacementAdapter) SingleShot(_ context.Context, opts core.SingleShotOpts) (*core.Reply, error) {
	if strings.Contains(opts.Message, "Verify recovered dependency") {
		data, err := os.ReadFile(filepath.Join(opts.Cwd, ".gitignore"))
		if err != nil || string(data) != "updated\n" {
			return nil, fmt.Errorf("dependency edit missing: %q %v", data, err)
		}
		if _, err := os.Stat(filepath.Join(opts.Cwd, "a.go")); !os.IsNotExist(err) {
			return nil, errors.New("dependency lost the staged cleanup")
		}
		return &core.Reply{Text: `{"action":"final","content":"dependency verified"}`}, nil
	}
	if strings.Contains(opts.Message, "Exact replacement applied") {
		return &core.Reply{Text: `{"action":"final","content":"metadata updated"}`}, nil
	}
	return &core.Reply{Text: `{"action":"replace","path":".gitignore","oldText":"before","newText":"updated"}`}, nil
}

func TestReplacementContinuesRetainedDAGAfterExhaustedRetries(t *testing.T) {
	cfg := testConfig(t)
	cfg.Workspace = gitFixture(t)
	cfg.MaxRetries = 2
	cfg.Profiles[1].Runtime, cfg.Profiles[1].CLI, cfg.Profiles[1].Endpoint, cfg.Profiles[1].MaxOutputTokens = "cli", "opencode", "", 0
	cfg.Profiles[1].AllowEdits = true
	m := retryManagerFixture(t, &retainedReplacementAdapter{dagAdapter{name: "opencode"}})
	ctx := context.Background()
	id := "retained-replacement"
	if _, err := m.core.CreateChat(ctx, core.CreateChatRequest{ID: id, Title: "Retained replacement", WorkspacePath: cfg.Workspace, CLIAgent: "workers", Settings: core.ChatSettings{Surface: "workers"}}); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(cfg)
	if _, err := m.core.AddMessage(ctx, id, "system", string(raw), nil); err != nil {
		t.Fatal(err)
	}
	w := WorktreeManager{Workspace: cfg.Workspace, RunID: id}
	base, branch, err := w.Repository(ctx)
	if err != nil {
		t.Fatal(err)
	}
	worktree, err := w.Create(ctx, "task-n", base)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktree.Path, ".gitignore"), []byte("before\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktree.Path, "partial.txt"), []byte("retain previous work"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(worktree.Path, "a.go")); err != nil {
		t.Fatal(err)
	}
	if _, err := gitCommand(ctx, worktree.Path, "add", "-u"); err != nil {
		t.Fatal(err)
	}
	m.runs[id] = &Run{ID: id, Title: "Retained replacement", Workspace: cfg.Workspace, Profiles: cfg.Profiles, config: cfg, MaxRetries: 2, Status: "failed", DAG: &DAG{BaseCommit: base, TargetBranch: branch, MaxParallel: 1, Tasks: []DAGTask{
		{ID: "done", Description: "Already complete", Worker: WorkerConfig{ProfileID: Middle}, Status: "completed", Review: "accepted", Output: "previous findings", Result: &TaskResult{TaskID: "done", BaseCommit: base, ResultCommit: base}},
		{ID: "task-n", Description: "Update cleanup metadata", Dependencies: []string{"done"}, Worker: WorkerConfig{ProfileID: Middle}, Status: "failed", FailurePhase: "execution", Error: "replacement requires a source code file", AutoRetriesUsed: 2, Worktree: worktree},
		{ID: "child", Description: "Verify recovered dependency", Dependencies: []string{"task-n"}, Worker: WorkerConfig{ProfileID: Middle}, Status: "blocked"},
	}}}
	if err := m.RetryDAGTask(id, "task-n"); err != nil {
		t.Fatal(err)
	}
	if err := m.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	m = NewManager(m.core, ctx)
	t.Cleanup(func() {
		if err := m.Stop(ctx); err != nil {
			t.Error(err)
		}
	})
	if err := m.ExecuteDAG(id, nil); err != nil {
		t.Fatal(err)
	}
	run := waitGraph(t, m, id, "review")
	if run.DAG.Tasks[0].Output != "previous findings" || run.DAG.Tasks[0].Review != "accepted" || run.DAG.Tasks[1].AutoRetriesUsed != 2 || run.DAG.Tasks[1].Worktree.Path != worktree.Path || run.DAG.Tasks[2].Status != "completed" {
		t.Fatalf("recovery lost work or blocked a dependent: %+v", run.DAG.Tasks)
	}
	data, err := os.ReadFile(filepath.Join(worktree.Path, "partial.txt"))
	if err != nil || string(data) != "retain previous work" {
		t.Fatalf("partial work lost: %q %v", data, err)
	}
}
