# Start PrAImate Server with the system

The **Server edition**, from 1.3.2, can register an optional background service
for its current profile. It starts the headless Core when the machine boots,
including before desktop login. Desktop-only builds do not install a listener
or background service.

## Configure from the host GUI

1. Install Server in a stable location, open it and unlock the intended profile.
   Save its connection settings in **Server**. Use a fixed listener port for a
   Cloudflare route, normally `127.0.0.1:8844`.
2. In **Server → System startup**, select **Set up system startup**.
3. Enter the current database password and explicitly allow unattended unlock.
   On Windows, also enter the password for the same Windows account, not a PIN
   or Windows Hello credential. This is needed for a background logon with
   network access for model CLIs.
4. If using Cloudflare, select **Run the configured Cloudflare Tunnel with the
   background Server**. Remove a previously installed separate connector service
   first using the Cloudflare controls below; its encrypted tunnel settings are
   retained. The managed connector does not depend on an interactive user login.
5. Choose **Enable system startup** and approve any OS authorization dialog.
   Installation enables future startup and leaves the current workspace running.
6. Reboot when convenient, or choose **Start background service**. Starting it
   from the foreground GUI asks for confirmation, closes that window and stops
   its foreground terminals/tasks before handing over the profile. It does not
   migrate live processes. Reopen PrAImate to connect to the background workspace.

The existing **Start remote access when the desktop app opens and unlocks this
profile** checkbox controls the foreground GUI only. Registering or disabling
system startup is a separate operation. An explicitly started background service
uses the saved listener even if that desktop checkbox is off.

## Platform behavior

| Platform | Supervisor | Authorization |
|---|---|---|
| Linux with systemd | Per-user unit, enabled under `default.target`, with account lingering | `loginctl` authorization; a polkit dialog may be needed to enable lingering |
| Windows | Hidden boot-triggered Task Scheduler task, password logon, least privilege | Approve UAC using the same Windows account; its password is passed once to Windows Task Scheduler |
| macOS | System LaunchDaemon in `/Library/LaunchDaemons`, with `UserName` set to the profile owner | Administrator authorization through the OS dialog |

Services use the same account, database and persisted CLI configuration as the
host GUI. Each profile has its own service name. Linux uninstall does not disable
account lingering, because other user services may depend on it. Windows polling
and helper commands hide their console windows.

The program and profile must be accessible at boot. This feature does not unlock
encrypted volumes or third-party CLI credential vaults that require interactive
login. Configure those CLIs for noninteractive authentication where supported.
Shell-only API key exports are not copied into the generated service. The wizard
preserves the profile location and supported XDG/Codex/Claude configuration paths.

CLI discovery also checks stable NVM and FNM installations, including
`~/.config/nvm`, `~/.nvm` and the FNM data directory. It respects an active
manager runtime, then the manager's default alias, with numeric version ordering
as a fallback. Custom `NVM_DIR`, `FNM_DIR`, `NVM_HOME` and `NVM_SYMLINK` roots are
preserved when configuring startup. Setup also records the selected Node binary
directory after resolving symlinks, so the service keeps the host's runtime when
several managers are installed. Update startup configuration to select another
runtime. Per-terminal FNM links are not saved.
Global npm updates of a detected version-managed CLI use its own Node runtime
and installation prefix, so updating it does not silently install into another
Node version. PrAImate does not elevate an npm update to write root-owned packages.

## Credentials and data

Unattended startup deliberately allows this OS account to unlock the profile
without entering the database password after every reboot. The database and its
password-protected key envelope remain encrypted; the password is unchanged.

- Windows protects the database service credential with user-scoped DPAPI and an
  account/System-only ACL. The Windows account password is used only by Task
  Scheduler registration. PrAImate retains no copy after its transient encrypted
  authorization request is removed.
- Linux and macOS store the database password in a **separate owner-readable
  service credential file** with mode `0600` inside a `0700` private directory.
  This file is not additionally encrypted at rest. Enable startup only if this
  local unattended-unlock arrangement is appropriate for the host.
- Startup files live beside the selected database in `<database>.startup/`.
  This private directory excludes its contents from Git; the application state
  backup exports the encrypted database/key envelope, not startup credentials.
  Do not manually publish, sync or add this directory to a knowledge source.
- Changing the database password through PrAImate updates an installed startup
  credential too. Check any warning if this update could not complete.
- **Remove startup** revokes the local service credential and removes the OS
  registration. It preserves the database, conversations and diagnostic logs.
  **Disable system startup** preserves configuration and does not stop an already
  running service. **Stop** leaves future startup enabled.

After upgrading from 1.3.2, reopen the updated Server app and choose **Update
startup configuration** once. This regenerates the Linux unit and saves your
stable runtime paths. Then restart the background service, or start it if it was
stopped. Existing Cloudflare configuration and device pairing are retained.

The manual [Linux service template](https://github.com/sPROFFEs/PrAImate/blob/v1.3.3/scripts/server/praimate-server.service)
remains available for administrators who prefer systemd encrypted credentials
with `LoadCredentialEncrypted` and their own service configuration.

## GUI, remote devices and recovery

When the managed service owns the profile, the host GUI connects to its existing
Core instead of opening a second database owner or starting duplicate schedules.
Closing that client window leaves the background Server, workers and terminals
running. The local owner capability stays in Go and a private IPC descriptor; it
is not put in JavaScript, a launch URL or a device pairing record.

The host GUI uses the configured Server listener. Stopping remote access
disconnects this client too, while Core continues running. Host startup controls
and logs use the native bridge independently and can restart the background
service. Listener address changes refresh the local connection descriptor.
Accepted API operations and paired-device state are retained across listener
restarts.

If the background connection fails, the desktop shows a recovery screen with
service controls and logs. Stop the service first, then choose **Open foreground
mode** to unlock/configure the profile locally, or **Retry connection** after
repairing its service. A profile lease prevents two owners opening it together.
The Android/browser administrator view can inspect startup state and logs;
installation and OS lifecycle changes require the host desktop app.

The panel shows the configured executable and service name. Logs rotate at
4 MiB with one previous file; **View logs** returns a bounded tail. On Windows,
also inspect the task's last result/Event Viewer after an account-password or
batch-logon-policy error. A supervised Windows retry may take up to a minute.

Stop and disable an older manually configured service before switching to the
GUI wizard. The wizard does not replace registrations it does not own. Before
rolling back to an earlier Server version, remove this startup registration and
configure it again with that version: older binaries may not recognize newer
managed-startup flags or saved environment keys. No database schema or
key-envelope migration is required for this feature.

Automated checks validate service definitions, Windows DPAPI, helper isolation,
credentials, bounded transports and real headless Core lifecycle. Tests do not
enable persistent OS services. Native authorization dialogs and full reboot
behavior still need validation on each target system.
