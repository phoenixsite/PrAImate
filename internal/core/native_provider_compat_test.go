package core

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestNativeProviderAcceptsJSONCompletionWithoutRepeatingRequest(t *testing.T) {
	for _, tc := range []struct {
		name, body, text, reasoning string
		calls                       int
		wantErr                     bool
	}{
		{"text", `{"choices":[{"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":100,"completion_tokens":4,"total_tokens":104}}`, "hello", "", 0, false},
		{"reasoning", `{"choices":[{"message":{"content":"answer","reasoning":"thought"},"finish_reason":"stop"}]}`, "answer", "thought", 0, false},
		{"tools", `{"choices":[{"message":{"content":null,"tool_calls":[{"id":"read","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"a\"}"}},{"id":"list","type":"function","function":{"name":"list_files","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`, "", "", 2, false},
		{"length", `{"choices":[{"message":{"content":"partial"},"finish_reason":"length"}]}`, "partial", "", 0, true},
		{"bad-arguments", `{"choices":[{"message":{"tool_calls":[{"id":"call","function":{"name":"read_file","arguments":"{"}}]},"finish_reason":"tool_calls"}]}`, "", "", 0, true},
		{"bad-tool-type", `{"choices":[{"message":{"tool_calls":[{"id":"call","type":"unexpected","function":{"name":"read_file","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`, "", "", 0, true},
		{"api-error", `{"error":{"message":"temporary failure"}}`, "", "", 0, true},
		{"missing-finish", `{"choices":[{"message":{"content":"partial"}}]}`, "partial", "", 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := 0
			p := nativeProvider{route: ChatLocalEndpoint{Endpoint: "http://local.test/v1", Model: "fixture"}, http: &http.Client{Transport: nativeTransport(func(*http.Request) (*http.Response, error) {
				requests++
				res := nativeHTTP(tc.body)
				res.Header.Set("Content-Type", "application/json; charset=utf-8")
				return res, nil
			})}}
			var streamed strings.Builder
			reply, err := p.turn(context.Background(), []nativeMessage{{Role: "user", Content: "hello"}}, nil, func(ev StreamEvent) {
				if ev.Type == "text" {
					streamed.WriteString(ev.Text)
				}
			})
			if (err != nil) != tc.wantErr {
				t.Fatalf("error=%v want error=%v", err, tc.wantErr)
			}
			if reply.Content != tc.text || reply.Reasoning != tc.reasoning || len(reply.ToolCalls) != tc.calls {
				t.Fatalf("unexpected completion: %+v", reply)
			}
			if streamed.String() != tc.text || requests != 1 {
				t.Fatalf("streamed=%q requests=%d", streamed.String(), requests)
			}
			if tc.name == "text" && (reply.Usage == nil || reply.Usage.TotalTokens != 104) {
				t.Fatal("JSON completion lost endpoint usage")
			}
			if tc.name == "length" && !errors.Is(err, errNativeOutputLimit) {
				t.Fatal("JSON output limit did not enter native recovery")
			}
		})
	}
}

func TestNativeProviderDetectsActualResponseFormat(t *testing.T) {
	for _, tc := range []struct{ name, header, body, want string }{
		{"json-without-content-type", "", ` {"choices":[{"message":{"content":"JSON reply"},"finish_reason":"stop"}]}`, "JSON reply"},
		{"json-with-stream-content-type", "text/event-stream", `{"choices":[{"message":{"content":"JSON reply"},"finish_reason":"stop"}]}`, "JSON reply"},
		{"stream-with-json-content-type", "application/json", nativeSSE("stream reply", "stop"), "stream reply"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := nativeProvider{route: ChatLocalEndpoint{Endpoint: "http://local.test/v1", Model: "fixture"}, http: &http.Client{Transport: nativeTransport(func(*http.Request) (*http.Response, error) {
				res := nativeHTTP(tc.body)
				res.Header.Set("Content-Type", tc.header)
				return res, nil
			})}}
			reply, err := p.turn(context.Background(), []nativeMessage{{Role: "user", Content: "hello"}}, nil, nil)
			if err != nil || reply.Content != tc.want {
				t.Fatalf("wrong response format: %+v %v", reply, err)
			}
		})
	}
}

func TestNativeProviderTransientRecoveryIsBounded(t *testing.T) {
	for _, tc := range []struct {
		name                                         string
		status, successAt, wantRequests, wantRetries int
		wantError                                    bool
	}{
		{"recovers", 500, 2, 2, 1, false},
		{"exhausted", 503, 0, 3, 2, true},
		{"authentication", 401, 0, 1, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests, retries := 0, 0
			p := nativeProvider{route: ChatLocalEndpoint{Endpoint: "http://local.test/v1", Model: "fixture"}, http: &http.Client{Transport: nativeTransport(func(*http.Request) (*http.Response, error) {
				requests++
				if requests == tc.successAt {
					return nativeHTTP(nativeSSE("recovered", "stop")), nil
				}
				res := nativeHTTP(`{"error":{"message":"backend unavailable"}}`)
				res.StatusCode = tc.status
				return res, nil
			})}}
			_, err := p.turn(context.Background(), []nativeMessage{{Role: "user", Content: "hello"}}, nil, func(ev StreamEvent) {
				if ev.Type == "retry" {
					retries++
				}
			})
			if (err != nil) != tc.wantError || requests != tc.wantRequests || retries != tc.wantRetries {
				t.Fatalf("error=%v requests=%d retries=%d", err, requests, retries)
			}
		})
	}
}

func TestNativeProviderReportsTransientRetriesAndAllowsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	requests := 0
	p := nativeProvider{route: ChatLocalEndpoint{Endpoint: "http://local.test/v1", Model: "fixture"}, http: &http.Client{Transport: nativeTransport(func(*http.Request) (*http.Response, error) {
		requests++
		res := nativeHTTP(`{"error":{"message":"unavailable"}}`)
		res.StatusCode = 500
		return res, nil
	})}}
	retries := 0
	_, err := p.turn(ctx, []nativeMessage{{Role: "user", Content: "hello"}}, nil, func(ev StreamEvent) {
		if ev.Type == "retry" {
			retries++
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) || requests != 1 || retries != 1 || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("error=%v requests=%d retries=%d", err, requests, retries)
	}
}
