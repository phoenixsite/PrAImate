# CLI integrations and usage metrics

PrAImate can launch installed CLIs in Desktop, Studio and the VS Code extension.
The Desktop **CLIs** page provides installation and update commands. Authentication
still belongs to each CLI: complete its sign-in in an interactive terminal before
using it as a chat backend. Installing a CLI does not grant access to its models.

## Backends

| Backend ID | Executable | Streaming chat and resume | Managed Workers | Tokens from terminals launched by PrAImate |
| --- | --- | --- | --- | --- |
| `praimate-cli` | `praimate-cli` | Yes | Yes | Through its core chat |
| `claude` | `claude` | Yes | Yes | OpenTelemetry API request reports |
| `openclaude` | `openclaude` | Yes | Yes | Compatible Claude telemetry reports |
| `codex` | `codex` | Yes | Yes | OpenTelemetry completed response reports |
| `opencode` | `opencode` | Yes | Yes | Per-launch usage plugin |
| `praimate-code` | `praimate-code` | Yes | Yes | Per-launch usage plugin |
| `copilot` | `copilot` | Yes | Yes | OpenTelemetry model call reports |
| `antigravity` | `agy` | Yes | No | Not available |

Token coverage depends on the installed CLI version and the provider returning
usage. Unsupported or missing reports remain **unavailable**, never estimated
from message length. External terminals opened independently of PrAImate are not
tracked. This feature does not import past conversations or scan CLI histories.

Model fields accept the CLI's native model identifier. A blank field keeps its
default. Suggestions are examples, not an entitlement check or a complete list.
Each Worker tier can select its own CLI and model. Saved local endpoint routing
continues to use the backends that explicitly support it; adding these two CLIs
does not add arbitrary local endpoint routing to them.

### OpenCode compatibility

Worker profiles now pass their selected workspace edit permission to the native
CLI. **Edits** explicitly selects the Build agent and grants workspace read/edit
tools through a launch-scoped configuration; native commands remain denied and
supervised commands use PrAImate's approval path. **Full** explicitly selects
Build with native automatic approval. Planning still selects Plan. The native
edit regression starts with Plan as the backend default and verifies a real
read/edit cycle against an isolated completion fixture.

OpenCode **1.18.34** (the stable release checked on October 1, 2026) is compatible
with the updated adapter. The official Linux binary was tested against an
isolated OpenAI-compatible HTTP fixture: a new conversation, native session
resumption, loading a project skill, a stdio MCP call, a shell command that writes
a temporary file, JSON usage events, the per-launch usage plugin, and a simulated
HTTP 429 quota response with a one-hour retry delay all passed. This exercises the
adapter shared by chats and OpenCode Worker tiers; it does not test paid accounts
or an entire multi-provider Worker run.

The review found and fixed a launch bug: the process changed directory while
inheriting the Desktop/Studio parent's `PWD`. OpenCode uses `PWD` to select the
workspace, which could load a different project's configuration and cause an
internal server error. The adapter now derives its environment after setting
the working directory. The integration test deliberately starts with a different
parent `PWD` and verifies both the first turn and resumption. This correction also
applies to the shared PrAImate Code adapter; older published app binaries do not
include it until a new application build is installed.

`run --format json --thinking`, `--session`, and `--model provider/model` retain
their contracts. Full mode's `--dangerously-skip-permissions` remains a supported
alias for `--auto`; no runtime flag migration is required. Existing V1
`opencode.json` provider and MCP configuration remains supported. PrAImate Code
uses its separately bundled version and is not upgraded by this check.

Upstream's JSON command omits retry statuses. PrAImate adds a temporary,
launch-scoped status plugin for OpenCode and PrAImate Code chats and workers.
Short retries appear in the activity feed. A quota limit or a retry scheduled
more than 30 seconds away ends the current call with the provider's explanation,
leaving the native session and partial activity available for retry or a model
change. This threshold applies to reported retry waits, not model reasoning or
normal execution time. Project configuration and interactive terminals are not
modified by this plugin.

### Model catalogues and detached chats

Model suggestions share a five-minute in-memory cache across Desktop and Studio
requests. Concurrent probes are coalesced; CLI/configuration changes select a
new cache entry. **Refresh models** bypasses a completed cached result. Native
catalogues are scoped to their endpoint, credentials and certificate consent.
Saved model assignments remain live, and model fields still accept free text.

OpenCode and PrAImate Code retain full `provider/model` identifiers; bare aliases
are not added because two providers may expose the same model. PrAImate CLI uses
`HOST_ID::MODEL` for saved local assignments. Catalogues are deduplicated and
grouped by host; changing the default host preserves provider identities and
each host's own credential. Active-model removal targets the selected provider.
CLI probes resolve the same managed executable as normal runs. Live Codex
results replace fallback suggestions, and Antigravity probes `agy models`.
Failed probes do not cache fallback suggestions. Local discovery has a five-second
budget and does not query generation limits; refreshing one host preserves
other hosts' cached catalogues. Picker results from an older CLI selection or a
closed dialog are discarded.

