# PrAImate 1.3.5

**[Install Desktop, Server or Android](https://github.com/sPROFFEs/PrAImate/blob/v1.3.5/docs/INSTALLATION.md)**.

Desktop and Server are **1.3.5**. This release fixes certificate trust for local
LLM providers in PrAImate Code and external OpenCode. The Android preview remains
**1.3.5**; existing Android 1.3.5 installations do not need an update for this
server-side fix.

- Route accepted HTTPS providers through an authenticated, loopback-only
  transport in the core. Model discovery, praimate-cli, PrAImate Code and OpenCode
  use the same TLS verifier instead of depending on the CLI runtime's handling of
  partial certificate chains.
- Reuse existing accepted leaf certificates, including legacy trust records and
  private endpoints that omit issuing certificates. A manual root CA import is
  optional. Hostname, expiry and exact accepted-leaf checks remain enabled;
  explicitly imported CAs retain their normal chain-validation behavior.
- Preserve model IDs, provider paths, credentials, custom headers, plugins,
  streaming and resumed sessions. Ephemeral routing does not rewrite saved
  project provider URLs. The local authentication header never reaches the LLM
  server, and redirects are blocked.
- Check current trust on every forwarded request. Revoking trust blocks further
  requests from already running clients. Close the listener on app exit or data
  reset.
- Apply the shared changes to both Desktop (`main`) and Server (`server`). Keep
  edition-safe updates and the Windows Desktop compatibility archives from 1.3.4.

**After updating, restart PrAImate and reopen existing CLI terminals** so they
receive the new transport configuration. Existing certificate consent is reused.
To trust a private CA across leaf renewals, use **Private certificate authority**
in Local LLM settings instead of accepting individual leaf certificates.

Validation includes six real HTTPS scenarios with PrAImate Code and official
OpenCode, first and resumed completions, Bun 1.3.14, authenticated streaming,
revocation, redirects, provider configuration preservation, race checks and real
Code PTYs. Core, host TLS, local-provider and CLI tests cover both editions;
Windows core tests cross-compile. Target-device validation is still required.

Application archives contain their edition's binaries. Shared standalone tools
are published once per platform. The two `praimate-desktop-windows-*` ZIPs are
byte-identical compatibility aliases for old Desktop updaters. `SHA256SUMS`
covers every artifact; `ANDROID-VERSION` records the independent APK version.
