package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sPROFFEs/PrAImate/internal/core"
)

func TestSessionViewShowsHonestCompletionState(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{{nil, "Done"}, {context.Canceled, "Cancelled"}, {errors.New("failed"), "Failed"}} {
		var out bytes.Buffer
		v := sessionView{out: &out, width: 80}
		v.finish(time.Second, 2, nil, tc.err)
		if !strings.Contains(out.String(), tc.want) || (tc.err != nil && strings.Contains(out.String(), "Done")) {
			t.Fatal(out.String())
		}
	}
}

func TestSessionViewSanitizesMetadataAndRespectsNoColor(t *testing.T) {
	var out bytes.Buffer
	v := sessionView{out: &out, width: 80}
	v.banner("test", "work\x1b]52;c;injection\a", "session")
	v.session("local-model", "safe", nil, 2, 3)
	if strings.ContainsAny(out.String(), "\x1b\a") || !strings.Contains(out.String(), "2 attached") || !strings.Contains(out.String(), "3 pinned") {
		t.Fatal(out.String())
	}
}

func TestSessionViewRestoresSavedDialogueWithoutChangingTranscript(t *testing.T) {
	t.Setenv("PRAIMATE_HOME", t.TempDir())
	c, closeCore, err := openCore(bufio.NewReader(strings.NewReader("test-password\n")), true, false)
	if err != nil {
		t.Fatal(err)
	}
	defer closeCore()
	ctx := context.Background()
	chat, err := c.CreateChat(ctx, core.CreateChatRequest{CLIAgent: "praimate-cli"})
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range []core.Message{
		{Role: "system", Content: "private-system-instructions"},
		{Role: "user", Content: "saved-question\x1b]52;c;injection\a"},
		{Role: "assistant", Content: "```go\npartial-code", Meta: map[string]any{"interrupted": true}},
		{Role: "tool", Content: "internal-tool-result"},
		{Role: "user", Meta: map[string]any{"attachments": []string{"/fixture/screen shot.png"}}},
		{Role: "assistant", Content: "# Latest reply\nSaved answer", Meta: map[string]any{"error": "test-failure\x1b[2J"}},
		{Role: "command", Content: "literal **command** output"},
	} {
		if _, err := c.AddMessage(ctx, chat.ID, message.Role, message.Content, message.Meta); err != nil {
			t.Fatal(err)
		}
	}
	before, err := c.ListMessages(ctx, chat.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	v := sessionView{out: &out, width: 80}
	if err := v.restoreHistory(ctx, c, chat.ID); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{"5 saved message(s)", "saved-question", "partial-code", "interrupted", "Attached: screen shot.png", "LATEST REPLY", "Saved answer", "failed", "test-failure", "literal **command** output"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in restored history: %s", want, text)
		}
	}
	if strings.Contains(text, "private-system-instructions") || strings.Contains(text, "internal-tool-result") || strings.ContainsAny(text, "\x1b\a") {
		t.Fatalf("unsafe or internal content rendered: %q", text)
	}
	if strings.Index(text, "saved-question") >= strings.Index(text, "Saved answer") {
		t.Fatal("restored history is not chronological")
	}
	after, err := c.ListMessages(ctx, chat.ID, 0)
	if err != nil || len(after) != len(before) || after[len(after)-1].ID != before[len(before)-1].ID {
		t.Fatalf("viewing history changed saved messages: %+v, %v", after, err)
	}
	out.Reset()
	if err := v.restoreHistory(ctx, c, "empty-chat"); err != nil || out.Len() != 0 {
		t.Fatalf("empty chat replay: %q, %v", out.String(), err)
	}
}
