package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/term"
)

func testEditor(t *testing.T, keys string, approval bool) (string, bool, error) {
	t.Helper()
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	input := newTerminalInput(bufio.NewReader(reader))
	defer close(input.done)
	ctx, stop := context.WithTimeout(context.Background(), 3*time.Second)
	defer stop()
	prompt := input.begin(ctx)
	defer prompt.close()
	editor := term.NewTerminal(struct {
		io.Reader
		io.Writer
	}{prompt, io.Discard}, "> ")
	editor.AutoCompleteCallback = completeCommand
	go func() { _, _ = io.WriteString(writer, keys) }()
	return readEditedInput(editor, prompt, approval)
}

func TestNativeTerminalEditingPasteAndCommands(t *testing.T) {
	for _, tc := range []struct {
		name, keys, want string
		pasted           bool
	}{
		{"edit", "helo\x1b[Dlo\x7f\r", "hello", false},
		{"complete", "/cont\t\r", "/context ", false},
		{"multiline", "first\\\rsecond\r", "first\nsecond", true},
		{"triple_quote", "\"\"\"first line\rsecond line\r\"\"\"\r", "first line\nsecond line", true},
		{"triple_quote_inline", "\"\"\"inline content\"\"\"\r", "inline content", false},
		{"paste", "\x1b[200~first\nsecond\x1b[201~\r", "first\nsecond", true},
		{"pasted_command", "\x1b[200~/clear\x1b[201~\r", "/clear", true},
		{"partial_paste", "prefix \x1b[200~/clear\x1b[201~\r", "prefix /clear", true},
		{"pasted_newline", "\x1b[200~first\n\x1b[201~\r", "first\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			line, pasted, err := testEditor(t, tc.keys, false)
			if err != nil || line != tc.want || pasted != tc.pasted {
				t.Fatalf("line=%q pasted=%v err=%v", line, pasted, err)
			}
		})
	}
}

func TestNativeTerminalInterruptEOFAndLimits(t *testing.T) {
	if _, _, err := testEditor(t, "partial\x03", false); !errors.Is(err, errInputCanceled) {
		t.Fatal(err)
	}
	if _, _, err := testEditor(t, "\x04", false); !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
	if _, _, err := testEditor(t, strings.Repeat("a", 4100)+"\r", false); err == nil || !strings.Contains(err.Error(), "4096") {
		t.Fatalf("silently truncated long input: %v", err)
	}
	for _, keys := range []string{"\x1b[200~y\x1b[201~\r", "\x1b[200~y\n\x1b[201~\r"} {
		if _, pasted, err := testEditor(t, keys, true); err != nil || !pasted {
			t.Fatalf("pasted approval not marked: %v %v", pasted, err)
		}
	}
}

func TestNativeTerminalHistoryCompletionAndOutput(t *testing.T) {
	var history terminalHistory
	history.Add("approval y")
	history.remember("hello")
	history.remember("hello")
	history.remember("/context")
	if history.Len() != 2 || history.At(0) != "/context" {
		t.Fatal(history)
	}
	if result, _, ok := completeCommand("/mo", 3, '\t'); !ok || result != "/model" {
		t.Fatalf("ambiguous completion: %q %v", result, ok)
	}
	var output bytes.Buffer
	text := "normal\x1b]52;c;unsafe\a\r\u009b31m\nUTF8: 日本語 😀\tOK"
	n, err := (safeTerminalWriter{&output}).Write([]byte(text))
	if n != len(text) || err != nil || strings.ContainsAny(output.String(), "\x1b\a\r\u009b") || !strings.Contains(output.String(), "日本語 😀") {
		t.Fatalf("unsafe/corrupted rendering: %q %v", output.String(), err)
	}
}

func TestNativeTerminalArgumentCompletion(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "screen shot.png"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ input, want string }{
		{"/tools ed", "/tools edits "},
		{"/model qwen", "/model qwen3-coder "},
		{"/detach 2", "/detach 2 "},
		{"/attach scr", `/attach "screen shot.png" `},
	} {
		got, _, ok := completeInteractive(tc.input, len(tc.input), '\t', []string{"qwen3-coder", "llama"}, []string{"one", "two"}, root)
		if !ok || got != tc.want {
			t.Errorf("complete %q = %q (ok=%v), want %q", tc.input, got, ok, tc.want)
		}
	}
}

func TestNativeCLIQueuedAttachments(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "screen shot.png")
	if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	paths, err := queueAttachment(nil, root, `"screen shot.png"`)
	if err != nil || len(paths) != 1 || paths[0] != path {
		t.Fatalf("%v %v", paths, err)
	}
	paths, err = queueAttachment(paths, root, path)
	if err != nil || len(paths) != 1 {
		t.Fatal("duplicate attachment queued")
	}
	if _, err := queueAttachment(paths, root, "."); err == nil {
		t.Fatal("accepted directory")
	}
	if _, err := detachAttachment(paths, "0"); err == nil {
		t.Fatal("accepted invalid index")
	}
	paths, err = detachAttachment(paths, "1")
	if err != nil || len(paths) != 0 {
		t.Fatal("detach failed")
	}
}

func TestNativeTerminalCancellationDoesNotStealNextInput(t *testing.T) {
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	input := newTerminalInput(bufio.NewReader(reader))
	defer close(input.done)
	ctx, cancel := context.WithCancel(context.Background())
	first := input.begin(ctx)
	cancel()
	if _, err := first.Read(make([]byte, 1)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	first.close()
	nextCtx, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	next := input.begin(nextCtx)
	defer next.close()
	go func() { _, _ = writer.Write([]byte("x")) }()
	b := make([]byte, 1)
	if n, err := next.Read(b); err != nil || n != 1 || b[0] != 'x' {
		t.Fatalf("next prompt lost input: %q %v", b, err)
	}
}
