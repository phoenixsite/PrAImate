package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestCreateChat_AutoSlugAndDefaults(t *testing.T) {
	c := newMemCore(t)
	ctx := context.Background()

	ch, err := c.CreateChat(ctx, CreateChatRequest{
		Title:    "Refactor the Launcher!",
		CLIAgent: "claude",
	})
	if err != nil {
		t.Fatalf("CreateChat: %v", err)
	}
	if ch.ID == "" || !strings.Contains(ch.ID, "refactor-the-launcher") {
		t.Fatalf("expected slug-ish id, got %q", ch.ID)
	}
	if ch.Title != "Refactor the Launcher!" {
		t.Fatalf("title lost: %q", ch.Title)
	}
	if ch.EndedAt != nil {
		t.Fatal("EndedAt should be nil for a new chat")
	}
}

func TestCreateChat_RejectsMissingCLIAgent(t *testing.T) {
	c := newMemCore(t)
	if _, err := c.CreateChat(context.Background(), CreateChatRequest{Title: "x"}); err == nil {
		t.Fatal("expected CLIAgent-required error")
	}
}

func TestChatIDsAreUniqueWithSameTitleAndTimestamp(t *testing.T) {
	stamp := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	seen := make(map[string]bool)
	for range 100 {
		id := chatID(stamp, "Repeated title")
		if seen[id] {
			t.Fatalf("same-title chats in the same clock tick collide: %s", id)
		}
		seen[id] = true
		if !strings.HasPrefix(id, "20261105-100000-") || !strings.Contains(id, "repeated-title") {
			t.Fatalf("chat ID lost its readable timestamp/title: %s", id)
		}
	}
}

func TestAddMessage_BumpsChatUpdatedAt(t *testing.T) {
	c := newMemCore(t)
	ctx := context.Background()
	ch, _ := c.CreateChat(ctx, CreateChatRequest{Title: "msg test", CLIAgent: "claude"})

	_, err := c.AddMessage(ctx, ch.ID, "user", "hi", nil)
	if err != nil {
		t.Fatalf("AddMessage: %v", err)
	}
	_, _ = c.AddMessage(ctx, ch.ID, "assistant", "hello", nil)

	msgs, _ := c.ListMessages(ctx, ch.ID, 0)
	if len(msgs) != 2 || msgs[0].Role != "user" || msgs[1].Role != "assistant" {
		t.Fatalf("messages out of order: %+v", msgs)
	}
}

func TestAddMessage_PreservesUnicode(t *testing.T) {
	c := newMemCore(t)
	ctx := context.Background()
	ch, _ := c.CreateChat(ctx, CreateChatRequest{Title: "unicode", CLIAgent: "claude"})
	want := "Canción, pingüino, ¿qué tal? — € 😀 e\u0301"
	if _, err := c.AddMessage(ctx, ch.ID, "user", want, nil); err != nil {
		t.Fatalf("AddMessage: %v", err)
	}
	msgs, err := c.ListMessages(ctx, ch.ID, 0)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Content != want {
		t.Fatalf("Unicode message changed: %#v", msgs)
	}
}

