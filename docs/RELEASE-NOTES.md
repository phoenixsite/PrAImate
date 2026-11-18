# PrAImate 1.3.7

**[Install Desktop, Server or Android](https://github.com/sPROFFEs/PrAImate/blob/v1.3.7/docs/INSTALLATION.md)**.

Desktop, Server and the Android preview are **1.3.7**. This release resolves
#50, #51 and #53, incorporating the reviewed contributions from PRs #52 and #54.
Thanks to **phoenixsite** for the updater and Linux icon fixes.

- Let slow, active self-update downloads finish. Downloads now have a 60-second
  inactivity deadline instead of a 60-second total limit. Response headers and
  body data renew the idle budget; stalled downloads are canceled with a clear
  error and partial files are removed. Metadata request limits are preserved.
- Restore the Linux application-menu icon after installation from a release
  archive. Keep the extracted icon available when creating shortcuts, copy it to
  the user's icon locations, and always write the themed `Icon=praimate` entry.
- Support personal extensions in native Studio through **Editor environment →
  Extensions folder → Save and refresh**, or
  `praimate studio extensions /absolute/path/to/extensions`. Existing plugins
  are preserved; only PrAImate's integration is provisioned in the selected folder.
- Keep Studio settings and history private with a separate editor data profile
  for each extensions folder. New windows use the saved choice without reusing
  an editor process configured for another folder. **Use managed folder** or
  `praimate studio extensions --managed` restores the original default.
- Refresh standalone Studio launchers after changing the folder. Serialize
  profile changes and treat quotes and shell metacharacters in paths literally.
  Missing folders and corrupt preferences produce recoverable errors.
- Retain the Studio/Workers improvements from 1.3.6, the local LLM certificate
  transport fixes and edition-safe updates.

**If an older version's updater stops a slow download after 60 seconds**, use the
[installer or manually download the archive](https://github.com/sPROFFEs/PrAImate/blob/v1.3.7/docs/INSTALLATION.md)
for this update. The running old updater still has its original timeout until
its binary is replaced.

The bundled Studio extension remains **0.7.4**. Restart PrAImate after updating.
The combined release includes the Android **1.3.7** preview APK, also available
through the client's update checker.

Validation includes reproducible updater/icon regressions, Go tests and race
checks, offline installer tests for both editions, 27 Studio frontend tests per
branch, production frontend builds, GUI package checks and Windows Studio test
cross-compilation. Firefox checks use the real Svelte DOM with a synthetic native
bridge in both themes at mobile/desktop widths. Native editor tests use a fixture
launcher; actual VS Code/VSCodium and target-device smoke tests remain pending.

Application archives contain their edition's binaries. Shared standalone tools
are published once per platform. The two `praimate-desktop-windows-*` ZIPs are
byte-identical compatibility aliases for old Desktop updaters. `SHA256SUMS`
covers every artifact; `ANDROID-VERSION` records the independent APK version.
