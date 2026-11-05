# PrAImate CLI

`praimate-cli` is the local-model frontend to the PrAImate core, not a separate
agent implementation. Desktop chats and workflows execute the provider inside
the core process; the terminal executable calls the same core APIs and opens
the same encrypted database. External providers can continue to use their own
CLI adapters. Native execution does not silently fall back to a cloud provider.

## Start and resume

```sh
praimate-cli --endpoint http://127.0.0.1:11434/v1 --model YOUR_MODEL --tools ask
praimate-cli --chat CORE_CHAT_ID
praimate-cli --continue
praimate-cli --model YOUR_VISION_MODEL --attach "screenshots/problem.png" "Explain this error"
praimate-cli --agent dev-team --model YOUR_MODEL "Review this project"
praimate-cli --agent dev-team --model YOUR_MODEL --workflow "Fix a bug" --inputs '{"bug":"Describe the bug"}'
```

The endpoint must implement OpenAI-compatible Chat Completions streaming and
function calling for tool use. Ollama, compatible local servers and routers
such as OmniRoute can supply the endpoint. Use the endpoint's model ID, including its provider/model namespace.
`/models` lists assigned core models, or queries the selected endpoint when no
assignments exist. `HOST_ID::MODEL` selects an assigned model on a specific host.
The core resolves credentials for the selected saved host. Standalone callers
can use `OPENAI_BASE_URL`, `OPENAI_MODEL` (or `PRAIMATE_MODEL`) and
`OPENAI_API_KEY`. Keys are not accepted in argv or written to project files.
Router URL prefixes are preserved; cross-origin redirects are rejected.

Storage uses PrAImate's canonical data directory, including `PRAIMATE_HOME`.
An interactive terminal asks for the database password if no remembered OS
credential is available. For automation, `--db-password-stdin` reads the
password from the first newline-terminated stdin line; provide the prompt in
argv or on the remaining stdin. A new installation asks to create a database
password. Beta plaintext session files are not imported automatically.

`--chat`/`--session` takes a **core chat ID**, not the internal model session ID.
Desktop and Studio terminals receive that identity directly, preserving their
agent, skills, local route, MCP selection, permissions and transcript. An
endpoint change on a live model session requires a new chat, preventing silent
transfer of its private context to another host.

## Context and output budgets

Automatic context uses the loaded server window: Ollama `/api/ps`, LM Studio
`/api/v1/models`, llama.cpp `/props`, or serving `max_model_len` from OpenAI model
cards such as vLLM. Saved host values are planning hints and are superseded by
loaded backend metadata. Explicit per-chat or `--context-tokens` settings remain
manual ceilings.

When metadata is unavailable, the initial 8192-token value is a **planning
threshold**, not an input limit. It grows to retain the complete current request,
instructions, images, skills and tool schemas. Eligible older history can still
be compacted. The CLI does not guess a physical window from a model name or
training maximum. A context rejection that reports a serving limit teaches the
session and its in-memory route cache that limit.

Discovery uses cached, bounded read-only requests to the selected server. It
preserves proxy prefixes and does not load models or change their configuration.
A client setting cannot enlarge the server's context allocation.

Output is automatic unless pinned explicitly for a chat or with `--output-tokens`.
A saved host output value is a reservation hint, not a generation ceiling. The
runtime adapts reservations to the essential input. With a known window, each
response can use the estimated remaining space for reasoning and visible output.
With an unknown window, it initially omits `max_tokens` so the backend selects its
default. If that default produces a length stop, recovery requests more output
space automatically. Reasoning and visible text share the model's generation
budget.

Use `/context` to inspect the source, automatic/fixed output mode, last requested
output limit, reservations, input estimate, compactions and last endpoint usage.
`/context auto` restores automatic budgets and backend discovery. `/context 32768 4096` sets a 32768-token window and 4096-token
fixed output limit for this chat. Desktop Chats and Studio expose the same per-chat
settings. Values of 0 use automatic selection and treat host settings as planning hints.

Context budgeting normally reserves output plus a safety margin of 5% of the
window, with at least 256 safety tokens. Automatic reservations yield space to
essential input when necessary; the latest request is not rejected merely to
preserve these reservations. Compaction receipts also shrink to the free space. It estimates text, Unicode, tool schemas/calls,
skills and vision input separately; base64 bytes are not counted as text tokens.
Endpoint-reported prompt usage calibrates subsequent estimates upward. These
are estimates, **not exact tokenizer counts**; image token costs vary by model.
If mandatory input alone exceeds a discovered window according to the estimate,
automatic mode asks the backend with that input intact instead of rejecting it
based on the heuristic. A real backend rejection remains authoritative.

The transport requests streaming usage when supported and retries without that
optional field only if the endpoint explicitly rejects it. Backends returning a
complete JSON completion instead of an event stream are also accepted, with the
same usage, tool-call validation and output-limit recovery. This does not trigger
a second generation request. HTTP 429, 500, 502, 503 and 504 responses receive at
most two retries before any output is accepted; retry progress is visible and
Ctrl+C cancels the wait. Authentication errors and broken partial streams are
not automatically retried.

