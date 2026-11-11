# PrAImate Server

`main` remains the desktop product. `server` adds an optional remote adapter,
a foreground headless runner and a browser client using the same Svelte UI.
Core owns one encrypted database, worker manager and set of CLI sessions. The
remote module is excluded from desktop builds by the Go `server` build tag.

## Install and build

For prebuilt binaries, use the **[Desktop/Server installation guide](INSTALLATION.md)**.
It includes Windows, Linux and macOS commands, the release asset matrix, separate
prefixes/profiles and the Android connection sequence.

Linux or macOS:

```sh
curl -fsSL https://raw.githubusercontent.com/sPROFFEs/PrAImate/server/scripts/install.sh | bash -s -- --binary --edition server --user
```

Windows PowerShell:

```powershell
Invoke-WebRequest -UseBasicParsing https://raw.githubusercontent.com/sPROFFEs/PrAImate/server/scripts/install.ps1 -OutFile "$env:TEMP\praimate-install.ps1"
& "$env:TEMP\praimate-install.ps1" -Mode Binary -Edition Server
```

The installer selects `praimate-server-<platform>`, not the Desktop archive or
a standalone CLI asset. From a local checkout of the `server` branch, source
installation is also supported:

```sh
./scripts/install.sh --edition server --source --user
# Desktop remains the default, including in a server checkout:
./scripts/install.sh --edition desktop --source --user
```

Windows installers accept `-Edition Server` or `-Edition Desktop`. Build scripts
accept `--edition=server` (Bash) or `-Edition Server` (PowerShell). Published binary
installs select the matching edition and reject another edition's bundle before
replacing executables. Unmarked older bundles are desktop-only.

| Edition | Branch | Bundle | Release tag | Output directory |
| --- | --- | --- | --- | --- |
| Desktop | `main` | `praimate-<os>-<arch>` | `v<version>` | `dist/` |
| Server | `server` | `praimate-server-<os>-<arch>` | `v<version>` (combined) or legacy `server-v<version>` | `dist/server/` |

