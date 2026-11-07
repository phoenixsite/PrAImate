<p align="center">
  <img src="assets/monke-icon.png" alt="PrAImate" width="120" />
</p>

# PrAImate user guide

PrAImate provides Desktop and an optional Server edition for Linux, Windows and
macOS Intel/Apple Silicon. The Android preview connects to Server.
External CLI adapters retain their provider authentication and native sessions.
PrAImate CLI uses core-owned model routing, tools and encrypted sessions shared
by Desktop, Studio and its terminal frontend.

This guide describes the current GUI. Workpath compiler details are kept in
[QUICKSTART.md](QUICKSTART.md), [SCHEMA.md](SCHEMA.md),
[TARGETS.md](TARGETS.md), and [ACTIVATION.md](ACTIVATION.md).

## Contents

- [Install and launch](#install-and-launch)
- [First launch](#first-launch)
- [Storage and encryption](#storage-and-encryption)
- [Code](#code)
- [Chats](#chats)
- [Studio and Documents](#studio-and-documents)
- [Workers](#workers)
- [Agents](#agents)
- [Skills](#skills)
- [CLI & Tools](#cli--tools)
- [Local LLM](#local-llm)
- [MCP](#mcp)
- [Settings](#settings)
- [About and privacy](#about-and-privacy)
- [Per-chat MEMORY.md](#per-chat-memorymd)
- [Git backup](#git-backup)
- [Delete all stored data](#delete-all-stored-data)
- [Files and ownership boundaries](#files-and-ownership-boundaries)
- [Build from source](#build-from-source)

## Install and launch

Choose **Desktop** for local use or **Server** for browser/Android access.
Start with the [installation guide](INSTALLATION.md) for all commands, package
names, first server startup and side-by-side installation. The `main` branch
contains Desktop; the `server` branch also contains Server and Android.

Linux or macOS, Desktop:

```sh
curl -fsSL https://raw.githubusercontent.com/sPROFFEs/PrAImate/server/scripts/install.sh | bash -s -- --binary --edition desktop --user
```

Linux or macOS, Server instead:

```sh
curl -fsSL https://raw.githubusercontent.com/sPROFFEs/PrAImate/server/scripts/install.sh | bash -s -- --binary --edition server --user
```

Windows PowerShell: download once, then choose one edition:

```powershell
Invoke-WebRequest -UseBasicParsing https://raw.githubusercontent.com/sPROFFEs/PrAImate/server/scripts/install.ps1 -OutFile "$env:TEMP\praimate-install.ps1"
& "$env:TEMP\praimate-install.ps1" -Mode Binary -Edition Desktop
# For Server, use instead:
& "$env:TEMP\praimate-install.ps1" -Mode Binary -Edition Server
```

Both editions use the same default prefix and profile. Use separate prefixes and
`PRAIMATE_HOME` values to keep both; otherwise a second install replaces the first.
For Android, install the signed [preview APK](https://github.com/sPROFFEs/PrAImate/releases/download/v1.2.27/praimate-mobile-preview.apk)
and connect it to a running Server using HTTPS and a device pairing code. See
[Server setup](REMOTE-SERVER.md) and the [Android guide](MOBILE-ANDROID.md).

Launch and maintenance commands:

```sh
praimate
praimate --gui
praimate code
praimate agent run --agent DEV-TEAM --cli praimate-code --folder /path/to/project --prompt "review this code"
praimate -check-update
praimate -update
praimate -version
```

`praimate` is a lightweight bootstrap and maintenance command. It opens the
mandatory sibling `praimate-gui` desktop binary. `--gui` is a compatibility
alias; there is no supported text-user-interface mode.

`praimate agent run` is the non-GUI automation interface. It runs an installed
agent through the same encrypted settings, execution resolver, local routing,
knowledge, and runtime policy as the desktop app, then emits parseable JSON.
It supports one-off prompts and named workflows, authenticated local-model
preflight, correlated JSONL events, and opt-in durable `--run-id` status/replay.
If the database password is not remembered, an interactive terminal asks for
it with input echo disabled. See the dedicated [CLI agent API](CLI_AGENT_API.md)
and [Python example](../examples/praimate_agent_capture.py).

Release target archives:

| System | Desktop archive | Server archive |
|---|---|---|
| Linux amd64 | `praimate-linux-amd64.tar.gz` | `praimate-server-linux-amd64.tar.gz` |
| Windows amd64 | `praimate-windows-amd64.zip` | `praimate-server-windows-amd64.zip` |
| Windows arm64 | `praimate-windows-arm64.zip` | `praimate-server-windows-arm64.zip` |
| macOS Intel | `praimate-darwin-amd64.tar.gz` | `praimate-server-darwin-amd64.tar.gz` |
| macOS Apple Silicon | `praimate-darwin-arm64.tar.gz` | `praimate-server-darwin-arm64.tar.gz` |

Linux arm64 can be built on a native arm64 host with the GUI dependencies.
macOS builds require a native Apple toolchain on the target architecture.
Both Intel and Apple Silicon archives are published. The Unix installer supports
both. Shared standalone CLI/tools are published once per platform; the APK is a
client, not a server package.

The Linux installer registers `praimate.desktop` and its icon in
`${XDG_DATA_HOME:-~/.local/share}` and copies the launcher to the configured
Desktop folder when present. Launchers support install paths containing spaces
and use core PATH discovery without running interactive shell startup files.
Re-run the installer with the same prefix to refresh an older shortcut; updating
the executable alone does not replace previously installed launcher scripts.
The runtime uses the same `praimate` window identity for taskbar matching and a
bounded window icon so GTK does not discard it as an oversized X11 property.

Windows amd64 and arm64 executables contain the application icon. The macOS
installer creates `~/Applications/PrAImate.app` and a Desktop link when available;
direct launches also set the Dock icon. Title-bar and minimized-window icon
placement is controlled by the operating system and desktop theme.

Appearance settings apply to both light and dark surfaces. The interface follows
the system's reduced-motion preference, including dialogs and notifications.

## First launch

Startup is intentionally locked until the database is available.

1. **Create a database password.** It must contain at least 12 characters.
   PrAImate asks for confirmation before creating the database.
2. **Choose whether to remember it.** This is optional. Windows uses
   Credential Manager; Linux uses the desktop Secret Service; macOS uses
   Keychain. If the
   credential service is unavailable, the database can still open, but the
   password is not remembered.
3. **Read and accept the privacy notice.** It explains local storage,
   provider/CLI data flow, agent file access, encryption limits, and backup.
4. **Choose a projects/workspaces folder.** This is separate from the
   PrAImate application-data folder.
5. **Choose how to start.** Create a fresh folder, seed bundled samples, or
   connect an existing Git-backed workspace.

Later launches require the database password unless the explicit remember
option succeeded. A wrong password does not open Core or SQLite.

## Storage and encryption

PrAImate centralizes app-owned data:

| System | Application-data root |
|---|---|
| Linux | `$XDG_CONFIG_HOME/praimate`, normally `~/.config/praimate` |
| Windows | `%APPDATA%\praimate` |
| macOS | `~/Library/Application Support/praimate` |

The root contains:

- `db.sqlite`, encrypted with AES-256-XTS;
- `db.sqlite.key`, an authenticated password envelope rather than a raw key;
- `config.json`, restricted to non-secret bootstrap configuration;
- agents, skills, managed tools, and source-built bundled binaries.

Encryption flow:

1. PrAImate generates a random 512-bit database key.
2. Argon2id derives a key from the user's password.
3. XChaCha20-Poly1305 wraps and authenticates the database key.
4. The raw key exists only in the unlocked process and is cleared on close.

This protects a copied database from direct inspection without the password.
It does not protect against an attacker controlling the same OS account or
reading the unlocked process. AES-XTS encrypts database pages but does not
provide database-page authentication. Back up the password separately; there
is no recovery bypass.

Saved Local LLM keys, MCP environment/authentication data, and chat-local API
keys live in the encrypted database. They are not written back to
`config.json`.

## Code

The Code page opens a live terminal-backed CLI in a selected project folder.

You can:

- choose an installed supported CLI;
- choose or enter a model where that CLI supports a model flag;
- use the saved Local LLM route where supported;
- start a clean session;
- launch an agent in the selected folder so its instructions and knowledge
  are available;
- reopen recorded Code sessions from the Code page.

Terminal scrollback exists only in GUI memory and disappears when the process
closes. PrAImate does not write terminal-output logs. The child CLI can still
create its own native session files.

When no agent is selected, enabled MCP servers can still be prepared for the
clean Code session. With an agent, the agent's MCP references and current
configuration are used.

## Chats

Chats lists regular conversations. Code, Studio, Documents and Workers provide
their own session views; opening their transcript explicitly does not add them
to the regular Chats list.

For a clean chat, choose the CLI, optional model, tool level, per-chat skills,
and MCP servers. If a compatible Local LLM default exists, the new-chat form
can route the chat through that endpoint.

Opening a chat shows rendered Markdown rather than raw Markdown delimiters.
Code blocks, headings, tables, lists, and links are presented as formatted
content.

Chat settings remain per-chat truth. Changing the CLI starts a new native CLI
session while preserving the PrAImate conversation record.

External adapters delegate provider communication to the selected CLI;
PrAImate CLI connects through the core. Native chat configuration includes
context-window and output-reserve fields; enter 0 to use host settings or loaded
server metadata. See [PrAImate CLI](native-cli.md#context-and-output-budgets).

PrAImate cannot add HTTPS to a model server that only exposes HTTP.

## Studio and Documents

**Studio** opens project sessions in the integrated Code-OSS / VSCodium editor.
Recent projects appear first. Use **Open project** to choose a folder, CLI,
model, agent and tool permissions. CLI detection and model loading happen when
opening that dialog, so they do not delay the project list. **Editor environment**
below the projects contains setup, integration updates and repair actions.
If setup is required, Studio asks before installing and only opens the editor
after setup succeeds. Environment and history errors remain on the Studio page.
The **CLIs & Tools** Studio installer and `praimate studio install` use the same
setup as Studio's **Repair** action. Installation, status, updates and repair do
not require unlocking the database. They use a private editor profile and
extension directory, leaving your normal VS Code / VSCodium profile unchanged.
If the extension files exist but the editor no longer lists them, repair
registers the bundled extension again using the editor's extension installer.
Opening a project also refreshes the integration and its displayed status.
The PrAImate sidebar provides its own saved assistant chat, model and permissions
settings, skills, and Workers view. Native CLI terminals opened in the editor
are separate from that sidebar chat.

**Documents** combines a document editor and assistant chat. These sessions have
their own page and saved conversation context.
The file tree loads on opening and follows external and agent file changes.
Unsaved edits are preserved when an external change arrives. Changing the folder
in the Document window saves drafts first, switches the editor and assistant
together, and starts a fresh native CLI session while retaining chat history.
Choose an existing directory; PrAImate does not create a missing workspace.
To change the folder from the Documents list, first close its open window.

## Workers

Choose **Workers → New worker chat**, select a workspace and configure the
Reasoner, Middle and Fast profiles. Each profile has its own CLI/runtime, model,
permissions and limits. The same CLI can be selected with different models.

The creation dialog stores those profiles with the new chat. Follow live
requests, responses and delegated results in three collapsible horizontal
panes. Saved chats support follow-up, Stop, Rename and Delete. Interrupted
chats retain recorded activity for review before continuing. Workers is also
available in the Studio extension through **PrAImate: Workers**.

See the [Workers guide](WORKERS.md) for delegation, real host tools, context
isolation, limits and the distinction between controlled tests and live model
quality.

## Agents

The Agents page manages reusable YAML agents and imports Markdown agent definitions. The old legacy-template import
button is not part of the current UI.

An agent can define:

```yaml
schema: praimate.agent/v1
id: example
name: Example
description: Example agent
instructions: |
  You are an example agent.
supports: [claude, codex, opencode, praimate-code]
tools: [edits]
mcp_servers: [local-filesystem]
surfaces: [chat, terminal, editor]
knowledge: raw
```

Optional fields include:

- workflows and a default workflow;
- workflow inputs and ordered steps;
- MCP server IDs;
- raw or `rag` knowledge mode;
- a requirements script.

Requirements scripts can install software and modify the computer. PrAImate
shows an explicit confirmation before running one.

### Agent Studio

Agent Studio offers three entry paths:

- **Guided** asks for the agent's purpose, knowledge mode, capabilities, and a
  preset, then previews the exact generated files before creation.
- **Manual** creates the familiar YAML agent and leaves every field editable.
- **Import** accepts a bare YAML definition or a portable agent pack.

The three guided presets are deterministic. Simple and Tool-enabled use the
existing native CLI runtime. Autonomous uses the managed single-agent runtime
in Chats, document Studio, and Workflows. Team remains a valid fail-closed
manifest value for package compatibility, but is not offered in guided
creation until coordinator/delegation execution exists.

Managed Autonomous execution provides explicit lifecycle completion, bounded
context/output, per-run working memory, artifacts, checkpoints, and live
events. Its policy broker exposes only capabilities declared by the agent:
contained project access, Git, argv-only commands, bounded network GET,
Raw/indexed knowledge, and configured MCP tools. File writes, mutating Git,
commands, network requests, and MCP connection/tool calls require GUI approval. The underlying
CLI stays in safe mode and never receives those host tools directly.

Agent Studio lists recent managed runs, shows final output, memory, and text
artifacts, and can resume stopped, failed, stalled, or crash-interrupted runs.
Its authoring helper stays native so it can edit `agent.yaml`. MCP requires the
`external_services` capability; Graphify RAG requires an index built in Agent
Studio. Autonomous does not provide OS-level sandbox isolation: approved
commands run on the host. Interactive Terminal execution remains native.

The editor exposes advanced configuration as a separate, strictly validated
`runtime.json` tab. Closing and reopening that tab reloads the saved manifest.

Agent Studio can also use an installed CLI as a helper, then lets the user
review/apply the helper's changes.

See the [Agent creation manual](AGENT_GUIDE.md) for a step-by-step tutorial,
the complete `praimate.agent/v1` field reference, workflow templates,
knowledge and RAG setup, MCP references, requirements scripts, and pack
portability.

### Knowledge

Each agent can keep files under:

```text
<application-data>/agents/<agent-id>/knowledge/
```

Modes:

- **raw**: the agent is instructed to read relevant files directly;
- **rag**: built-in offline retrieval indexes text/code; Graphify remains optional for semantic extraction and PDF/media.

The GUI can add files or folders, edit supported text files, remove entries,
choose the built-in or a Graphify indexing backend, and rebuild the index.
Built-in queries refresh changed files automatically and include source citations.
See [Agent guide](AGENT_GUIDE.md#built-in-rag-and-optional-graphify) for limits.

### Agent packs

Exporting an agent produces YAML, OpenCode-compatible Markdown, or a `.praimate-agent` pack.
Markdown preserves external frontmatter without granting its permissions to PrAImate.
Use a pack to include knowledge files and the runtime manifest. Packs can
contain:

```text
agent.yaml
runtime.json
knowledge/**
requirements/**
```

`runtime.json` is optional. A pack without it replaces the recipient agent
with native-runtime behavior, so a stale advanced configuration cannot survive
an import invisibly.

Import validates a staged copy before replacing live agent data. Built-in and
Graphify output may travel with the pack, so an indexed agent can arrive pre-indexed.

Reviewed agent packs can embed exact locked skill versions and resources.
Import installs and approves the bundled digests; new sessions from that agent
inherit those skills. The shared PrAImate library is separate from each CLI's
native skill catalogue. See [Skills](#skills) and the
[agent pack reference](AGENT_GUIDE.md#agent-pack).

## Skills

Skills are reusable prompt/instruction resources. The Skills page can:

- browse and search built-in, imported and locally authored skills;
- import a local folder, ZIP archive or pinned GitHub repository;
- review and approve exact package content;
- create and publish a skill locally;
- assign skills and their loading mode to existing sessions.

To import one GitHub skill, paste its folder URL, such as
`https://github.com/owner/repo/tree/main/skills/example`, or supply a repository
root, ref and subpath separately. Inspection resolves the ref to a commit and
keeps that revision through review and installation. For branch names containing
slashes, specify the complete ref explicitly or use the separate fields.

An explicit subpath scopes both skill discovery and optional shared files to
that directory. The candidate shows its own files, including `SKILL.md`, scripts,
references and licenses, which are included automatically. Shared files are
optional additions selected before reviewing the exact package content. Leave
the subpath blank to browse the whole repository.

Single-skill imports open the content review directly. For a repository or ZIP
with several skills, use **Select all** or **Clear selection**, then review the
selection once. **Import and enable** installs and approves the reviewed
versions together so they are immediately available in chat and agent selectors.
Choose **Import only** to skip enabling during import. Existing versions
can be enabled from their content view without another acknowledgement checkbox.

Chat and agent skill selectors also support **Select all** (or **Select matching**
when searching). Bulk selection keeps existing loading modes and adds new skills
in automatic mode, with one version per skill and up to 128 selected skills.
Only up to three skills can be pinned into the initial prompt; the rest load on
demand. **Clear selection** removes the explicit choices.

Repository inspection and bulk import allow up to 20,000 archive entries and
100 MiB of expanded content (25 MiB compressed; 5 MiB per resource). Each
selected skill remains limited to 1,000 files and 8 MiB so it can be opened in
the editor. These are host limits; imported metadata cannot change them.

Portable instruction-only skills can be selected across supported PrAImate
CLIs and surfaces. A skill that names host-specific tools or loading conventions
still depends on that runtime. Every import route enters the same immutable
package library; agent packs carry their selected skills and the generic agent
importer reviews and installs them together. The PrAImate library is separate
from each CLI's native skill catalogue: a Chat may receive a skill in its
controlled request payload without that skill appearing in the CLI's own
`/skills` listing. Skills loaded only by a CLI are not automatically registered
or approved by PrAImate. Delivery receipts describe controlled payloads, not
native private context or proof the model followed a procedure. See the
[native CLI guide](native-cli.md) for embedded skill discovery and execution.

## CLI & Tools

This page detects installed CLIs and managed tools, shows the resolved binary
path/version, and exposes the supported install/update/repair action.

Selectable CLI families are:

- Claude Code;
- OpenClaude;
- Codex CLI;
- OpenCode;
- PrAImate Code.

PrAImate Code is one product entry: the managed, rebranded OpenCode binary.
The UI no longer shows a second duplicate item for a source-built copy.
Detection resolves the installed binary regardless of whether it came from a
release asset or a local source build.

Managed tools include PrAImate Code and Graphify. Other optional integrations
appear only when their supported installer/detection entry exists.

## Local LLM

The Local LLM page stores a default OpenAI-compatible endpoint, optional
Bearer key, and model. It probes common model-list routes and permits manual
model entry if probing fails.

Typical compatible servers include:

- Ollama;
- GPUStack;
- vLLM;
- LiteLLM;
- llama.cpp server;
- LocalAI.

### HTTP warning

If the endpoint begins with `http://`, the page warns that the underlying CLI
will communicate with the model server over unencrypted HTTP. This may be
reasonable for loopback or a trusted private network, but it is unsafe across
untrusted networks. Configure TLS on the server or a trusted reverse proxy to
obtain HTTPS.

### Per-CLI behavior

| CLI | Configuration |
|---|---|
| Claude Code | Anthropic connection only; PrAImate does not inject local endpoints. |
| OpenClaude | Per-launch OpenAI-compatible environment variables, model, and token limits. |
| OpenCode/PrAImate Code | Provider configuration references a separate `PRAIMATE_LLM_<provider hash>` variable for each provider; PrAImate supplies each host's secret at launch. Legacy `OPENAI_API_KEY` references are supported. |
| Codex | Not routed by PrAImate; Codex retains its own provider and authentication configuration. |
| PrAImate CLI | Core-owned endpoint/credentials, assigned host/model and automatic or configured context limits. |

The Local LLM key is migrated out of older plaintext configuration into the
encrypted database and is resolved in Go for the selected native request or
supported child process. It is not returned to the GUI renderer.

Changing the default host preserves each host's key and existing provider/model
assignments. Models from different hosts or providers remain separate even when
their names match. Accept a self-signed HTTPS certificate in Local LLM settings
to use it with the native runtime, Assistant's existing-endpoint mode, or the
patched bundled PrAImate Code. Certificate and CA consent stays scoped to the
selected HTTPS origin. Assistant picks up trust changes on its next check or
turn. See
[CLI integrations](cli-integrations.md#self-signed-local-endpoints-in-praimate-code).

### Execution preflight

Chat, Studio, workflow, and Code-terminal launches share one backend resolver.
It computes the effective model, local route, credential environment, MCP
selection, and permission level before invoking the CLI. The launch preflight
checks:

- the selected CLI is installed and supported by the agent;
- the agent permits the requested GUI surface;
- the working folder exists and is a directory;
- referenced MCP servers exist, with disabled servers reported;
- the CLI can enforce the requested permission level;
- local routing is supported and has a model;
- remote plaintext HTTP endpoints are explicitly warned about.

Preflight errors block launch. Warnings are shown for confirmation. Unsupported
permission levels degrade to safe mode; they are never silently upgraded.
Workflow execution also defaults to safe mode rather than granting full tool
access.

## MCP

The MCP page is limited to servers the user configures or runs locally. It
does not present a catalogue of third-party hosted services.

Supported transports:

- **stdio**: a local executable, script, package runner, or container command;
- **HTTP**: a Streamable HTTP endpoint hosted on infrastructure the user controls;
- **SSE**: a legacy server-sent events endpoint.

The add/edit form supports:

- name;
- transport;
- quoted stdio command and arguments;
- HTTP URL;
- HTTP headers, one `Header: value` per line (including `Authorization` or API keys);
- environment variables.

Stdio commands expand `~/`, `$VAR`, and `${VAR}` before launch. MCP secrets
are stored in the encrypted database. Every saved server can be enabled,
disabled, edited, or removed.
JSON imports preserve a server's `headers` map. Explicit header values are sent
literally, so include the required `Bearer` or `Basic` prefix. Connection tests
use the same authenticated MCP handshake as the runtime and list its tools.
The Core MCP client accepts protocol messages up to 16 MiB over stdio, HTTP or
SSE, so large tool catalogues do not hit the model-facing tool-output limit.
JSON frames are decoded whole; an oversized frame produces an explicit error.
Tool result text passed to the model remains bounded separately.

The Local catalogue contains optional utilities PrAImate launches as local
processes. Catalogue entries can be configured and edited like other local
servers.

Chats select enabled MCP servers per chat. Agents can reference server IDs in
their YAML `mcp_servers` field. A requirements script can install an MCP
implementation, but installation alone does not create its PrAImate server
record; the server must still be configured or referenced by a known ID.

## Settings

### Updates

**Check for updates** compares the running version with the latest published
GitHub release. `praimate -update` performs the actual self-update and refreshes
the sibling GUI binary.

### Build bundled tools from source

PrAImate can build PrAImate Code or Graphify from source when prerequisites
are present. The GUI shows each required program, streams build output, installs
the finished binary under the application-data root, and deletes the temporary
checkout.

PrAImate Code requires Git, Bash, and Bun. Graphify requires Git and `uv`.

### Appearance

Choose light, dark, or system theme and an accent color.

### Data and privacy

The page shows the exact application-data and projects-folder paths. It offers:

- **Require password next launch**, which removes the remembered OS credential;
- **Delete all stored data**, described below.

## About and privacy

About shows:

- PrAImate version and build platform;
- database encryption state;
- database/key/config/workspace paths;
- supported systems;
- the same complete privacy disclosure shown on first launch.

PrAImate itself:

- sends no product analytics;
- writes no application diagnostics log;
- writes no Graphify query log;
- writes no Code-terminal output log.
- stores only sanitized versioned-skill delivery receipts in the encrypted chat
  record (identity, digest, block kind/size, epoch, and state; never skill body,
  prompt, resource text, paths, command arguments, or reasoning).

The selected CLI/model provider receives prompts and context necessary to
perform the user's request. Agent instructions can authorize filesystem or
tool access. Review the CLI's own permissions and provider policy.

## Per-chat MEMORY.md

The removed cross-chat Memory GUI/database section is not part of the current
tool. There is no episodes/facts/identity profile accumulated across unrelated
chats.

Legacy workpath chats can still use per-chat `MEMORY.md`:

```text
<projects-root>/chats/<chat-id>/MEMORY.md
```

When enabled for that workpath, the file is staged into the sandbox before
launch and synchronized back after exit. It is normal workspace content and
can be included in Git backup. This file-based, on-chat behavior is separate
from the deleted cross-chat memory functions.

## Git backup

Git backup is optional. It uses the installed `git` executable and the user's
existing HTTPS credential helper or SSH agent.

### Initial configuration

When no backup repository exists, Settings requires an explicit choice:

1. **Start a new backup**: initialize local history; a remote is optional.
2. **Connect an existing backup**: test/fetch the remote and compare histories
   before any reconciliation.

This avoids treating an enable switch as permission to create or overwrite a
remote repository. PrAImate-owned backup commits use the repository-local
`PrAImate <praimate@local>` identity, so a global Git identity is neither
required nor modified.

### Normal controls

After initialization:

- enable or pause backup without deleting local history;
- edit and test the remote URL;
- sync now;
- enable startup/exit auto-sync;
- optionally prefer recent local state;
- merge or rebase diverged histories;
- force-push local state after confirmation;
- reset from remote after two confirmations;
- disconnect the remote.

### Encrypted database snapshot

Before a backup commit, PrAImate writes:

```text
.praimate-state/db.sqlite
.praimate-state/db.sqlite.key
```

The snapshot remains encrypted and the adjacent file remains a
password-protected envelope. A fresh Windows or Linux installation adopts the
snapshot and asks for its original password. An installation with existing
data can use its own password: when the remote uses a different one, Settings
asks for the backup password and merges the downloaded snapshot without changing
the local password or deleting local chats. A successfully verified backup key
is retained only in memory until the app closes; the backup password is not saved.
The password and raw key are not committed.

An unfinished restore blocks snapshot exports and uploads so Sync cannot replace
the downloaded copy. Enter its password in **Settings → Git backup → Enter backup
password** to retry the import without repeating Git reset/pull/merge. This also
protects an unreadable remote snapshot after restarting PrAImate.

Old plaintext SQLite snapshots do not need an envelope and are encrypted during
first-run password setup. An encrypted snapshot whose key is already unlocked
can also be read locally without its envelope. To restore that snapshot on a new
machine, `.praimate-state/db.sqlite.key` is required: retrieve it from the original
installation or a complete backup. A new key cannot decrypt an existing snapshot.

Background Git commands, including the auxiliary origin probe, suppress Windows
console creation. Git credential helpers may still show their own authentication
dialogs when sign-in is required.

The encrypted snapshot preserves structured credentials so a restore is
complete. Anyone with the repository can attempt offline password guessing,
so use a strong unique password and a private remote.

Managed-run state under `runs/` is not included in Git backup. Its request,
transcript checkpoint, JSON memory, and text artifacts are permission-restricted ordinary files outside the
encrypted database; protect access to the operating-system account and the
PrAImate data folder.

### What Git does not encrypt

Workspace files are normal Git objects, including:

- workpaths and project content;
- captured transcripts;
- mirrored native session slices;
- per-chat `MEMORY.md`.

Do not treat the repository as an encrypted vault.

### Upgrade warning

Backups created by older releases may contain plaintext SQLite snapshots in
Git history. Publishing a new encrypted snapshot does not remove old Git
objects. Recreate the backup repository or deliberately rewrite its history if
those old objects must be eliminated.

## Delete all stored data

Open **Settings → Data and privacy → Delete all stored data**.

The dialog:

1. lists the types of application data that will be removed;
2. lets the user keep the projects folder or select it for deletion;
3. requires an acknowledgement checkbox;
4. requires typing the displayed confirmation phrase;
5. shows a final OS confirmation dialog.

Deletion removes:

- the encrypted database and password envelope;
- non-secret bootstrap configuration;
- agents, skills, MCP credentials, managed tools, and managed-run requests,
  checkpoints, memory, and artifacts;
- remembered database credential;
- PrAImate-managed routing blocks from supported CLI configuration without
  deleting unrelated CLI data.

If projects-folder deletion is selected, the entered folder must match the
configured projects root. The application closes after deletion. The next
launch starts clean.

## Files and ownership boundaries

| Path | Owner and contents |
|---|---|
| `<config>/praimate/` | PrAImate application data. |
| `<config>/praimate/db.sqlite` | Encrypted SQLite state. |
| `<config>/praimate/db.sqlite.key` | Password-protected key envelope. |
| `<config>/praimate/config.json` | Non-secret bootstrap settings. |
| `<config>/praimate/agents/` | Agent YAML, knowledge, requirements. |
| `<config>/praimate/skills/` | User skill definitions/content. |
| `<config>/praimate/bin/`, `tools/` | Managed or source-built binaries. |
| `<projects-root>/` | User-selected chats/templates/projects and optional Git repository. |
| CLI-specific home directories | Native CLI authentication, configuration, and sessions; not wholly owned by PrAImate. |

`PRAIMATE_HOME` can override the application-data root for portable installs
and tests.

## Build from source

Linux GUI dependencies:

```sh
sudo apt-get install -y npm pkg-config libwebkit2gtk-4.1-dev libgtk-3-dev
```

Build the release bundles:

```sh
scripts/build.sh
scripts/build.sh --with-code --with-graphify
```

Build only the GUI:

```sh
cd cmd/praimate-gui
./build.sh
```

Build PrAImate Code:

```sh
PATH="$HOME/.bun/bin:$PATH" \
  OUT="dist/$(go env GOOS)-$(go env GOARCH)" \
  scripts/build-praimate-code.sh
```

See [RELEASE-GITHUB.md](RELEASE-GITHUB.md) for every required release asset,
baseline/no-AVX2 variants, and checksum publication.
