package core

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Client.Timeout covers the response body, so streamed completions must not
// set it: a reasoning model can legitimately stream for longer than 3 minutes.
func TestNativeClientTimeoutDoesNotCapStreamedCompletions(t *testing.T) {
	if got := nativeClientTimeout(http.MethodPost); got != 0 {
		t.Fatalf("completion timeout = %s, want none (bounded by context)", got)
	}
	if got := nativeClientTimeout(http.MethodGet); got != 3*time.Minute {
		t.Fatalf("metadata timeout = %s", got)
	}
}

func nativeIdleServer(t *testing.T, step func(w http.ResponseWriter, f http.Flusher)) nativeProvider {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		f := w.(http.Flusher)
		f.Flush()
		step(w, f)
	}))
	t.Cleanup(srv.Close)
	return nativeProvider{route: ChatLocalEndpoint{Endpoint: srv.URL + "/v1"}}
}

func setNativeIdle(t *testing.T, d time.Duration) {
	old := nativeStreamIdleTimeout
	nativeStreamIdleTimeout = d
	t.Cleanup(func() { nativeStreamIdleTimeout = old })
}

// A stream that keeps producing data must outlive the idle window.
func TestNativeIdleTimeoutResetsOnActivity(t *testing.T) {
	setNativeIdle(t, 150*time.Millisecond)
	p := nativeIdleServer(t, func(w http.ResponseWriter, f http.Flusher) {
		for i := 0; i < 6; i++ { // ~300ms total, > idle window
			time.Sleep(50 * time.Millisecond)
			io.WriteString(w, ": thinking\n\n")
			f.Flush()
		}
	})
	res, err := p.request(context.Background(), http.MethodPost, "/chat/completions", []byte("{}"))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if _, err := io.ReadAll(res.Body); err != nil {
		t.Fatalf("active stream was cut: %v", err)
	}
}

// A backend that goes silent must fail with a clear idle error.
func TestNativeIdleTimeoutCutsSilentStream(t *testing.T) {
	setNativeIdle(t, 100*time.Millisecond)
	p := nativeIdleServer(t, func(w http.ResponseWriter, f http.Flusher) {
		io.WriteString(w, ": hello\n\n")
		f.Flush()
		time.Sleep(2 * time.Second)
	})
	res, err := p.request(context.Background(), http.MethodPost, "/chat/completions", []byte("{}"))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	_, err = io.ReadAll(res.Body)
	if err == nil || !strings.Contains(err.Error(), "sent no data") {
		t.Fatalf("err = %v, want idle error", err)
	}
}
