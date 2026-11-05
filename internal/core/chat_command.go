package core

// Shell-command turns — the GUI chat composer treats input starting
// with "!" as a local shell command run in the chat's working
// directory (the same "CLI basics" affordance terminal users get for
// free). The command and its output are persisted as chat messages
// (role "user" / role "command") so the transcript stays complete and
// the native agent can see a bounded, redacted record on later turns. Running
// the command itself does not make a model request.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// chatCommandTimeout bounds a "!" command so a hung process can't wedge
// the chat. Long builds belong in the Code terminal, not the composer.
const chatCommandTimeout = 60 * time.Second

// chatCommandMaxOutput caps the persisted output (the UI renders it
// inline; megabytes of build logs would drown the thread).
const chatCommandMaxOutput = 16 * 1024

// RunChatCommand executes command through the platform shell in the
// chat's working directory and persists both sides of the exchange.
// Returns the combined output (stdout+stderr, truncated) — exit codes
// are reported in the output header rather than as a Go error so the
// chat keeps flowing.
func (c *Core) RunChatCommand(ctx context.Context, chatID, command string) (*ChatTurn, error) {
	if c.store == nil {
		return nil, errors.New("RunChatCommand: no store configured")
	}
	command = strings.TrimSpace(command)
	if command == "" {
		return nil, errors.New("RunChatCommand: empty command")
	}
	chat, err := c.GetChat(ctx, chatID)
	if err != nil {
		return nil, err
	}
	if chat.CLIAgent == "praimate-cli" {
		var release func()
		ctx, release, err = c.nativeLease(ctx, "chat."+chatID)
		if err != nil {
			return nil, err
		}
		defer release()
		chat, err = c.GetChat(ctx, chatID)
		if err != nil {
			return nil, err
		}
	}
	cwd := chat.WorkspacePath
	if cwd == "" || cwd == "." {
		home, err := os.UserHomeDir()
		if err == nil {
			cwd = home
		} else {
			cwd = "."
		}
	}

	if _, err := c.AddMessage(ctx, chatID, "user", "! "+command, map[string]any{"kind": "command"}); err != nil {
		return nil, fmt.Errorf("persist command: %w", err)
	}

	cctx, cancel := context.WithTimeout(ctx, chatCommandTimeout)
	defer cancel()
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(cctx, "cmd", "/C", command)
	} else {
		cmd = exec.CommandContext(cctx, "sh", "-c", command)
	}
	hideConsole(cmd)
	cmd.WaitDelay = 2 * time.Second
	cmd.Dir = cwd
	out := limitedBuffer{limit: chatCommandMaxOutput}
	cmd.Stdout = &out
	cmd.Stderr = &out

	start := time.Now()
	runErr := cmd.Run()
	text := strings.TrimRight(strings.ToValidUTF8(out.String(), ""), "\n")
	if out.truncated {
		text += "\n… (output truncated)"
	}
	switch {
	case cctx.Err() == context.Canceled:
		text += "\n[cancelled]"
	case cctx.Err() == context.DeadlineExceeded:
		text += fmt.Sprintf("\n[killed after %s]", chatCommandTimeout)
	case runErr != nil:
		var ee *exec.ExitError
		if errors.As(runErr, &ee) {
			text += fmt.Sprintf("\n[exit %d]", ee.ExitCode())
		} else {
			text += "\n[failed to run: " + runErr.Error() + "]"
		}
	}
	if strings.TrimSpace(text) == "" {
		text = "(no output)"
	}

	// Cancellation stops execution, not the durable record of actions/output.
	persistCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer stop()
	meta := map[string]any{"kind": "command", "command": command}
	if cctx.Err() != nil {
		meta["interrupted"] = true
	}
	if _, err := c.AddMessage(persistCtx, chatID, "command", text, meta); err != nil {
		return nil, fmt.Errorf("persist command output: %w", err)
	}
	return &ChatTurn{
		UserMessage: "! " + command,
		Reply:       text,
		DurationMs:  time.Since(start).Milliseconds(),
	}, nil
}
