package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/sPROFFEs/PrAImate/internal/core"
)

// A scrollback-friendly view: no alternate screen, redraw loop or TUI dependency.
type sessionView struct {
	out   io.Writer
	color bool
	width int
}

func (v sessionView) style(text, code string) string {
	text = strings.ReplaceAll(strings.ReplaceAll(cleanTerminalText(text), "\n", " "), "\r", " ")
	if v.color {
		return "\x1b[" + code + "m" + text + "\x1b[0m"
	}
	return text
}

func (v sessionView) banner(version, workspace, id string) {
	fmt.Fprintln(v.out, v.style("\n  PrAImate  ·  "+version, "1;36"))
	fmt.Fprintln(v.out, v.style("  "+workspace, "2"))
	fmt.Fprintln(v.out, v.style("  Chat "+id, "2"))
	fmt.Fprintln(v.out, "  /model · /tools · /context · /help   |   Tab completes · Ctrl+C cancels")
}

const recentHistoryLimit = 20

func (v sessionView) restoreHistory(ctx context.Context, c *core.Core, chatID string) error {
	messages, err := c.ListRecentConversationMessages(ctx, chatID, recentHistoryLimit)
	if err != nil {
		return fmt.Errorf("load saved chat history: %w", err)
	}
	v.history(messages)
	return nil
}

func (v sessionView) history(messages []core.Message) {
	if len(messages) == 0 {
		return
	}
	fmt.Fprintln(v.out, v.style(fmt.Sprintf("  RECENT HISTORY · %d saved message(s)", len(messages)), "2"))
	for _, message := range messages {
		label := "  " + strings.ToUpper(message.Role) + " · " + message.TS.Local().Format("Jan 02 15:04")
		if message.Meta["interrupted"] == true {
			label += " · interrupted"
		} else if message.Meta["error"] != nil {
			label += " · failed"
		}
		fmt.Fprintln(v.out, "\n"+v.style(label, "1;36"))
		if message.Role == "assistant" {
			// Each saved reply starts with fresh Markdown state, even if the
			// preceding reply ended midway through a code block or table.
			renderer := newTextRenderer(v.out, v.color, v.width)
			renderer.WriteChunk(message.Content)
			renderer.Flush()
		} else if message.Content != "" {
			fmt.Fprintln(safeTerminalWriter{v.out}, message.Content)
		}
		var attachments []string
		switch paths := message.Meta["attachments"].(type) {
		case []string:
			attachments = paths
		case []any:
			for _, path := range paths {
				if path, ok := path.(string); ok {
					attachments = append(attachments, path)
				}
			}
		}
		for _, path := range attachments {
			fmt.Fprintln(v.out, v.style("  Attached: "+filepath.Base(path), "2"))
		}
		if detail, ok := message.Meta["error"].(string); ok && detail != "" {
			fmt.Fprintln(v.out, v.style("  Failed: "+detail, "31"))
		}
	}
	fmt.Fprintln(v.out)
}

func (v sessionView) session(model, tools string, status *core.NativeContextStatus, attached int, pinned ...int) {
	if tools == "" {
		tools = "safe"
	}
	width := max(20, min(v.width-4, 88))
	fmt.Fprintln(v.out, v.style("  "+strings.Repeat("─", width), "2"))
	fmt.Fprint(v.out, v.style("  "+truncateRunes(model, max(16, width-24)), "36"), v.style("  ·  "+tools, "2"))
	if attached > 0 {
		fmt.Fprintf(v.out, "  ·  %d attached", attached)
	}
	if len(pinned) > 0 && pinned[0] > 0 {
		fmt.Fprintf(v.out, "  ·  %d pinned", pinned[0])
	}
	fmt.Fprintln(v.out)
	if status != nil && status.InputLimit > 0 {
		if !status.WindowKnown {
			mode := "automatic"
			if !status.OutputAutomatic {
				mode = fmt.Sprintf("fixed %d", status.OutputReserve)
			}
			fmt.Fprintln(v.out, v.style(fmt.Sprintf("  last input ~%d · backend context unknown · output %s", status.EstimatedInput, mode), "2"))
			return
		}
		filled := min(10, max(0, status.EstimatedInput*10/status.InputLimit))
		bar := strings.Repeat("━", filled) + strings.Repeat("·", 10-filled)
		label := fmt.Sprintf("  %s  last input ~%d/%d · output reserve %d", bar, status.EstimatedInput, status.InputLimit, status.OutputReserve)
		if status.OutputAutomatic {
			label = fmt.Sprintf("  %s  last input ~%d/%d · output auto", bar, status.EstimatedInput, status.InputLimit)
			if status.LastOutputLimit > 0 {
				label += fmt.Sprintf(" (last limit %d)", status.LastOutputLimit)
			}
		}
		code := "2"
		if status.EstimatedInput*100/status.InputLimit >= 80 {
			code = "33"
		}
		fmt.Fprintln(v.out, v.style(label, code))
	}
}

func (v sessionView) finish(elapsed time.Duration, tools int, usage *core.NativeUsage, err error) {
	state, code := "✓ Done", "32"
	if errors.Is(err, context.Canceled) {
		state, code = "■ Cancelled", "33"
	} else if err != nil {
		state, code = "✗ Failed", "31"
	}
	line := fmt.Sprintf("  %s · %s · %d tool(s)", state, elapsed.Round(time.Millisecond), tools)
	if usage != nil {
		line += fmt.Sprintf(" · last call %d in / %d out", usage.PromptTokens, usage.CompletionTokens)
	}
	fmt.Fprintln(v.out, v.style(line, code))
}