Combined `v*` releases contain both editions and the Android preview. Server
installers and updates accept such a release only when it contains Server archives;
a newer Desktop-only release cannot replace Server. Legacy `server-v*` releases
remain supported and standalone Server publication stays a prerelease. Shared
standalone tools are published once. The
[1.3.4 combined release](https://github.com/sPROFFEs/PrAImate/releases/tag/v1.3.4)
is published with Server archives for all five release platforms and the Android APK.

Both editions use the existing profile location. Use `PRAIMATE_HOME` for separate
profiles. Run one owner per profile: either the server GUI or the headless server,
with other clients connecting over the network. Server owners enforce a profile
lease; older desktop releases do not participate in that lease. Do not run an
older desktop process independently against the same active server profile.

## Start without a display

```sh
praimate server --listen 127.0.0.1:8844 --db-password-stdin --pair
```

Terminal password input hides keystrokes. Piped stdin, `--db-password-file`, and
an existing securely remembered password are also supported. A new profile can
be initialized explicitly with `--initialize-db`; an existing profile retains
its database password. `--db PATH` selects a database explicitly.

The runner does not call Wails or create a window. The current distribution still
links the native desktop runtime, so Linux hosts need the WebKitGTK/GTK runtime
libraries shipped by the desktop packaging. An X server, Wayland session, GPU,
and browser process are not needed for the headless runner itself.

`--pair` prints a one-use, eight-digit administrator pairing code valid for five
minutes. Open the server URL in a browser, enter that code, and choose a device
name. The browser and Android APK use the same protocol. Browser credentials are
kept in session storage, while server-side device records contain only token
hashes in the encrypted database. Pair additional devices and revoke access from
the connection panel in the browser. Roles are `viewer`, `control`, and `admin`.
Device tokens expire after 90 days. Keep an authorized owner device or provision
an optional administrator token for recovery. These are capabilities within one
trusted application profile, not separate OS users or isolated tenant databases;
control permission includes running CLI processes under the server's OS account.

Installation never enables a listener, modifies a firewall, installs a service,
or starts a background process automatically. The Server GUI has a **SERVER**
edition label and a dedicated **Server** sidebar entry (also linked from
**Settings → Configure Server**). This page configures the listen address, TLS
certificate/key file paths, allowed browser origins and automatic startup after
database unlock. Settings are stored in the encrypted database.

Choose **Save Server settings**, then **Start remote access**. Changes to a
running listener require **Apply settings / restart**; saving alone does not
disconnect clients. **Stop access** stops only the remote listener, preserving
the GUI, Core tasks and native terminals. If a new port cannot bind, the previous
listener configuration is restored and the error remains visible. Explicit
startup flags/environment variables take precedence over saved preferences.
Older profiles without a saved Server configuration retain their existing
Cloudflare loopback startup behavior until Server settings are saved.

**Pairing and devices** creates single-use codes for direct or tunnel
connections and lists paired devices for revocation, even while access is
stopped. Pairing itself requires a running listener. No administrator token or
device credential is exposed by the configuration/status APIs. Remote Server
administration requires the `admin` role; applying or stopping the listener
schedules the change after the request receipt and disconnects remote clients.

## Cloudflare Tunnel and optional Access

In the **Server edition**, open **Server → Cloudflare Tunnel**. Save your Server
connection settings and start remote access before starting the connector.

1. Create a remotely managed tunnel in your Cloudflare dashboard. Configure a
   public hostname, such as `https://praimate.example.com`, whose service points
   to this server's local origin (normally `http://127.0.0.1:8844`). PrAImate shows
   the actual origin on the Server page. Do not point the tunnel at its own public URL.
2. Paste the public HTTPS origin and **only the connector token**, then save.
   Saving config does not install or start the connector.
3. Choose **Install and start connector**. PrAImate downloads the official
   `cloudflare/cloudflared` release for the host platform and checks the asset's
   SHA-256 digest from GitHub release metadata before installation. Refresh/status
   polling distinguishes a running process from Cloudflare's `/ready` health check.
4. Optionally create a self-hosted Cloudflare Access application protecting the
   entire hostname, including `/v1/*`, and configure allowed users and email
   one-time PIN or your identity provider. These policies stay in Cloudflare;
   PrAImate does not require a Cloudflare account API key or create DNS records.
5. Create a device pairing code in **Server → Pairing and devices** or the remote connection panel. Add
   the public hostname in Android. If Access is enabled, sign in there first,
   then pair with PrAImate using its separate code.

Headless setup can perform the same explicit operation at startup:

```sh
praimate server --listen 127.0.0.1:8844 --db-password-stdin \
  --public-url https://praimate.example.com \
  --cloudflare-token-file /private/path/connector-token \
  --cloudflare-service install --pair
```

The token file contains only the token, is at most 16 KiB and must be private
(`0600` on Unix). Never put the token itself in shell arguments. Omit the setup
flags on later server starts. `--cloudflare-service` also accepts `status`,
`start`, `stop`, `restart`, and `uninstall`; the server then remains running.
Use `--initialize-db` separately when creating a new encrypted profile.

Connector configuration and its write-only token are stored in the encrypted
database. Installing the service also creates a private token file under
`<database-path>.cloudflare/`, because the OS service must read it without
unlocking the application database. Unix files use owner-only permissions;
Windows uses an owner/SYSTEM ACL. No token is embedded in process arguments or
the service definition. Removing the service removes that credential file and
retains the encrypted configuration. Remove the service before rotating its
token, save the replacement, then install it again.

Each profile has its own service name and managed binary. Existing system-wide
`cloudflared` services are not reused or replaced. Linux uses a **systemd user
service**; persistence after logout requires the account's administrator to
configure lingering. macOS uses a **LaunchAgent**, and Windows a **hidden logon
task** running as the current user. These user services do not replace the
separate PrAImate Server service: both must run for devices to connect. Stopping
or removing the connector disconnects remote clients but leaves server tasks
running. On Linux inspect connector errors with `journalctl --user -u <serviceName>`;
its name appears in the status API/CLI output.

Public hostname changes update the exact permitted browser origin without
restarting chats, workers or event streams. PrAImate never treats Cloudflare
headers as a substitute for its own device authorization. Without Access, device
pairing is still required. With Access, both authorizations are required. Tunnel
connectivity, DNS/ingress correctness and Access policy authorization are separate
checks: a ready connector alone does not validate all three.

References: [Cloudflare tunnel parameters](https://developers.cloudflare.com/tunnel/reference/run-parameters/),
[Access authorization cookies](https://developers.cloudflare.com/cloudflare-one/access-controls/applications/http-apps/authorization-cookie/).

## Network configuration

- `PRAIMATE_REMOTE_LISTEN`: for example `127.0.0.1:8844` or `0.0.0.0:8844`.
- `PRAIMATE_REMOTE_TLS_CERT` and `PRAIMATE_REMOTE_TLS_KEY`: certificate chain and
  private-key paths. `--tls-cert` / `--tls-key` provide equivalent runner options.
- `PRAIMATE_REMOTE_ORIGINS`: optional comma-separated exact additional origins
  for a separately hosted client. The server's own browser origin is allowed.
- `PRAIMATE_REMOTE_TOKEN`: optional high-entropy administrator token, at least
  32 characters. It is consumed before any child CLIs launch and is not printed.
- `PRAIMATE_DB_PASSWORD` or `PRAIMATE_DB_PASSWORD_FILE`: optional password input;
  consumed before child CLIs launch. Prefer a service credential file for a
  managed background service. Unix credential files must not be group/world
  readable. On Windows protect that file with the account's filesystem ACL.

HTTP is allowed only on a numeric loopback listener. A directly network-bound
listener requires TLS. Clients must trust its certificate/CA. A reverse proxy or
SSH tunnel may connect to the loopback listener; the application does not accept
forwarded headers as authentication or implicitly trust arbitrary browser origins.
When terminating HTTPS at a reverse proxy, add its exact public browser origin
to `PRAIMATE_REMOTE_ORIGINS`, for example `https://praimate.example.com`.
Secrets never go in URL parameters. Every application API, event stream and file
transfer requires a bearer credential. Only discovery, static login assets and
the rate-limited pairing exchange are public.

## Persistent service

The host GUI provides **Server → System startup** for an optional background
service that starts before desktop login. It installs only after the owner
confirms unattended profile unlock. Linux uses systemd user services with
lingering; Windows uses a boot-triggered, password-logon task; macOS uses a
system LaunchDaemon under the profile's account. The same panel shows status,
controls future startup and displays bounded logs. The Android/browser admin
view can inspect status and logs; OS authorization stays on the host.

See [system startup, credentials and recovery](SERVER-STARTUP.md) before enabling.
The desktop listener checkbox controls only the foreground GUI; it does not
register a background service. A background service explicitly starts its saved
listener even if that desktop checkbox is off.

### Manual Linux template

`scripts/server/praimate-server.service` is an optional Linux user-service template
for an installation in `~/.local/bin`. It is also included in server bundles as
`scripts-server/`. Configure its environment and encrypted systemd credential,
then install/enable the unit yourself. The shipped template uses
`LoadCredentialEncrypted` and passes the credential path to the runner, rather
than placing a password in the unit's environment. Consult the installed systemd
version and its [credential documentation](https://www.freedesktop.org/software/systemd/man/latest/systemd.exec.html#Credentials).
A non-secret environment example is included alongside the template.

Perform initial interactive pairing before switching to the service. Existing
paired devices survive that switch and subsequent server restarts. The service
sets `PRAIMATE_SERVER_SUPERVISED=1`: its administrator panel can request a graceful
restart, which exits with code 75 for the supervisor to restart. A normal stop
exits 0. Foreground installations are stopped/restarted from their console; the
administrator API can also stop them gracefully.

The GUI's service manager is separate from this manually named unit. Stop and
disable an older manual service before switching to GUI-managed startup; the
wizard does not take ownership of unrelated OS services. OS definitions and
helper code are validated in tests/builds; native authorization dialogs and full
reboot behavior still require validation on each target OS. Automated tests do
not install or enable persistent services on the host.

A server update replaces its binaries but leaves active jobs running. Restart
explicitly afterwards. It never launches a duplicate owner or tries to replay a
database password from consumed stdin. Desktop update/restart behavior is unchanged.

## API contract (protocol 2)

`GET /v1/discovery` identifies the server and protocol without profile data.
Authenticated `GET /v1/status` returns version, edition, identity and server time.
`GET /v1/methods` returns the permitted catalog and typed positional arguments.
The catalog is an explicit allowlist; arbitrary Wails methods cannot be invoked.

```json
{"method":"api.ListChats","params":[]}
```

Send calls to `POST /v1/call`. Desktop data/execution bindings use `api.<Name>` and
the same positional arguments as `frontend/src/lib/api.js`. The catalog covers
chats, agents, workers, workflows, schedules, watchers, skills, MCP, Local LLM
hosts/models/certificates, usage, settings, privacy, backup, installers, artifacts
and updates. `server.capabilities` describes host-dependent functionality.

Read-only calls return `{ "result": ... }`. Persistent mutations also require
`requestId`, formatted as `<server Unix milliseconds>_<8-64 random URL-safe chars>`.
Use the time offset from `/v1/status` when the client's clock differs. Mutations
return HTTP 202 and an operation record. Poll `GET /v1/operations/<id>` or list
`GET /v1/operations` to recover results after a lost connection.

A repeated ID with the same owner/method/arguments returns the existing operation.
A reused ID with different arguments conflicts. Unknown IDs older than five
minutes, or more than two minutes ahead of server time, are rejected; retained
completed operations remain queryable. The bounded history keeps recent IDs
rather than silently evicting their replay protection. Under pressure it rejects
new operations instead of executing an ambiguous duplicate. Executions belong to
the server lifecycle, not the HTTP connection. After restart, unfinished records
become `interrupted`; they are never executed again automatically. Existing chat
and worker recovery controls remain available for an explicit retry.

Live terminal input and resizing are transient calls and are never automatically
replayed. Terminal control requires a per-connection `X-Praimate-Client` identifier
(1–80 letters, digits, underscores or hyphens), unique to each browser tab/client.
`terminals.claim` takes `[terminalId, force]` and grants a renewable 45-second
controller lease. Administrators may force a takeover; other controllers can
explicitly take over another tab belonging to their own paired device.
Desktop keyboard input takes local control; desktop resize does not fight an
active remote controller. The browser preserves input order, splits large pastes
on base64 boundaries and coalesces rapid resizes. Terminals outlive an event-stream
disconnect.

### Events and documents

`GET /v1/events` is an authenticated SSE stream. Use authenticated fetch streaming,
not a bearer token in an EventSource URL. Events contain `id`, `name`, and `payload`.
Supply the last ID through `?cursor=` or `Last-Event-ID` to replay retained events.
The server keeps a bounded ring (512 events / 8 MiB); an old cursor or new server
epoch emits `resync`. Fetch current snapshots after that notification. Terminal
snapshots/chunks retain byte offsets for deduplication. Slow subscribers disconnect
rather than blocking CLIs. Revoked devices cannot continue receiving events.

Events cover terminal output/exits, chat activity, approvals, workers, workflows,
installers, agent requirements, Assistant progress, artifacts and watched documents.
`api.RemoteActivity` recovers active chat IDs and pending approvals after a
reconnect. A passive window never rejects another client's pending approval;
the existing broker timeout still denies unanswered requests.
`files.watch` monitors a workspace for external CLI edits; subscriptions are bounded
and require administrator permission. The browser's document editor uses these
notifications and optimistic hashes to detect conflicting saves.

`files.list`, `files.tree`, `files.read`, `files.write`, `files.mkdir`, `files.rename`,
and `files.remove` take one object with an absolute server `workspace` and relative
`path`. Reads/previews use base64 and SHA-256 hashes with an 8 MiB preview limit.
Writes require the last `expectedHash` or explicit `create: true`; stale saves are
rejected. Relative traversal and symlink escapes are rejected using `os.Root`.
This is per-workspace path containment, not a restriction on an administrator's
choice of accessible workspace.

Large transfers do not use JSON buffers:

- `PUT /v1/upload?name=<filename>` accepts a raw body up to 512 MiB and returns a
  staged attachment. Partial uploads are removed on failure. Staged files stay in
  the application's private attachments tree so conversation references survive.
- `GET /v1/download?workspace=<absolute-dir>&path=<relative-file>` streams a regular
  file, supports HTTP byte ranges, and downloads it as an attachment. `HEAD` works.
- Agent exports return a temporary download descriptor. These temporary exports
  are removed after 30 minutes or shutdown. Skill export/import retains the same
  reviewed-package and approval checks as desktop.

File transfers require administrator permission. `uploads.put` remains a small
base64 convenience API. Path-based agent import/export/inspection, knowledge
uploads, requirement scripts, skill workspaces and project registration replace
native file dialogs. The browser uses its own picker, downloads and microphone;
voice audio is sent to the existing server transcription service.

Native operating-system windows are not screen-streamed. A remotely opened
project uses PrAImate's browser document/code editor and server-side CLI terminal.
Remote Studio opens without checking/installing a native editor on the server.
`projects.prepare` creates its persistent Studio chat, preserving the selected
CLI, model, agent and tool policy; reopening an existing chat reuses that state.
The external VS Code/VSCodium window itself requires a separate remote editor or
remote-desktop solution; its native GUI is not part of this API. The APK will
provide its own routing and window/picker UI over these server operations.

## Maintaining both branches

Put shared desktop/core fixes in focused commits on `main`, then merge `main` into
`server`. For a shared fix found on `server`, isolate it and cherry-pick only that
commit to `main`. Never merge the entire server branch into `main` for a desktop
fix. Keep transport/authentication in `internal/remote`, optional GUI integration
in server-tagged files, and application state in the existing Core services.

The Android client reuses the browser adapter through an origin-scoped native
transport. See [Android builds and device integration](MOBILE-ANDROID.md) for
packaging, Keystore credentials, private CAs, file transfers and monitoring.
Android artifacts are built separately; server builds do not produce an APK.
