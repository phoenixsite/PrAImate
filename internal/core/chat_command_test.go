package core

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestChatCommandCancellationRetainsPartialOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell fixture")
	}
	c := nativeTestCore(t)
	root := t.TempDir()
	chat, err := c.CreateChat(context.Background(), CreateChatRequest{CLIAgent: "praimate-cli", WorkspacePath: root})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type result struct {
		turn *ChatTurn
		err  error
	}
	done := make(chan result, 1)
	go func() {
		turn, err := c.RunChatCommand(ctx, chat.ID, "printf 'before-cancel\\n'; touch started; sleep 10")
		done <- result{turn, err}
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(root, "started")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("command did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("cancelled command lost its result: %v", got.err)
		}
		if !strings.Contains(got.turn.Reply, "before-cancel") || !strings.Contains(got.turn.Reply, "[cancelled]") {
			t.Fatalf("missing partial output/cancellation marker: %q", got.turn.Reply)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancelling the shell did not stop its children promptly")
	}
	messages, err := c.ListMessages(context.Background(), chat.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 || messages[1].Role != "command" || messages[1].Meta["interrupted"] != true || !strings.Contains(messages[1].Content, "before-cancel") {
		t.Fatalf("partial command transcript was not retained: %+v", messages)
	}
}

func TestChatCommandOutputLimitPreservesUTF8(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell fixture")
	}
	c := nativeTestCore(t)
	chat, err := c.CreateChat(context.Background(), CreateChatRequest{CLIAgent: "praimate-cli", WorkspacePath: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	turn, err := c.RunChatCommand(context.Background(), chat.ID, `printf 'a'; i=0; while [ "$i" -lt 5000 ]; do printf '😀'; i=$((i+1)); done`)
	if err != nil {
		t.Fatal(err)
	}
	if !utf8.ValidString(turn.Reply) {
		t.Fatal("command output was truncated inside a UTF-8 rune")
	}
	if len(turn.Reply) > chatCommandMaxOutput+200 || !strings.Contains(turn.Reply, "output truncated") {
		t.Fatal("missing bounded output/truncation notice")
	}
}

func TestChatCommandCannotRaceANativeTurn(t *testing.T) {
	c := nativeTestCore(t)
	ctx := context.Background()
	chat, err := c.CreateChat(ctx, CreateChatRequest{CLIAgent: "praimate-cli", WorkspacePath: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	_, release, err := c.nativeLease(ctx, "chat."+chat.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err := c.RunChatCommand(ctx, chat.ID, "echo must-not-run"); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("concurrent command was not rejected: %v", err)
	}
	messages, err := c.ListMessages(ctx, chat.ID, 0)
	if err != nil || len(messages) != 0 {
		t.Fatalf("rejected command changed transcript: %+v %v", messages, err)
	}
}

func TestChatCommandCaptureIsBoundedDuringWrites(t *testing.T) {
	b := limitedBuffer{limit: chatCommandMaxOutput}
	chunk := []byte(strings.Repeat("x", 1<<20))
	for range 4 {
		n, err := b.Write(chunk)
		if err != nil || n != len(chunk) || b.b.Len() > chatCommandMaxOutput {
			t.Fatalf("unbounded/short capture: %d %v %d", n, err, b.b.Len())
		}
	}
	if !b.truncated || len(b.String()) != chatCommandMaxOutput {
		t.Fatal("bounded capture did not mark discarded output")
	}
}
