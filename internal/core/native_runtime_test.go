package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

type nativeTransport func(*http.Request) (*http.Response, error)

func (f nativeTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func nativeHTTP(body string) *http.Response {
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}
}
func nativeSSE(content, finish string, calls ...nativeToolCall) string {
	delta := map[string]any{"content": content}
	if len(calls) > 0 {
		var fragments []any
		for i, c := range calls {
			fragments = append(fragments, map[string]any{"index": i, "id": c.ID, "function": c.Function})
		}
		delta["tool_calls"] = fragments
	}
	raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": delta, "finish_reason": finish}}})
	return "data: " + string(raw) + "\n\ndata: [DONE]\n\n"
}
func nativeCall(id, name, args string) nativeToolCall {
	return nativeToolCall{ID: id, Type: "function", Function: nativeFunction{Name: name, Arguments: args}}
}
func nativeTestCore(t *testing.T) *Core {
	t.Helper()
	t.Setenv("PRAIMATE_HOME", t.TempDir())
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("OPENAI_BASE_URL", "")
	s := openTempStore(t)
	t.Cleanup(func() { _ = s.Close() })
	c, err := New(Options{Store: s})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	c.nativeLimits.http = &http.Client{Transport: nativeTransport(func(*http.Request) (*http.Response, error) {
		res := nativeHTTP(`{}`)
		res.StatusCode = 404
		return res, nil
	})}
	return c
}
func nativeTestRun(c *Core) *nativeExecution {
	return &nativeExecution{core: c, local: ChatLocalEndpoint{Endpoint: "http://local.test/v1", Model: "local-model", ContextTokens: 8192, OutputTokens: 1024}}
}

func TestNativeProviderStreamingAndFailureModes(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		wantErr    bool
	}{
		{"text", nativeSSE("ok", "stop"), false},
		{"tool", nativeSSE("", "tool_calls", nativeCall("one", "read_file", `{"path":"a"}`)), false},
		{"truncated", `data: {"choices":[{"delta":{"content":"partial"}}]}` + "\n\n", true},
		{"budget", nativeSSE("partial", "length"), true},
		{"bad_arguments", nativeSSE("", "tool_calls", nativeCall("one", "read_file", `{`)), true},
		{"duplicate_ids", nativeSSE("", "tool_calls", nativeCall("one", "read_file", `{}`), nativeCall("one", "read_file", `{}`)), true},
		{"missing_calls", nativeSSE("", "tool_calls"), true},
		{"no_finish", "data: [DONE]\n\n", true},
		{"api_error", "data: {\"error\":{\"message\":\"no model\"}}\n\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := nativeProvider{route: ChatLocalEndpoint{Endpoint: "http://local.test/team/v1", Model: "namespace/model", OutputTokens: 512, APIKey: "test-only"}, http: &http.Client{Transport: nativeTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path != "/team/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer test-only" {
					t.Fatal("lost route/auth")
				}
				var req map[string]any
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Fatal(err)
				}
				if req["model"] != "namespace/model" || req["max_tokens"] != float64(512) {
					t.Fatalf("wrong limits/model: %v", req)
				}
				return nativeHTTP(tc.body), nil
			})}}
			_, err := p.turn(context.Background(), []nativeMessage{{Role: "user", Content: "hi"}}, nil, nil)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error=%v wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

