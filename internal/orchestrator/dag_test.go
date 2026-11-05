package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sPROFFEs/PrAImate/internal/core"
	"github.com/sPROFFEs/PrAImate/internal/store"
)

func gitFixture(t *testing.T) string {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("LocalAppData", t.TempDir())
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("Git required")
	}
	dir := t.TempDir()
	t.Cleanup(func() {
		_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err == nil && info != nil && info.Mode().IsRegular() {
				_ = os.Chmod(path, info.Mode().Perm()|0600)
			}
			return nil
		})
	})
	ctx := context.Background()
	if _, err := gitCommand(ctx, dir, "init"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.go", "b.go", "c.go"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("base\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := gitCommand(ctx, dir, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := gitCommand(ctx, dir, "commit", "-m", "fixture"); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestTaskGraphValidationAndBackendOverrides(t *testing.T) {
	config := testConfig(t)
	valid := []DAGTask{{ID: "a", Description: "A", Worker: WorkerConfig{ProfileID: Middle}}, {ID: "b", Description: "B", Dependencies: []string{"a"}, Worker: WorkerConfig{ProfileID: Fast, Runtime: "cli", CLI: "opencode", Model: "provider/model"}}}
	if err := ValidateDAG(valid, config); err != nil {
		t.Fatal(err)
	}
	p, w, err := resolveTaskProfile(config, valid[1].Worker)
	if err != nil || p.Runtime != "cli" || p.Endpoint != "" || p.Model != "provider/model" || w.Provider != "provider" {
		t.Fatalf("route: %#v %#v %v", p, w, err)
	}
	cycle := append([]DAGTask(nil), valid...)
	cycle[0].Dependencies = []string{"b"}
	if err := ValidateDAG(cycle, config); err == nil {
		t.Fatal("cycle accepted")
	}
	reserved := append([]DAGTask(nil), valid...)
	reserved[0].ID = "review-merge"
	if err := ValidateDAG(reserved, config); err == nil {
		t.Fatal("host merge worktree ID accepted as a task")
	}
	invalid := append([]DAGTask(nil), valid...)
	invalid[0].ID = "../escape"
	if err := ValidateDAG(invalid, config); err == nil {
		t.Fatal("unsafe ID accepted")
	}
	if _, err := parsePlan(`{"tasks":[{"id":"a","description":"A","worker":{"profile":"middle"},"result":{"commit":"invented"}}]}`, config); err == nil {
		t.Fatal("model supplied state accepted")
	}
}

func TestWorktreeDependencyIntegrationReviewAndConflict(t *testing.T) {
	ctx := context.Background()
	dir := gitFixture(t)
	w := WorktreeManager{Workspace: dir, RunID: "test-run"}
	base, branch, err := w.Repository(ctx)
	if err != nil {
		t.Fatal(err)
	}
	a, err := w.Create(ctx, "a", base)
	if err != nil {
		t.Fatal(err)
	}
	b, err := w.Create(ctx, "b", base)
	if err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(a.Path, "a.go"), []byte("A\n"), 0600)
	_ = os.WriteFile(filepath.Join(b.Path, "b.go"), []byte("B\n"), 0600)
	ra, err := w.Complete(ctx, a, WorkerConfig{CLI: "codex", Model: "model-A"})
	if err != nil {
		t.Fatal(err)
	}
	rb, err := w.Complete(ctx, b, WorkerConfig{CLI: "claude", Model: "model-B"})
	if err != nil {
		t.Fatal(err)
	}
	c, err := w.Create(ctx, "c", base)
	if err != nil {
		t.Fatal(err)
	}
	if err = w.IntegrateDependencies(ctx, c, []string{ra.ResultCommit, rb.ResultCommit}); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"a.go": "A\n", "b.go": "B\n"} {
		body, _ := os.ReadFile(filepath.Join(c.Path, name))
		if string(body) != want {
			t.Fatalf("dependency %s absent", name)
		}
	}
	_ = os.WriteFile(filepath.Join(c.Path, "c.go"), []byte("C\n"), 0600)
	rc, err := w.Complete(ctx, c, WorkerConfig{CLI: "opencode", Model: "model-C"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rc.Diff, "a.go") || !strings.Contains(rc.Diff, "c.go") {
		t.Fatalf("dependent patch includes inherited changes: %s", rc.Diff)
	}
	untouched, _ := os.ReadFile(filepath.Join(dir, "a.go"))
	if string(untouched) != "base\n" {
		t.Fatal("main workspace mutated before review")
	}
	merged, err := w.Merge(ctx, branch, []string{ra.ResultCommit, rb.ResultCommit, rc.ResultCommit})
	if err != nil {
		t.Fatal(err)
	}
	for _, tree := range []*Worktree{a, b, c, merged} {
		if err = w.Remove(ctx, tree); err != nil {
			t.Fatal(err)
		}
	}
	// Conflicting independent patches stop in an owned integration tree.
	base, _, _ = w.Repository(ctx)
	x, _ := w.Create(ctx, "x", base)
	y, _ := w.Create(ctx, "y", base)
	_ = os.WriteFile(filepath.Join(x.Path, "a.go"), []byte("X\n"), 0600)
	_ = os.WriteFile(filepath.Join(y.Path, "a.go"), []byte("Y\n"), 0600)
	rx, _ := w.Complete(ctx, x, WorkerConfig{})
	ry, _ := w.Complete(ctx, y, WorkerConfig{})
	conflict, err := w.Merge(ctx, branch, []string{rx.ResultCommit, ry.ResultCommit})
	if err == nil || conflict == nil {
		t.Fatal("conflict was not retained for review")
	}
	unchanged, _ := gitCommand(ctx, dir, "rev-parse", "HEAD")
	if unchanged != base {
		t.Fatal("failed merge changed target")
	}
	for _, tree := range []*Worktree{x, y, conflict} {
		if err = w.Remove(ctx, tree); err != nil {
			t.Fatal(err)
		}
	}
}

func TestWorktreeMergeCheckpointFailurePreservesTarget(t *testing.T) {
	ctx := context.Background()
	dir := gitFixture(t)
	w := WorktreeManager{Workspace: dir, RunID: "checkpoint"}
	base, branch, _ := w.Repository(ctx)
	tree, err := w.Create(ctx, "change", base)
	if err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(tree.Path, "a.go"), []byte("changed\n"), 0600)
	result, err := w.Complete(ctx, tree, WorkerConfig{})
	if err != nil {
		t.Fatal(err)
	}
	merge, err := w.Merge(ctx, branch, []string{result.ResultCommit}, func(*Worktree, bool) error { return errors.New("snapshot unavailable") })
	if err == nil {
		t.Fatal("merge proceeded without a persisted checkpoint")
	}
	head, _ := gitCommand(ctx, dir, "rev-parse", "HEAD")
	if head != base {
		t.Fatal("unrecorded merge mutated target")
	}
	for _, owned := range []*Worktree{tree, merge} {
		if err = w.Remove(ctx, owned); err != nil {
			t.Fatal(err)
		}
	}
}

