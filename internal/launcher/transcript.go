package launcher

// Launcher-owned transcript capture. After each agent process exits the
// launcher walks the agent's native session-store, finds the file that
// belongs to the just-ended session (matched by cwd + recency), copies
// it into the chat's own sessions/ dir, and hands a normalised view of
// it to summary.go.
//
// We deliberately don't proxy the agent's stdio — that would force the
// launcher to stay alive as a PTY parent and break the "TUI quits then
// exec" flow. Instead we read the artifacts every agent already writes
// to disk during the chat and copy them into the chat dir so they
// outlive the agent's own rotation policy and travel with the chat.
//
// Each agent stores transcripts somewhere different. We split that into
// per-agent locators below. Locators are best-effort: a missing or
// unrecognised store returns (nil, nil) rather than an error so the
// rest of the post-exit flow still runs.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// CapturedTranscript is the agent-agnostic shape summary.go consumes.
// Bytes is the canonical JSONL we wrote into the chat (one JSON object
// per line). Entries is a parsed view — empty when the agent format
// isn't one we can parse, in which case Bytes is still preserved so
// the user can read the raw file later.
type CapturedTranscript struct {
	// Agent is the AgentID the transcript came from.
	Agent AgentID

	// SourcePath is where the agent stored it (absolute path), for
	// audit / debugging. Empty when we couldn't locate anything.
	SourcePath string

	// DestPath is where we copied it inside the chat
	// (<chat>/sessions/<ts>-<agent>/transcript.jsonl). Empty when
	// nothing was captured.
	DestPath string

	// Bytes of the canonical JSONL written to DestPath. nil when
	// capture failed or wasn't supported.
	Bytes []byte

	// Entries is the parsed normalisation; len(Entries)==0 means we
	// either captured nothing OR the format isn't one we parse yet.
	Entries []TranscriptEntry

	// StartedAt is the timestamp of the first entry (or the launch
	// time if entries don't carry timestamps).
	StartedAt time.Time
	// EndedAt mirrors StartedAt logic for the last entry.
	EndedAt time.Time

	// Note carries human-readable hints surfaced in the summary when
	// capture was partial (e.g. "format not yet parsed — file copied
	// verbatim"). Always safe to display.
	Note string
}

// TranscriptEntry is the normalised shape we summarise from. Tool calls
// land here too — they're meaningful signal for "what did we do this
// session".
type TranscriptEntry struct {
	Kind      string // "user" | "assistant" | "tool_call" | "tool_result" | "system"
	Timestamp time.Time
	Text      string // user/assistant prose; tool name + arg digest for tool entries
	Tool      string // populated for kind=tool_call
}

// CaptureTranscript locates and copies the just-ended session's
// transcript for the given chat + agent. sessionStart is the time the
// launcher decided to spawn the agent — we use it to filter the
// agent's session-store to files written after that moment, so we
// don't accidentally pick up an older session for the same cwd.
//
// destDir is the per-launch directory under <chat>/sessions/. Caller
// owns its creation; we just write transcript.jsonl into it.
//
// Returns (capture, nil) on every code path — internal failures land
// in capture.Note so the caller can render them but the launch flow
// keeps moving. err is only non-nil on genuinely unexpected I/O.
func CaptureTranscript(agent Agent, sandboxDir string, sessionStart time.Time, destDir string) (CapturedTranscript, error) {
	return captureTranscript(agent, sandboxDir, sessionStart, destDir, "")
}

