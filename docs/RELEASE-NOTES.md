# PrAImate 1.3.6

**[Install Desktop, Server or Android](https://github.com/sPROFFEs/PrAImate/blob/v1.3.6/docs/INSTALLATION.md)**.

Desktop, Server and the Android preview are **1.3.6**. This release integrates the
reviewed Studio and Workers contributions from PRs #33, #35, #37, #38, #40, #42,
#43, #45, #47 and #49, with additional regression fixes for both editions.
Thanks to **phoenixsite** for these contributions.

- Show Studio model suggestions for the CLI selected in session settings before
  Save. Ignore stale responses, including out-of-order refreshes of the same CLI,
  and preserve unsaved settings during background updates.
- Preserve the Include selection / active file preference across webview reloads.
  Add a persistent Hide/Show activity control. When a followed assistant turn
  finishes, show the start of its reply without interrupting older-message reading.
- Open saved Worker results with **Compare in editor** in Studio. Use the run's
  repository when its worktree has been cleaned, validate paths and commit refs,
  and include all changed files in the editor comparison.
- Make Worker review actions visible above the board, with awaiting, accepted and
  rejected counts, a clear Merge action, and distinct Accept/Reject buttons. Keep
  the summary visible when every result is rejected. Prevent a delayed merge
  confirmation from applying to a different run.
- Reveal and focus the plan when Review plan or Route options is chosen. Respect
  reduced-motion settings and keep automatic plan opening from forcing a scroll.
- Keep Changes/Result selected when switching between completed tasks. Show
  Rejected explicitly on rejected task cards. Preserve Studio board scroll,
  search focus and caret while worker output streams.
- Open Desktop web and mail links through the local system browser with URL
  validation and error feedback. Managed Server windows use their local opener;
  remote browsers and Android keep their own link handling.
- Retain the 1.3.5 local LLM certificate transport fixes, edition-safe updates and
  Windows Desktop compatibility archives.

The bundled Studio extension is **0.7.4**. Restart PrAImate and reopen Studio after
updating to load the new extension. The Android **1.3.6** APK includes the shared
Worker interface changes and is available through the client's update checker.

Validation includes extension host/webview and frontend regressions on both
branches, Go Studio and URL checks, production frontend builds, and real Firefox
DOM checks using synthetic backend data. Server remote/mobile transport tests
also pass. Native OS browser launch, the editor's diff UI and target-device
behavior still require a manual smoke test.

Application archives contain their edition's binaries. Shared standalone tools
are published once per platform. The two `praimate-desktop-windows-*` ZIPs are
byte-identical compatibility aliases for old Desktop updaters. `SHA256SUMS`
covers every artifact; `ANDROID-VERSION` records the independent APK version.
