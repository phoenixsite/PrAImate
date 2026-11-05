package core

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// Many small deltas are typical of streamed local models. Keep the fixture
// transport in memory so allocations reflect parsing/assembly rather than TLS.
func BenchmarkNativeProviderStream(b *testing.B) {
	const delta = `data: {"choices":[{"delta":{"content":"incremental output ","reasoning_content":"reasoning "}}]}` + "\n\n"
	body := strings.Repeat(delta, 2000) + nativeSSE("done", "stop")
	p := nativeProvider{route: ChatLocalEndpoint{Endpoint: "http://local.test/v1", Model: "fixture"}, http: &http.Client{Transport: nativeTransport(func(*http.Request) (*http.Response, error) { return nativeHTTP(body), nil })}}
	b.ReportAllocs()
	b.SetBytes(int64(len(body)))
	b.ResetTimer()
	for b.Loop() {
		result, err := p.turn(context.Background(), []nativeMessage{{Role: "user", Content: "fixture"}}, nil, nil)
		if err != nil || len(result.Content) != 2000*len("incremental output ")+4 {
			b.Fatalf("incomplete stream: %v", err)
		}
	}
}

func BenchmarkNativeProviderToolArguments(b *testing.B) {
	frame := func(args, name, finish string) string {
		raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "write", "function": map[string]string{"name": name, "arguments": args}}}}, "finish_reason": finish}}})
		return "data: " + string(raw) + "\n\n"
	}
	body := frame(`{"path":"file","content":"`, "write_file", "") + strings.Repeat(frame(strings.Repeat("x", 32), "", ""), 2000) + frame(`"}`, "", "tool_calls") + "data: [DONE]\n\n"
	p := nativeProvider{route: ChatLocalEndpoint{Endpoint: "http://local.test/v1", Model: "fixture"}, http: &http.Client{Transport: nativeTransport(func(*http.Request) (*http.Response, error) { return nativeHTTP(body), nil })}}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		reply, err := p.turn(context.Background(), []nativeMessage{{Role: "user", Content: "fixture"}}, nil, nil)
		if err != nil || len(reply.ToolCalls) != 1 {
			b.Fatalf("invalid fragmented tool call: %v", err)
		}
	}
}