func captureTranscript(agent Agent, sandboxDir string, sessionStart time.Time, destDir, homeOverride string) (CapturedTranscript, error) {
	out := CapturedTranscript{Agent: agent.ID}

	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return out, err
	}

	src, raw, parsed, note, err := locateTranscript(agent.ID, sandboxDir, sessionStart, homeOverride)
	if err != nil {
		// Locator hit an unexpected fs error. Don't fail the launch —
		// record the note and move on.
		out.Note = "transcript locator: " + err.Error()
		return out, nil
	}

	if note != "" {
		out.Note = note
	}
	if len(raw) == 0 {
		// Nothing to copy — that's fine, the summary file will still
		// be written from launch metadata alone.
		if out.Note == "" {
			out.Note = fmt.Sprintf("no transcript found for %s (agent may not have written one yet, or its store rotated)", agent.ID)
		}
		return out, nil
	}

	out.SourcePath = src
	dst := filepath.Join(destDir, "transcript.jsonl")
	if err := os.WriteFile(dst, raw, 0o644); err != nil {
		out.Note = "transcript copy: " + err.Error()
		return out, nil
	}
	out.DestPath = dst
	out.Bytes = raw
	out.Entries = parsed
	if len(parsed) > 0 {
		out.StartedAt = parsed[0].Timestamp
		out.EndedAt = parsed[len(parsed)-1].Timestamp
	}
	return out, nil
}

// locateTranscript dispatches to a per-agent finder. Each finder
// returns (sourcePath, rawBytes, parsedEntries, note, err). A nil
// rawBytes with nil err means "agent's store searched, nothing
// matches this session" — a normal case, not a failure.
func locateTranscript(id AgentID, sandboxDir string, sessionStart time.Time, homeOverride string) (string, []byte, []TranscriptEntry, string, error) {
	switch id {
	case AgentClaude:
		return locateClaudeTranscript(sandboxDir, sessionStart)
	case AgentOpenClaude:
		return locateOpenClaudeTranscript(sandboxDir, sessionStart, homeOverride)
	case AgentCodex:
		return locateCodexTranscript(sandboxDir, sessionStart)
	case AgentOpenCode:
		return locateOpenCodeTranscript(sandboxDir, sessionStart)
	case AgentGemini:
		return locateGeminiTranscript(sandboxDir, sessionStart)
	case AgentDeepSeek:
		return "", nil, nil, "deepseek-tui transcript capture not yet implemented — only summary metadata is recorded", nil
	}
	return "", nil, nil, "", nil
}

// homeDir is a thin wrapper that prefers os.UserHomeDir but falls back
// to $HOME / %USERPROFILE% when the OS lookup misbehaves (CI containers).
func homeDir() string {
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		return h
	}
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	if h := os.Getenv("USERPROFILE"); h != "" {
		return h
	}
	return ""
}

// openclaudeProjectSlug maps an absolute path to OpenClaude's on-disk
// project slug. OpenClaude's sanitizePath (src/utils/sessionStoragePortable.ts)
// replaces EVERY non-alphanumeric char with '-' — more aggressive than
// claude code's targeted '/', '\\', '.', ':' replacement, so paths
// containing '_' or other punctuation slug differently. Returns "" for
// empty input.
//
// Truncation/hashing fallback (>120 chars in upstream) is omitted here
// — sandbox paths are well under that limit in normal use.
func openclaudeProjectSlug(cwd string) string {
	if cwd == "" {
		return ""
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		abs = cwd
	}
	abs = filepath.ToSlash(abs)
	var b strings.Builder
	b.Grow(len(abs))
	for _, r := range abs {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	return b.String()
}

// locateOpenClaudeTranscript mirrors locateClaudeTranscript but against
// ~/.openclaude/projects/<slug>/. The JSONL schema is inherited verbatim
// from claude code so parseClaudeJSONL handles parsing.
func locateOpenClaudeTranscript(sandboxDir string, sessionStart time.Time, homeOverride string) (string, []byte, []TranscriptEntry, string, error) {
	home := homeOverride
	if home == "" {
		home = homeDir()
	}
	if home == "" {
		return "", nil, nil, "no home dir resolved — openclaude store skipped", nil
	}
	dir := filepath.Join(home, ".openclaude", "projects", openclaudeProjectSlug(sandboxDir))
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil, nil, "openclaude store empty for this sandbox (path: " + dir + ")", nil
		}
		return "", nil, nil, "", err
	}

	type cand struct {
		path    string
		modTime time.Time
	}
	var cands []cand
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(sessionStart.Add(-2 * time.Second)) {
			continue
		}
		cands = append(cands, cand{filepath.Join(dir, e.Name()), info.ModTime()})
	}
	if len(cands) == 0 {
		return "", nil, nil, "no openclaude transcript matched this launch (store: " + dir + ")", nil
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].modTime.After(cands[j].modTime) })
	picked := cands[0].path

	raw, err := os.ReadFile(picked)
	if err != nil {
		return "", nil, nil, "", err
	}
	parsed := parseClaudeJSONL(raw)
	return picked, raw, parsed, "", nil
}