func TestWorktreeCleanupAfterCacheDirectoryDisappears(t *testing.T) {
	ctx := context.Background()
	dir := gitFixture(t)
	w := WorktreeManager{Workspace: dir, RunID: "missing-cache"}
	base, _, err := w.Repository(ctx)
	if err != nil {
		t.Fatal(err)
	}
	tree, err := w.Create(ctx, "a", base)
	if err != nil {
		t.Fatal(err)
	}
	other, err := w.Create(ctx, "b", base)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.RemoveAll(tree.Path); err != nil {
		t.Fatal(err)
	}
	if err = w.Remove(ctx, tree); err != nil {
		t.Fatal(err)
	}
	if err = w.Remove(ctx, tree); err != nil {
		t.Fatalf("cleanup is not idempotent: %v", err)
	}
	registered, err := gitCommand(ctx, dir, "worktree", "list", "--porcelain", "-z")
	regSlash := filepath.ToSlash(registered)
	if err != nil || strings.Contains(regSlash, filepath.ToSlash(tree.Path)) || !strings.Contains(regSlash, filepath.ToSlash(other.Path)) {
		t.Fatalf("cleanup altered another tree or retained the missing tree: %q %v", registered, err)
	}
	if err = w.Remove(ctx, other); err != nil {
		t.Fatal(err)
	}
}