### PrAImate CLI Full access

`--tools full` (or `/tools full`) auto-approves enabled tools, including file
access outside the workspace, command execution with an optional `cwd`, and
HTTP(S) redirects across origins. Commands retain the user's home/configuration
directories and SSH agent socket, without inheriting provider credentials or
PrAImate vault variables. Normal OS permissions and explicit agent manifest
capabilities still apply. Safe, Ask and Edits retain workspace containment;
Full does not enable unselected MCP servers or grant administrator privileges.

### Self-signed local endpoints in PrAImate Code

Desktop chats and Code terminals resolve the same PrAImate Code executable.
The compatible binary shipped beside Desktop takes precedence over a managed
installation or a copy on `PATH`, so an older installation cannot shadow the
bundled runtime/TLS patches. On amd64 without AVX2, the bundled baseline variant
is used when available; otherwise resolution falls back to the managed copy.
The updater also refreshes installed baseline sidecars.

Accept the presented certificate and its issuing CA chain in Local LLM settings
before connecting. Consent is stored in the encrypted database. PrAImate-launched
PrAImate Code and OpenCode providers use an authenticated loopback transport for
accepted HTTPS hosts. The core validates the remote HTTPS connection with the
same verifier as model discovery and praimate-cli, preserving the accepted leaf
pin, hostname and expiry checks. This also supports a private server that omits
its root or intermediate CA, without depending on the CLI runtime's support for
partial chains. Existing accepted leaf certificates do not require a CA import.

To trust a private CA across leaf certificate renewals instead, expand **Private
certificate authority** in Host Settings, paste its public root CA PEM (plus
intermediates if needed), then confirm **Review & trust CA**. Never paste a private
key. Explicit CA trust replaces the leaf pin for that host and allows certificates
issued by the approved CA. The local transport re-checks consent on each request,
blocks redirects and removes its authentication header before forwarding to the
remote provider. Revoking consent also blocks new requests from existing CLI
processes. Its transient routes do not change saved provider URLs, and its
listener closes with the core.

The same launch preparation serves Desktop chats, Studio, workflows and
terminals. Both PrAImate Code and external OpenCode also receive a public PEM
bundle through `NODE_EXTRA_CA_CERTS` at process startup for compatibility.
Extra CA trust for other CLI requests remains process-wide. PrAImate Code retains
its scoped `PRAIMATE_HOST_TLS` build patch. The operating
system trust store is unchanged. Restart terminals opened before this transport
was configured; subsequent launches receive its routes. Local providers use
separate `PRAIMATE_LLM_<provider hash>`
environment references, so multiple providers in one process receive their own
keys. Legacy `OPENAI_API_KEY` references remain supported for the selected route.

Detached chats use the same activity renderer and tool choices as embedded
chats. Tool changes are saved through the authenticated main-process broker and
apply to the next turn. Code keeps one terminal emulator for each live PTY while
the page is hidden, retaining its screen, palette and cursor. Terminal state
remains in memory and is released when the process closes.

The installed-binary test is optional and uses temporary configuration, session,
cache and skill directories, with model requests directed at localhost. Download
and verify the official CLI separately, then run:

```sh
PRAIMATE_TEST_OPENCODE_BINARY=/absolute/path/to/opencode go test ./internal/core -run '^TestOpenCodeInstalledCompatibility$' -count=1 -v
```

The command fixture currently requires Linux or macOS. Without the environment
variable, ordinary test runs skip this integration test. The JSON-RPC helper
accepts zero and string request IDs, including OpenCode's initialization request.

