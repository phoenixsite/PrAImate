package updater

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// withDownloadTimings shrinks the total and idle limits for a test and
// restores them afterwards. The total limit is set to the old fixed value
// so the test reproduces the original bug if it returns.
func withDownloadTimings(t *testing.T, total, idle time.Duration) {
	t.Helper()
	prevClient, prevIdle := httpClient, downloadIdleTimeout
	httpClient = &http.Client{Timeout: total}
	downloadIdleTimeout = idle
	t.Cleanup(func() {
		httpClient = prevClient
		downloadIdleTimeout = prevIdle
	})
}

func TestDownloadToTempSurvivesSlowActiveDownload(t *testing.T) {
	// Total limit 200ms; the body trickles for ~600ms but never stalls for
	// longer than the 100ms idle limit. It must complete.
	withDownloadTimings(t, 200*time.Millisecond, 100*time.Millisecond)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		for i := 0; i < 12; i++ {
			_, _ = w.Write([]byte(strings.Repeat("x", 1024)))
			if flusher != nil {
				flusher.Flush()
			}
			time.Sleep(50 * time.Millisecond)
		}
	}))
	t.Cleanup(srv.Close)

	asset := &Asset{Name: "praimate-linux-amd64.tar.gz", BrowserDownloadURL: srv.URL}
	path, err := downloadToTemp(asset)
	if err != nil {
		t.Fatalf("slow but active download failed: %v", err)
	}
	defer os.Remove(path)

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := int64(12 * 1024); info.Size() != want {
		t.Fatalf("downloaded %d bytes, want %d", info.Size(), want)
	}
}

func TestDownloadToTempFailsWhenStalled(t *testing.T) {
	// The server sends headers and a little data, then blocks forever.
	// The idle limit must cancel the download instead of hanging.
	withDownloadTimings(t, time.Minute, 100*time.Millisecond)

	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("partial"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) }) // runs before srv.Close (LIFO)

	asset := &Asset{Name: "praimate-linux-amd64.tar.gz", BrowserDownloadURL: srv.URL}
	start := time.Now()
	path, err := downloadToTemp(asset)
	if err == nil {
		os.Remove(path)
		t.Fatal("stalled download succeeded; want idle-timeout error")
	}
	if !strings.Contains(err.Error(), "no data received") {
		t.Fatalf("error = %v; want idle-timeout message", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("stalled download took %v; idle limit not enforced", elapsed)
	}
}
