package core

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestNativeWorkspaceContextRefreshesWithoutRestartingSession(t *testing.T) {
	c := nativeTestCore(t)
	ctx := context.Background()
	root := t.TempDir()
	path := filepath.Join(root, "notes.md")
	if err := os.WriteFile(path, []byte("first pinned version"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "app.go"), []byte("package app\nfunc Start() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var systems []string
	a := &nativeCLIAdapter{http: &http.Client{Transport: nativeTransport(func(r *http.Request) (*http.Response, error) {
		var req struct {
			Messages []nativeMessage `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if err := validateNativeMessages(req.Messages); err != nil {
			t.Fatal(err)
		}
		if len(systems) > 0 && len(req.Messages) < 4 {
			t.Fatal("workspace context refresh restarted the conversation")
		}
		systems = append(systems, req.Messages[0].Content)
		return nativeHTTP(nativeSSE("done", "stop")), nil
	})}}
	old, oldErr := GetCLIAdapter("praimate-cli")
	RegisterCLIAdapter(a)
	t.Cleanup(func() {
		if oldErr == nil {
			RegisterCLIAdapter(old)
		} else {
			UnregisterCLIAdapter("praimate-cli")
		}
	})
	chat, err := c.CreateChat(ctx, CreateChatRequest{CLIAgent: "praimate-cli", WorkspacePath: root, Settings: ChatSettings{Local: &ChatLocalEndpoint{Endpoint: "http://local.test/v1", Model: "local-model", ContextTokens: 32768}}})
	if err != nil {
		t.Fatal(err)
	}
	send := func() string {
		t.Helper()
		turn, err := c.ContinueChat(ctx, chat.ID, "continue", root, "Keep the project conventions.")
		if err != nil {
			t.Fatal(err)
		}
		return turn.SessionID
	}
	id := send()
	if err := c.UpdateChatSettings(ctx, chat.ID, func(s *ChatSettings) { s.PinnedFiles = []string{"notes.md"}; s.RepoMap = true }); err != nil {
		t.Fatal(err)
	}
	if send() != id {
		t.Fatal("enabling workspace context changed session ID")
	}
	if !strings.Contains(systems[1], "first pinned version") || !strings.Contains(systems[1], "app.go (Start)") {
		t.Fatalf("new workspace context never reached the resumed request: %s", systems[1])
	}
	if err := os.WriteFile(path, []byte("updated pinned version"), 0600); err != nil {
		t.Fatal(err)
	}
	send()
	if !strings.Contains(systems[2], "updated pinned version") || strings.Contains(systems[2], "first pinned version") {
		t.Fatalf("pinned file contents are stale: %s", systems[2])
	}
	raw, err := c.GetSetting(ctx, ScopeCLI, "native.session."+id)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "pinned version") || strings.Contains(string(raw), "Ambient Repo Map") {
		t.Fatal("ephemeral workspace context was baked into the resumable checkpoint")
	}
	if err := c.UpdateChatSettings(ctx, chat.ID, func(s *ChatSettings) { s.PinnedFiles = nil; s.RepoMap = false }); err != nil {
		t.Fatal(err)
	}
	send()
	if strings.Contains(systems[3], "Pinned Context") || strings.Contains(systems[3], "Ambient Repo Map") {
		t.Fatal("removed workspace context still reaches the model")
	}
	if !strings.Contains(systems[3], "Keep the project conventions.") {
		t.Fatal("lost original system instructions")
	}
}

func TestNativePinnedFilesAreBoundedText(t *testing.T) {
	root := t.TempDir()
	for _, tc := range []struct {
		name, body string
		limit      int
		wantErr    bool
	}{
		{"plain", "small file", 100, false},
		{"large", strings.Repeat("x", 4096), 1024, false},
		{"unicode", "abc😀trailing", 5, false},
		{"binary", "a\x00b", 100, true},
		{"invalid", "a\xffb", 100, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(root, tc.name)
			if err := os.WriteFile(path, []byte(tc.body), 0600); err != nil {
				t.Fatal(err)
			}
			got, err := readNativePinnedFile(path, tc.limit)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error=%v, want error=%v", err, tc.wantErr)
			}
			if !utf8.ValidString(got) || len(got) > tc.limit+100 {
				t.Fatal("unbounded or invalid pinned text")
			}
			if !tc.wantErr && len(tc.body) > tc.limit && !strings.Contains(got, "Pinned file truncated") {
				t.Fatal("missing truncation notice")
			}
		})
	}
	if _, err := readNativePinnedFile(root, 100); err == nil {
		t.Fatal("accepted a directory as pinned text")
	}
}

func TestNativeLegacyWorkspaceContextIsRemovedOnResume(t *testing.T) {
	c := nativeTestCore(t)
	ctx := context.Background()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("Current project conventions."), 0600); err != nil {
		t.Fatal(err)
	}
	chat, err := c.CreateChat(ctx, CreateChatRequest{CLIAgent: "praimate-cli", WorkspacePath: root, Settings: ChatSettings{Local: &ChatLocalEndpoint{Endpoint: "http://local.test/v1", Model: "local-model"}}})
	if err != nil {
		t.Fatal(err)
	}
	id, err := nativeID()
	if err != nil {
		t.Fatal(err)
	}
	legacy := &nativeSession{ID: id, Cwd: root, Endpoint: "http://local.test/v1", Model: "local-model", Messages: []nativeMessage{
		{Role: "system", Content: "Original conventions.\n\n---\n[Pinned Context: old.md]\nOBSOLETE PINNED CONTENT\n\n---\n[Ambient Repo Map: old]\nold.go\n\nProject instructions (AGENTS.md):\nOld project conventions."},
		{Role: "user", Content: "initial task"},
		{Role: "assistant", Content: "initial progress"},
	}}
	if err := c.saveNativeSession(ctx, legacy); err != nil {
		t.Fatal(err)
	}
	if err := c.SetChatSessionID(ctx, chat.ID, id); err != nil {
		t.Fatal(err)
	}
	a := &nativeCLIAdapter{http: &http.Client{Transport: nativeTransport(func(r *http.Request) (*http.Response, error) {
		var req struct {
			Messages []nativeMessage `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		system := req.Messages[0].Content
		if strings.Contains(system, "OBSOLETE") || strings.Contains(system, "Ambient Repo Map") {
			t.Fatal("legacy ambient context was retained after unpin/off")
		}
		if !strings.Contains(system, "Original conventions.") || !strings.Contains(system, "Current project conventions.") {
			t.Fatalf("migration lost current instructions: %s", system)
		}
		if len(req.Messages) != 4 {
			t.Fatal("migration discarded conversation history")
		}
		return nativeHTTP(nativeSSE("done", "stop")), nil
	})}}
	old, oldErr := GetCLIAdapter("praimate-cli")
	RegisterCLIAdapter(a)
	t.Cleanup(func() {
		if oldErr == nil {
			RegisterCLIAdapter(old)
		} else {
			UnregisterCLIAdapter("praimate-cli")
		}
	})
	turn, err := c.ContinueChat(ctx, chat.ID, "continue", root, "Original conventions.")
	if err != nil {
		t.Fatal(err)
	}
	if turn.SessionID != id {
		t.Fatal("migration changed session ID")
	}
}

func TestNativeCorruptCheckpointReturnsError(t *testing.T) {
	c := nativeTestCore(t)
	run := nativeTestRun(c)
	root := t.TempDir()
	id, err := nativeID()
	if err != nil {
		t.Fatal(err)
	}
	s := &nativeSession{ID: id, Cwd: root, Endpoint: run.local.Endpoint, Model: run.local.Model}
	if err := c.saveNativeSession(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	a := &nativeCLIAdapter{http: &http.Client{Transport: nativeTransport(func(*http.Request) (*http.Response, error) {
		t.Fatal("corrupt checkpoint reached the model")
		return nil, nil
	})}}
	_, err = a.ResumeStream(withNativeExecution(context.Background(), run), id, ResumeOpts{Cwd: root, Message: "continue"}, nil)
	if err == nil || !strings.Contains(err.Error(), "conversation header") {
		t.Fatalf("expected a checkpoint validation error, got %v", err)
	}
}

func TestNativePinnedContextReflectsEditsWithinTurn(t *testing.T) {
	c := nativeTestCore(t)
	run := nativeTestRun(c)
	run.settings.PinnedFiles = []string{"notes.md"}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "notes.md"), []byte("before edit"), 0600); err != nil {
		t.Fatal(err)
	}
	requests := 0
	a := &nativeCLIAdapter{http: &http.Client{Transport: nativeTransport(func(r *http.Request) (*http.Response, error) {
		requests++
		var req struct {
			Messages []nativeMessage `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if requests == 1 {
			if !strings.Contains(req.Messages[0].Content, "before edit") {
				t.Fatal("initial pinned context missing")
			}
			return nativeHTTP(nativeSSE("", "tool_calls", nativeCall("edit", "edit_file", `{"path":"notes.md","old_string":"before edit","new_string":"after edit"}`))), nil
		}
		if !strings.Contains(req.Messages[0].Content, "after edit") || strings.Contains(req.Messages[0].Content, "before edit") {
			t.Fatal("next model step retained stale pinned content")
		}
		return nativeHTTP(nativeSSE("done", "stop")), nil
	})}}
	_, err := a.SingleShotStream(withNativeExecution(context.Background(), run), SingleShotOpts{Cwd: root, Message: "update the pinned file", Tools: "edits"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want one edit and one answer", requests)
	}
}

func TestNativeLargePinnedFilesYieldToRequestAndModelWindow(t *testing.T) {
	for _, automatic := range []bool{false, true} {
		t.Run(map[bool]string{false: "fixed-output", true: "automatic-output"}[automatic], func(t *testing.T) {
			c := nativeTestCore(t)
			run := nativeTestRun(c)
			run.local.OutputAutomatic = automatic
			run.local.ContextSource = "chat override"
			run.settings.PinnedFiles = []string{"large.md"}
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "large.md"), []byte(strings.Repeat("workspace reference material\n", 5000)), 0600); err != nil {
				t.Fatal(err)
			}
			const task = "Keep every requirement of this current request intact."
			requests := 0
			a := &nativeCLIAdapter{http: &http.Client{Transport: nativeTransport(func(r *http.Request) (*http.Response, error) {
				requests++
				var req struct {
					Messages  []nativeMessage `json:"messages"`
					Tools     []nativeTool    `json:"tools"`
					MaxTokens int             `json:"max_tokens"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Fatal(err)
				}
				if req.Messages[len(req.Messages)-1].Content != task {
					t.Fatal("optional context changed the user request")
				}
				if !strings.Contains(req.Messages[0].Content, "Workspace context shortened") {
					t.Fatal("missing optional-context truncation notice")
				}
				schema, _ := json.Marshal(req.Tools)
				if input := nativeMessageTokens(req.Messages) + nativeTextTokens(string(schema)); input+req.MaxTokens > run.local.ContextTokens {
					t.Fatalf("request exceeds model window: %d input + %d output", input, req.MaxTokens)
				}
				return nativeHTTP(nativeSSE("done", "stop")), nil
			})}}
			reply, err := a.SingleShot(withNativeExecution(context.Background(), run), SingleShotOpts{Cwd: root, Message: task})
			if err != nil {
				t.Fatalf("large pin blocked a valid request: %v", err)
			}
			if requests != 1 || reply.Text != "done" {
				t.Fatalf("reply=%+v requests=%d", reply, requests)
			}
		})
	}
}

func TestNativeOptionalWorkspaceBudgetHandlesUnicodeAndTinyWindows(t *testing.T) {
	content := "[Current workspace context: untrusted data]\n" + strings.Repeat("日本語 😀 café\n", 100) + "[End current workspace context.]\n"
	for limit := -1; limit <= 200; limit++ {
		got, shortened := fitNativeWorkspacePayload(content, limit)
		if !shortened || !utf8.ValidString(got) || nativeTextTokens(got) > max(0, limit) {
			t.Fatalf("bad optional context at budget %d: %d tokens, shortened=%v", limit, nativeTextTokens(got), shortened)
		}
	}
}