// claudeProjectSlug maps an absolute path to Claude Code's on-disk
// project slug. Claude replaces every '/' (and '\\' on Windows) and
// every '.' with '-' to build the directory name under
// ~/.claude/projects/. Empty input or no slug computed → "".
func claudeProjectSlug(cwd string) string {
	if cwd == "" {
		return ""
	}
	abs := cwd
	if !filepath.IsAbs(cwd) && !strings.HasPrefix(cwd, "/") && !strings.HasPrefix(cwd, "\\") {
		if a, err := filepath.Abs(cwd); err == nil {
			abs = a
		}
	}
	// Claude on Windows still uses forward-slash-style slugs derived
	// from the path with both separators replaced.
	abs = filepath.ToSlash(abs)
	r := strings.NewReplacer(
		"/", "-",
		"\\", "-",
		".", "-",
		":", "-",
	)
	slug := r.Replace(abs)
	return slug
}

// locateClaudeTranscript finds the newest *.jsonl under
// ~/.claude/projects/<slug>/ whose mtime is at or after sessionStart.
// Claude writes one file per session and appends turn entries as they
// happen, so picking the most recent one after launch matches the
// just-ended session.
func locateClaudeTranscript(sandboxDir string, sessionStart time.Time) (string, []byte, []TranscriptEntry, string, error) {
	home := homeDir()
	if home == "" {
		return "", nil, nil, "no home dir resolved — claude store skipped", nil
	}
	dir := filepath.Join(home, ".claude", "projects", claudeProjectSlug(sandboxDir))
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil, nil, "claude store empty for this sandbox (path: " + dir + ")", nil
		}
		return "", nil, nil, "", err
	}

	type cand struct {
		path    string
		modTime time.Time
	}
	var cands []cand
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(sessionStart.Add(-2 * time.Second)) {
			// Predates this launch by more than a couple of seconds —
			// some other session's file. The slack is for clock drift.
			continue
		}
		cands = append(cands, cand{filepath.Join(dir, e.Name()), info.ModTime()})
	}
	if len(cands) == 0 {
		return "", nil, nil, "no claude transcript matched this launch (store: " + dir + ")", nil
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].modTime.After(cands[j].modTime) })
	picked := cands[0].path

	raw, err := os.ReadFile(picked)
	if err != nil {
		return "", nil, nil, "", err
	}
	parsed := parseClaudeJSONL(raw)
	return picked, raw, parsed, "", nil
}

