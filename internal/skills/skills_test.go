package skills

import (
	"archive/zip"
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestDeriveName(t *testing.T) {
	cases := map[string]string{
		"https://github.com/juliusbrussee/caveman.git": "caveman",
		"https://github.com/org/Cool-Tool":             "cool-tool",
		"git@github.com:org/Some-Repo.git":             "some-repo",
		"file:///tmp/myrepo":                           "myrepo",
		"myrepo":                                       "myrepo",
		"myrepo.git":                                   "myrepo",
		"https://example.com/p/under_scored":           "under_scored",
		"https://example.com/p/MixCASE":                "mixcase",
	}
	for in, want := range cases {
		got, err := DeriveName(in)
		if err != nil {
			t.Errorf("DeriveName(%q) err: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("DeriveName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFetchAll_ClonesLocalRepo(t *testing.T) {
	// Skip if git isn't installed (some CI containers lack it).
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not on PATH: %v", err)
	}

	// Build a minimal local bare repo to act as the "remote".
	src := filepath.Join(t.TempDir(), "fakeskill")
	t.Cleanup(func() {
		_ = filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
			if err == nil && info != nil && info.Mode().IsRegular() {
				_ = os.Chmod(path, info.Mode().Perm()|0600)
			}
			return nil
		})
	})
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("# fakeskill\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"add", "."},
		{"-c", "user.email=t@e", "-c", "user.name=t", "commit", "-qm", "init"},
	} {
		c := exec.Command("git", args...)
		c.Dir = src
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	target := filepath.Join(t.TempDir(), "skills")
	results := FetchAll(context.Background(), []string{src}, target)
	if len(results) != 1 {
		t.Fatalf("results = %v", results)
	}
	r := results[0]
	if r.Err != nil {
		t.Fatalf("FetchAll err: %v", r.Err)
	}
	want := filepath.Join(target, "fakeskill", "SKILL.md")
	if _, err := os.Stat(want); err != nil {
		t.Errorf("expected %s: %v", want, err)
	}

	// Idempotent: second call shouldn't re-clone.
	mtime1Path := filepath.Join(target, "fakeskill")
	stat1, _ := os.Stat(mtime1Path)
	results = FetchAll(context.Background(), []string{src}, target)
	stat2, _ := os.Stat(mtime1Path)
	if stat1.ModTime() != stat2.ModTime() {
		t.Error("second FetchAll re-cloned (mtime changed)")
	}
	if results[0].Err != nil {
		t.Errorf("second call err: %v", results[0].Err)
	}
}

func TestFetchAll_CapturesPerURLErrors(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not on PATH")
	}
	target := t.TempDir()
	// Nonexistent path → git clone fails.
	results := FetchAll(context.Background(), []string{"/this/path/does/not/exist"}, target)
	if len(results) != 1 || results[0].Err == nil {
		t.Errorf("expected error, got %v", results)
	}
}

func TestIsZipURL(t *testing.T) {
	cases := map[string]bool{
		"https://example.com/skill.zip":         true,
		"https://example.com/path/skill.ZIP":    true,
		"https://example.com/skill.zip?token=x": true,
		"http://example.com/skill.zip":          true,
		"https://example.com/skill.tar.gz":      false,
		"https://github.com/org/repo":           false,
		"https://github.com/org/repo.git":       false,
		"git@github.com:org/repo.git":           false,
		"file:///tmp/skill.zip":                 false, // we only do http(s) zip
		"":                                      false,
	}
	for in, want := range cases {
		if got := isZipURL(in); got != want {
			t.Errorf("isZipURL(%q) = %v, want %v", in, got, want)
		}
	}
}

// buildSampleZip writes a zip whose contents look like a GitHub
// archive download: every entry under a single top-level directory.
// Returns the raw bytes ready to serve over HTTP.
func buildSampleZip(t *testing.T, topDir string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	files := map[string][]byte{
		topDir + "/SKILL.md":          []byte("# fake skill\n"),
		topDir + "/scripts/hello.sh":  []byte("#!/usr/bin/env bash\necho hi\n"),
		topDir + "/scripts/inner.txt": []byte("inner\n"),
	}
	for name, body := range files {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestFetchAll_ZipFlattensTopDir(t *testing.T) {
	// Serve a GitHub-style zip from an in-process http server so we
	// don't need network access. The top-level directory inside the
	// archive should be stripped on extraction.
	zipBytes := buildSampleZip(t, "myrepo-main")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write(zipBytes)
	}))
	defer srv.Close()

	target := filepath.Join(t.TempDir(), "skills")
	results := FetchAll(context.Background(),
		[]string{srv.URL + "/myrepo-main.zip"}, target)
	if len(results) != 1 || results[0].Err != nil {
		t.Fatalf("fetch failed: %#v", results)
	}

	// Skill files should land directly under the derived name (no
	// "myrepo-main/" middle layer).
	want := filepath.Join(target, "myrepo-main", "SKILL.md")
	if _, err := os.Stat(want); err != nil {
		t.Errorf("expected %s: %v", want, err)
	}
	wantInner := filepath.Join(target, "myrepo-main", "scripts", "hello.sh")
	if _, err := os.Stat(wantInner); err != nil {
		t.Errorf("expected %s: %v", wantInner, err)
	}
}

func TestFetchAll_ZipIdempotent(t *testing.T) {
	zipBytes := buildSampleZip(t, "tool-v1")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(zipBytes)
	}))
	defer srv.Close()
	target := filepath.Join(t.TempDir(), "skills")

	results := FetchAll(context.Background(), []string{srv.URL + "/tool.zip"}, target)
	if results[0].Err != nil {
		t.Fatalf("first fetch failed: %v", results[0].Err)
	}
	stat1, _ := os.Stat(filepath.Join(target, "tool"))

	results = FetchAll(context.Background(), []string{srv.URL + "/tool.zip"}, target)
	if results[0].Err != nil {
		t.Errorf("second fetch err: %v", results[0].Err)
	}
	stat2, _ := os.Stat(filepath.Join(target, "tool"))
	if stat1.ModTime() != stat2.ModTime() {
		t.Error("second fetch re-downloaded (mtime changed)")
	}
}

func TestFetchAll_ZipBlocksZipSlip(t *testing.T) {
	// Hand-craft a zip with a "../escape.txt" entry — should be
	// rejected outright.
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	f, _ := w.Create("../escape.txt")
	_, _ = f.Write([]byte("you shouldn't see this"))
	_ = w.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(buf.Bytes())
	}))
	defer srv.Close()

	target := filepath.Join(t.TempDir(), "skills")
	results := FetchAll(context.Background(), []string{srv.URL + "/evil.zip"}, target)
	if len(results) != 1 || results[0].Err == nil {
		t.Fatalf("expected zip-slip error, got %#v", results)
	}
}

func TestFetchAll_ZipHttpError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	}))
	defer srv.Close()
	target := filepath.Join(t.TempDir(), "skills")
	results := FetchAll(context.Background(), []string{srv.URL + "/missing.zip"}, target)
	if len(results) != 1 || results[0].Err == nil {
		t.Fatalf("expected 404 error, got %#v", results)
	}
}
