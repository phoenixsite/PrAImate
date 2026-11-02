package main

// Streaming chat bindings — the GUI-side half of the desktop-app feel:
// SendChatStream forwards core StreamEvents over the Wails event bus as
// the turn runs (text deltas, tool activity), and CancelChatTurn is the
// Stop button. CLIs without an event stream silently degrade to the
// buffered behavior — the promise just resolves with the full reply and
// no events fire first.

import (
	"context"
	"sync"
	"time"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/sPROFFEs/PrAImate/internal/core"
)

// ChatStreamEvent is what the frontend receives on the
// "praimate:chat-stream" event channel.
type ChatStreamEvent struct {
	ChatID string `json:"chatId"`
	Type   string `json:"type"` // "text" | "reasoning" | "step_*" | "tool_*" | "error"
	Text   string `json:"text,omitempty"`
	Tool   string `json:"tool,omitempty"`
	Detail string `json:"detail,omitempty"`
	ID     string `json:"id,omitempty"`
	OK     bool   `json:"ok,omitempty"`
}

type ChatFinishedEvent struct {
	ChatID string `json:"chatId"`
	Error  string `json:"error,omitempty"`
}

// SendChatStream sends one message (with optional staged attachment
// paths) and streams progress events while the CLI runs. The returned
// turn is the persisted final state — after the promise resolves the
// frontend reloads messages from the DB exactly like the non-streaming
// path.
func (a *App) SendChatStream(chatID, message string, attachments []string) (*core.ChatTurn, error) {
	if a.detachedClient != nil {
		return a.detachedClient.sendChatStream(chatID, message, attachments)
	}
	c, err := a.requireCore()
	if err != nil {
		return nil, err
	}
	chat, err := c.GetChat(a.ctx, chatID)
	if err != nil {
		return nil, err
	}
	systemPrompt := ""
	if chat.AgentID != "" {
		if agent, err := c.GetAgent(a.ctx, chat.AgentID); err == nil {
			systemPrompt = core.AgentSystemPrompt(agent)
		}
	}
	// Prepend enabled skills (CLI-agnostic at the prompt level; the
	// catalogue's per-skill CLI tag is advisory in the UI). Skills add
	// to whatever the agent itself contributes, never replace it.
	if prefix := core.ResolveSkillsPrefix(chat.Settings.Skills); prefix != "" {
		if systemPrompt != "" {
			systemPrompt = prefix + "\n\n---\n\n" + systemPrompt
		} else {
			systemPrompt = prefix
		}
	}
	if chat.Settings.Surface == "workflow" {
		systemPrompt = appendPromptContext(systemPrompt, core.WorkflowSystemContext(chat.WorkspacePath))
	}

	ctx, cancel := context.WithCancel(a.ctx)
	a.chatCancelMu.Lock()
	if old, ok := a.chatCancels[chatID]; ok {
		old() // one in-flight turn per chat; a racing second send wins
	}
	a.chatCancelSeq++
	runID := a.chatCancelSeq
	a.chatCancels[chatID] = cancel
	a.chatCancelIDs[chatID] = runID
	a.chatCancelMu.Unlock()
	cleanup := func() {
		cancel()
		a.chatCancelMu.Lock()
		// A newer racing send may already own this chat ID. Do not erase its
		// cancellation handle while the older turn unwinds.
		if a.chatCancelIDs[chatID] == runID {
			delete(a.chatCancels, chatID)
			delete(a.chatCancelIDs, chatID)
		}
		a.chatCancelMu.Unlock()
	}

	emitImmediate := func(streamEvent ChatStreamEvent) {
		wruntime.EventsEmit(a.ctx, "praimate:chat-stream", streamEvent)
		if a.detached != nil {
			a.detached.publish("chat", chatID, "praimate:chat-stream", streamEvent)
			a.detached.publish("studio", chatID, "praimate:chat-stream", streamEvent)
		}
	}
	coalescer := newStreamCoalescer(20*time.Millisecond, emitImmediate)
	defer coalescer.Flush()

	onEvent := func(ev core.StreamEvent) {
		streamEvent := ChatStreamEvent{
			ChatID: chatID,
			Type:   ev.Type,
			Text:   ev.Text,
			Tool:   ev.Tool,
			Detail: ev.Detail,
			ID:     ev.ID,
			OK:     ev.OK,
		}
		coalescer.push(streamEvent)
	}
	turn, streamErr := c.ContinueChatStream(ctx, chatID, message, chat.WorkspacePath, systemPrompt, attachments, onEvent)
	coalescer.Flush()
	cleanup()
	if a.detached != nil {
		finished := ChatFinishedEvent{ChatID: chatID}
		if streamErr != nil {
			finished.Error = streamErr.Error()
		}
		a.detached.publish("chat", chatID, "praimate:chat-finished", finished)
		a.detached.publish("studio", chatID, "praimate:chat-finished", finished)
	}
	return turn, streamErr
}

func appendPromptContext(systemPrompt, context string) string {
	if context == "" {
		return systemPrompt
	}
	if systemPrompt == "" {
		return context
	}
	return systemPrompt + "\n\n---\n\n" + context
}

// ActiveChatIDs returns the IDs of every chat that currently has an
// in-flight streamed turn (something the user can switch back to without
// losing it). Used by the Sessions panel's "live" indicator.
func (a *App) ActiveChatIDs() []string {
	a.chatCancelMu.Lock()
	defer a.chatCancelMu.Unlock()
	out := make([]string, 0, len(a.chatCancels))
	for id := range a.chatCancels {
		out = append(out, id)
	}
	return out
}

// CancelChatTurn interrupts the chat's in-flight streamed turn. No-op
// when nothing is running. The interrupted turn persists whatever text
// already streamed (flagged in the message meta).
func (a *App) CancelChatTurn(chatID string) {
	if a.detachedClient != nil {
		_ = a.detachedClient.cancelChatTurn(chatID)
		return
	}
	a.chatCancelMu.Lock()
	cancel, ok := a.chatCancels[chatID]
	a.chatCancelMu.Unlock()
	if ok {
		cancel()
	}
}

// streamCoalescer aggregates rapid sequential text deltas over a small duration
// (e.g. 20ms) to avoid overwhelming the frontend event bus and IPC serialization.
type streamCoalescer struct {
	emitFn   func(ChatStreamEvent)
	mu       sync.Mutex
	pending  ChatStreamEvent
	timer    *time.Timer
	interval time.Duration
}

func newStreamCoalescer(interval time.Duration, emitFn func(ChatStreamEvent)) *streamCoalescer {
	return &streamCoalescer{
		emitFn:   emitFn,
		interval: interval,
	}
}

func (c *streamCoalescer) push(ev ChatStreamEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if ev.Type == "text" || ev.Type == "reasoning" {
		if c.pending.Type == ev.Type {
			c.pending.Text += ev.Text
			return
		}
		c.flushLocked()
		c.pending = ev
		if c.timer == nil {
			c.timer = time.AfterFunc(c.interval, func() {
				c.mu.Lock()
				c.flushLocked()
				c.mu.Unlock()
			})
		} else {
			c.timer.Reset(c.interval)
		}
		return
	}

	c.flushLocked()
	c.emitFn(ev)
}

func (c *streamCoalescer) flushLocked() {
	if c.timer != nil {
		c.timer.Stop()
	}
	if c.pending.Type != "" {
		c.emitFn(c.pending)
		c.pending = ChatStreamEvent{}
	}
}

func (c *streamCoalescer) Flush() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.flushLocked()
}
