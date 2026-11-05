<p align="center">
  <img src="docs/assets/monke-icon.png" alt="PrAImate" width="160" />
</p>

# PrAImate

> Official source and releases:
> [sPROFFEs/PrAImate](https://github.com/sPROFFEs/PrAImate)

PrAImate is a desktop harness for Claude Code,
OpenClaude, Codex CLI, OpenCode, and the bundled **PrAImate Code** build.
It also includes **PrAImate CLI**, a core-native runtime for self-configured
OpenAI-compatible endpoints, with a shared terminal frontend.
It gives those CLIs one place for coding terminals, chats, Document Studio,
PrAImate Studio (integrated Code-OSS / VSCodium development environment),
reusable agents, workflows, internal on-demand Skills via embedded MCP,
user-configured MCP servers, multi-host Local LLM routing, privacy controls,
and optional Git backup.

External CLI adapters retain their own model requests, authentication and
session/configuration files. PrAImate CLI instead uses core-owned tools,
permissions, skills, MCP, context and encrypted sessions. See the
[native CLI guide](docs/native-cli.md) for local models, routers, terminal
commands and permission boundaries.

## Supported systems

- Linux amd64 (release archive)
- Windows amd64 and arm64 (release archives)
- macOS Apple Silicon / arm64 (release target)
- Linux arm64 source builds where the required native WebKitGTK toolchain is
  available

The macOS build targets Apple Silicon; Intel Mac archives are not provided.
Running `praimate` opens the desktop application. `praimate-cli` provides the
shared native terminal frontend.

## Install

Linux:

```sh
curl -fsSL https://raw.githubusercontent.com/sPROFFEs/praimate/main/scripts/install.sh | bash
```

Windows PowerShell:

```powershell
iwr -useb https://raw.githubusercontent.com/sPROFFEs/praimate/main/scripts/install.ps1 | iex
```

Useful commands:

```sh
praimate                # open the desktop application
praimate code           # launch the managed PrAImate Code CLI
praimate agent run --agent DEV-TEAM --cli praimate-code --folder /project --prompt "review this code"
praimate -check-update  # check the latest GitHub release
praimate -update        # update the installed release
praimate -version       # print version and platform
```

The updater checks the resolved installation directory before downloading an
update. System-wide installations may require an elevated updater; user-owned
installations do not require sudo. Release labels display one `v` prefix.

Headless agent runs are a versioned machine interface: JSON is written to
stdout and diagnostics stay on stderr. Use `--output jsonl` for live events,
use a protected `--prompt-file` to keep large/sensitive text out of the process
list, or use `--workflow` with repeatable `--input key=value` parameters. A
caller-supplied `--run-id` enables encrypted status lookup and idempotent result
replay. Add `--persist` only when the run should appear in Chats. The default
`--tools safe` policy is read/answer-only; `edits` and `full` are explicit
automation trust decisions. When the database password is not remembered, an
interactive terminal prompts for it with input echo disabled. See the
[CLI agent API guide](docs/CLI_AGENT_API.md) and
[Python example](examples/praimate_agent_capture.py).

The installer supports binary/source, user/system, and uninstall modes:

```sh
./scripts/install.sh --binary
./scripts/install.sh --source
./scripts/install.sh --user
./scripts/install.sh --system
./scripts/install.sh --uninstall
./scripts/install.sh --uninstall --purge
```

```powershell
.\scripts\install.ps1 -Mode Binary
.\scripts\install.ps1 -Mode Source
.\scripts\install.ps1 -AllUsers
.\scripts\install.ps1 -Uninstall
.\scripts\install.ps1 -Uninstall -Purge
```

Normal uninstall keeps PrAImate data. Purge removes it.

## First launch and encrypted storage

The first GUI launch requires a database password of at least 12 characters.
PrAImate generates a random 512-bit SQLite encryption key and wraps it with
XChaCha20-Poly1305 under an Argon2id-derived password key. The database itself
uses AES-256-XTS.

On later launches:

- without **Remember on this device**, the password is required again;
- with it enabled, Windows Credential Manager, Linux desktop Secret
  Service, or macOS Keychain stores the password for automatic unlock;
- **Settings → Data and privacy** can forget that stored credential.

The raw database key exists only in the unlocked process. Losing the password
means losing the encrypted database and encrypted backup snapshots.

All PrAImate-owned persistent data is centralized under:

| System | Data root |
|---|---|
| Linux | `$XDG_CONFIG_HOME/praimate` or `~/.config/praimate` |
| Windows | `%APPDATA%\praimate` |
| macOS | `~/Library/Application Support/praimate` |

This root contains the encrypted database and key envelope, non-secret
bootstrap configuration, agents, skills, managed tools, and managed-run state
(`runs/<run-id>/request.json`, status, transcript checkpoint, per-run memory,
and artifacts). A user-selected
projects/workspaces folder remains separate.

Managed-run JSON and artifacts are permission-restricted ordinary files, not
records inside the encrypted database. They are not included in Git backup.
Anyone who can read the operating-system account's PrAImate data folder can
read them.

Saved Local LLM and MCP credentials live in the encrypted database. PrAImate
does not create application, Graphify-query, or terminal-output log files.
Underlying CLIs may still keep native logs or sessions.

## Desktop pages

| Page | Purpose |
|---|---|
| **Dashboard** | Monthly reported tokens, averages, and most-used CLIs/models, stored in the encrypted profile database. |
| **Code** | Run a supported CLI live in a chosen project folder, with optional model, local endpoint, agent context, and MCP wiring. |
| **Chats** | Create and reopen conversations with per-chat CLI, model, tools, skills, MCP and native context limits. |
| **Studio** | Open projects in the integrated Code-OSS / VSCodium environment with the PrAImate sidebar. |
| **Documents** | Work on documents with an editor and assistant chat. |
| **Workers** | Save hierarchical chats or parallel task graphs; track CLI/model assignments, tools and handoffs in an execution window, and review isolated worktree changes. |
| **Agents** | Create, import/export and run agents and workflows; manage built-in, optional Graphify or remote knowledge. |
| **Skills** | Enable built-in skills or add a skill from a URL, local ZIP, or manual definition. |
| **CLI & Tools** | Detect, install, update, or repair supported CLIs and managed tools. |
| **Local LLM** | Save an OpenAI-compatible endpoint, API key, and model; route supported CLIs and see an explicit warning for HTTP transport. |
| **MCP** | Add, edit, enable, or remove locally configured stdio/HTTP MCP servers and local catalogue entries. |
| **Settings** | Updates, source builds, appearance, Git backup, and storage/privacy controls. |
| **About** | Version, platform, encryption status, paths, compatibility, and the full privacy disclosure. |

Usage tracking starts with this update. Chat/Studio turns, Worker model calls and
reported terminal API calls are counted separately; missing provider token reports
are not estimated. Terminals launched by PrAImate support local usage collection
for Codex, Claude Code/OpenClaude, Copilot and OpenCode/PrAImate Code. PrAImate CLI
already records through Core. Antigravity interactive terminals and terminals
started outside PrAImate are not measured. Older CLI versions may omit reports.
Months use UTC. See [CLI integrations and usage](docs/cli-integrations.md).
The VS Code extension's **Overview** reads the same encrypted profile metrics.
Metrics contain CLI/model labels, timestamps, counts and durations, without
prompts, responses, file contents or credentials. No separate telemetry file or
remote analytics service is used.

The sidebar **Sessions** button opens a floating panel of saved conversations and
running terminals. **Delete session** and **Delete all** require confirmation:
they stop active work and permanently delete those conversations from PrAImate,
including their saved messages. Project files are kept. Closing the panel itself
does not stop or delete any session.

## Agents and workflows

Agents are portable YAML definitions. They can contain:

- instructions and supported CLIs;
- tool policy and allowed surfaces;
- `mcp_servers` references;
- one or more parameterized workflows;
- a default workflow;
- raw, built-in RAG or optional Graphify knowledge, with optional model enrichment and remote knowledge endpoints;
- an optional, explicitly confirmed requirements installation script.

Agent Studio starts with **Guided**, **Manual**, and **Import** paths. Guided
creation turns a purpose, knowledge choice, and explicit capability checklist
into a deterministic Simple, Tool-enabled, or Autonomous preset. The
result is always reviewable as `agent.yaml` plus an optional `runtime.json`.

Agents with knowledge, requirements, or runtime capabilities are exported as
`.praimate-agent` packs. The pack contains `agent.yaml`, optional
`runtime.json`, `knowledge/**`, and `requirements/**`. Import validates and
stages the complete pack before replacing live data. Agents without a runtime
manifest keep the existing native CLI behavior.

Simple and Tool-enabled presets run through the current native CLI path.
Autonomous runs use a managed single-agent lifecycle in Chats, document Studio,
and Workflows. The runtime provides explicit completion, structured per-run
working memory, artifacts, bounded context/output, live events, and durable
checkpoints. Declared capabilities expose brokered project read/search/write,
Git, argv-only commands, bounded HTTP GET, local or remote agent knowledge, and
configured MCP tools. File writes, commands, mutating Git, network requests,
and MCP connection/tool calls pause for explicit approval. The underlying CLI remains in
safe mode and never receives those host tools directly.

Stopped, failed, stalled, and crash-interrupted runs can resume from Agent
Studio. If interruption occurred during a tool call, PrAImate records its
outcome as unknown and tells the agent to inspect current state before retrying.
Run checkpoints are functional state, not diagnostic/event logs.

The Autonomous preset does not claim OS-level sandbox isolation: an approved
command is still a real process on the host. Team/delegation and manifests that
claim `sandbox` remain fail-closed. Team is not offered in guided creation until
that coordinator exists. The separate [Workers mode](docs/WORKERS.md) provides
three-tier coordination without enabling team runtime manifests. Interactive
Terminal execution remains native.

Skills are managed in PrAImate's shared immutable library and can be bundled
inside reviewed agent packs. The generic agent importer installs and approves
the exact bundled digests, so a new Chat, Studio or Terminal opened from that
agent inherits its skills without a second activation step. This library is
separate from each CLI's native skill catalogue: a Chat can receive an
approved skill in PrAImate's controlled request payload without the skill
appearing in the CLI's `/skills` listing. Skills loaded only by a CLI or copied
into a project are not automatically registered or approved by PrAImate.

## MCP

External MCP connections use servers you configure or install locally:

- **stdio** servers run a local command or container;
- **HTTP** servers point to an endpoint you control;
- environment values are stored in the encrypted database;
- servers can be edited after creation;
- enabled servers are selected per chat, or referenced by an agent's
  `mcp_servers` YAML field.

The local catalogue lists locally launched utilities. PrAImate also supplies
its internal skills integration: native chats use core skill tools, while
compatible external adapters receive the embedded skills MCP integration.

## Local LLM routing

When an HTTPS host uses a private/self-signed certificate, **Test connection**
offers an explicit confirmation showing its SHA-256 fingerprint and expiry.
Accepted certificates are stored in the encrypted database and trusted only for
that HTTPS host and port; hostname and expiry verification remain enabled.
Discovery and PrAImate's native model requests use this exception. A changed
certificate needs another confirmation. **Remove certificate exception** restores
system trust. Third-party CLIs retain their own TLS configuration.

The Local LLM page accepts Ollama, vLLM, GPUStack, LiteLLM, llama.cpp,
LocalAI, and similar OpenAI-compatible endpoints.

PrAImate can configure:

- PrAImate CLI directly through core-owned model routing and tools;
- OpenClaude through per-launch OpenAI-compatible environment variables;
- OpenCode/PrAImate Code through their provider configuration plus
  environment-based secrets.

Claude Code stays on its supported Anthropic transport. Use OpenClaude for
Claude-style agents backed by a local model. PrAImate also does not modify
Codex provider configuration; Codex uses whatever provider and authentication
the user configured directly in Codex.

If an endpoint uses `http://`, the GUI warns that the underlying CLI will send
model traffic over unencrypted HTTP. PrAImate cannot add HTTPS to a server that
does not provide it.

## Execution reliability

Chat, Studio, workflows, and live Code terminals use the same backend launch
resolver. Before a run starts, PrAImate validates the CLI, agent surface,
working folder, local route, permission level, and referenced MCP servers.
Launch dialogs show the selected CLI's effective capabilities and block invalid
configurations before any child process starts.

Permission levels are capability-based. Unsupported levels are visibly reduced
to safe mode instead of being silently promoted. Workflow runs default to safe
mode and never force full tool access.

Agentic runs use a separate managed path rather than replacing the workflow
runner. Its policy broker exposes only capabilities declared by the agent:
project read/search/write, Git, argv-only commands, bounded HTTP GET, raw,
built-in, optional Graphify or remote knowledge, configured MCP tools, working
memory, and artifacts.
Mutating or external operations pause for explicit GUI approval; MCP
configuration and credentials remain backend-only.
Plain model prose cannot finish a managed run: completion requires the runtime's
explicit `finish` action. Large continuation or final output is shortened in
the model/UI context and preserved as a text artifact.

## Memory and sessions

The removed cross-chat Memory GUI/database feature is not part of PrAImate.
PrAImate does not build an episodes/facts/identity memory profile across
unrelated conversations.

Autonomous working memory is scoped to one managed run. It persists only so the
user can inspect that run in Agent Studio; it is not injected into other chats
or later runs and does not recreate the removed cross-chat memory feature.

Existing workpath chats may still use their own per-chat `MEMORY.md`. That file
is ordinary workspace content staged into the chat sandbox and synchronized
back after a session. It is intentionally separate from the removed cross-chat
memory feature.

External CLIs own their provider communication and native resume. PrAImate CLI
uses encrypted core sessions shared with Desktop and Studio. Worker chats save
profiles, activity and bounded prior results in the same core database. Legacy
workspace chats can mirror relevant external CLI session slices into their
chat folder for portability.

## Git backup

Backup is optional and disabled until configured. Settings offers two explicit
starting points:

1. create local Git history, optionally with a new remote;
2. connect to and compare an existing remote before choosing how to reconcile.

PrAImate uses the installed `git` client and its existing HTTPS/SSH
credentials. Its own backup commits use the repository-local
`PrAImate <praimate@local>` identity, so global Git author configuration is
not required or modified.

The database backup consists of:

- `.praimate-state/db.sqlite` — encrypted snapshot;
- `.praimate-state/db.sqlite.key` — password-protected key envelope.

A second installation can import it with the backup's original password.
If this differs from the local database password, Settings asks for it and
preserves the local password and conversations. Pending restores pause uploads
to protect the downloaded snapshot. Workspace files, transcripts, native session slices, and
per-chat `MEMORY.md` files are normal Git objects, not encrypted vault content.

Important for upgrades: older backup commits may contain plaintext SQLite
snapshots. Creating encrypted snapshots does not erase those historical Git
objects. Recreate the backup repository or rewrite its history if old
plaintext snapshots must be removed.

## Delete all stored data

**Settings → Data and privacy → Delete all stored data** uses two confirmation
steps. It removes PrAImate's data root, encrypted database, key envelope,
settings, agents, skills, managed tools, remembered database credential, and
PrAImate-managed CLI routing blocks. The projects/workspaces folder is shown
explicitly and can be included or kept.

After deletion the app quits. Opening it again starts with a clean database and
first-run setup.

## Build

Linux source builds require Go, Node/npm, pkg-config, GTK 3, and WebKitGTK 4.1:

```sh
sudo apt-get install -y npm pkg-config libwebkit2gtk-4.1-dev libgtk-3-dev
```

Build the supported release bundles:

```sh
scripts/build.sh --version=1.2.26
scripts/build.sh --version=1.2.26 --with-code --with-graphify
```

Build PrAImate Code from the vendored OpenCode source:

```sh
PATH="$HOME/.bun/bin:$PATH" \
  OUT="dist/$(go env GOOS)-$(go env GOARCH)" \
  scripts/build-praimate-code.sh
```

See [docs/RELEASE-GITHUB.md](docs/RELEASE-GITHUB.md) for the complete release
asset matrix and checksum process.

## Documentation

| Document | Scope |
|---|---|
| [Full guide](docs/GUIDE.md) | Installation, every GUI page, storage, privacy, agents, skills, MCP, local LLMs, sessions, backup, and deletion. |
| [PrAImate CLI](docs/native-cli.md) | Terminal commands, local model context, tools, skills, sessions and attachments. |
| [Assistant and Voice](docs/assistant-voice.md) | Optional desktop assistant, permissions, Worker delegation, local dictation and SHA-256 verified model distribution. |
| [Workers](docs/WORKERS.md) | Three-tier profiles, delegation, saved runs, execution limits and verification boundaries. |
| [Agent knowledge](docs/KNOWLEDGE.md) | Built-in retrieval, optional local/CLI enrichment and remote knowledge API. |
| [Agent creation manual](docs/AGENT_GUIDE.md) | Create, configure, test, package, and share agents; complete `praimate.agent/v1` YAML reference. |
| [Workpath quickstart](docs/QUICKSTART.md) | Create and compile a workpath with `wpc`. |
| [Workpath schema](docs/SCHEMA.md) | Source files, imports, hooks, tools, and subagents. |
| [Compile targets](docs/TARGETS.md) | Files produced for each `wpc` target. |
| [Activation](docs/ACTIVATION.md) | Where compiled files must be placed for each host CLI. |
| [Release guide](docs/RELEASE-GITHUB.md) | Supported release targets, artifacts, checksums, and publication. |

## License

[MIT](LICENSE)
