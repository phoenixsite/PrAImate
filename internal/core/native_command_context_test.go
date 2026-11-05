package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestNativeSessionReceivesManualCommandResultsOnce(t *testing.T) {
	c := nativeTestCore(t)
	ctx := context.Background()
	root := t.TempDir()
	chat, err := c.CreateChat(ctx, CreateChatRequest{CLIAgent: "praimate-cli", WorkspacePath: root, Settings: ChatSettings{Local: &ChatLocalEndpoint{Endpoint: "http://local.test/v1", Model: "local-model"}}})
	if err != nil {
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
		if err := validateNativeMessages(req.Messages); err != nil {
			t.Fatal(err)
		}
		first, second := 0, 0
		for _, m := range req.Messages {
			if strings.Contains(m.Content, "manual-command-result") {
				first++
			}
			if strings.Contains(m.Content, "second-command-result") {
				second++
			}
		}
		if first != 1 {
			t.Fatalf("manual output appears in %d messages, want once", first)
		}
		if requests >= 3 && second != 1 {
			t.Fatal("command issued after resume never reached model")
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
	if _, err := c.RunChatCommand(ctx, chat.ID, "echo manual-command-result"); err != nil {
		t.Fatal(err)
	}
	if requests != 0 {
		t.Fatal("manual command made an unsolicited model request")
	}
	for i := range 3 {
		if i == 2 {
			if _, err := c.RunChatCommand(ctx, chat.ID, "echo second-command-result"); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := c.ContinueChat(ctx, chat.ID, "use the result", root, ""); err != nil {
			t.Fatal(err)
		}
	}
}

func TestNativeCommandContextIsBoundedRedactedAndChatScoped(t *testing.T) {
	c := nativeTestCore(t)
	ctx := context.Background()
	chat, err := c.CreateChat(ctx, CreateChatRequest{CLIAgent: "praimate-cli", WorkspacePath: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	other, err := c.CreateChat(ctx, CreateChatRequest{CLIAgent: "praimate-cli", WorkspacePath: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	const fixtureKey = "sk-abcdefghijklmnopqrstuvwxyzz"
	var lastID int64
	for i := range 25 {
		m, err := c.AddMessage(ctx, chat.ID, "command", fmt.Sprintf("command-%02d %s", i, fixtureKey), map[string]any{"kind": "command"})
		if err != nil {
			t.Fatal(err)
		}
		lastID = m.ID
	}
	if _, err := c.AddMessage(ctx, other.ID, "command", "OTHER-CHAT-OUTPUT", nil); err != nil {
		t.Fatal(err)
	}
	run := nativeTestRun(c)
	run.chatID = chat.ID
	session := &nativeSession{}
	if err := run.appendCommandContext(ctx, session); err != nil {
		t.Fatal(err)
	}
	if len(session.Messages) != 20 || session.LastCommandID != lastID {
		t.Fatalf("unbounded or incorrect cursor: %d %d", len(session.Messages), session.LastCommandID)
	}
	for _, message := range session.Messages {
		if strings.Contains(message.Content, fixtureKey) || strings.Contains(message.Content, "OTHER-CHAT-OUTPUT") {
			t.Fatal("command context leaked raw key/another chat")
		}
	}
	if !strings.Contains(session.Messages[0].Content, "command-05") || !strings.Contains(session.Messages[19].Content, "command-24") {
		t.Fatal("command context is not the latest bounded chronological slice")
	}
	if err := run.appendCommandContext(ctx, session); err != nil {
		t.Fatal(err)
	}
	if len(session.Messages) != 20 {
		t.Fatal("same command records were inserted twice")
	}
}