func TestNativeToolsFailClosedAndContainPaths(t *testing.T) {
	c := nativeTestCore(t)
	run := nativeTestRun(c)
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "private"), []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	symlinkErr := os.Symlink(outside, filepath.Join(root, "escape"))
	for _, level := range []string{"", "ask", "edits", "full"} {
		t.Run("policy_"+level, func(t *testing.T) {
			approvals := 0
			b, defs, err := run.nativeTools(context.Background(), root, level, &ApprovalConfig{Request: func(context.Context, string, map[string]any) (bool, error) { approvals++; return false, nil }})
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			_, err = executeNativeTool(context.Background(), b, defs, nativeCall("write", "write_file", `{"path":"created","content":"yes"}`))
			wantWrite := level == "edits" || level == "full"
			if (err == nil) != wantWrite {
				t.Fatalf("write policy: %v", err)
			}
			if level == "ask" && approvals != 1 {
				t.Fatal("Ask did not prompt")
			}
			for _, name := range []string{"shell", "bash", "project.write", "file_write"} {
				if _, err := executeNativeTool(context.Background(), b, defs, nativeCall("x", name, `{}`)); err == nil {
					t.Fatalf("accepted alias %s", name)
				}
			}
			paths := []string{filepath.Join(outside, "private"), "../private"}
			if _, err := executeNativeTool(context.Background(), b, defs, nativeCall("x", "git_inspect", `{"operation":"branch","args":["-D","main"]}`)); err == nil {
				t.Fatal("Git read tool permits mutations")
			}
			if symlinkErr == nil {
				paths = append(paths, "escape/private")
			}
			for _, path := range paths {
				raw, _ := json.Marshal(map[string]string{"path": path})
				_, err := executeNativeTool(context.Background(), b, defs, nativeCall("x", "read_file", string(raw)))
				if level != "full" && err == nil {
					t.Fatalf("restricted mode read escaped: %s", path)
				}
				if level == "full" && path != "../private" && err != nil {
					t.Fatalf("Full could not read an OS-accessible file: %s: %v", path, err)
				}
			}
			if level == "ask" || level == "edits" {
				if _, err := executeNativeTool(context.Background(), b, defs, nativeCall("x", "run_command", `{"command":"unapproved-command","args":[]}`)); err == nil {
					t.Fatal("unapproved command ran")
				}
			}
		})
	}
	run.servers = []MCPServer{{ID: "workspace-spawn", Command: "must-not-run"}}
	if _, _, err := run.nativeTools(context.Background(), root, "", nil); err == nil {
		t.Fatal("safe mode connected MCP")
	}
	if _, _, err := run.nativeTools(context.Background(), root, "ask", nil); err == nil {
		t.Fatal("Ask without provider connected MCP")
	}
	name := nativeMCPName("server.with/slash", "tool:invalid")
	if !regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`).MatchString(name) {
		t.Fatal("invalid provider function name")
	}
}

func TestNativeCoreChatResumeStreamingAndWorkspaceConfig(t *testing.T) {
	c := nativeTestCore(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".mcp.json"), []byte(`{"mcpServers":{"evil":{"command":"must-not-run"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	requests := 0
	a := &nativeCLIAdapter{http: &http.Client{Transport: nativeTransport(func(r *http.Request) (*http.Response, error) {
		requests++
		var req struct {
			Messages []nativeMessage `json:"messages"`
			Tools    []nativeTool    `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if err := validateNativeMessages(req.Messages); err != nil {
			t.Fatal(err)
		}
		if requests == 1 {
			return nativeHTTP(nativeSSE("", "tool_calls", nativeCall("write-id", "write_file", `{"path":"hello.txt","content":"hello"}`))), nil
		}
		if requests == 2 {
			if req.Messages[len(req.Messages)-1].Role != "tool" {
				t.Fatal("missing tool result")
			}
			return nativeHTTP(nativeSSE("done", "stop")), nil
		}
		if len(req.Messages) < 5 {
			t.Fatal("resume lost history")
		}
		return nativeHTTP(nativeSSE("resumed", "stop")), nil
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
	chat, err := c.CreateChat(context.Background(), CreateChatRequest{CLIAgent: "praimate-cli", WorkspacePath: root, Settings: ChatSettings{Tools: "edits", ToolsConfigured: true, Local: &ChatLocalEndpoint{Endpoint: "http://local.test/v1", Model: "namespace/model"}}})
	if err != nil {
		t.Fatal(err)
	}
	var events []StreamEvent
	turn, err := c.ContinueChatStream(context.Background(), chat.ID, "write a file", root, "", nil, func(e StreamEvent) { events = append(events, e) })
	if err != nil {
		t.Fatal(err)
	}
	if turn.Reply != "done" || !nativeIDPattern.MatchString(turn.SessionID) {
		t.Fatalf("turn: %+v", turn)
	}
	var start, end bool
	for _, e := range events {
		if e.ID == "write-id" {
			start = start || e.Type == "tool_start"
			end = end || e.Type == "tool_end" && e.OK
		}
	}
	if !start || !end {
		t.Fatalf("missing correlated events: %v", events)
	}
	raw, err := os.ReadFile(filepath.Join(root, "hello.txt"))
	if err != nil || string(raw) != "hello" {
		t.Fatalf("edit: %q %v", raw, err)
	}
	if _, err := c.ContinueChat(context.Background(), chat.ID, "continue", root, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "AGENTS.md")); !os.IsNotExist(err) {
		t.Fatal("native runtime wrote project configuration")
	}
}

func TestNativeSessionsErrorsLeasesAndModelOnly(t *testing.T) {
	c := nativeTestCore(t)
	run := nativeTestRun(c)
	run.modelOnly = true
	ctx := withNativeExecution(context.Background(), run)
	a := &nativeCLIAdapter{http: &http.Client{Transport: nativeTransport(func(r *http.Request) (*http.Response, error) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		if _, ok := req["tools"]; ok {
			t.Fatal("managed model received native tools")
		}
		return nativeHTTP("data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n"), nil
	})}}
	var finish StreamEvent
	reply, err := a.SingleShotStream(ctx, SingleShotOpts{Cwd: t.TempDir(), Message: "hello"}, func(e StreamEvent) {
		if e.Type == "step_finish" {
			finish = e
		}
	})
	if err == nil || reply == nil || reply.ExitCode == 0 || finish.OK {
		t.Fatalf("truncation reported success: %+v %v", reply, err)
	}
	if _, err := a.Resume(ctx, "../../outside", ResumeOpts{}); err == nil {
		t.Fatal("accepted arbitrary session path")
	}
	_, release, err := c.nativeLease(context.Background(), reply.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, _, err := c.nativeLease(context.Background(), reply.SessionID); err == nil {
		t.Fatal("concurrent session accepted")
	}
	if _, err := NewPraimateCLIAdapter().SingleShot(context.Background(), SingleShotOpts{}); err == nil {
		t.Fatal("unprepared native execution accepted")
	}
}

func TestNativeCompactionKeepsPairsAndLatestRequest(t *testing.T) {
	messages := []nativeMessage{{Role: "system", Content: "rules"}, {Role: "user", Content: "old task"}, {Role: "assistant", ToolCalls: []nativeToolCall{nativeCall("a", "read_file", `{"path":"x"}`)}}, {Role: "tool", ToolCallID: "a", Content: strings.Repeat("old data ", 1000)}, {Role: "assistant", Content: "done"}, {Role: "user", Content: "CURRENT TASK"}, {Role: "assistant", ToolCalls: []nativeToolCall{nativeCall("b", "read_file", `{"path":"b"}`)}}, {Role: "tool", ToolCallID: "b", Content: strings.Repeat("current data ", 1000)}}
	result, changed, err := compactNativeMessages(messages, 2000)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("did not compact")
	}
	if err := validateNativeMessages(result); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(result)
	if len(raw) > 2000 || !strings.Contains(string(raw), "CURRENT TASK") || !strings.Contains(string(raw), "Earlier context excerpts") {
		t.Fatalf("bad compaction: %s", raw)
	}
	if _, _, err := compactNativeMessages([]nativeMessage{{Role: "system", Content: strings.Repeat("x", 4000)}, {Role: "user", Content: "must retain"}}, 2000); err == nil {
		t.Fatal("silently dropped oversized instructions")
	}
	if _, _, err := compactNativeMessages([]nativeMessage{{Role: "system"}, {Role: "tool", ToolCallID: "orphan"}}, 2000); err == nil {
		t.Fatal("accepted broken history")
	}
}

func TestNativeRouteCredentialIsolationAndLimits(t *testing.T) {
	c := nativeTestCore(t)
	ctx := WithNativeAPIKey(context.Background(), "https://one.test/v1", "only-one")
	route, err := c.resolveNativeRoute(ctx, &ChatLocalEndpoint{Endpoint: "https://two.test/prefix/v1", Model: "explicit/model", ContextTokens: 4096, OutputTokens: 333}, "stale-model")
	if err != nil {
		t.Fatal(err)
	}
	if route.APIKey != "" || route.Model != "explicit/model" || route.OutputTokens != 333 {
		t.Fatalf("route: %+v", route)
	}
	route, err = c.resolveNativeRoute(ctx, &ChatLocalEndpoint{Endpoint: "https://one.test/v1", Model: "m"}, "")
	if err != nil || route.APIKey != "only-one" {
		t.Fatalf("credential not scoped: %v", err)
	}
	for _, url := range []string{"file:///tmp/a", "http://user:password@local/v1", "https://host/v1?key=value"} {
		if _, err := nativeBaseURL(url); err == nil {
			t.Fatalf("accepted %s", url)
		}
	}
	if _, err := c.resolveNativeRoute(ctx, &ChatLocalEndpoint{Endpoint: "http://local/v1", Model: "m", ContextTokens: 2048, OutputTokens: 2048}, ""); err == nil {
		t.Fatal("accepted no input budget")
	}
}

func TestNativeRouteSelectsAssignedModelAndHostByDefault(t *testing.T) {
	c := nativeTestCore(t)
	ctx := context.Background()
	for _, host := range []LocalHost{
		{ID: "default", Endpoint: "http://first.test:8000", IsDefault: true},
		{ID: "gpu", Endpoint: "http://gpu.test:8000", NativeModels: []string{"qwen"}, ContextTokens: 16384},
	} {
		if _, err := c.SaveLocalHost(ctx, host); err != nil {
			t.Fatal(err)
		}
	}
	route, err := c.resolveNativeRoute(ctx, nil, "")
	if err != nil || route.Endpoint != "http://gpu.test:8000/v1" || route.Model != "qwen" || route.ContextTokens != 16384 {
		t.Fatalf("did not select sole assignment: %+v, %v", route, err)
	}
	route, err = c.resolveNativeRoute(ctx, nil, "gpu::qwen")
	if err != nil || route.Model != "qwen" || route.Endpoint != "http://gpu.test:8000/v1" {
		t.Fatalf("qualified model did not select host: %+v, %v", route, err)
	}
	route, err = c.resolveNativeRoute(ctx, &ChatLocalEndpoint{
		Endpoint: "http://first.test:8000/v1", Model: "gpu::qwen", APIKey: "first-host-only",
	}, "")
	if err != nil || route.Endpoint != "http://gpu.test:8000/v1" || route.APIKey != "" {
		t.Fatalf("qualified model carried credentials across hosts: %+v, %v", route, err)
	}
}

func TestNativeProviderRetriesUniqueCanonicalModel(t *testing.T) {
	posts := 0
	p := nativeProvider{route: ChatLocalEndpoint{Endpoint: "http://local.test/v1", Model: "qwen", OutputTokens: 128}, http: &http.Client{Transport: nativeTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method == "GET" {
			if r.URL.Path != "/v1/models" || r.URL.Query().Get("prefix") != "canonical" {
				t.Fatalf("unexpected catalogue request: %s", r.URL)
			}
			return nativeHTTP(`{"data":[{"id":"openai/qwen"}]}`), nil
		}
		posts++
		var body struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if posts == 1 && body.Model == "qwen" {
			return &http.Response{StatusCode: 400, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"Unable to determine provider for model 'qwen'"}}`))}, nil
		}
		if posts != 2 || body.Model != "openai/qwen" {
			t.Fatalf("unexpected retry: %d, %q", posts, body.Model)
		}
		return nativeHTTP(nativeSSE("ok", "stop")), nil
	})}}
	message, err := p.turn(context.Background(), []nativeMessage{{Role: "user", Content: "hi"}}, nil, nil)
	if err != nil || message.Content != "ok" || p.route.Model != "openai/qwen" || posts != 2 {
		t.Fatalf("canonical retry failed: %+v, %v, posts=%d", message, err, posts)
	}
}