func TestDAGRestoreMarksInterruptedTasksAndDetectsCompletedMerge(t *testing.T) {
	dir := gitFixture(t)
	st, err := store.InitializeWithPassword(filepath.Join(t.TempDir(), "state.db"), "test-password")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	c, _ := core.New(core.Options{Store: st})
	ctx := context.Background()
	config := testConfig(t)
	config.Workspace = dir
	id := "restore-run"
	w := WorktreeManager{Workspace: dir, RunID: id}
	base, branch, _ := w.Repository(ctx)
	tree, err := w.Create(ctx, "a", base)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.CreateChat(ctx, core.CreateChatRequest{ID: id, Title: "Recovery", CLIAgent: "workers", Settings: core.ChatSettings{Surface: "workers"}})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(config)
	_, _ = c.AddMessage(ctx, id, "system", string(raw), nil)
	run := Run{ID: id, Status: "running", Workspace: dir, DAG: &DAG{BaseCommit: base, TargetBranch: branch, MaxParallel: 1, Tasks: []DAGTask{{ID: "a", Description: "A", Worker: WorkerConfig{ProfileID: Middle}, Status: "running", Worktree: tree}}}}
	raw, _ = json.Marshal(run)
	if err = c.SaveWorkerGraphSnapshot(ctx, id, string(raw)); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(c, ctx)
	got, err := manager.Snapshot(id)
	if err != nil || got.Status != "cancelled" || got.DAG.Tasks[0].Status != "failed" || got.DAG.Tasks[0].Worktree == nil {
		t.Fatalf("interrupted recovery: %+v %v", got, err)
	}
	_ = manager.Stop(ctx)
	// Simulate a successful branch update whose final DB checkpoint was lost.
	_ = os.WriteFile(filepath.Join(tree.Path, "a.go"), []byte("A\n"), 0600)
	result, err := w.Complete(ctx, tree, WorkerConfig{})
	if err != nil {
		t.Fatal(err)
	}
	merged, err := w.Merge(ctx, branch, []string{result.ResultCommit})
	if err != nil {
		t.Fatal(err)
	}
	run.Status = "merging"
	run.DAG.MergeCommit = merged.BaseRef
	run.DAG.MergeTasks = []string{"a"}
	run.DAG.MergeWorktree = merged
	run.DAG.Tasks[0].Status = "completed"
	run.DAG.Tasks[0].Review = "accepted"
	run.DAG.Tasks[0].Result = result
	raw, _ = json.Marshal(run)
	if err = c.SaveWorkerGraphSnapshot(ctx, id, string(raw)); err != nil {
		t.Fatal(err)
	}
	restored := NewManager(c, ctx)
	defer restored.Stop(ctx)
	got, err = restored.Snapshot(id)
	if err != nil || got.DAG.Tasks[0].Review != "merged" || got.Status != "completed" {
		t.Fatalf("completed merge recovery: %+v %v", got, err)
	}
	if err = restored.CleanupDAG(id); err != nil {
		t.Fatal(err)
	}
}

type dagAdapterState struct {
	mu      sync.Mutex
	started int
	both    chan struct{}
	paths   map[string]string
	repo    string
}
type dagAdapter struct {
	name  string
	state *dagAdapterState
}