// parseClaudeJSONL normalises Claude Code's JSONL into TranscriptEntry.
// Claude's schema (~0.40+): each line has "type":"user"|"assistant"|"summary",
// "message" (Anthropic-shaped content array), and "timestamp" (RFC3339).
func parseClaudeJSONL(raw []byte) []TranscriptEntry {
	var out []TranscriptEntry
	for _, line := range splitLines(raw) {
		if len(line) == 0 {
			continue
		}
		var rec struct {
			Type      string          `json:"type"`
			Timestamp time.Time       `json:"timestamp"`
			Message   json.RawMessage `json:"message"`
			Summary   string          `json:"summary"`
		}
		if err := json.Unmarshal(line, &rec); err != nil {
			continue
		}
		switch rec.Type {
		case "user":
			text := extractAnthropicText(rec.Message)
			if text == "" {
				continue
			}
			out = append(out, TranscriptEntry{Kind: "user", Timestamp: rec.Timestamp, Text: text})
		case "assistant":
			text, toolCalls := extractAnthropicAssistant(rec.Message)
			if text != "" {
				out = append(out, TranscriptEntry{Kind: "assistant", Timestamp: rec.Timestamp, Text: text})
			}
			for _, tc := range toolCalls {
				out = append(out, TranscriptEntry{Kind: "tool_call", Timestamp: rec.Timestamp, Tool: tc, Text: tc})
			}
		case "summary":
			if rec.Summary != "" {
				out = append(out, TranscriptEntry{Kind: "system", Timestamp: rec.Timestamp, Text: rec.Summary})
			}
		}
	}
	return out
}

// extractAnthropicText pulls the joined text from a Claude-style
// message.content array of {"type":"text","text":"..."} parts.
func extractAnthropicText(msg json.RawMessage) string {
	if len(msg) == 0 {
		return ""
	}
	var m struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(msg, &m); err != nil {
		// Sometimes user messages are bare strings.
		var s string
		if json.Unmarshal(msg, &s) == nil {
			return s
		}
		return ""
	}
	var parts []string
	for _, c := range m.Content {
		if c.Type == "text" && c.Text != "" {
			parts = append(parts, c.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// extractAnthropicAssistant returns (text, []tool-name) for an
// assistant message with a content array mixing text + tool_use.
func extractAnthropicAssistant(msg json.RawMessage) (string, []string) {
	if len(msg) == 0 {
		return "", nil
	}
	var m struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
			Name string `json:"name"`
		} `json:"content"`
	}
	if err := json.Unmarshal(msg, &m); err != nil {
		return "", nil
	}
	var parts []string
	var tools []string
	for _, c := range m.Content {
		switch c.Type {
		case "text":
			if c.Text != "" {
				parts = append(parts, c.Text)
			}
		case "tool_use":
			if c.Name != "" {
				tools = append(tools, c.Name)
			}
		}
	}
	return strings.Join(parts, "\n"), tools
}

// locateCodexTranscript scans ~/.codex/sessions/YYYY/MM/DD/ for rollout-*.jsonl
// files modified after sessionStart whose first record's cwd matches
// our sandbox. Codex writes one file per session under a date-stamped
// dir tree; only one CLI process writes per session so the most-recent
// file with a matching cwd is correct.
func locateCodexTranscript(sandboxDir string, sessionStart time.Time) (string, []byte, []TranscriptEntry, string, error) {
	home := homeDir()
	if home == "" {
		return "", nil, nil, "no home dir resolved — codex store skipped", nil
	}
	base := filepath.Join(home, ".codex", "sessions")
	if _, err := os.Stat(base); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil, nil, "codex store not present", nil
		}
		return "", nil, nil, "", err
	}

	// Walk just the dirs whose name is YYYY → MM → DD, filtered to the
	// last 2 days from sessionStart to keep the scan small.
	cutoff := sessionStart.Add(-2 * time.Second)
	wantCwd := normaliseCwd(sandboxDir)

	// Two passes through the walker: first prefer files whose first record
	// names our sandbox cwd (handles concurrent codex sessions from other
	// terminals — we don't want to steal an unrelated chat's session).
	// On miss, fall back to the newest rollout in the session window,
	// regardless of cwd. The strict pass was too brittle: codex stores cwd
	// after a few path-normalisation steps (symlink resolution on macOS,
	// drive-letter casing on Windows), and any mismatch silently produced
	// "no transcript captured" with only summary.json left behind.
	var strictPick, looseLatest string
	var strictMod, looseMod time.Time
	var looseNote string
	err := filepath.WalkDir(base, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil // ignore unreadable subtrees
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if !strings.HasPrefix(name, "rollout-") || !strings.HasSuffix(name, ".jsonl") {
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return nil
		}
		if info.ModTime().Before(cutoff) {
			return nil
		}
		// Track the newest rollout in the window unconditionally (fallback).
		if info.ModTime().After(looseMod) {
			looseLatest = path
			looseMod = info.ModTime()
		}
		// Cheap: peek at first ~2KB to find the cwd field.
		if codexFileMatchesCwd(path, wantCwd) {
			if info.ModTime().After(strictMod) {
				strictPick = path
				strictMod = info.ModTime()
			}
		}
		return nil
	})
	if err != nil {
		return "", nil, nil, "", err
	}
	picked := strictPick
	if picked == "" && looseLatest != "" {
		picked = looseLatest
		looseNote = "codex transcript captured via time-window fallback " +
			"(cwd field in rollout didn't match the sandbox path verbatim; if you " +
			"had concurrent codex sessions from another terminal this could be the wrong one)"
	}
	if picked == "" {
		return "", nil, nil, "no codex transcript modified after " + sessionStart.Format(time.RFC3339) + " (codex may not have flushed yet, or its store rotated)", nil
	}

	raw, err := os.ReadFile(picked)
	if err != nil {
		return "", nil, nil, "", err
	}
	parsed := parseCodexJSONL(raw)
	return picked, raw, parsed, looseNote, nil
}