A context-length HTTP
rejection allows one recovery attempt per model step with a more conservative
budget and any physical window reported by the backend. Automatic mode
reduces the output allowance and increases the input estimate; fixed mode
trims eligible history while retaining the configured output limit. Completed tools are
not replayed. Older history and
large tool results can be compacted. If the latest request, images, instructions
or tool schemas cannot fit, the run stops without silently cutting out the
current request or system instructions.

A response ending at the output limit retains visible partial text, marks it as
incomplete in the model checkpoint, and continues automatically with a recovery
notice. Automatic mode compacts eligible history and makes more generation room
inside the backend window; explicit output limits remain fixed. Truncated tool
calls are discarded. Completed tools are not automatically replayed. The model
receives continuation instructions and retained tool receipts; it must inspect
state before deciding to repeat an action.

Recovery is bounded to eight consecutive automatic continuations (three with
an explicit output limit) to avoid endless reasoning. Unknown backend defaults
can grow past 8192 output tokens as the reasoning requires.
Network errors and arbitrary broken streams are not replayed. Managed structured
responses only retry when no visible answer has started, preserving the JSON
contract. If essential instructions, images or the latest request cannot fit,
or the backend repeatedly exhausts output, the error remains visible. Use
`/context` or the chat settings to select a window supported by the backend.

