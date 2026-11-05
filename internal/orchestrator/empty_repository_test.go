package orchestrator

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestParallelWorkersInitializeEmptyRepositoryWithoutStagingFiles(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	t.Cleanup(func() {
		_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err == nil && info != nil && info.Mode().IsRegular() {
				_ = os.Chmod(path, info.Mode().Perm()|0600)
			}
			return nil
		})
	})
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if out, err := exec.Command("git", "init", "-b", "main", dir).CombinedOutput(); err != nil {
		t.Fatalf("init: %s: %v", out, err)
	}
	w := WorktreeManager{Workspace: dir, RunID: "empty-repository"}
	base, branch, err := w.Repository(ctx)
	if err != nil {
		t.Fatalf("empty repository should be usable: %v", err)
	}
	if base == "" || branch != "main" {
		t.Fatalf("invalid repository baseline: %q %q", base, branch)
	}
	files, err := gitCommand(ctx, dir, "ls-tree", "--name-only", base)
	if err != nil || files != "" {
		t.Fatalf("baseline must be empty: %q %v", files, err)
	}
	second, _, err := w.Repository(ctx)
	if err != nil || second != base {
		t.Fatalf("initialization must be idempotent: %q %v", second, err)
	}
	tree, err := w.Create(ctx, "first-task", base)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Remove(ctx, tree) })
	if err := os.WriteFile(filepath.Join(tree.Path, "result.md"), []byte("result"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := w.Complete(ctx, tree, WorkerConfig{})
	if err != nil {
		t.Fatal(err)
	}
	merge, err := w.Merge(ctx, branch, []string{result.ResultCommit})
	if merge != nil {
		t.Cleanup(func() { _ = w.Remove(ctx, merge) })
	}
	if err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "result.md")); err != nil || string(data) != "result" {
		t.Fatalf("merge from empty baseline failed: %q %v", data, err)
	}
}

func TestEmptyRepositoryWithUncommittedFilesIsNotInitialized(t *testing.T) {
	dir := t.TempDir()
	t.Cleanup(func() {
		_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err == nil && info != nil && info.Mode().IsRegular() {
				_ = os.Chmod(path, info.Mode().Perm()|0600)
			}
			return nil
		})
	})
	if out, err := exec.Command("git", "init", "-b", "main", dir).CombinedOutput(); err != nil {
		t.Fatalf("init: %s: %v", out, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "private.txt"), []byte("must not be staged"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := (WorktreeManager{Workspace: dir, RunID: "dirty-empty"}).Repository(context.Background()); err == nil {
		t.Fatal("dirty repository must require user review")
	}
	if _, err := gitCommand(context.Background(), dir, "rev-parse", "--verify", "HEAD"); err == nil {
		t.Fatal("a rejected operation must not create a commit")
	}
}