// normaliseCwd lower-cases the cwd on Windows (case-insensitive FS) and
// canonicalises separators so a memberwise compare against codex's
// stored cwd matches even with mixed slashes.
func normaliseCwd(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		abs = p
	}
	abs = filepath.ToSlash(abs)
	if runtime.GOOS == "windows" {
		return strings.ToLower(abs)
	}
	return abs
}

// codexFileMatchesCwd peeks at the first records of a codex rollout
// file looking for "cwd":"<path>". Cheap string search — we don't need
// to fully parse to make this decision.
func codexFileMatchesCwd(path, wantCwd string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 4096)
	n, _ := f.Read(buf)
	if n == 0 {
		return false
	}
	chunk := string(buf[:n])
	// Codex emits "cwd":"<absolute path>" near the top. Compare in a
	// way that survives JSON escaping (forward slashes don't need it
	// in JSON, so a plain substring match works).
	needle := `"cwd":"` + wantCwd
	if strings.Contains(strings.ToLower(chunk), strings.ToLower(needle)) {
		return true
	}
	// Older codex versions used "workingDirectory" — accept that too.
	needle = `"workingDirectory":"` + wantCwd
	return strings.Contains(strings.ToLower(chunk), strings.ToLower(needle))
}

// parseCodexJSONL normalises codex rollouts. Schema varies across
// versions; we handle the two main shapes:
//
//  1. {"type":"session_meta",...} / {"type":"user_message","text":...}
//     / {"type":"agent_message","text":...} / {"type":"function_call",...}
//  2. Newer wrapped: {"type":"...","payload":{...}}
//
// Unknown lines are skipped silently.
func parseCodexJSONL(raw []byte) []TranscriptEntry {
	var out []TranscriptEntry
	for _, line := range splitLines(raw) {
		if len(line) == 0 {
			continue
		}
		var rec struct {
			Type      string          `json:"type"`
			Timestamp time.Time       `json:"timestamp"`
			Text      string          `json:"text"`
			Content   string          `json:"content"`
			Name      string          `json:"name"`
			Payload   json.RawMessage `json:"payload"`
		}
		if err := json.Unmarshal(line, &rec); err != nil {
			continue
		}
		// Newer wrapped shape: unwrap once.
		if len(rec.Payload) > 0 {
			var inner struct {
				Text    string `json:"text"`
				Content string `json:"content"`
				Name    string `json:"name"`
			}
			if json.Unmarshal(rec.Payload, &inner) == nil {
				if rec.Text == "" {
					rec.Text = inner.Text
				}
				if rec.Content == "" {
					rec.Content = inner.Content
				}
				if rec.Name == "" {
					rec.Name = inner.Name
				}
			}
		}
		text := rec.Text
		if text == "" {
			text = rec.Content
		}
		switch rec.Type {
		case "user", "user_message", "user_turn":
			if text != "" {
				out = append(out, TranscriptEntry{Kind: "user", Timestamp: rec.Timestamp, Text: text})
			}
		case "assistant", "agent_message", "assistant_message":
			if text != "" {
				out = append(out, TranscriptEntry{Kind: "assistant", Timestamp: rec.Timestamp, Text: text})
			}
		case "function_call", "tool_call":
			if rec.Name != "" {
				out = append(out, TranscriptEntry{Kind: "tool_call", Timestamp: rec.Timestamp, Tool: rec.Name, Text: rec.Name})
			}
		}
	}
	return out
}

