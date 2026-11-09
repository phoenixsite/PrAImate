# Workers

Workers coordinates three independently configured model profiles: **Reasoner**
(`primary`), **Middle** and **Fast**. The feature is available in the Desktop
Workers page and the Studio / VS Code extension's **PrAImate: Workers** view.

## Task board and attempts

The execution monitor uses four columns: **Needs you**, **Working**, **Done** and
**Queued**. Failed/interrupted assignments appear in Needs you immediately. A
completed change awaiting review moves to Needs you once the task graph stops
executing; while other tasks are still running it stays in Done marked
**Completed · review when execution ends**. A card identifies the task, backend,
model, dependencies and attempt count. Search by task/model or filter by backend
in Desktop.

Select a card to focus its console. The attempt selector preserves the actual
CLI/model of each historical attempt, even after Run settings change. **Activity**
shows live output, reported reasoning, tool results, delegations and errors;
**Result** shows its reported result; **Changes** previews the current task worktree;
**Assignment** shows its task, workspace, parent and CLI session. Follow active
focuses a working assignment; selecting a card allows inspecting another task.

The board and separate execution window/panel share the same Core state and
operations in Desktop and VS Code. This design adapts the durable run/task/attempt
and agent dashboard patterns reviewed in [ORCA](https://github.com/stablyai/orca)
(revision `7b26725ff4b38e1981ad2e1153118fa24faa7ac3`). PrAImate retains its
three profiles, host tool protocol and encrypted database. Execution belongs to
the owning PrAImate process: closing a monitor keeps it running; closing the app
interrupts it and requires explicit continuation after reopening. There is no
independent persistent terminal daemon in this implementation.

## Parallel task graphs

Select **Parallel task graph** (Desktop) or **Task board** (VS Code) when creating a worker chat. This is the default mode for new runs. The workspace must
be a clean Git repository root on a checked-out branch. For an empty repository,
PrAImate creates an empty initial commit without staging any files. Repositories
with uncommitted files still require the user to commit or stash those files.
The Reasoner returns a draft DAG; it does not modify the project while planning.
Review task descriptions, dependencies, profile, CLI/local endpoint and model
before pressing **Run task graph**. Routes reuse the existing backend/model
registry. A task override takes precedence over its selected profile; provider
routing follows that backend's existing model ID or configured local endpoint.
**Review plan** opens the plan without starting it; **Run task graph** saves the
current plan and starts execution directly.

The host schedules independent tasks concurrently (default 2, maximum 4 per
run). Every task receives a separate Git worktree and scoped context. Its
worktree lives in PrAImate's dedicated user-cache directory outside the project,
so CLI project discovery sees a normal checkout. Its
transitive dependencies are cherry-picked into that worktree before execution.
Workers use the existing JSON tool protocol and profile permissions; delegation
inside a DAG task is disabled. The host creates a single task commit and its
diff, including any model-created commits, rather than trusting a worker's
claimed changes. A no-change task has a saved result without a new commit.
Git commands delegated through managed tools inherit the configured public Git
author name and email, with a PrAImate Workers fallback when none is configured.
This does not forward the user's global configuration or credentials.

Task cards preserve profile, CLI, provider/model route, branch, worktree path,
base/result commits, changed files, result and Git diff. Diff previews are capped
at 256 KiB; the worktree and saved commits provide the complete patch. Live events and saved attempts include task IDs in the board and activity console.

Accept dependencies before accepting their dependent results. **Merge accepted
changes** cherry-picks the accepted task commits, in dependency order, into a
separate review worktree based on the current target branch. Only a successful
integration fast-forwards the target branch. A conflict stops there and reports
the review worktree path; the original checkout remains unchanged. No automatic
conflict resolution runs. Return to the recorded target branch with a clean
workspace before merging. Inspect a failed merge and use **Clean reviewed
worktrees** before retrying it.

When a task fails while integrating dependencies, **Resolve conflicts with
worker** asks its currently configured worker to reconcile the conflicted files
in the retained worktree. File edits must be enabled (or select full access).
The host stages the resolved files, continues the pending cherry-pick, and then
executes the original assignment. Unresolved conflict markers stop execution.
For a manual resolution, edit and stage the reported files, then choose
**Continue after manual resolution**. Neither action repeats completed tasks or
discards their results; dependent tasks become ready once the recovered task
completes. **Start over…** explicitly confirms discarding only that task's tree.
Git records successful conflict resolutions with command-scoped `rerere`, so
review integration can reuse them. New conflicts still stop for inspection.

Merged worktrees are removed after integration. Rejected worktrees can be cleaned
explicitly; saved diffs and private Git result refs preserve review history.
Deleting a worker chat removes its temporary worktrees and result refs, including
unmerged work, after a frontend confirmation. Interrupted tasks never restart
automatically: inspect their worktrees, then choose **Continue with existing changes** to queue another attempt, or **Start over…** to explicitly discard failed work. Ready tasks can run while unrelated failed tasks remain for review. Completed descendants prevent retrying or resetting
their dependencies. Task worktrees remain pinned to the original planning base.
Pending tasks can resume after this run merges its accepted results, including
after an app restart. Unrelated workspace HEAD changes require a new plan.

Worktrees separate checkouts, not operating-system permissions. They share Git
metadata. Supervised runs use the existing host approvals; Full access runs execute
commands and edits autonomously. Each task has
the existing per-worker turn/request/time bounds. The graph supports up to 32
tasks; concurrent run limits also remain in place. Plans, task states, activity
and review results use the same encrypted local chat storage as Workers.
Task IDs use lowercase letters, numbers, underscores and hyphens (up to 64
characters); `review-merge` is reserved for the host. Graph checkpoints replace
the current stored snapshot instead of duplicating all diffs. Retained graph
live activity is capped at 500 events and 1 MiB of event text. Attempt summaries, reported token totals and the paginated activity journal are persisted separately, so trimming the live buffer does not remove completed/failed attempts or their usage. Existing runs retain the legacy activity tail that is still available; previously discarded events cannot be recovered.

The same operations are available in VS Code and the maintenance CLI:

```sh
praimate workers plan --config workers.json --workspace /path/to/repo \
  --task "Implement the feature and its tests" --parallel 2
praimate workers show --id RUN_ID
praimate workers activity --id RUN_ID --worker-id ATTEMPT_ID --limit 100
praimate workers changes --id RUN_ID --task-id task-a
# Queue a continuation while preserving its worktree; then execute ready tasks.
praimate workers retry --id RUN_ID --task-id task-a
praimate workers resolve --id RUN_ID --task-id task-a
# Optionally edit a tasks JSON array and pass --plan tasks.json.
praimate workers execute --id RUN_ID
praimate workers review --id RUN_ID --task-id task-a --decision accepted
praimate workers merge --id RUN_ID
```

`workers.json` is the existing `Config` format with `workspace` and the three
`profiles`; saved defaults are used when `--config` is omitted. CLI execution
denies approval-dependent tools in supervised mode unless explicitly given
`--approve-tools`. `accessMode: "full"` enables autonomous execution.
`reset` and `cleanup` require `--discard`; inspect affected worktrees first.
The existing hierarchical mode below remains available for non-Git workspaces.

## Create and resume a worker chat

1. Open **Workers → New worker chat** and enter the task for Reasoner.
2. Choose the project workspace and configure all three profiles. Select a CLI
   and its model, or a native OpenAI-compatible endpoint and model. A CLI can be
   selected in multiple profiles with different models.
3. Set each profile's instructions, input/timeout limits and edit/command
   permissions. Native profiles also require an output token limit. External
   CLI profiles use the selected CLI's own output limits.
4. Create the chat. Follow the task board, worker assignments and activity
   console. **Open execution window** opens an independent Desktop monitor;
   **Open execution panel** opens a VS Code panel. Closing a monitor does not
   stop the run. Execution and encrypted persistence stay in the owning process.

Profiles are stored with each new chat. Saving new defaults does not change
existing chats. Saved runs can be renamed, stopped, deleted after stopping,
and continued with a follow-up to Reasoner. Closing and reopening the app
restores the saved runs. A previously running task is marked cancelled with an interruption notice; review recorded activity and workspace state
before continuing.

CLI backends that support resume keep a session ID per assignment. Continuations reuse that session only when the complete effective profile is unchanged. Changing the model, CLI, permissions, instructions or limits starts a fresh session with bounded task context; the worktree is retained. Stateless backends receive bounded observations and previous results. Children receive their own delegated tasks and sessions, and never inherit the parent session. Filesystem changes are not rolled back.

Each new invocation records its worker ID, parent invocation, task ID (parallel
tasks), actual CLI/runtime, model, workspace, phase and call number. The monitor
groups assignments rather than mixing concurrent tasks that use the same tier.
Handoffs name their destination; child results return to the recorded parent.
Activity filters separate input/output, tool events, handoffs, errors and
**reported reasoning**. Reasoning appears only when the CLI/provider exposes it;
no private model reasoning is inferred. A live call shows elapsed time even if
the backend sends no progress. Historical snapshots without these fields remain
readable, using their saved task/profile routes.

**Run settings** edits profiles on an inactive saved run: CLI, model, instructions,
Codex reasoning effort (CLI default or an explicitly supported level),
permissions and limits. Changes affect future calls and survive app restarts;
recorded invocation routes and explicit task overrides retain their values.
Parallel tasks store requested routing separately from each executed attempt.
After changing a profile, failed, cancelled, blocked and pending tasks that inherit
it show the updated CLI/model as **Next attempt**. **Last attempt** and recorded
activity keep the previous route. **Use current profile** removes a task-specific
route without deleting its worktree or starting another attempt. Inspect partial
changes, choose **Continue with existing changes**, then run ready tasks. **Start over…** remains the confirmed destructive alternative.
Completed tasks and accepted results are retained. Older saved runs recover
inheritance by comparing resolved routes with their original creation profiles;
legacy pins equal to those defaults cannot be distinguished from inheritance.
The workspace cannot be changed. A failed planning call without tasks can be
retried explicitly with **Retry planning** after reviewing partial activity and
adjusting its settings. Existing tasks use their review/reset controls instead.
For a planning deadline with a positive timeout, **Retry with no reasoner timeout**
saves 0 for Reasoner and starts a new planning attempt; other profiles and prior
activity remain intact. Stop is still available. In parallel mode, Middle and Fast
wait until the plan is ready and the user reviews and runs its tasks.
External CLI process startup and supported session/turn events appear in the
activity feed, alongside reported models, exposed reasoning, tool calls and
backend errors. PrAImate CLI/native profiles report preparation of their model
request. A started process or prepared request alone does not prove the backend
accepted it. Codex reconnect errors can be recoverable; `turn.failed`, Claude
error results, OpenCode session errors, aborted Copilot calls and nonzero process
exit codes fail the assignment even when partial output exists. Reported output
and token usage remain available on failure. OpenCode/PrAImate Code error
references are retained when the CLI supplies them, for correlation with its
server logs. A failing router/provider must be fixed in that backend or changed
explicitly in **Run settings**; PrAImate does not silently switch models.
Timeout failures report the worker, CLI/model and phase. Automatic retries are
disabled by default and follow the configured policy below. Pending approvals remain available in the main window
and are recovered when opening the independent monitor.

Refreshes run sequentially, slow down while idle and pause requests while the
window is hidden; active runs continue in the core. Live previews remain bounded. **Load earlier activity** reads older entries from the encrypted journal, without reading third-party CLI session log files.

For local server routing, detected context windows and output reservations,
see [PrAImate CLI](native-cli.md#context-and-output-budgets).

## Communication and tools

PrAImate coordinates workers using one JSON action per response. The coordinator
passes the result of each action back on the next turn; this protocol does not
require an additional MCP server. Primary may delegate to Middle or
Fast, and Middle may delegate to Fast. A child receives its scoped task and
instructions; its result goes back to its caller as bounded evidence.

No ORCA installation or external `orchestration` skill is required. The host
provides the coordinator instructions for each run. In **Parallel task graph**,
the reasoner's JSON becomes a saved draft: **Plan ready** means it is awaiting
review, not that the work has executed. Review the visible assignments and select
**Run task graph** to dispatch the workers. In **Hierarchical worker chat**, the
reasoner dispatches scoped assignments through the built-in delegation protocol
and receives their results before finalizing; a task list alone does not run them.

Supported external adapters include Codex, Claude Code, OpenClaude, GitHub Copilot,
OpenCode and PrAImate Code. Antigravity is available for native chats and terminals,
but not managed Workers: its plan mode does not enforce a read-only tool policy.
Selecting `praimate-cli` resolves the saved core host/model route. Third-party CLI adapters use their safe permissions in supervised mode,
and their autonomous full access mode when Full access is selected. OpenCode and
PrAImate Code use **Build** when workspace edits are enabled, with read/edit
permissions and no native command access; supervised commands use the host
approval path. Planning always uses **Plan**. Workspace reads, edits and commands requested through the
JSON protocol execute through the PrAImate host and its configured approvals.
Native workers use the core model transport and validate their input/output
budget before sending the request. Their model transport has no direct tools;
the coordinator executes the returned host action. Edit and command permissions
are selected independently; commands and brokered writes use the host approval
flow. Bounded exact replacements require the profile's edit permission.

Exact replacements support existing UTF-8 project files up to 2 MiB, including
code, documentation, configuration and public metadata such as `.gitignore`.
The old text must occur exactly once; both old and new text are limited to
4096 bytes. Sensitive paths, binary files and paths outside the workspace remain
excluded. Activity identifies the file before the operation and includes it in
errors. A missing file or invalid text match gets one corrective turn within the same
assignment, with instructions to inspect the file and correct the operation.
No edit is applied when validation fails.

## Autonomous execution and retries

**Execution controls** appear when creating a run and in **Run settings** in
Desktop and VS Code. **Supervised** is the default. **Full access · autonomous**
enables edits and commands for every execution profile, approves host operations
without a user prompt, and selects the external CLI's full access mode. This is
scoped to the worker run; agent manifests and global application permissions are
not changed. Planning remains read-only. Task graphs still require reviewing the
initial plan and accepting/merging the resulting Git changes explicitly.

**Automatic retries per task** accepts 0–10 additional attempts (0 disables them).
The policy covers planning, hierarchical assignments and individual graph tasks.
**Retry delay** accepts 0–60 seconds, doubles between attempts and caps at 60
seconds. The board shows **Retry scheduled**, the attempt count and next attempt
time. **Stop** cancels both running calls and scheduled retries.

Retries retain partial files, results, activity and compatible CLI sessions. The
worker is instructed to inspect prior effects before repeating operations;
commands are not transactionally rolled back or guaranteed to be idempotent.
Only execution failures are automatically retried for graph tasks. Dependency
integration, worktree setup and Git checkpoint failures require inspection.
Denied approvals, cancelled runs and persistence failures are not retried.
Persistent replacement validation errors also stop automatic task retries;
repeating the same invalid edit does not repair it. After updating the app or
correcting the task, **Continue with existing changes** queues a manual attempt
even if its automatic retry allowance was exhausted. Completed results and
partial work are retained, and blocked dependents can run after it succeeds.

The automatic retry count is persisted per graph task and survives continuation
and app restarts. A manual continuation preserves that count; increase the run's
retry limit if additional automatic attempts are desired. A new hierarchical
follow-up or explicit planning retry gets a new bounded allowance. Closing the
app interrupts execution and does not restart retries automatically. A recovery
summary uses only the available input space; it does not replay complete logs.

Failed tasks show their specific error and retained attempt history. Blocked tasks
name the dependency problem. Completed results stay available for review even
when another task fails; accept and reject unlock when the graph stops executing.
The board groups attempts under the same assignment, with separate Activity,
Result, Changes and Assignment views.

Example configuration additions:

```json
{
  "accessMode": "full",
  "maxRetries": 3,
  "retryDelaySeconds": 5
}
```

Add these fields alongside the existing `workspace` and three `profiles` in
`workers.json`. An external provider failure never silently changes the model
or CLI; change its route in Run settings if needed.

## Execution limits and verification

Initial tasks are limited to 16 KiB and follow-up requests to 8 KiB. Reference
workspace paths so workers can read focused ranges instead of pasting files.

Each tier has at most 16 model turns per invocation, with a shared maximum of
64 host dispatches. There is no additional fixed ten-minute deadline for the
whole run. Each model call uses its profile timeout (0–3600 seconds); 0 disables
that timeout, while explicit Stop and parent cancellation remain available.
New CLI profiles default to no host call timeout; native API profiles default to
600 seconds for Reasoner and 300 for other workers. A positive timeout is a wall
clock limit for the entire dispatch, including CLI startup, reasoning and tools;
saved profiles keep their configured values. An external CLI can make several
provider calls within one dispatch. A malformed response or failed read
gets one correction opportunity; consecutive failures stop that invocation.
Child failures return to the parent with a warning about possible prior effects.
At most three worker chats can run concurrently, including continued chats.

These bounds control work but do not prove token savings. Supported CLI adapters
forward provider-reported token usage to the worker runtime and encrypted usage
dashboard. Missing reports remain unavailable. Integration
tests verify routing, context isolation, host operations and persistence using
controlled model responses. Live model quality and task-specific savings still
require testing with the selected CLI/model combination.