func TestListRecentConversationMessagesReturnsTailWithoutInternalMessages(t *testing.T) {
	c := newMemCore(t)
	ctx := context.Background()
	chat, err := c.CreateChat(ctx, CreateChatRequest{Title: "recent dialogue", CLIAgent: "praimate-cli"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := c.CreateChat(ctx, CreateChatRequest{Title: "another dialogue", CLIAgent: "praimate-cli"})
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for i := range 25 {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		if i == 24 {
			role = "command"
		}
		message, err := c.AddMessage(ctx, chat.ID, role, fmt.Sprintf("message-%02d", i), map[string]any{"interrupted": i == 23})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, message.ID)
	}
	for range 25 {
		for _, role := range []string{"system", "tool"} {
			if _, err := c.AddMessage(ctx, chat.ID, role, "internal", nil); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := c.AddMessage(ctx, other.ID, "assistant", "another chat", nil); err != nil {
			t.Fatal(err)
		}
	}
	messages, err := c.ListRecentConversationMessages(ctx, chat.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 20 {
		t.Fatalf("want 20 recent visible messages, got %d", len(messages))
	}
	for i, message := range messages {
		if message.ID != ids[i+5] || message.Content != fmt.Sprintf("message-%02d", i+5) {
			t.Fatalf("wrong chat, oldest messages or wrong order: %+v", message)
		}
	}
	if messages[18].Meta["interrupted"] != true || messages[19].Role != "command" {
		t.Fatal("partial reply metadata or command output lost")
	}
	// The existing API still returns the first messages when given a limit.
	first, err := c.ListMessages(ctx, chat.ID, 1)
	if err != nil || len(first) != 1 || first[0].ID != ids[0] {
		t.Fatalf("ListMessages contract changed: %+v, %v", first, err)
	}
	for _, limit := range []int{0, -1} {
		if _, err := c.ListRecentConversationMessages(ctx, chat.ID, limit); err == nil {
			t.Fatalf("accepted unbounded history limit %d", limit)
		}
	}
	missing, err := c.ListRecentConversationMessages(ctx, "missing", 20)
	if err != nil || len(missing) != 0 {
		t.Fatalf("empty history: %+v, %v", missing, err)
	}
}

func TestEndChat_StampsExitKind(t *testing.T) {
	c := newMemCore(t)
	ctx := context.Background()
	ch, _ := c.CreateChat(ctx, CreateChatRequest{Title: "x", CLIAgent: "claude"})
	if err := c.EndChat(ctx, ch.ID, string(OutcomeCompleted)); err != nil {
		t.Fatalf("EndChat: %v", err)
	}
	got, _ := c.GetChat(ctx, ch.ID)
	if got.EndedAt == nil || got.ExitKind != string(OutcomeCompleted) {
		t.Fatalf("EndChat didn't stamp: %+v", got)
	}
}

func TestEndChat_NotFound(t *testing.T) {
	c := newMemCore(t)
	err := c.EndChat(context.Background(), "ghost", "x")
	if !errors.Is(err, ErrChatNotFound) {
		t.Fatalf("expected ErrChatNotFound, got %v", err)
	}
}

func TestDeleteChat_CascadesMessages(t *testing.T) {
	c := newMemCore(t)
	ctx := context.Background()
	ch, _ := c.CreateChat(ctx, CreateChatRequest{Title: "del", CLIAgent: "claude"})
	_, _ = c.AddMessage(ctx, ch.ID, "user", "first", nil)
	_, _ = c.AddMessage(ctx, ch.ID, "assistant", "second", nil)

	if err := c.DeleteChat(ctx, ch.ID); err != nil {
		t.Fatalf("DeleteChat: %v", err)
	}
	msgs, _ := c.ListMessages(ctx, ch.ID, 0)
	if len(msgs) != 0 {
		t.Fatalf("messages survived chat deletion: %+v", msgs)
	}
}

func TestListChats_NewestFirst(t *testing.T) {
	c := newMemCore(t)
	ctx := context.Background()
	a, _ := c.CreateChat(ctx, CreateChatRequest{Title: "alpha", CLIAgent: "claude"})
	_, _ = c.CreateChat(ctx, CreateChatRequest{Title: "beta", CLIAgent: "claude"})
	_, _ = c.CreateChat(ctx, CreateChatRequest{Title: "gamma", CLIAgent: "claude"})

	// Touch alpha last so it bubbles to the top via updated_at.
	_, _ = c.AddMessage(ctx, a.ID, "user", "ping", nil)

	chats, err := c.ListChats(ctx, 0)
	if err != nil {
		t.Fatalf("ListChats: %v", err)
	}
	if len(chats) != 3 {
		t.Fatalf("want 3, got %d", len(chats))
	}
	if chats[0].Title != "alpha" {
		t.Fatalf("expected alpha first (most-recent activity), got %q", chats[0].Title)
	}
}