// locateOpenCodeTranscript walks ~/.opencode/storage/session/info for
// the session JSON whose "directory" field matches our sandbox, then
// reads its corresponding message dir under
// ~/.opencode/storage/session/message/<id>/.
func locateOpenCodeTranscript(sandboxDir string, sessionStart time.Time) (string, []byte, []TranscriptEntry, string, error) {
	home := homeDir()
	if home == "" {
		return "", nil, nil, "no home dir resolved — opencode store skipped", nil
	}
	infoDir := filepath.Join(home, ".opencode", "storage", "session", "info")
	entries, err := os.ReadDir(infoDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil, nil, "opencode session store not present", nil
		}
		return "", nil, nil, "", err
	}
	wantCwd := normaliseCwd(sandboxDir)
	cutoff := sessionStart.Add(-2 * time.Second)
	var picked string
	var pickedMod time.Time
	var pickedSession string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		info, infoErr := e.Info()
		if infoErr != nil || info.ModTime().Before(cutoff) {
			continue
		}
		path := filepath.Join(infoDir, e.Name())
		raw, rerr := os.ReadFile(path)
		if rerr != nil {
			continue
		}
		var meta struct {
			ID        string `json:"id"`
			Directory string `json:"directory"`
		}
		if json.Unmarshal(raw, &meta) != nil {
			continue
		}
		if normaliseCwd(meta.Directory) != wantCwd {
			continue
		}
		if info.ModTime().After(pickedMod) {
			pickedMod = info.ModTime()
			picked = path
			pickedSession = meta.ID
		}
	}
	if picked == "" {
		return "", nil, nil, "no opencode session matched this sandbox after " + sessionStart.Format(time.RFC3339), nil
	}

	// Pull messages — they live under message/<id>/*.json, one file per turn.
	msgDir := filepath.Join(home, ".opencode", "storage", "session", "message", pickedSession)
	files, err := os.ReadDir(msgDir)
	if err != nil {
		// Session exists but no messages dir yet — return the info file as evidence.
		raw, _ := os.ReadFile(picked)
		return picked, raw, nil, "opencode messages dir not found at " + msgDir, nil
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Name() < files[j].Name() })

	// We'll concatenate the message files into a single canonical JSONL.
	var b bytes.Buffer
	var entries2 []TranscriptEntry
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".json") {
			continue
		}
		p := filepath.Join(msgDir, f.Name())
		raw, rerr := os.ReadFile(p)
		if rerr != nil {
			continue
		}
		// Each file is a JSON object — re-encode as a single JSON-line.
		// Strip newlines so the JSONL stays line-delimited.
		compact := compactJSONLine(raw)
		if len(compact) > 0 {
			b.Write(compact)
			b.WriteByte('\n')
		}
		entries2 = append(entries2, parseOpenCodeMessage(raw)...)
	}
	out := b.Bytes()
	return msgDir, out, entries2, "", nil
}

