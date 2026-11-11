# PrAImate 1.3.4

**[Install Desktop, Server or Android](https://github.com/sPROFFEs/PrAImate/blob/v1.3.4/docs/INSTALLATION.md)**.

This combined release fixes edition changes during updates and includes the
reviewed Workers, Studio and Documents fixes. Desktop and Server are **1.3.4**;
the signed Android preview is **1.3.5**, newer than the previous APK-only update.

- Select the exact Desktop or Server archive for the current platform. Validate
  its internal edition marker and the installation's marker before replacing any
  executable. Legacy unmarked archives are Desktop-only.
- Preserve the installed edition when re-running either installer unless an
  edition is explicitly selected. Upload Desktop archives before Server archives
  for compatibility with older Desktop selectors.
- Keep completed worker results in Done while other tasks execute, then move
  pending reviews to Needs you. Explain when Accept/Reject become available in
  Desktop and Studio. Resolves #26; incorporates reviewed PR #27.
- Preserve sidebar navigation across VS Code/Codium view recreation. Dedicated
  worker windows no longer consume sidebar requests or navigate away from their
  run. Resolves #28; incorporates and extends reviewed PR #29.
- Keep primary worker buttons readable on hover in light and dark themes, and
  distinguish Studio Stop actions. Resolves #24; incorporates reviewed PR #30.
- Select MCP servers when creating a Documents session, persist IDs before the
  editor opens, show persona defaults and honor an explicitly empty selection.
  Failed selection saves prevent launch. Resolves #25; incorporates reviewed PR
  #31 with functional regression coverage.
- Include these shared interface fixes in Android 1.3.5 while preserving its
  application ID, local profiles and persistent preview signer.

**If an older updater already installed the wrong edition:** reinstall the
intended edition explicitly with `--edition desktop` / `--edition server`, or
PowerShell's `-Edition Desktop` / `-Edition Server`, into the original prefix.
Do not use purge. A new marker records the intended edition for future updates.
Use separate installation directories and profiles when keeping both editions.

Verification covers both branches: Core, Workers, Skills, installers and GUI
regressions; updater race checks; Windows updater cross-compilation; PowerShell
edition selection; 42 Studio extension tests; browser hover and Documents MCP
creation in both themes; and frontend production builds. Native OS startup and
end-user update flows still need target-device testing.

Application archives are self-contained. Shared CLI tools are published once per
platform, and `SHA256SUMS` covers every published artifact. `ANDROID-VERSION`
records the independent APK version. Desktop remains on `main`; Server services
remain on `server`.
