# Install Desktop, Server or Android

Choose your product before downloading from the [combined release](https://github.com/sPROFFEs/PrAImate/releases/tag/v1.2.28):

| Product | Install it on | What it does |
|---|---|---|
| **Desktop** | Your workstation | Local GUI, CLIs, chats, Studio, agents and workers. No remote server module. |
| **Server** | The computer or VM that will run your work | Includes the GUI and a headless remote service. Browsers and Android control the same server-owned workspace. |
| **Android preview** | Your phone or tablet | Connects to Server. Models, CLI processes, project files and jobs execute on the server. |

Desktop and Server support Linux amd64, Windows amd64/arm64, and macOS
Intel/Apple Silicon. Linux arm64 requires a source build on an appropriate native
host. Android requires Android 8.0+ and a current Android System WebView; its
isolated Cloudflare Access login requires Android 9+.

## Desktop

### Linux or macOS

```sh
curl -fsSL https://raw.githubusercontent.com/sPROFFEs/PrAImate/server/scripts/install.sh | bash -s -- --binary --edition desktop --user
praimate
```

### Windows PowerShell

Download the installer before passing arguments:

```powershell
Invoke-WebRequest -UseBasicParsing https://raw.githubusercontent.com/sPROFFEs/PrAImate/server/scripts/install.ps1 -OutFile "$env:TEMP\praimate-install.ps1"
& "$env:TEMP\praimate-install.ps1" -Mode Binary -Edition Desktop
praimate
```

On first launch, create or unlock the encrypted database, then configure your
CLI accounts or Local LLM endpoints. See the [user guide](GUIDE.md).

## Server

Install this edition on the host that will own the projects, CLIs and database.
The server must remain running for remote clients to connect.

### Linux or macOS

```sh
curl -fsSL https://raw.githubusercontent.com/sPROFFEs/PrAImate/server/scripts/install.sh | bash -s -- --binary --edition server --user
```

### Windows PowerShell

```powershell
Invoke-WebRequest -UseBasicParsing https://raw.githubusercontent.com/sPROFFEs/PrAImate/server/scripts/install.ps1 -OutFile "$env:TEMP\praimate-install.ps1"
& "$env:TEMP\praimate-install.ps1" -Mode Binary -Edition Server
```

The edition-aware installer is intentionally fetched from `server` for both
products. `--edition` / `-Edition` selects the binary package; the desktop-only
`main` branch remains separate. Installing another edition into the same prefix
replaces its binaries. To keep both on one machine, use the
[separate prefixes and profiles](#keep-both-editions-on-one-computer) below.

### Start with the GUI

After installing Server, `praimate` opens its GUI with a **SERVER** edition label
and a **Server** entry in the sidebar. No environment variables are required:

1. Open **Server** (or **Settings → Configure Server**).
2. Set the listen address, normally `127.0.0.1:8844` for Cloudflare Tunnel.
   For a network listener, also supply TLS certificate and private-key file paths
   on the Server host. Optionally enable automatic startup after database unlock.
3. Choose **Save Server settings**, then **Start remote access**. Saving settings
   alone does not start or restart the listener.
4. Configure **Cloudflare Tunnel** on the same page, or use your direct HTTPS
   endpoint. Under **Pairing and devices**, create a code for the Android app.

The page displays the active local origin and public hostname, supports listener
restart/stop, and lists paired devices with access revocation. **Stop access**
leaves local chats, workers, and terminals running. Changing a running listener
requires **Apply settings / restart** and disconnects remote clients.

For scripted startup, flags and environment variables remain supported and take
precedence over saved settings:

```sh
PRAIMATE_REMOTE_LISTEN=127.0.0.1:8844 praimate
```

```powershell
$env:PRAIMATE_REMOTE_LISTEN = "127.0.0.1:8844"
praimate
```

Unlock or initialize the database before starting access. The Desktop edition
does not include the Server page; check **About → Edition** if it is missing.

### Start without a display

For a **new** server profile:

```sh
praimate server --initialize-db --listen 127.0.0.1:8844 --db-password-stdin --pair
```

For an **existing** profile, omit `--initialize-db`:

```sh
praimate server --listen 127.0.0.1:8844 --db-password-stdin --pair
```

These commands also work in PowerShell. Terminal password input is hidden.
The console prints a one-use, eight-digit administrator pairing code, valid
for five minutes. Open `http://127.0.0.1:8844` on the server itself, or use the
configured public HTTPS origin from another device. Linux headless installations
still need the GTK/WebKitGTK runtime dependencies used by the server binary.

### Expose Server over HTTPS

Choose one connection method:

- **Cloudflare Tunnel:** create a remotely managed tunnel and public hostname
  in Cloudflare. On Server, save that HTTPS origin and connector token in
  **Server → Cloudflare Tunnel**, then choose **Install and start connector**.
  Start remote access first. Point the Cloudflare
  hostname at the local service URL shown by PrAImate. Optional Cloudflare Access
  adds email/PIN login before the separate PrAImate device pairing.
- **Direct TLS or a reverse proxy:** configure the certificate/key and, when
  using a proxy, the exact public browser origin. See [Server network configuration](REMOTE-SERVER.md#network-configuration).

`127.0.0.1` refers to the device using it. A phone cannot connect to the server's
loopback address over the network; use its public HTTPS origin. The
[Android guide](MOBILE-ANDROID.md#connect) also describes USB loopback forwarding.

Installing Server does not automatically install a persistent PrAImate service.
The optional Cloudflare connector service is separate from the PrAImate Server
process; both must run for tunnel access. See [persistent server setup](REMOTE-SERVER.md#persistent-service)
and [Cloudflare setup](REMOTE-SERVER.md#cloudflare-tunnel-and-optional-access).

## Android preview

1. Install and start **Server**. A Desktop-only installation cannot serve Android.
2. Download **[praimate-mobile-preview.apk](https://github.com/sPROFFEs/PrAImate/releases/download/v1.2.28/praimate-mobile-preview.apk)**
   onto the device and open it. Android may ask you to allow installation from
   your browser or file manager.
3. Add the server's HTTPS origin. Complete Cloudflare Access login first if
   enabled, then enter the separate PrAImate pairing code.
4. Open the server's chats, Studio, workers, terminals and files. Closing Android
   does not cancel server jobs; stopping Server does.

The preview uses `com.praimate.mobile.debug` and a persistent preview signer.
Installing a newer compatible APK over an existing preview preserves its
encrypted profiles; uninstalling it removes local app data.

Use **Check for app updates** in the server chooser or **Check Android app
updates** in the connection panel. Automatic checking when the app opens is
optional and limited to once daily. The client queries GitHub over HTTPS,
verifies checksum, package, version code and signer, then opens Android's installer.
Android's update-install permission is separate from the browser/file manager
permission used for the first installation.

See [Android connections, certificate trust and updates](MOBILE-ANDROID.md).
Native installation, permissions and a real Cloudflare Access flow still need
device/account validation; automated checks are described in that guide.

## Choose a release asset

The [1.2.28 release](https://github.com/sPROFFEs/PrAImate/releases/tag/v1.2.28)
contains both editions. For newer versions, select a compatible entry in
[Releases](https://github.com/sPROFFEs/PrAImate/releases).

| System | Desktop | Server |
|---|---|---|
| Linux amd64 | `praimate-linux-amd64.tar.gz` | `praimate-server-linux-amd64.tar.gz` |
| Windows amd64 | `praimate-windows-amd64.zip` | `praimate-server-windows-amd64.zip` |
| Windows arm64 | `praimate-windows-arm64.zip` | `praimate-server-windows-arm64.zip` |
| macOS Intel | `praimate-darwin-amd64.tar.gz` | `praimate-server-darwin-amd64.tar.gz` |
| macOS Apple Silicon | `praimate-darwin-arm64.tar.gz` | `praimate-server-darwin-arm64.tar.gz` |

`praimate-mobile-preview.apk` is the Android client. `praimate-cli-*`,
`praimate-code-*` and `praimate-graphify-*` are shared standalone tools, not
replacement application packages. App archives are self-contained; shared
standalone assets are published once per platform. `SHA256SUMS` covers the assets.

### Install an extracted archive

Extract into its own directory and open its platform subdirectory, such as
`linux-amd64`, `windows-amd64` or `darwin-arm64`. Run the matching installer:

```sh
# Choose the edition matching the extracted archive:
./scripts/install.sh --edition desktop --user
# For a Server archive, use instead:
./scripts/install.sh --edition server --user
```

```powershell
.\scripts\install.ps1 -Edition Desktop
# For a Server archive, use instead:
.\scripts\install.ps1 -Edition Server
```

Omitting `--binary` / `-Mode Binary` here uses the extracted local binaries. The
installer checks `praimate-edition` and rejects a mismatched edition. Do not
extract both editions into the same directory: their internal executable names
are shared.

## Keep both editions on one computer

Both editions use `praimate`, the same default installation directory and the
same default application-data root. Keep them in different prefixes **and** use
different `PRAIMATE_HOME` profiles. For example, download the Unix installer from
the URL above to `install.sh`, then run:

```sh
bash ./install.sh --binary --edition desktop --prefix "$HOME/.local/opt/praimate-desktop/bin"
bash ./install.sh --binary --edition server --prefix "$HOME/.local/opt/praimate-server/bin"

PRAIMATE_HOME="$HOME/.local/share/praimate-desktop" "$HOME/.local/opt/praimate-desktop/bin/praimate" &
PRAIMATE_HOME="$HOME/.local/share/praimate-server" "$HOME/.local/opt/praimate-server/bin/praimate" server --initialize-db --listen 127.0.0.1:8844 --db-password-stdin --pair
```

For Windows, use the downloaded PowerShell installer:

```powershell
& "$env:TEMP\praimate-install.ps1" -Mode Binary -Edition Desktop -Prefix "$env:LOCALAPPDATA\Programs\PrAImateDesktop"
& "$env:TEMP\praimate-install.ps1" -Mode Binary -Edition Server -Prefix "$env:LOCALAPPDATA\Programs\PrAImateServer"

$env:PRAIMATE_HOME = "$env:LOCALAPPDATA\PrAImateDesktopProfile"
Start-Process -FilePath "$env:LOCALAPPDATA\Programs\PrAImateDesktop\praimate.exe"
$env:PRAIMATE_HOME = "$env:LOCALAPPDATA\PrAImateServerProfile"
& "$env:LOCALAPPDATA\Programs\PrAImateServer\praimate.exe" server --initialize-db --listen 127.0.0.1:8844 --db-password-stdin --pair
```

Use `--initialize-db` only on the first start of a new profile. Launch with full
paths when keeping both editions: PATH and shared shortcut names can resolve to
the most recently installed edition. Set the profile for every launch, including
services and updates. Run one owner per profile. External CLI account settings
can still be shared by the operating-system user.

## Updates, source builds and removal

Use `praimate -check-update` / `praimate -update` from the installation you want
to update. With separate editions, use its full path and profile. Updates select
the matching archive prefix. Server accepts combined releases containing Server
assets and legacy `server-v*` releases, skipping Desktop-only releases. Restart
Server explicitly after an update when its active work can be interrupted.

Installers also support `--source` / `-Mode Source`, system-wide installation and
explicit prefixes. A fresh source clone selects `main` for Desktop and `server`
for Server; inside a checkout, the installer builds that checkout with the chosen
edition's tags. See [Server development](REMOTE-SERVER.md#install-and-build) and
the [release/build guide](RELEASE-GITHUB.md).

Use the matching installer's `--uninstall` / `-Uninstall` with the correct prefix
for normal removal; it retains data. Do not use `--purge` / `-Purge` merely to
remove one side-by-side edition: purge targets default user data locations,
not the chosen binary prefix or an isolated `PRAIMATE_HOME`. Custom profiles are
managed separately. Remove a configured Cloudflare connector through Server
settings before uninstalling its server installation.