// parseOpenCodeMessage extracts user/assistant/tool entries from one
// opencode message JSON. OpenCode's schema is similar to Anthropic's
// (content arrays) under a "role" field.
func parseOpenCodeMessage(raw []byte) []TranscriptEntry {
	var m struct {
		Role    string          `json:"role"`
		Time    int64           `json:"time"`
		Created int64           `json:"created"`
		Content json.RawMessage `json:"content"`
		Text    string          `json:"text"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	ts := time.Unix(m.Created, 0)
	if m.Time != 0 {
		ts = time.UnixMilli(m.Time)
	}
	text := m.Text
	if text == "" {
		// Try Anthropic-style content array.
		text = extractAnthropicText(m.Content)
	}
	kind := strings.ToLower(m.Role)
	if kind != "user" && kind != "assistant" {
		kind = "system"
	}
	if text == "" {
		return nil
	}
	return []TranscriptEntry{{Kind: kind, Timestamp: ts, Text: text}}
}

// locateGeminiTranscript looks under ~/.gemini/tmp/<hash>/logs.json
// where the hash is derived from the cwd. The exact hashing scheme
// has changed across Gemini CLI versions, so we accept any subdir
// modified after sessionStart and just take the newest one as a
// best-effort match.
func locateGeminiTranscript(sandboxDir string, sessionStart time.Time) (string, []byte, []TranscriptEntry, string, error) {
	home := homeDir()
	if home == "" {
		return "", nil, nil, "no home dir resolved — gemini store skipped", nil
	}
	base := filepath.Join(home, ".gemini", "tmp")
	entries, err := os.ReadDir(base)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil, nil, "gemini tmp store not present", nil
		}
		return "", nil, nil, "", err
	}
	cutoff := sessionStart.Add(-2 * time.Second)
	var picked string
	var pickedMod time.Time
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		logsPath := filepath.Join(base, e.Name(), "logs.json")
		info, err := os.Stat(logsPath)
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			continue
		}
		if info.ModTime().After(pickedMod) {
			pickedMod = info.ModTime()
			picked = logsPath
		}
	}
	if picked == "" {
		return "", nil, nil, "no gemini log matched this launch (best-effort capture; schema varies across versions)", nil
	}
	raw, err := os.ReadFile(picked)
	if err != nil {
		return "", nil, nil, "", err
	}
	// gemini logs are a single JSON object with an entries array; we
	// just keep the raw bytes and skip parsing for v1.
	return picked, raw, nil, "gemini transcript copied verbatim — parsing not yet implemented", nil
}

// splitLines yields each line of raw without copying — strips trailing
// \r so it's robust against Windows line endings.
func splitLines(raw []byte) [][]byte {
	var out [][]byte
	start := 0
	for i := 0; i < len(raw); i++ {
		if raw[i] == '\n' {
			line := raw[start:i]
			if len(line) > 0 && line[len(line)-1] == '\r' {
				line = line[:len(line)-1]
			}
			out = append(out, line)
			start = i + 1
		}
	}
	if start < len(raw) {
		line := raw[start:]
		if len(line) > 0 && line[len(line)-1] == '\r' {
			line = line[:len(line)-1]
		}
		out = append(out, line)
	}
	return out
}

// compactJSONLine returns the JSON re-encoded onto a single line so we
// can stream multiple files into one JSONL transcript. On parse failure
// the original bytes are returned with embedded newlines stripped as a
// best effort.
func compactJSONLine(raw []byte) []byte {
	var v interface{}
	if err := json.Unmarshal(raw, &v); err == nil {
		out, _ := json.Marshal(v)
		return out
	}
	// Fallback: strip newlines.
	cleaned := make([]byte, 0, len(raw))
	for _, b := range raw {
		if b == '\n' || b == '\r' {
			continue
		}
		cleaned = append(cleaned, b)
	}
	return cleaned
}