func (a *dagAdapter) Name() string                  { return a.name }
func (*dagAdapter) Available(context.Context) error { return nil }
func (*dagAdapter) ManagedSafeMode() bool           { return true }
func (*dagAdapter) SupportsResume() bool            { return false }
func (*dagAdapter) Resume(context.Context, string, core.ResumeOpts) (*core.Reply, error) {
	return nil, errors.New("unexpected resume")
}
func (a *dagAdapter) SingleShot(ctx context.Context, opts core.SingleShotOpts) (*core.Reply, error) {
	if opts.Model == "planner" {
		return &core.Reply{Text: `{"tasks":[{"id":"a","description":"Update a.go","dependencies":[],"worker":{"profile":"middle"}},{"id":"b","description":"Update b.go","dependencies":[],"worker":{"profile":"middle","cli":"claude","model":"B"}},{"id":"c","description":"Update c.go after A and B","dependencies":["a","b"],"worker":{"profile":"fast","cli":"opencode","model":"C"}}]}`}, nil
	}
	if strings.Contains(opts.Message, "Exact replacement applied") {
		return &core.Reply{Text: `{"action":"final","content":"updated and inspected"}`}, nil
	}
	a.state.mu.Lock()
	a.state.paths[opts.Model] = opts.Cwd
	if opts.Model == "A" || opts.Model == "B" {
		a.state.started++
		if a.state.started == 2 {
			close(a.state.both)
		}
	}
	a.state.mu.Unlock()
	if opts.Model == "A" || opts.Model == "B" {
		select {
		case <-a.state.both:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if opts.Model == "C" {
		for name, want := range map[string]string{"a.go": "A\n", "b.go": "B\n"} {
			body, _ := os.ReadFile(filepath.Join(opts.Cwd, name))
			if string(body) != want {
				return nil, errors.New("missing integrated dependency")
			}
		}
		original, _ := os.ReadFile(filepath.Join(a.state.repo, "a.go"))
		if string(original) != "base\n" {
			return nil, errors.New("target mutated")
		}
	}
	body, _ := json.Marshal(map[string]string{"action": "replace", "path": strings.ToLower(opts.Model) + ".go", "oldText": "base", "newText": opts.Model})
	return &core.Reply{Text: string(body)}, nil
}

func waitGraph(t *testing.T, m *Manager, id, want string) Run {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		run, err := m.Snapshot(id)
		if err != nil {
			t.Fatal(err)
		}
		if run.Status == want {
			return run
		}
		if run.Status == "failed" {
			t.Fatalf("graph failed: %s %+v", run.Error, run.DAG)
		}
		time.Sleep(10 * time.Millisecond)
	}
	run, _ := m.Snapshot(id)
	t.Fatalf("timeout waiting for %s: %+v", want, run)
	return Run{}
}

func TestParallelDAGPersistsProvenanceAndIsolatedBackends(t *testing.T) {
	dir := gitFixture(t)
	st, err := store.InitializeWithPassword(filepath.Join(t.TempDir(), "state.db"), "test-password")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	c, err := core.New(core.Options{Store: st})
	if err != nil {
		t.Fatal(err)
	}
	state := &dagAdapterState{both: make(chan struct{}), paths: map[string]string{}, repo: dir}
	for _, name := range []string{"codex", "claude", "opencode"} {
		previous, _ := core.GetCLIAdapter(name)
		core.RegisterCLIAdapter(&dagAdapter{name: name, state: state})
		t.Cleanup(func() {
			if previous != nil {
				core.RegisterCLIAdapter(previous)
			} else {
				core.UnregisterCLIAdapter(name)
			}
		})
	}
	manager := NewManager(c, context.Background())
	defer manager.Stop(context.Background())
	config := Config{Workspace: dir}
	for _, tier := range []Tier{Primary, Middle, Fast} {
		config.Profiles = append(config.Profiles, Profile{Tier: tier, Runtime: "cli", CLI: "codex", Model: "A", AllowEdits: true, TimeoutSeconds: 10, MaxInputBytes: 32768})
	}
	config.Profiles[0].Model = "planner"
	id, err := manager.PlanDAG("Make A, B and their dependent C", config, 2)
	if err != nil {
		t.Fatal(err)
	}
	draft := waitGraph(t, manager, id, "draft")
	if err = manager.UpdateDAG(id, draft.DAG.Tasks, 2); err != nil {
		t.Fatal(err)
	}
	if err = manager.ExecuteDAG(id, nil); err != nil {
		t.Fatal(err)
	}
	review := waitGraph(t, manager, id, "review")
	for _, task := range review.DAG.Tasks {
		if task.Result == nil || task.Result.WorkerConfig.Model == "" || task.Worktree == nil || task.Result.ResultCommit == task.Result.BaseCommit {
			t.Fatalf("missing provenance: %+v", task)
		}
	}
	state.mu.Lock()
	if state.paths["A"] == state.paths["B"] || state.paths["B"] == state.paths["C"] {
		t.Error("workers shared a workspace")
	}
	state.mu.Unlock()
	restored := NewManager(c, context.Background())
	snapshot, err := restored.Snapshot(id)
	if err != nil || snapshot.DAG.Tasks[1].Result.WorkerConfig.CLI != "claude" {
		t.Fatalf("restored provenance: %v", err)
	}
	_ = restored.Stop(context.Background())
	if err = manager.ReviewDAGTask(id, "c", "accepted"); err == nil {
		t.Fatal("dependent accepted before its dependencies")
	}
	for _, taskID := range []string{"a", "b", "c"} {
		if err = manager.ReviewDAGTask(id, taskID, "accepted"); err != nil {
			t.Fatal(err)
		}
	}
	if err = manager.MergeDAG(id); err != nil {
		t.Fatal(err)
	}
	final := waitGraph(t, manager, id, "completed")
	messages, err := c.ListMessages(context.Background(), id, 0)
	if err != nil {
		t.Fatal(err)
	}
	snapshots := 0
	for _, message := range messages {
		if message.Role == "assistant" {
			snapshots++
		}
	}
	if snapshots != 1 {
		t.Fatalf("graph snapshots accumulate duplicate diffs: %d", snapshots)
	}
	activity, err := manager.Activity(id, "", 0, 200)
	if err != nil || len(activity.Events) == 0 {
		t.Fatalf("durable activity is missing: %v", err)
	}
	for _, task := range final.DAG.Tasks {
		if task.Review != "merged" || task.Worktree != nil {
			t.Fatalf("merge/cleanup incomplete: %+v", task)
		}
		if _, err := gitCommand(context.Background(), dir, "show-ref", "--verify", task.Result.ResultRef); err != nil {
			t.Fatal("cleanup removed durable task commit:", err)
		}
	}
	for name, want := range map[string]string{"a.go": "A\n", "b.go": "B\n", "c.go": "C\n"} {
		body, _ := os.ReadFile(filepath.Join(dir, name))
		if string(body) != want {
			t.Fatalf("merged %s: %q", name, body)
		}
	}
	// A new graph with one slot deliberately blocks the fake independent
	// worker until cancellation. Its worktree must survive for inspection.
	blocked := &dagAdapterState{both: make(chan struct{}), paths: map[string]string{}, repo: dir}
	for _, name := range []string{"codex", "claude", "opencode"} {
		core.RegisterCLIAdapter(&dagAdapter{name: name, state: blocked})
	}
	cancelID, err := manager.PlanDAG("Cancel safely", config, 1)
	if err != nil {
		t.Fatal(err)
	}
	waitGraph(t, manager, cancelID, "draft")
	if err = manager.ExecuteDAG(cancelID, nil); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		blocked.mu.Lock()
		started := blocked.started
		blocked.mu.Unlock()
		if started == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err = manager.Cancel(cancelID); err != nil {
		t.Fatal(err)
	}
	cancelled := waitGraph(t, manager, cancelID, "cancelled")
	if cancelled.DAG.Tasks[0].Status != "cancelled" || cancelled.DAG.Tasks[0].Worktree == nil {
		t.Fatalf("interrupted work lost: %+v", cancelled.DAG)
	}
	if err = manager.ResetDAGTask(cancelID, "a"); err != nil {
		t.Fatal(err)
	}
	reset, _ := manager.Snapshot(cancelID)
	if reset.DAG.Tasks[0].Status != "pending" || reset.DAG.Tasks[0].Worktree != nil {
		t.Fatal("failed task not explicitly reset")
	}
	if err = manager.Delete(cancelID); err != nil {
		t.Fatal(err)
	}
}

func TestParallelDAGResumesAfterMergingCompletedDependencies(t *testing.T) {
	ctx := context.Background()
	dir := gitFixture(t)
	st, err := store.InitializeWithPassword(filepath.Join(t.TempDir(), "state.db"), "test-password")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	c, err := core.New(core.Options{Store: st})
	if err != nil {
		t.Fatal(err)
	}
	state := &dagAdapterState{both: make(chan struct{}), paths: map[string]string{}, repo: dir}
	close(state.both)
	previous, _ := core.GetCLIAdapter("codex")
	core.RegisterCLIAdapter(&dagAdapter{name: "codex", state: state})
	t.Cleanup(func() {
		if previous != nil {
			core.RegisterCLIAdapter(previous)
		} else {
			core.UnregisterCLIAdapter("codex")
		}
	})
	config := testConfig(t)
	config.Workspace = dir
	for i := range config.Profiles {
		config.Profiles[i].Runtime = "cli"
		config.Profiles[i].CLI = "codex"
		config.Profiles[i].Endpoint = ""
		config.Profiles[i].MaxOutputTokens = 0
		config.Profiles[i].Model = "B"
		config.Profiles[i].AllowEdits = true
	}
	if err = config.Validate(); err != nil {
		t.Fatal(err)
	}
	id := "partial-merge"
	w := WorktreeManager{Workspace: dir, RunID: id}
	base, branch, err := w.Repository(ctx)
	if err != nil {
		t.Fatal(err)
	}
	a, err := w.Create(ctx, "a", base)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(a.Path, "a.go"), []byte("A\n"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := w.Complete(ctx, a, WorkerConfig{ProfileID: Middle, CLI: "codex", Model: "A"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.CreateChat(ctx, core.CreateChatRequest{ID: id, Title: "Partial merge", CLIAgent: "workers", Settings: core.ChatSettings{Surface: "workers"}}); err != nil {
		t.Fatal(err)
	}
	configJSON, _ := json.Marshal(config)
	if _, err = c.AddMessage(ctx, id, "system", string(configJSON), nil); err != nil {
		t.Fatal(err)
	}
	m := NewManager(c, ctx)
	run := &Run{ID: id, Status: "failed", Workspace: dir, Profiles: config.Profiles, config: config, DAG: &DAG{BaseCommit: base, TargetBranch: branch, MaxParallel: 1, Tasks: []DAGTask{
		{ID: "a", Description: "Update a.go", Worker: WorkerConfig{ProfileID: Middle}, Status: "completed", Review: "pending", Worktree: a, Result: result},
		{ID: "b", Description: "Update b.go after A", Worker: WorkerConfig{ProfileID: Middle}, Dependencies: []string{"a"}, Status: "failed"},
	}}}
	m.runs[id] = run
	if err = m.ReviewDAGTask(id, "a", "accepted"); err != nil {
		t.Fatal(err)
	}
	if err = m.MergeDAG(id); err != nil {
		t.Fatal(err)
	}
	waitGraph(t, m, id, "review")
	if err = m.ResetDAGTask(id, "b"); err != nil {
		t.Fatal(err)
	}
	_ = m.Stop(ctx)
	// Retry from a restored chat after A's merge changed the target HEAD.
	m = NewManager(c, ctx)
	defer m.Stop(ctx)
	mergedHead, err := gitCommand(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = gitCommand(ctx, dir, "commit", "--allow-empty", "-m", "external branch update"); err != nil {
		t.Fatal(err)
	}
	if err = m.ExecuteDAG(id, nil); err == nil {
		t.Fatal("resumed a plan after unrelated target changes")
	}
	// Restore only the fixture ref: the intervening commit changed no files.
	if _, err = gitCommand(ctx, dir, "update-ref", "refs/heads/"+branch, mergedHead); err != nil {
		t.Fatal(err)
	}
	if err = m.ExecuteDAG(id, nil); err != nil {
		t.Fatal(err)
	}
	review := waitGraph(t, m, id, "review")
	b := review.DAG.Tasks[1]
	inherited, err := os.ReadFile(filepath.Join(b.Worktree.Path, "a.go"))
	if err != nil || string(inherited) != "A\n" || strings.Contains(b.Result.Diff, "a.go") {
		t.Fatalf("resumed task did not isolate its patch from dependencies: %+v %v", b, err)
	}
	if err = m.ReviewDAGTask(id, "b", "accepted"); err != nil {
		t.Fatal(err)
	}
	if err = m.MergeDAG(id); err != nil {
		t.Fatal(err)
	}
	waitGraph(t, m, id, "completed")
}
