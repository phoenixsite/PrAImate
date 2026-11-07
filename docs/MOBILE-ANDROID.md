# PrAImate for Android

The Android client belongs to the `server` branch. It embeds the same Svelte
application as desktop, with a mobile navigation shell, touch terminal controls
and a native transport. Models, CLI processes, agents, workers, application state
and documents are owned by the selected PrAImate Server. Closing Android does not
cancel server jobs. Native VS Code/VSCodium windows are not screen-streamed.

## Install the published APK

Install **[praimate-mobile-preview.apk](https://github.com/sPROFFEs/PrAImate/releases/download/v1.3.0/praimate-mobile-preview.apk)**
from the combined release. You do not need the Android SDK or a source build to
use it. Android may ask you to allow installation from your browser/file manager.
The APK requires Android 8.0+ and a current Android System WebView; isolated
Cloudflare Access login requires Android 9+.

Install and start the [Server edition](INSTALLATION.md#server) on the host first.
Add its HTTPS origin in the app, complete Cloudflare Access login if enabled,
then enter the separate PrAImate pairing code. The Desktop-only edition cannot
serve the APK. See the [complete installation guide](INSTALLATION.md) for the
host commands and [Connect](#connect) for certificates and profile management.

The published preview uses `com.praimate.mobile.debug` and a persistent signing
key. Install a newer compatible APK over the old one to preserve local profiles;
uninstalling the app removes them. App updates are available from the server
chooser and the connected client's connection panel; see [Android updates](#android-updates).

## Build from source

Build from the `server` branch, which contains `mobile/android` and the script below.

Prerequisites: JDK 17+, Gradle 8.13, Android SDK platform 36/build tools, Node/npm
and the frontend dependencies. Tool installation is explicit; the build script
does not install system packages. Gradle resolves pinned Android dependencies.

```sh
export JAVA_HOME=/path/to/jdk-17
export ANDROID_HOME=/path/to/android-sdk
export PRAIMATE_ANDROID_GRADLE=/path/to/gradle-8.13/bin/gradle
bash scripts/build-android.sh
```

The debug APK is `dist/android/praimate-mobile-debug.apk`. It is installable for
local testing and uses the separate package `com.praimate.mobile.debug`.
`--release` produces an **unsigned** release APK. Sign it with your own protected
Android signing key; keys/passwords do not belong in the repository. The combined
release workflow automatically publishes the signed **preview** APK using its
persistent CI signer. It does not publish the local unsigned release APK.

The Android Studio project is `mobile/android`. Android 8.0+ and a current Android
System WebView are required. No native model libraries/NDK are bundled.

## Connect

1. Start the [server edition](REMOTE-SERVER.md) with TLS, or use loopback through
   an explicitly configured tunnel. The debug APK also supports the emulator's
   `http://10.0.2.2` host alias.
2. Add an HTTPS server origin in the APK, including its port when needed. Up to
   20 encrypted server profiles are supported. Reverse proxies expose the API at
   the origin's `/v1/` paths.
3. On the Server host, open **Server → Pairing and devices**, enter a device name
   and choose **Full administration** for full control (or a more restricted
   role). Click **Create pairing code** and enter its eight digits in the APK,
   including any leading zeroes. Each code is single use and valid for five
   minutes. A headless host can print an administrator code with
   `praimate server --pair`. Complete Cloudflare Access email sign-in first if
   enabled; it has its own separate verification code.
4. Use the same chats, workers, terminals, settings and documents as other clients
   of that server profile. Project/session windows use Android's activity back stack.

Administrator-token login is optional. The host owner supplies a high-entropy
value of at least 32 characters through `PRAIMATE_REMOTE_TOKEN` before starting
the Server. The GUI does not automatically create this token. A pairing code
with **Full administration** grants administrator access without configuring a
bootstrap token. The database password, Cloudflare connector token and
Cloudflare Access email code are separate credentials.

Open a saved server's options (the ellipsis button, or long-press) to import its
PEM/DER private CA/server certificate.
Review the displayed SHA-256 fingerprint before trusting it. Trust applies only
to that server profile; certificate expiry and hostname verification remain
enabled. Removing a local pairing does not revoke the server-side token: revoke
it from an administrator's device panel when appropriate.

Server options also allow renaming or changing an address. A changed origin clears
the pairing, certificate exception and saved view. Activities and delayed pairing
responses from the previous origin cannot use the replacement server's credential.
Forgetting a pairing or changing its credentials/trust closes other Android views
for that profile and stops its monitor. A pairing response already in flight
cannot restore a credential that the user has forgotten.

## Device integration

- Device credentials and server profiles are encrypted with AES-GCM using an
  Android Keystore key. Native HTTP injects the bearer token; JavaScript receives
  only a session marker. Profiles/keys are excluded from Android backup/transfer.
- Only the APK's packaged main frame receives the native message bridge. Remote
  HTML is never loaded as executable application code. External links open in
  the system browser. HTTP redirects never forward credentials.
- Native HTTP streaming includes backpressure and cancellation. Persistent
  mutation IDs recover accepted server operations without submitting them again.
  Terminal input is ordered and never automatically replayed after ambiguity.
  Request bursts are queued below the native connection limit. Reloaded pages
  use distinct request IDs and cancel the previous page's transfers.
- File uploads and exports use Android's Storage Access Framework. Large files
  stream outside the WebView; uploads are bounded to 512 MiB. No broad storage
  permission is requested. The microphone permission is requested on first use.
- Optional background monitoring is started by the user from the connection
  panel and displays an ongoing notification with a stop action. Task/approval
  notifications contain generic activity text rather than prompts or outputs.
  Monitoring stops when access is revoked or the OS enforces its service limit.
  Android 15+ limits background `dataSync` services; server execution continues
  independently. This is not an unlimited background-service guarantee.

## Mobile interface

The navigation drawer preserves desktop's sections, names, icons and theme
settings. Chats, worker runs and configuration use the shared application views.
Studio and agent authoring switch between files, editor and assistant panels
instead of shrinking three desktop columns onto a phone. Project windows open
as separate Android activities; Back returns to the previous workspace. Terminal
sessions include an extra row for Escape, Tab, control keys and arrows.

Each server remembers its last page and selected conversation, worker, terminal
or workspace. Closed terminals are not restarted automatically. Returning to the
foreground refreshes server state and monitoring status. Back dismisses dialogs
before navigating, and Studio saves pending edits before leaving; failed saves
require explicit confirmation to discard. Agent authoring also confirms unsaved
changes. A renderer or bootstrap failure offers recovery without stopping server
tasks. Drafts that have not been saved are not guaranteed to survive OS process death.
Remote Studio restores active-turn state, saved replies and pending approvals
after reconnection. An unrelated Studio window never decides another chat or
worker's permission request.

For a local USB-connected device test without exposing a plaintext network
listener, forward the server's loopback port with `adb reverse tcp:8844 tcp:8844`
and add `http://127.0.0.1:8844` in the APK. Network connections require HTTPS.

## Cloudflare Access

Add the public tunnel hostname as an ordinary HTTPS server. If its discovery
request encounters Cloudflare Access, Android offers **Sign in** and opens the
real Cloudflare challenge in an isolated login activity. Enter your email and
verification code there (or use the identity provider configured by the server
owner). The app verifies that the authorized endpoint is a compatible PrAImate
Server before saving the session, then continues to PrAImate's device pairing.
Cloudflare authentication does not replace the PrAImate pairing code.

The login WebView runs in a separate Android process and data directory, with no
PrAImate native bridge, device bearer, file picker or microphone access. Only
HTTPS navigation is allowed and certificate errors are never bypassed. Its
cookies and web storage are cleared when starting and closing login. Only the
selected application's `CF_Authorization`, `CF_Binding` and `CF_AppSession`
cookies are transferred to the native client, encrypted in the profile vault
alongside its user agent. They are never passed to application JavaScript.
Native requests never follow redirects with the PrAImate bearer. Profile origin
changes and **Forget Cloudflare Access sign-in** clear the session; a delayed
login cannot restore a forgotten session or move it to another server.

When Access expires, the foreground app offers sign-in again; the connection
panel and server options also offer **Cloudflare Access sign-in**. Declining does
not cause a repeating prompt. Background monitoring instead stops and posts a
notification asking you to open the app. Re-authenticate and enable monitoring
again if desired. Interrupted commands are not replayed automatically. Server
work continues independently. In a browser, **Reconnect / sign in** reloads the
page so Cloudflare can present its challenge before restoring the app session.

The isolated Android login requires **Android 9+**; direct server connections
still support Android 8+. Cloudflare account policies, supported identity-provider
flows and a real email/PIN exchange require validation against the owner's
configured tunnel; the local fixture tests do not prove those external flows.

## Android updates

Use **Check for app updates** on the native server chooser, or **Check Android
app updates** in the connected client's connection panel. The optional
**Check for updates when app opens** setting performs at most one automatic
check per day; repeated manual checks reuse a one-minute metadata cache. Checks
use the public repository's HTTPS release API, independently of the connected
server. No PrAImate token, Access cookie or GitHub account token is sent. The
client selects releases containing its own APK type, so Desktop-only releases
do not become incompatible Android updates. An HTTPS release redirect and
checksum manifest provide a fallback when the API is unavailable.

Updates are downloaded to private app cache, bounded to 64 MiB, and checked
against SHA-256, the installed package name, a newer Android version code and
the existing signing certificate. Android then requests permission to install
updates from PrAImate and presents its package installer. Installation is never
silent; server jobs keep running independently. A changed signing key is rejected
without replacing the installed app or its encrypted profiles.

The GitHub preview asset is `praimate-mobile-preview.apk`, using the existing
`com.praimate.mobile.debug` ID and a stable private preview signer. Future CI
builds must reuse that signer. The key is an encrypted GitHub Actions secret,
never source code or a release asset. Production signing/application ID are a
separate distribution; an APK with another ID or signer cannot replace a preview.

## Verification

`npm run test:mobile` tests native transport queuing, cancellation, reply isolation,
dialog dismissal and navigation parity. `npm run test:remote` also covers session
restoration and all desktop routes. Gradle unit tests
exercise endpoint/routing restrictions and Cloudflare cookie/challenge parsing.
`scripts/build-android.sh` runs unit tests,
Android lint and APK assembly. With a connected device/emulator, run
`gradle -p mobile/android connectedDebugAndroidTest` for the Keystore persistence
tests. Instrumentation covers encrypted profile persistence, origin changes,
Access-session isolation and stale-login rejection, and
an actual WebView → native pairing → authenticated HTTP exchange against an
isolated loopback fixture. These instrumented tests must complete on a working
Android device/emulator; compiling them alone is not a passing device test.
Device permissions, TLS with a real private CA, file picker transfers,
keyboard/terminal input, background service restrictions and phone/tablet layouts
also require real-device checks before a production release.

References: [Android Keystore](https://developer.android.com/privacy-and-security/keystore),
[origin-scoped WebView messaging](https://developer.android.com/reference/androidx/webkit/WebViewCompat#addWebMessageListener(android.webkit.WebView,java.lang.String,java.util.Set,androidx.webkit.WebViewCompat.WebMessageListener)),
[foreground service limits](https://developer.android.com/develop/background-work/services/fgs/timeout).