func TestNativeCancellation(t *testing.T) {
	p := nativeProvider{route: ChatLocalEndpoint{Endpoint: "http://local.test/v1"}, http: &http.Client{Transport: nativeTransport(func(r *http.Request) (*http.Response, error) { <-r.Context().Done(); return nil, r.Context().Err() })}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := p.turn(ctx, nil, nil, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatal(fmt.Sprintf("cancellation lost: %v", err))
	}
}

func installNativeTestAdapter(t *testing.T, a *nativeCLIAdapter) {
	t.Helper()
	old, err := GetCLIAdapter("praimate-cli")
	RegisterCLIAdapter(a)
	t.Cleanup(func() {
		if err == nil {
			RegisterCLIAdapter(old)
		} else {
			UnregisterCLIAdapter("praimate-cli")
		}
	})
}

func TestNativeDynamicSkillsUseCoreTrustAndDoNotPersistBodies(t *testing.T) {
	c, agent, v := v2AgentFixture(t)
	approveRuntimeFixture(t, v)
	agent.Skills.Bindings[0].Activation = "auto"
	agent.Skills.Budget = forgeBudget()
	run := nativeTestRun(c)
	run.local.ContextTokens = 16384
	run.settings = ChatSettings{SkillsV2: agent.Skills, SkillsLock: agent.SkillsLock, Local: &run.local}
	requests := 0
	a := &nativeCLIAdapter{http: &http.Client{Transport: nativeTransport(func(r *http.Request) (*http.Response, error) {
		requests++
		var req struct {
			Messages []nativeMessage `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		system := req.Messages[0].Content
		switch requests {
		case 1:
			if strings.Contains(system, "EXACT INSTRUCTIONS") {
				t.Fatal("auto skill loaded before request")
			}
			return nativeHTTP(nativeSSE("", "tool_calls", nativeCall("load", "skill_load", fmt.Sprintf(`{"ref":%q,"digest":%q}`, v.Ref, v.Digest)))), nil
		case 2:
			if strings.Count(system, "EXACT INSTRUCTIONS") != 1 {
				t.Fatal("skill body not delivered exactly once")
			}
			return nativeHTTP(nativeSSE("", "tool_calls", nativeCall("read", "skill_read", fmt.Sprintf(`{"ref":%q,"digest":%q,"path":"LICENSE","start":1,"lines":10}`, v.Ref, v.Digest)))), nil
		default:
			if !strings.Contains(system, "TEST LICENSE") {
				t.Fatal("selected resource missing")
			}
			return nativeHTTP(nativeSSE("done", "stop")), nil
		}
	})}}
	reply, err := a.SingleShot(withNativeExecution(context.Background(), run), SingleShotOpts{Cwd: t.TempDir(), Message: "review"})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := c.GetSetting(context.Background(), ScopeCLI, "native.session."+reply.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "EXACT INSTRUCTIONS") || strings.Contains(string(raw), "TEST LICENSE") {
		t.Fatal("skill bodies persisted in protocol checkpoint")
	}
}

func TestNativeAutonomousRunsKeepCoreMemoryArtifactsAndBudgets(t *testing.T) {
	c := nativeTestCore(t)
	agent := autonomousTestAgent("native-managed", "praimate-cli")
	saveAutonomousRuntime(t, agent.ID)
	steps := []string{`{"action":"tool","tool":"memory.note","arguments":{"content":"checked parser"}}`, `{"action":"tool","tool":"artifact.write","arguments":{"name":"report.md","content":"native report"}}`, `{"action":"finish","message":"Done safely."}`}
	requests := 0
	a := &nativeCLIAdapter{http: &http.Client{Transport: nativeTransport(func(r *http.Request) (*http.Response, error) {
		var req struct {
			Tools []nativeTool `json:"tools"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if len(req.Tools) != 0 {
			t.Fatal("Autonomous provider can execute native tools")
		}
		if requests >= len(steps) {
			t.Fatal("managed finish ignored")
		}
		body := steps[requests]
		requests++
		return nativeHTTP(nativeSSE(body, "stop")), nil
	})}}
	installNativeTestAdapter(t, a)
	run, err := c.RunManagedAgent(context.Background(), ManagedRunRequest{Surface: SurfaceChat, Agent: agent, CLI: "praimate-cli", Cwd: t.TempDir(), Task: "review", Local: &ChatLocalEndpoint{Endpoint: "http://local.test/v1", Model: "local", ContextTokens: 16384, OutputTokens: 1024}})
	if err != nil {
		t.Fatal(err)
	}
	if run.State != "completed" || len(run.Memory) != 1 || len(run.Artifacts) != 1 {
		t.Fatalf("managed run lost core features: %+v", run)
	}
}

func TestNativeMCPUsesRegisteredSelectionAndActionApprovals(t *testing.T) {
	c := nativeTestCore(t)
	run := nativeTestRun(c)
	run.servers = []MCPServer{{ID: "stdio-with.dots", Name: "test", Transport: MCPTransportStdio, Command: os.Args[0], Args: []string{"-test.run=TestManagedStdioMCPClient"}, Env: map[string]string{"PRAIMATE_MCP_TEST_HELPER": "1"}, Enabled: true}}
	var names []string
	b, defs, err := run.nativeTools(context.Background(), t.TempDir(), "ask", &ApprovalConfig{Request: func(_ context.Context, name string, args map[string]any) (bool, error) {
		names = append(names, name)
		if !strings.HasPrefix(name, "mcp.connect.") && args["arguments"] == nil {
			t.Fatal("approval omitted MCP arguments")
		}
		return true, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	text, err := executeNativeTool(context.Background(), b, defs, nativeCall("id", nativeMCPName("stdio-with.dots", "echo"), `{"value":"hello"}`))
	if err != nil || !strings.Contains(text, "hello") {
		t.Fatalf("MCP call: %q %v", text, err)
	}
	if len(names) != 2 || names[0] != "mcp.connect.stdio-with.dots" || names[1] != "mcp.stdio-with.dots.echo" {
		t.Fatalf("approval bypass: %v", names)
	}
}

func TestNativeExplicitCompactionAndAttachmentGrants(t *testing.T) {
	c := nativeTestCore(t)
	id, _ := nativeID()
	s := &nativeSession{ID: id, Messages: []nativeMessage{{Role: "system", Content: "rules"}, {Role: "user", Content: "important task"}, {Role: "assistant", Content: "work already done"}}}
	for i := 0; i < 8; i++ {
		s.Messages = append(s.Messages, nativeMessage{Role: "assistant", Content: strings.Repeat("later detail ", 500)})
	}
	if err := c.saveNativeSession(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if err := c.compactNativeSession(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	raw, _ := c.GetSetting(context.Background(), ScopeCLI, "native.session."+id)
	if !strings.Contains(string(raw), "important task") || !strings.Contains(string(raw), "work already done") {
		t.Fatal("explicit compact lost all context")
	}
	path := filepath.Join(t.TempDir(), "attachment.txt")
	if err := os.WriteFile(path, []byte("selected document"), 0600); err != nil {
		t.Fatal(err)
	}
	text, err := readNativeAttachment([]string{path}, json.RawMessage(`{"index":0}`))
	if err != nil || !strings.Contains(text, "selected document") {
		t.Fatalf("attachment: %q %v", text, err)
	}
	if _, err := readNativeAttachment([]string{path}, json.RawMessage(`{"index":1}`)); err == nil {
		t.Fatal("read unselected attachment")
	}
	if _, err := readNativeAttachment([]string{path}, json.RawMessage(`{"index":0,"path":"elsewhere"}`)); err == nil {
		t.Fatal("model can override granted attachment path")
	}
}

func TestNativeFailedToolOnlyTurnRetainsCheckpointAndDeleteRemovesIt(t *testing.T) {
	c := nativeTestCore(t)
	root := t.TempDir()
	calls := 0
	a := &nativeCLIAdapter{http: &http.Client{Transport: nativeTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return nativeHTTP(nativeSSE("", "tool_calls", nativeCall("w", "write_file", `{"path":"done.txt","content":"done"}`))), nil
		}
		return nativeHTTP("data: {\"error\":{\"message\":\"context length exceeded\"}}\n\n"), nil
	})}}
	installNativeTestAdapter(t, a)
	chat, err := c.CreateChat(context.Background(), CreateChatRequest{CLIAgent: "praimate-cli", WorkspacePath: root, Settings: ChatSettings{Tools: "edits", ToolsConfigured: true, Local: &ChatLocalEndpoint{Endpoint: "http://local.test/v1", Model: "local"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.ContinueChat(context.Background(), chat.ID, "write", root, ""); err == nil {
		t.Fatal("provider failure reported success")
	}
	if calls != 2 {
		t.Fatal("core replayed a native turn after tool side effects")
	}
	chat, err = c.GetChat(context.Background(), chat.ID)
	if err != nil || !nativeIDPattern.MatchString(chat.SessionID) {
		t.Fatalf("lost interrupted checkpoint: %+v %v", chat, err)
	}
	raw, err := c.GetSetting(context.Background(), ScopeCLI, "native.session."+chat.SessionID)
	if err != nil || !strings.Contains(string(raw), "done.txt") {
		t.Fatalf("missing checkpoint: %v", err)
	}
	if err := c.DeleteChat(context.Background(), chat.ID); err != nil {
		t.Fatal(err)
	}
	raw, err = c.GetSetting(context.Background(), ScopeCLI, "native.session."+chat.SessionID)
	if err != nil || len(raw) != 0 {
		t.Fatalf("deleted chat retained native transcript: %q %v", raw, err)
	}
}