Reference: [OpenCode 1.18.34 release](https://github.com/anomalyco/opencode/releases/tag/v1.18.34)
and its [run command implementation](https://github.com/anomalyco/opencode/blob/v1.18.34/packages/opencode/src/cli/cmd/run.ts).

### Codex terminal usage

Terminal usage is collected from Codex's completed-response OpenTelemetry logs,
using an authenticated receiver on localhost. The launcher passes the complete
authorization header through `OTEL_EXPORTER_OTLP_LOGS_HEADERS` and clears the
previous exporter's configured headers for this launch. The token stays out of
command-line arguments; prompt capture remains disabled.

The previous launcher passed `Bearer ${PRAIMATE_USAGE_TOKEN}` through a Codex
`-c` override. Codex 0.160.0 sent this text literally, so the receiver rejected
the reports and no terminal tokens reached the dashboard. Install the corrected
PrAImate build and reopen the terminal; an already-running CLI retains its old
launch configuration. Previously rejected reports are not reconstructed from
Codex histories.

The optional installed-binary regression test uses a localhost Responses fixture,
ephemeral authentication, read-only execution and temporary runtime state. It
checks Codex's real exporter against the encrypted usage dashboard, including
model attribution and cache/reasoning totals without duplicate counting:

```sh
PRAIMATE_TEST_CODEX_BINARY=/absolute/path/to/codex go test ./internal/core -run '^TestCodexInstalledTerminalUsageCompatibility$' -count=1 -v
```

The test was verified with Codex 0.160.0 and requires its isolation flags. No real
account or subscription is used. Ordinary test runs skip it when the environment
variable is absent. See the official [Codex telemetry configuration](https://learn.chatgpt.com/docs/config-file/config-advanced#observability-and-telemetry).

### GitHub Copilot CLI

- Install the official `@github/copilot` npm package from **CLIs**; Node/npm are prerequisites.
- Use `/model` in Copilot to check model identifiers available to your account.
- PrAImate sends prompts over stdin, parses JSONL output, and resumes using the
  returned session ID. A final `result` reports completion; token counts are
  collected separately from per-call OpenTelemetry reports because the current
  CLI filters SDK usage events out of JSONL.
- Safe mode exposes file reading/search tools. Edits adds file creation/editing.
  Full mode permits native commands and MCP tools. The same restrictions are
  applied again when resuming a conversation.
- Selected MCP servers use a private temporary configuration with environment
  references for credentials. PrAImate removes it after the process exits.
  The strict Safe/Edits tool allowlists exclude MCP tools; use Full when the
  conversation requires them.

See the official [programmatic reference](https://docs.github.com/en/copilot/reference/copilot-cli-reference/cli-programmatic-reference)
and `copilot help monitoring` for the installed version's telemetry options.

### Antigravity CLI

- The backend ID is `antigravity`; the official executable is `agy`.
- **CLIs** offers Google's installer for Linux/macOS/WSL or Windows PowerShell.
- Use `agy models` for supported model identifiers. PrAImate supports streaming
  input/output and saved conversation IDs for headless chat resumption.
- Safe and Plan use `--mode=plan`, Edits uses `--mode=accept-edits`, and
  Full uses `--dangerously-skip-permissions`. Native terminals use CLI defaults.
  `--mode=default` is unsupported. Backend capacity errors (HTTP 503) require
  selecting another model or retrying later; PrAImate does not silently switch models.
  Plan is an instruction-based mode and does not enforce PrAImate's read-only
  restrictions. Consequently Antigravity is unavailable for managed Workers.
- Selected MCP servers, including the internal Skills MCP, are attached through
  a private agent/plugin generated for each launch. This integration is validated
  with Antigravity CLI 1.4.0. Since 1.4 selects discovered agents by name,
  PrAImate creates a uniquely named private agent under
  `~/.gemini/config/agents/` (or `ANTIGRAVITY_GEMINI_DIR/config/agents/`).
  It is shared by Desktop and Studio chats and native
  terminals, and is regenerated when resuming a conversation. The files are
  removed after the process exits; project files and the user's global MCP
  configuration are preserved. Only the generated launch agent and plugin are
  removed. Native CLI permissions still apply to MCP calls.
- Headless token metrics count completed step reports. The cumulative session
  total is ignored so resuming does not count earlier turns again. Interactive
  terminal usage has no verified exporter and is shown as unavailable.
- Installation success does not guarantee CPU compatibility. The official Linux
  binary tested during development required `pclmul`; a VM without that CPU feature
  could not run it. Check `agy --version` on the target machine.

See Google's [headless protocol](https://www.antigravity.google/docs/cli/headless/),
[installation guide](https://www.antigravity.google/docs/cli/install/) and
[mode reference](https://www.antigravity.google/docs/cli/modes/).

## Storage and counting

The dashboard stores usage rows in the current user's existing **encrypted
PrAImate database**. Rows contain CLI, model, timestamp, surface, outcome and token
counts; they do not contain prompts, responses, tool output or credentials.
CLI-owned histories and logs remain subject to the CLI's own storage policy.

Terminal launchers create an authenticated receiver bound to `127.0.0.1` with a
random port and per-launch token. Prompt/content capture is disabled in the
telemetry configuration. Only recognized token fields are retained. Duplicate
reports are ignored. Receivers and temporary plugins are removed when the
terminal closes or its owning backend shuts down. VS Code also reports failed
terminal creation so the prepared receiver can be released.

Input totals include cached input when the CLI reports it separately. Output
totals include reasoning when reported separately. Categories already included
in a provider total are not added a second time. These are usage counters, not
an invoice or a currency estimate.

Chat and Worker records represent turns/runs. Terminal records represent
individual reported model calls and are dated when received, in UTC. Dashboard
run counts therefore combine these units. Missing terminal reports (for example,
an older CLI, a hard process kill, or telemetry disabled by organization policy)
cannot be reconstructed or treated as zero usage.