Metadata contracts: [Ollama](https://docs.ollama.com/api/ps),
[LM Studio](https://lmstudio.ai/docs/developer/rest/list),
[llama.cpp](https://github.com/ggml-org/llama.cpp/blob/master/tools/server/README.md).

## Permissions and tools

| Mode | Files | Commands | Network and MCP |
| --- | --- | --- | --- |
| `safe` (default) | Read/search selected workspace | Disabled | Disabled |
| `ask` | Writes require approval | Approval required | Connection/action approval required |
| `edits` | Workspace writes allowed | Approval required | Disabled |
| `full` | Workspace writes allowed | Allowed | Selected services allowed |

Agent capability restrictions further narrow these permissions. Non-interactive
Ask requests are denied, never auto-approved. File tools reject absolute paths,
parent traversal and symlinks outside the workspace. Commands and MCP servers
run with the OS user's authority: approval is **not an operating-system sandbox**.

The provider exposes core file reading/listing/searching, read-only Git inspection, exact-string edits,
writes, commands, URL fetching, raw/RAG agent knowledge and selected MCP tools.
User-selected text attachments can be read by index without granting arbitrary
filesystem access. PNG, JPEG and GIF attachments are sent as multimodal image
parts to the selected endpoint, including in native Autonomous chat runs. The
endpoint/model must support vision; there is no automatic OCR, model switch or
cloud fallback. Other image formats (including WebP/SVG), PDFs and other binary
documents require conversion to a supported image or text first.
Shell syntax requires an explicit platform shell, such as `sh` or `cmd.exe`;
ordinary commands use an executable and argument array across supported platforms.

MCP uses the core's registered servers, transports, authentication and timeouts.
HTTP connections negotiate the legacy SSE transport when initialization is
rejected with status 400, 404 or 405, preserving the session URL announced by
the server (including Burp's `sessionId` query). Connection checks initialize
the server and list tools using the same client as execution; a reachable web
page alone is not reported as a working MCP. Authentication failures are kept
as errors rather than triggering another transport.
Choose them in Desktop/Studio or use `--mcp ID1,ID2`. A workspace `.mcp.json`
never starts processes automatically. `AGENTS.md` and `.praimate/rules.md` are
read as bounded project instructions, not permission grants.
`/mcp` shows registered servers and the chat's selection. Native skill tools
are separate from external MCP servers and appear when the chat has a versioned
skill selection. `/skills` shows that selection and approved installed versions.

Pinned skills and dynamic `skill_load`/`skill_read` use the existing core trust,
exact version locks, resource limits and context budgets. Skill bodies are
revalidated for every model request, not retained in native protocol checkpoints.
Autonomous agents continue through the managed runtime for working memory,
plans, checkpoints, artifacts, approvals and execution budgets; their native
provider is model-only to avoid a second, competing tool loop.

## Sessions, output and builds

The terminal includes editable prompts, arrow-key history (memory only), Home/End,
Tab completion for slash commands, tool levels, model IDs, attachment paths and
detach indices, multiline input (end a line with `\`), and
bracketed paste. Press Enter after pasting; pasted slash commands remain prompt
text and pasted approvals are denied. Prompts are limited to 1 MiB overall and
fewer than 4096 characters per edited line; use attachments or piped stdin for
larger inputs. Terminal control characters in model/tool output are filtered.

Interactive commands: `/help`, `/status`, `/context [auto|WINDOW [OUTPUT]]`, `/models`, `/model [ID]`,
`/tools [LEVEL]`, `/attach PATH`, `/attachments`, `/detach [N|all]`, `/mcp`, `/skills`,
`/pin PATH`, `/pinned`, `/unpin PATH|N|all`, `/repomap [on|off|refresh]`,
`/diff [--stat|--staged]`, `/export [PATH]`, `/sessions`, `/compact`, `/clear`, `/exit`.
Use `! COMMAND` to run an explicit shell command through the core; Ctrl+C cancels
that command while retaining the chat and partial output. Capture is limited
during execution to 16 KiB; extra output is discarded with a notice. Commands run
directly without a model request. In native chats, their bounded, privacy-redacted
results enter model context on the next turn, including commands run before the
first model response. Results are checkpointed once, so reopens/retries do not
insert them again. Native commands cannot run concurrently with a model turn in
the same chat. Without an argument, `/model`,
`/tools` and `/detach` open a numbered selector; `/sessions` lets you switch
native chats. Type `n`/`p` for another page or Enter to cancel. Switching chats
clears queued attachments. Selecting a session, or reopening one interactively
with `--chat`, `--session` or `--continue`, displays its last 20 conversation
messages in chronological order, including saved attachments and partial-reply
status. System instructions and internal tool payloads are not displayed.
This reads the saved transcript without running tools or making model requests;
non-interactive text and JSONL output do not replay it.
Responses render headings, lists, code blocks and
tables in interactive terminals; non-interactive text and JSONL stay raw.
`/clear` creates a new chat and retains
the previous transcript. Automatic compaction removes older complete turns and
trims large tool results before dropping complete tool exchanges, keeping bounded
excerpts and the latest user request/images. It is lossy, not a semantic summary.
Explicit `/compact` replaces old model history with excerpts and **drops old
images**; attach them again if needed. Native
checkpoints and renewable run leases live in the encrypted core database.
Interrupted tool calls are not replayed automatically. Compaction receipts retain
the original goal and recent corrections, workspace-relative file paths, command
arguments, progress and explicit tool failures. Requested edits are not treated
as confirmed modifications.

`/pin PATH` keeps a selected text file available across turns. The core reloads
pinned content for every model request, including resumed Desktop and Studio
chats; `/unpin` takes effect on the next request. Reads are bounded to 64 KiB of
text across up to 20 files per request, with explicit notices for truncation or
unavailable files. Optional pinned/repository context is shortened further when
necessary to leave room for the current request, tool schemas and model output;
use `read_file` to inspect additional targeted ranges. Mandatory instructions and
the user's current request are preserved. Binary files should use attachments. `/repomap on` (or
`--repomap`) includes a small repository map; file edits and additions refresh
its symbols automatically. `/repomap off` removes it from subsequent requests.
Neither feature restarts the conversation or stores stale copies in new native
checkpoints. The map uses the native token estimate and is bounded to 800 tokens.

Use repeatable `--attach PATH` flags or `/attach PATH` (one path per command;
spaces and optional quotes supported). `/attachments` lists the next message's
queue; `/detach N` uses 1-based indexes. Enter on an empty prompt sends queued
attachments. The queue is consumed when a turn is attempted, including failed
turns, but is retained by `/compact`; `/clear` clears it. Attachment flags are
not supported for `--workflow`; use workflow inputs instead.

Up to 20 files may be selected per message. Images are limited to 4 files,
8 MiB each, 16 MiB combined, 8192 pixels per side and 32 megapixels. Retained
image payloads are also bounded to 32 MiB of encoded data by automatic compaction.
These limits do not override the selected core skills/runtime input budgets.
Native chat sessions store image snapshots in the encrypted checkpoint, so a
normal resume does not reread changed source images. An explicitly resumed
Autonomous run revalidates its selected source files. Images and image metadata
are **not text-redacted**; attach only content you want sent to that endpoint.

`--format json` emits newline-delimited core events, including a core chat ID,
text/reasoning deltas, correlated tool start/end events, structured `context`
and `usage` data in `raw`, and failure status. Interactive text mode shows a compact model/permissions header, attachments,
last-input context meter and a completion/cancellation/failure summary. Completed
tools show their duration; commands and tool failures show a bounded output
preview, while successful file reads keep scrollback compact.
`NO_COLOR` disables styling. Usage is shown when the endpoint reports it; `--show-reasoning` also displays reasoning text.
Errors, truncated responses and exhausted tool-turn budgets produce a nonzero
exit code for single-shot/JSON runs. The interactive prompt remains available
after a failed turn. Ctrl+C cancels the active turn or clears pending input;
Ctrl+D at an empty prompt exits. A normal native turn allows up to 64
model/tool rounds; Autonomous mode uses its configured managed budgets.

Build from the repository root with `go build ./cmd/praimate-cli`. The release
scripts bundle it alongside Desktop for their supported Linux, Windows and
macOS Apple Silicon targets; the installers and updater manage the executable. The GUI build script also builds its
terminal sibling. Cross-compilation does not replace a Windows runtime smoke
test, and mock-provider tests do not establish compatibility with every model.

For three-tier coordination and saved Worker chats, see [Workers](WORKERS.md).
