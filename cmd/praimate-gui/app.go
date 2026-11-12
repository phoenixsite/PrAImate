package main

// App is the Wails binding surface. Every exported method becomes a
// JS-callable function under window.go.main.App.*. All methods are
// thin delegations into internal/core — the GUI holds NO business
// logic (plan §1 iron rule).
//
// Error convention: methods return (T, error); Wails surfaces the
// error as a rejected promise so the frontend can toast it.

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/sPROFFEs/PrAImate/internal/assistant"
	"github.com/sPROFFEs/PrAImate/internal/backup"
	"github.com/sPROFFEs/PrAImate/internal/core"
	"github.com/sPROFFEs/PrAImate/internal/installer"
	"github.com/sPROFFEs/PrAImate/internal/launcher"
	"github.com/sPROFFEs/PrAImate/internal/ollama"
	"github.com/sPROFFEs/PrAImate/internal/orchestrator"
	"github.com/sPROFFEs/PrAImate/internal/store"
	"github.com/sPROFFEs/PrAImate/internal/studio"
	"github.com/sPROFFEs/PrAImate/internal/version"
	"github.com/sPROFFEs/PrAImate/internal/voice"
)

// App carries the shared Core plus the Wails context used for dialogs
// and event emission.
type App struct {
	ctx              context.Context
	st               *store.Store
	core             *core.Core
	dbPath           string
	initErr          string
	quit             func(context.Context)
	emit             func(context.Context, string, ...any)
	unlockMu         sync.Mutex
	backupRestoreMu  sync.Mutex
	backupRestoreDir string
	backupRestoreErr error

	// daemonMu guards the daemon handles — Wails dispatches each
	// binding call on its own goroutine, so two watcher mutations can
	// race on restartWatchers without it.
	daemonMu  sync.Mutex
	watchers  *core.WatcherDaemon
	schedules *core.ScheduleDaemon

	terms *termManager

	// chatCancels maps chatID → cancel func for the in-flight streamed
	// turn, so the Stop button can interrupt it. Guarded by chatCancelMu
	// (binding calls run on independent goroutines).
	chatCancelMu           sync.Mutex
	chatCancels            map[string]context.CancelFunc
	chatCancelIDs          map[string]uint64
	chatCancelSeq          uint64
	workflowCancelMu       sync.Mutex
	workflowCancels        map[string]context.CancelFunc
	managedCancelMu        sync.Mutex
	managedCancels         map[string]context.CancelFunc
	workers                *orchestrator.Manager
	assistantMu            sync.Mutex
	assistantRunMu         sync.Mutex
	assistantTasksMu       sync.Mutex
	assistantClosed        bool
	assistantHealthCancel  context.CancelFunc
	assistantService       *assistant.Service
	assistantProvider      assistant.Provider
	assistantFingerprint   string
	assistantUsage         *core.UsageRecorder
	voiceService           *voice.Service
	voiceCancel            context.CancelFunc
	voiceCaptureTimer      *time.Timer
	voiceCaptureGeneration uint64

	// ragCancels maps agent IDs to active graphify extractions so the RAG
	// controls can stop only their own child process. Guarded by ragCancelMu.
	ragCancelMu sync.Mutex
	ragCancels  map[string]*ragRun

	// requirementsCancels maps agent IDs to active requirements scripts so
	// their live progress panel can stop only the matching run.
	requirementsCancelMu sync.Mutex
	requirementsCancels  map[string]*requirementsRun

	resetMu   sync.Mutex
	dataReset bool

	// approval is the lazily-started mid-turn approval broker ("ask"
	// Tools level). Guarded by approvalMu.
	approvalMu sync.Mutex
	approval   *approvalBroker

	// skillsBroker is the internal MCP skills server for dynamic on-demand
	// skill loading in CLIs and sessions. Guarded by skillsBrokerMu.
	skillsBrokerMu sync.Mutex
	skillsBroker   *skillsBroker

	// editorOwnWrites suppresses fsnotify echoes of the studio's own
	// file flushes (editor_window.go). Guarded by editorMu.
	editorMu        sync.Mutex
	editorScopeMu   sync.Mutex
	editorOwnWrites map[string]time.Time

	// detached coordinates lightweight chat/terminal windows. Only the main
	// process owns it; detached child processes use detachedClient and never
	// open the database or start background daemons.
	detached       *detachedCoordinator
	detachedClient *detachedClient

	studioWindowMu     sync.Mutex
	studioWindowHidden bool

	dashboardMu    sync.Mutex
	dashboardCache map[string]dashboardCacheEntry
}

func NewApp() *App {
	a := &App{
		terms:               newTermManager(),
		chatCancels:         map[string]context.CancelFunc{},
		chatCancelIDs:       map[string]uint64{},
		managedCancels:      map[string]context.CancelFunc{},
		ragCancels:          map[string]*ragRun{},
		requirementsCancels: map[string]*requirementsRun{},
		quit:                wruntime.Quit,
		emit:                wruntime.EventsEmit,
	}
	if detachedProcessMode.active {
		a.terms = nil
		a.detachedClient = newDetachedClient(detachedProcessMode)
	} else {
		a.detached = newDetachedCoordinator(a)
	}
	return a
}

func (a *App) emitUIEvent(name string, payload any) {
	if a.emit != nil {
		a.emit(a.ctx, name, payload)
	} else {
		wruntime.EventsEmit(a.ctx, name, payload)
	}
}

// startup opens the shared DB and seeds builtins. Failures are
// recorded, not fatal — the frontend shows a setup-error banner via
// Health().
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	if a.detachedClient != nil {
		a.detachedClient.start(ctx, func(name string, payload any) {
			wruntime.EventsEmit(ctx, name, payload)
		})
		// A detached child is ready as soon as its broker client is alive.
		// Do not make the parent wait for a heavy Studio renderer/import tree.
		go func() { _ = a.detachedClient.rpc("window.ready", nil, nil) }()
		return
	}

	// Add managed tool prefixes (graphify, gstack, scrapegraph) and the praimate bin dir
	// (praimate-code). Without it the GUI — and every CLI child it
	// spawns — can't resolve tools installed into the managed dirs:
	// "graphify installs but isn't detected".
	refreshManagedPaths()

	// Live PATH rescan — periodically check the well-known per-user CLI
	// dirs in case the user installed bun/pnpm/cargo/uv in another
	// terminal while the GUI was already running. Cheap: just os.Stat
	// per dir, no fork. Goroutine ends when ctx is canceled (i.e. on
	// shutdown). The interval is wide enough not to be a busy loop and
	// tight enough that "I just ran `curl … | bash`" feels immediate.
	go func() {
		t := time.NewTicker(8 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				refreshManagedPaths()
			}
		}
	}()

	dbPath, err := store.DefaultDBPath()
	if err != nil {
		a.initErr = err.Error()
		return
	}
	a.dbPath = dbPath
	st, err := store.Open(dbPath)
	if err != nil {
		if errors.Is(err, store.ErrPasswordRequired) ||
			errors.Is(err, store.ErrPasswordSetupRequired) {
			return
		}
		a.initErr = err.Error()
		return
	}
	if err := a.initializeUnlockedStore(ctx, st); err != nil {
		_ = st.Close()
		a.initErr = err.Error()
	}
}

func (a *App) initializeUnlockedStore(ctx context.Context, st *store.Store) error {
	// Load the legacy workspaces root (best-effort) so existing on-disk
	// chats remain available in the Code terminal. Without a config the
	// workspace chats section stays empty.
	workspacesRoot := ""
	var launcherCfg *launcher.Config
	if cfg, cfgErr := launcher.LoadConfig(); cfgErr == nil && cfg != nil {
		launcherCfg = cfg
		workspacesRoot = cfg.WorkspacesRoot
	}
	c, err := core.New(core.Options{Store: st, WorkspacesRoot: workspacesRoot})
	if err != nil {
		return err
	}
	if _, err := c.SeedBuiltins(ctx); err != nil {
		return err
	}
	if err := migrateLegacyLocalLLMAPIKey(c, launcherCfg); err != nil {
		return err
	}
	// Codex local routing is no longer supported. Remove only the
	// ollama_remote blocks previously owned by PrAImate; unrelated Codex
	// configuration is preserved by DisableCodex.
	_, _ = ollama.DisableCodex()
	core.RegisterAllCLIAdapters()
	// From here on, every backup commit snapshots the DB + shareable
	// config, and every pull/merge/reset row-merges the remote's
	// snapshot back in. Must precede any Backup-tab binding call.
	backup.SetStateSyncer(coreStateSyncer{core: c, app: a})
	// "Ask" Tools level: chats route mid-turn permission requests to
	// the GUI's Allow/Deny card (claude/openclaude only; see
	// approval_broker.go).
	c.SetApprovalProvider(a.approvalProvider)
	runGUIAutoSync(ctx, launcherCfg)

	if editorFolder == "" {
		// Watchers and schedules run in the main desktop process. NOT in studio-window
		// processes: the main window already runs them, and two daemons
		// on one DB would double-fire every schedule.
		watchers, _ := c.StartWatcherDaemon(context.Background(), core.WatcherDaemonOptions{
			WatcherDispatchOptions: core.WatcherDispatchOptions{CLI: "claude"},
		})
		schedules, _ := c.StartScheduleDaemon(context.Background(), core.ScheduleDaemonOptions{
			ScheduleDispatchOptions: core.ScheduleDispatchOptions{CLI: "claude"},
		})

		a.daemonMu.Lock()
		a.watchers = watchers
		a.schedules = schedules
		a.daemonMu.Unlock()
	}

	a.st = st
	a.core = c
	a.workers = orchestrator.NewManager(c, ctx)
	if config, err := a.AssistantConfig(); err == nil && config.Enabled && config.StartWithApp {
		go func() { _ = a.AssistantHealth() }()
	}
	studio.SetDesktopWorkerManager(c, a.workers)
	a.connectStudioAssistant()
	studio.SetDesktopWindowHooks(c, &studio.DesktopWindowHooks{
		Hide: func() {
			a.studioWindowMu.Lock()
			a.studioWindowHidden = true
			a.studioWindowMu.Unlock()
			wruntime.WindowHide(a.ctx)
		},
		Show: func() {
			wruntime.WindowShow(a.ctx)
			wruntime.WindowUnminimise(a.ctx)
			a.studioWindowMu.Lock()
			a.studioWindowHidden = false
			a.studioWindowMu.Unlock()
		},
		IsHidden: func() bool {
			a.studioWindowMu.Lock()
			defer a.studioWindowMu.Unlock()
			return a.studioWindowHidden
		},
	})

	if editorFolder != "" {
		// Studio window: stream external file changes (the agent's
		// edits) into the open tabs.
		a.startEditorWatcher()
	}
	return nil
}

func (a *App) shutdown(ctx context.Context) {
	a.workflowCancelMu.Lock()
	for _, cancel := range a.workflowCancels {
		cancel()
	}
	a.workflowCancelMu.Unlock()
	if a.detachedClient != nil {
		a.detachedClient.close()
		return
	}
	stopAssistantCtx, stopAssistantCancel := context.WithTimeout(context.Background(), 10*time.Second)
	_ = a.stopAssistantServices(stopAssistantCtx)
	stopAssistantCancel()
	if a.core != nil {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = a.core.StopManagedArtifactInstalls(stopCtx)
		cancel()
	}
	a.resetMu.Lock()
	resetting := a.dataReset
	a.resetMu.Unlock()
	a.daemonMu.Lock()
	if a.watchers != nil {
		a.watchers.Stop()
		a.watchers = nil
	}
	if a.schedules != nil {
		a.schedules.Stop()
		a.schedules = nil
	}
	a.daemonMu.Unlock()
	if a.terms != nil {
		a.terms.closeAll()
	}
	if a.core != nil {
		_ = a.core.Close()
	}
	if !resetting {
		if cfg, err := launcher.LoadConfig(); err == nil {
			runGUIAutoSync(context.Background(), cfg)
		}
	}
	if a.st != nil {
		_ = a.st.Close()
	}
	if a.detached != nil {
		a.detached.close()
	}
}

// --- Terminal (live coding sessions) -------------------------------------

// PickProjectFolder opens a directory picker for a terminal's working
// folder.
func (a *App) PickProjectFolder() (string, error) {
	return wruntime.OpenDirectoryDialog(a.ctx, wruntime.OpenDialogOptions{
		Title: "Choose project folder",
	})
}

// StartTerminal launches a third-party CLI live in a PTY for an agent. The
// existing project instructions are never overwritten. Temporary context is
// exclusive; versioned Code chats can fall back to skills MCP on collision.
// Returns the
// terminal session id; output streams over "term:data:<id>" events.
func (a *App) StartTerminal(agentID, cli, model, cwd, localEndpoint, _ string, localModel string, resume bool, skills []string) (string, error) {
	return a.startTerminal(agentID, cli, model, cwd, localEndpoint, localModel, resume, skills, nil, "")
}

func (a *App) startTerminal(agentID, cli, model, cwd, localEndpoint, localModel string, resume bool, skills []string, skillSettings *core.ChatSettings, chatID string) (string, error) {
	c, err := a.requireCore()
	if err != nil {
		return "", err
	}
	if cwd == "" {
		return "", fmt.Errorf("a project folder is required")
	}
	if cli == "praimate-cli" {
		id, err := a.RecordCodeSession(agentID, cli, model, cwd, localEndpoint, "", localModel)
		if err != nil {
			return "", err
		}
		if skillSettings != nil || len(skills) > 0 {
			err = c.UpdateChatSettings(a.ctx, id, func(s *core.ChatSettings) {
				if skillSettings != nil {
					*s = *skillSettings
				} else {
					s.Skills = skills
				}
				s.Surface = "code"
			})
			if err != nil {
				_ = c.DeleteChat(a.ctx, id)
				return "", err
			}
		}
		termID, err := a.StartTerminalForChat(id, false)
		if err != nil {
			_ = c.DeleteChat(a.ctx, id)
		}
		return termID, err
	}
	var agent *core.Agent
	if agentID != "" {
		agent, err = c.GetAgent(a.ctx, agentID)
		if err != nil {
			return "", err
		}
	}
	var local *core.ChatLocalEndpoint
	if strings.TrimSpace(localEndpoint) != "" {
		local = &core.ChatLocalEndpoint{Endpoint: localEndpoint, Model: localModel}
	}
	effective, err := c.ResolveExecutionConfig(a.ctx, core.ExecutionRequest{
		Surface: core.SurfaceTerminal, Agent: agent, CLI: cli, Cwd: cwd,
		Model: model, Local: local, AllEnabledMCP: agent == nil,
	})
	if err != nil {
		return "", err
	}
	model = effective.Model
	name, args, err := terminalCommand(cli, model)
	if err != nil {
		return "", err
	}
	if cli == "antigravity" || cli == "copilot" {
		name, err = core.ResolveInteractiveCLIBinary(cli)
		if err != nil {
			return "", err
		}
	}
	nativeResume := false
	if resume {
		var supported bool
		name, args, supported, err = terminalResumeCommand(cli, model, cwd)
		if err != nil {
			return "", err
		}
		nativeResume = isNativeResumeCommand(cli, args)
		// For CLIs without a safe non-interactive resume selector, retain the
		// normal launch rather than opening an interactive picker unexpectedly.
		_ = supported
	}
	// Interactive CLIs do not expose their final prompt. PrAImate prepares the
	// reviewed skill payload and private resources for a compatible fallback.
	// Versioned Code chats can use their skills MCP when an existing project
	// context file prevents the temporary fallback. An agent requires a native
	// session being resumed so its persona has already been supplied.
	prefix := core.ResolveSkillsPrefix(skills)
	var nativePrefix string
	var nativeSkills *core.NativeSkillMaterialization
	var configured bool
	if skillSettings != nil {
		nativePrefix, nativeSkills, configured, err = c.PrepareTerminalSkillFallbackForSettings(a.ctx, *skillSettings)
	} else {
		nativePrefix, nativeSkills, configured, err = c.PrepareTerminalSkillFallback(a.ctx, agent)
	}
	if err != nil {
		return "", err
	}
	if configured {
		if len(skills) > 0 {
			_ = nativeSkills.Cleanup()
			return "", fmt.Errorf("legacy and versioned skill selections cannot be combined")
		}
		prefix = nativePrefix
	}
	versionedChat := chatID != "" && skillSettings != nil && skillSettings.SkillsV2 != nil
	legacyCleanup, bridgeOnly, err := prepareTerminalContext(cwd, cli, agent, prefix, versionedChat, nativeResume)
	if err != nil {
		if nativeSkills != nil {
			_ = nativeSkills.Cleanup()
		}
		return "", err
	}
	started := false
	defer func() {
		if !started {
			if legacyCleanup != nil {
				legacyCleanup()
			}
			if nativeSkills != nil {
				_ = nativeSkills.Cleanup()
			}
		}
	}()
	targetChatID := chatID
	if targetChatID == "" && agentID != "" {
		targetChatID = "agent:" + agentID
	}
	prov := a.skillsProvider(targetChatID)
	if bridgeOnly && prov == nil {
		return "", fmt.Errorf("cannot provide selected skills through the internal MCP bridge")
	}
	if prov != nil {
		effective.InternalMCPServers = append(effective.InternalMCPServers, core.MCPServer{
			ID:        "praimate_skills",
			Name:      "PrAImate Skills",
			Transport: core.MCPTransportStdio,
			Command:   prov.Command,
			Args:      prov.Args,
			Enabled:   true,
		})
	}
	// Validate the concrete terminal command before PrepareExecution writes
	// project-scoped MCP or provider configuration.
	if err := c.PrepareExecution(a.ctx, effective); err != nil {
		return "", err
	}
	env := appendEnvMap(nil, effective.Env)
	extra, configCleanup, err := core.PrepareInteractiveCLIConfig(cli, effective.Env)
	if err != nil {
		return "", err
	}
	args = append(args, extra...)
	usage, err := c.BeginTerminalUsage(a.ctx, cli, model, effective.Env)
	if err != nil {
		configCleanup()
		return "", fmt.Errorf("prepare terminal usage: %w", err)
	}
	if usage != nil {
		args = append(args, usage.Args...)
		env = appendEnvMap(env, usage.Env)
	}
	if nativeSkills != nil {
		env = appendEnvMap(env, nativeSkills.Env)
	}
	cleanup := func() {
		configCleanup()
		usage.Close()
		if legacyCleanup != nil {
			legacyCleanup()
		}
		if nativeSkills != nil {
			_ = nativeSkills.Cleanup()
		}
	}
	termID, err := a.terms.startWithCleanup(name, args, cwd, env, a.emitTerminalEvent, cleanup)
	if err != nil {
		cleanup()
	}
	started = err == nil
	return termID, err
}

// WriteTerminal forwards a base64-encoded chunk of keystrokes to the
// PTY. The frontend base64-encodes so arbitrary control bytes survive.
func (a *App) WriteTerminal(id, b64 string) error {
	if a.detachedClient != nil {
		return a.detachedClient.terminalWrite(id, b64)
	}
	data, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return fmt.Errorf("decode terminal input: %w", err)
	}
	return a.terms.write(id, data)
}

func (a *App) ResizeTerminal(id string, cols, rows int) error {
	if a.detachedClient != nil {
		return a.detachedClient.terminalResize(id, cols, rows)
	}
	return a.terms.resize(id, cols, rows)
}

// GetTerminalSnapshot returns the retained output of a live PTY so the Code
// page can reconstruct xterm after it was minimized/navigated away from.
func (a *App) GetTerminalSnapshot(id string) (TerminalSnapshot, error) {
	if a.detachedClient != nil {
		return a.detachedClient.terminalSnapshot(id)
	}
	return a.terms.snapshot(id)
}

func (a *App) CloseTerminal(id string) {
	if a.detachedClient != nil {
		_ = a.detachedClient.terminalClose(id)
		return
	}
	a.terms.close(id)
}

// InstallPraimateCodeResult tells the frontend whether the download
// succeeded, failed for a fixable network reason, or failed because no
// prebuilt asset exists for this OS/arch (in which case the GUI offers
// "Compile from source" instead of letting the user retry forever).
type InstallPraimateCodeResult struct {
	OK              bool   `json:"ok"`
	Log             string `json:"log"`
	Error           string `json:"error,omitempty"`
	NoPrebuiltAsset bool   `json:"noPrebuiltAsset,omitempty"`
}

// InstallPraimateCode downloads the prebuilt PrAImate Code binary into
// the managed bin dir. Returns a structured result so the frontend can
// distinguish "404 / asset missing" (offer compile) from a generic
// failure (offer retry).
func (a *App) InstallPraimateCode() InstallPraimateCodeResult {
	var buf strings.Builder
	err := installer.InstallPraimateCode(a.ctx, &buf)
	res := InstallPraimateCodeResult{OK: err == nil, Log: buf.String()}
	if err != nil {
		res.Error = err.Error()
		if errors.Is(err, installer.ErrNoPrebuiltAsset) {
			res.NoPrebuiltAsset = true
		}
	}
	return res
}

// PraimateCodeInstalled reports whether praimate-code resolves on this
// host using the same bundled/managed/PATH selection as chats and Code.
func (a *App) PraimateCodeInstalled() bool {
	_, err := core.ResolveInteractiveCLIBinary("praimate-code")
	return err == nil
}

func osIsWindows() bool { return runtime.GOOS == "windows" }

func (a *App) requireCore() (*core.Core, error) {
	if a.core == nil {
		if a.initErr != "" {
			return nil, fmt.Errorf("core unavailable: %s", a.initErr)
		}
		return nil, fmt.Errorf("core not initialised yet")
	}
	return a.core, nil
}

// --- Health / meta -------------------------------------------------------

// Health reports init status so the frontend can render a setup-error
// banner instead of failing every call individually.
type Health struct {
	OK      bool   `json:"ok"`
	Error   string `json:"error,omitempty"`
	Version string `json:"version"`
	DBPath  string `json:"db_path,omitempty"`
}

func (a *App) Health() Health {
	h := Health{Version: version.Current}
	if a.core == nil {
		h.Error = a.initErr
		return h
	}
	h.OK = true
	h.DBPath = a.st.Path()
	return h
}

// --- Chats ---------------------------------------------------------------

func (a *App) ListChats() ([]core.Chat, error) {
	if a.detachedClient != nil {
		return a.detachedClient.listChats()
	}
	c, err := a.requireCore()
	if err != nil {
		return nil, err
	}
	chats, err := c.ListChats(a.ctx, 200)
	if err != nil {
		return nil, err
	}
	return redactChatCredentials(chats), nil
}

// StartChat creates an interactive chat bound to an agent + CLI and
// returns it. The frontend then drives it with SendChat.
func (a *App) StartChat(agentID, cli, cwd string) (*core.Chat, error) {
	c, err := a.requireCore()
	if err != nil {
		return nil, err
	}
	if cwd == "" {
		if home, err := os.UserHomeDir(); err == nil {
			cwd = home
		} else {
			cwd = "."
		}
	}
	chat, err := c.StartInteractiveChat(a.ctx, agentID, cli, cwd)
	if err != nil {
		return nil, err
	}
	return redactChatCredential(chat), nil
}

// SendChat sends one message into an interactive chat and returns the
// assistant's reply. The agent's instructions frame the first turn of a
// fresh session automatically.
func (a *App) SendChat(chatID, message string) (*core.ChatTurn, error) {
	c, err := a.requireCore()
	if err != nil {
		return nil, err
	}
	chat, err := c.GetChat(a.ctx, chatID)
	if err != nil {
		return nil, err
	}
	systemPrompt := ""
	if chat.AgentID != "" {
		if agent, err := c.GetAgent(a.ctx, chat.AgentID); err == nil {
			systemPrompt = core.AgentSystemPrompt(agent)
		}
	}
	if prefix := core.ResolveSkillsPrefix(chat.Settings.Skills); prefix != "" {
		if systemPrompt != "" {
			systemPrompt = prefix + "\n\n---\n\n" + systemPrompt
		} else {
			systemPrompt = prefix
		}
	}
	return c.ContinueChat(a.ctx, chatID, message, chat.WorkspacePath, systemPrompt)
}

func (a *App) ChatMessages(chatID string) ([]core.Message, error) {
	if a.detachedClient != nil {
		return a.detachedClient.chatMessages(chatID)
	}
	c, err := a.requireCore()
	if err != nil {
		return nil, err
	}
	return c.ListMessages(a.ctx, chatID, 0)
}

func (a *App) DeleteChat(chatID string) error {
	c, err := a.requireCore()
	if err != nil {
		return err
	}
	if err := c.DeleteChat(a.ctx, chatID); err != nil {
		return err
	}
	return nil
}

// --- Clean chats (CLI + model, no agent) ----------------------------------

// CLIInfo is shared with the Studio frontend.
type CLIInfo = core.CLIInfo

func (a *App) ListCLIs() []CLIInfo {
	refreshManagedPaths()
	return core.ListCLIs(a.ctx)
}

func (a *App) ListCLIModels(cli string) []string {
	return a.cliModels(cli, false)
}

func (a *App) RefreshCLIModels(cli string) []string {
	return a.cliModels(cli, true)
}

func (a *App) cliModels(cli string, force bool) []string {
	if cli == "praimate-cli" {
		if c, err := a.requireCore(); err == nil {
			if assigned, err := c.NativeModelAssignments(a.ctx); err == nil && len(assigned) > 0 {
				models := make([]string, 0, len(assigned))
				for _, item := range assigned {
					models = append(models, item.HostID+"::"+item.Model)
				}
				return models
			}
			var models []string
			var err error
			if force {
				models, err = c.RefreshNativeModels(a.ctx, "")
			} else {
				models, err = c.NativeModels(a.ctx, "")
			}
			if err == nil {
				return models
			}
		}
	}
	if force {
		return core.RefreshCLIModels(a.ctx, cli)
	}
	return core.ListCLIModels(a.ctx, cli)
}

func parseCodexDebugModels(raw []byte) []string {
	return core.ParseCodexDebugModels(raw)
}

// StartCleanChat creates an agent-less chat on a CLI with an optional
// pinned model. The frontend drives it with SendChat like any other
// chat; SendChat finds no AgentID and sends no system prompt.
func (a *App) StartCleanChat(cli, model, cwd string) (*core.Chat, error) {
	c, err := a.requireCore()
	if err != nil {
		return nil, err
	}
	if cwd == "" {
		if home, err := os.UserHomeDir(); err == nil {
			cwd = home
		} else {
			cwd = "."
		}
	}
	chat, err := c.StartCleanChat(a.ctx, cli, model, cwd)
	if err != nil {
		return nil, err
	}
	return redactChatCredential(chat), nil
}

// --- Legacy workspace chats ------------------------------------------------

// WorkspaceChatInfo is one legacy on-disk chat that the GUI can reopen
// in the Code terminal. Its workpath and sandbox remain on disk.
type WorkspaceChatInfo struct {
	ID       string    `json:"id"`
	Label    string    `json:"label"`
	Agent    string    `json:"agent"`
	Template string    `json:"template"`
	LastUsed time.Time `json:"lastUsed"`
	Sandbox  string    `json:"sandbox"`
}

// ListWorkspaceChats lists legacy on-disk chats (newest first).
// It returns an empty list when no workspaces root is configured.
func (a *App) ListWorkspaceChats() ([]WorkspaceChatInfo, error) {
	c, err := a.requireCore()
	if err != nil {
		return nil, err
	}
	chats, err := c.ListLegacyChats(a.ctx)
	if err != nil {
		return []WorkspaceChatInfo{}, nil // unconfigured root = empty, not an error
	}
	out := make([]WorkspaceChatInfo, 0, len(chats))
	for _, ch := range chats {
		out = append(out, WorkspaceChatInfo{
			ID: ch.ID, Label: ch.Label, Agent: string(ch.AgentID),
			Template: ch.Template, LastUsed: ch.LastUsed, Sandbox: ch.SandboxDir,
		})
	}
	return out, nil
}

// OpenWorkspaceChatResult is what OpenWorkspaceChat hands the frontend
// so the Code page can attach to the already-running terminal.
type OpenWorkspaceChatResult struct {
	TermID string `json:"termId"`
	CLI    string `json:"cli"`
	Cwd    string `json:"cwd"`
	Label  string `json:"label"`
	Note   string `json:"note"`
}

// OpenWorkspaceChat relaunches a legacy chat's CLI inside the GUI's Code
// terminal: same sandbox, same agent CLI, native session resume where
// the CLI supports it (claude/openclaude/codex).
func (a *App) OpenWorkspaceChat(chatID string) (*OpenWorkspaceChatResult, error) {
	c, err := a.requireCore()
	if err != nil {
		return nil, err
	}
	root := c.WorkspacesRoot()
	if root == "" {
		return nil, fmt.Errorf("no workspaces root configured; complete first-run setup")
	}
	chat, err := launcher.LoadChat(root, chatID)
	if err != nil || chat == nil {
		return nil, fmt.Errorf("load workspace chat %q: %w", chatID, err)
	}
	var agent *launcher.Agent
	for _, ag := range launcher.KnownAgents() {
		if ag.ID == chat.AgentID {
			ag := ag
			agent = &ag
			break
		}
	}
	if agent == nil {
		return nil, fmt.Errorf("unknown agent %q on chat %q", chat.AgentID, chatID)
	}
	name, args, err := terminalCommand(string(chat.AgentID), "")
	if err != nil {
		return nil, err
	}
	resume := launcher.RestoreNativeSession(*agent, *chat)
	args = append(args, resume.Args...)
	termID, err := a.terms.start(name, args, chat.SandboxDir, nil, a.emitTerminalEvent)
	if err != nil {
		return nil, err
	}
	if err := a.terms.bindChat(termID, chatID); err != nil {
		a.terms.close(termID)
		return nil, err
	}
	return &OpenWorkspaceChatResult{
		TermID: termID,
		CLI:    string(chat.AgentID),
		Cwd:    chat.SandboxDir,
		Label:  chat.Label,
		Note:   resume.Note,
	}, nil
}

// ListTerminalSessions returns the live PTYs the GUI is managing. The
// Sessions panel uses this to offer "resume" for terminal chats whose
// PTY is still alive (instead of starting a duplicate).
func (a *App) ListTerminalSessions() []TermInfo {
	if a.terms == nil {
		return nil
	}
	return a.terms.list()
}

// --- Agents ----------------------------------------------------------------

// hiddenAgentIDs are built-in agents that exist in the DB (so the studio
// helper / installer pipelines can use them) but should NOT show up in
// the GUI's Agents list — embedding-only agents the user shouldn't open.
var hiddenAgentIDs = map[string]bool{
	"agent-builder": true, // drives the New-Agent studio's authoring assistant
}

func (a *App) ListAgents() ([]core.Agent, error) {
	if a.detachedClient != nil {
		return a.detachedClient.listAgents()
	}
	c, err := a.requireCore()
	if err != nil {
		return nil, err
	}
	all, err := c.ListAgents(a.ctx)
	if err != nil {
		return nil, err
	}
	out := all[:0]
	for _, ag := range all {
		if hiddenAgentIDs[ag.ID] {
			continue
		}
		out = append(out, ag)
	}
	return out, nil
}

// ImportAgentDialog opens a native file picker and imports the chosen
// agent — bare YAML or a .praimate-agent knowledge pack. Returns the
// imported agent, or nil if the user cancelled.
func (a *App) ImportAgentDialog() (*core.Agent, error) {
	c, err := a.requireCore()
	if err != nil {
		return nil, err
	}
	path, err := wruntime.OpenFileDialog(a.ctx, wruntime.OpenDialogOptions{
		Title: "Import agent (Markdown, YAML or pack)",
		Filters: []wruntime.FileFilter{
			{DisplayName: "Agents", Pattern: "*.md;*.markdown;*.yaml;*.yml;*" + core.AgentPackExt + ";*.zip"},
		},
	})
	if err != nil || path == "" {
		return nil, err
	}
	return c.ImportAgentAuto(a.ctx, path)
}

// ReviewAgentImportDialog returns an immutable pack inventory for one explicit
// host review. Bare YAML remains a compatibility import and carries no bundled
// code or trust decision.
func (a *App) ReviewAgentImportDialog() (*core.AgentPackReview, error) {
	c, err := a.requireCore()
	if err != nil {
		return nil, err
	}
	path, err := wruntime.OpenFileDialog(a.ctx, wruntime.OpenDialogOptions{
		Title:   "Import agent",
		Filters: []wruntime.FileFilter{{DisplayName: "Agents", Pattern: "*.md;*.markdown;*.yaml;*.yml;*" + core.AgentPackExt + ";*.zip"}},
	})
	if err != nil || path == "" {
		return nil, err
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case core.AgentPackExt, ".zip":
		return c.InspectAgentPack(a.ctx, path)
	default:
		agent, err := c.ImportAgentAuto(a.ctx, path)
		if err != nil {
			return nil, err
		}
		return &core.AgentPackReview{Agent: agent}, nil
	}
}

func (a *App) ImportReviewedAgentPack(path, reviewDigest string) (*core.Agent, error) {
	c, err := a.requireCore()
	if err != nil {
		return nil, err
	}
	return c.ImportReviewedAgentPack(a.ctx, path, reviewDigest)
}

// dirExists reports whether path is an existing directory.
func dirExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// ExportAgentDialog opens a native save dialog and writes the agent
// YAML there. Returns the chosen path ("" if cancelled).
func (a *App) ExportAgentDialog(agentID string) (string, error) {
	c, err := a.requireCore()
	if err != nil {
		return "", err
	}
	path, err := wruntime.SaveFileDialog(a.ctx, wruntime.SaveDialogOptions{
		Title:           "Export agent (YAML or Markdown)",
		Filters:         []wruntime.FileFilter{{DisplayName: "Agent YAML", Pattern: "*.yaml;*.yml"}, {DisplayName: "OpenCode-compatible Markdown", Pattern: "*.md"}},
		DefaultFilename: agentID + ".yaml",
	})
	if err != nil || path == "" {
		return "", err
	}
	if err := c.ExportAgent(a.ctx, agentID, path); err != nil {
		return "", err
	}
	return path, nil
}

func (a *App) DeleteAgent(agentID string) error {
	c, err := a.requireCore()
	if err != nil {
		return err
	}
	return c.DeleteAgent(a.ctx, agentID)
}

// --- Run (workflow execution) ---------------------------------------------

// PickFolder opens a directory picker for the run cwd.
func (a *App) PickFolder() (string, error) {
	return wruntime.OpenDirectoryDialog(a.ctx, wruntime.OpenDialogOptions{
		Title: "Choose working folder",
	})
}

// TurnEvent is the payload emitted on the "praimate:turn" event while
// a workflow run is in flight.
type TurnEvent struct {
	RunID        string `json:"run_id,omitempty"`
	Index        int    `json:"index"`
	WorkflowName string `json:"workflow_name,omitempty"`
	UserMsg      string `json:"user_msg"`
	Reply        string `json:"reply"`
	DurationMs   int64  `json:"duration_ms"`
}

// WorkflowStreamEvent is emitted on "praimate:workflow-stream" while a
// workflow run is active.
type WorkflowStreamEvent struct {
	RunID        string `json:"run_id,omitempty"`
	WorkflowName string `json:"workflow_name"`
	TurnIndex    int    `json:"turn_index"`
	Type         string `json:"type"`
	Text         string `json:"text,omitempty"`
	Tool         string `json:"tool,omitempty"`
	Detail       string `json:"detail,omitempty"`
	ID           string `json:"id,omitempty"`
	OK           bool   `json:"ok,omitempty"`
}

// RunResult mirrors core.RunResult in a JSON-friendly shape.
type RunResult struct {
	Outcome   string      `json:"outcome"`
	Error     string      `json:"error,omitempty"`
	ChatID    string      `json:"chat_id,omitempty"`
	Turns     []TurnEvent `json:"turns"`
	SessionID string      `json:"session_id,omitempty"`
}

// RunWorkflow executes one agent workflow synchronously (the JS
// promise resolves when the run completes); per-turn progress streams
// via the "praimate:turn" event.
func (a *App) RunWorkflow(agentID, workflowName, cli, model, cwd string, inputs map[string]string, localEndpoint, _ string, localModel string) (*RunResult, error) {
	return a.runWorkflow(a.ctx, "", agentID, workflowName, cli, model, cwd, inputs, localEndpoint, localModel, "")
}

func (a *App) runWorkflow(ctx context.Context, runID, agentID, workflowName, cli, model, cwd string, inputs map[string]string, localEndpoint, localModel, tools string) (*RunResult, error) {
	c, err := a.requireCore()
	if err != nil {
		return nil, err
	}
	agent, err := c.GetAgent(ctx, agentID)
	if err != nil {
		return nil, err
	}
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return nil, fmt.Errorf("a working folder is required")
	}
	settings := workflowChatSettings(model, localEndpoint, localModel)
	if tools == "agent" {
		config, configErr := c.ResolveEffectiveAgentConfig(ctx, agent)
		if configErr != nil {
			return nil, configErr
		}
		tools = config.DefaultTools
	}
	settings.Tools = tools

	res := c.RunWorkflow(ctx, core.RunOptions{
		Agent:        agent,
		WorkflowName: workflowName,
		Inputs:       inputs,
		CLI:          cli,
		Cwd:          cwd,
		Model:        model,
		Tools:        settings.Tools,
		Persist:      true,
		ChatTitle:    agent.Name + " · " + workflowName,
		ChatSettings: settings,
		OnTurn: func(t core.TurnResult) {
			a.emitUIEvent("praimate:turn", TurnEvent{
				RunID:        runID,
				Index:        t.Index,
				WorkflowName: t.WorkflowName,
				UserMsg:      t.UserMsg,
				Reply:        replyText(t.Reply),
				DurationMs:   t.DurationMs,
			})
		},
		OnEvent: func(ev core.WorkflowRunEvent) {
			a.emitUIEvent("praimate:workflow-stream", WorkflowStreamEvent{
				RunID:        runID,
				WorkflowName: ev.WorkflowName,
				TurnIndex:    ev.TurnIndex,
				Type:         ev.Type,
				Text:         ev.Text,
				Tool:         ev.Tool,
				Detail:       ev.Detail,
				ID:           ev.ID,
				OK:           ev.OK,
			})
		},
	})

	out := guiRunResult(res)
	return out, nil
}

// RunAllWorkflows executes every workflow on the agent in declaration
// order, sharing one resumable CLI session across the sequence.
func (a *App) RunAllWorkflows(agentID, cli, model, cwd string, inputsByWorkflow map[string]map[string]string, localEndpoint, _ string, localModel string) (*RunResult, error) {
	return a.runAllWorkflows(a.ctx, "", agentID, cli, model, cwd, inputsByWorkflow, localEndpoint, localModel, "")
}

func (a *App) runAllWorkflows(ctx context.Context, runID, agentID, cli, model, cwd string, inputsByWorkflow map[string]map[string]string, localEndpoint, localModel, tools string) (*RunResult, error) {
	c, err := a.requireCore()
	if err != nil {
		return nil, err
	}
	agent, err := c.GetAgent(ctx, agentID)
	if err != nil {
		return nil, err
	}
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return nil, fmt.Errorf("a working folder is required")
	}
	settings := workflowChatSettings(model, localEndpoint, localModel)
	if tools == "agent" {
		config, configErr := c.ResolveEffectiveAgentConfig(ctx, agent)
		if configErr != nil {
			return nil, configErr
		}
		tools = config.DefaultTools
	}
	settings.Tools = tools

	res := c.RunAllWorkflows(ctx, core.RunAllOptions{
		Agent:            agent,
		InputsByWorkflow: inputsByWorkflow,
		CLI:              cli,
		Cwd:              cwd,
		Model:            model,
		Tools:            settings.Tools,
		Persist:          true,
		ChatTitle:        agent.Name + " · all workflows",
		ChatSettings:     settings,
		OnTurn: func(t core.TurnResult) {
			a.emitUIEvent("praimate:turn", TurnEvent{
				RunID:        runID,
				Index:        t.Index,
				WorkflowName: t.WorkflowName,
				UserMsg:      t.UserMsg,
				Reply:        replyText(t.Reply),
				DurationMs:   t.DurationMs,
			})
		},
		OnEvent: func(ev core.WorkflowRunEvent) {
			a.emitUIEvent("praimate:workflow-stream", WorkflowStreamEvent{
				RunID:        runID,
				WorkflowName: ev.WorkflowName,
				TurnIndex:    ev.TurnIndex,
				Type:         ev.Type,
				Text:         ev.Text,
				Tool:         ev.Tool,
				Detail:       ev.Detail,
				ID:           ev.ID,
				OK:           ev.OK,
			})
		},
	})
	out := guiRunResult(res)
	return out, nil
}

func workflowChatSettings(model, localEndpoint, localModel string) core.ChatSettings {
	settings := core.ChatSettings{Surface: "workflow", Model: model}
	if strings.TrimSpace(localEndpoint) != "" {
		settings.Local = &core.ChatLocalEndpoint{Endpoint: localEndpoint, Model: localModel}
	}
	return settings
}

func guiRunResult(res *core.RunResult) *RunResult {
	out := &RunResult{
		Outcome:   string(res.Outcome),
		ChatID:    res.ChatID,
		SessionID: res.SessionID,
	}
	if res.Err != nil {
		out.Error = res.Err.Error()
	}
	for _, t := range res.Turns {
		out.Turns = append(out.Turns, TurnEvent{
			Index:        t.Index,
			WorkflowName: t.WorkflowName,
			UserMsg:      t.UserMsg,
			Reply:        replyText(t.Reply),
			DurationMs:   t.DurationMs,
		})
	}
	return out
}

func replyText(reply *core.Reply) string {
	if reply == nil {
		return ""
	}
	return reply.Text
}

// PrivacyPreview scans the would-be outbound text and returns category
// counts so the frontend can render a review sheet without ever
// echoing the secret values.
func (a *App) PrivacyPreview(text string) (map[string]int, error) {
	c, err := a.requireCore()
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	for _, m := range c.PrivacyScanner().Match(text) {
		counts[string(m.Category)]++
	}
	return counts, nil
}

// --- MCP ---------------------------------------------------------------------

func (a *App) MCPCatalogue() []core.MCPCatalogueEntry {
	entries := core.ListMCPCatalogue()
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	return entries
}

func (a *App) MCPServers() ([]core.MCPServer, error) {
	c, err := a.requireCore()
	if err != nil {
		return nil, err
	}
	return c.ListMCPServers(a.ctx, false)
}

// AddCustomMCP registers a user-defined MCP server (local or remote)
// that isn't in the catalogue — e.g. hexstrike-ai. envText is
// newline/comma-separated KEY=VALUE pairs.
func (a *App) AddCustomMCP(name, transport, command, url, envText string) (*core.MCPServer, error) {
	c, err := a.requireCore()
	if err != nil {
		return nil, err
	}
	return c.AddCustomMCP(a.ctx, core.AddCustomMCPRequest{
		Name:      name,
		Transport: transport,
		Command:   command,
		URL:       url,
		Env:       core.ParseEnvLines(envText),
	})
}

// UpdateMCPServer edits an MCP server while preserving its stable ID and
// enabled state.
func (a *App) UpdateMCPServer(id, name, transport, command, url, envText string) (*core.MCPServer, error) {
	c, err := a.requireCore()
	if err != nil {
		return nil, err
	}
	return c.UpdateMCPServer(a.ctx, id, core.AddCustomMCPRequest{
		Name:      name,
		Transport: transport,
		Command:   command,
		URL:       url,
		Env:       core.ParseEnvLines(envText),
	})
}

func (a *App) SetMCPEnabled(id string, enabled bool) error {
	c, err := a.requireCore()
	if err != nil {
		return err
	}
	return c.SetMCPEnabled(a.ctx, id, enabled)
}

func (a *App) DeleteMCPServer(id string) error {
	c, err := a.requireCore()
	if err != nil {
		return err
	}
	return c.DeleteMCPServer(a.ctx, id)
}

// --- Automation (watchers + schedules) --------------------------------------

func (a *App) ListWatchers() ([]core.Watcher, error) {
	c, err := a.requireCore()
	if err != nil {
		return nil, err
	}
	return c.ListWatchers(a.ctx, false)
}

func (a *App) AddWatcher(agentID, path, workflow string, patterns []string) (int64, error) {
	c, err := a.requireCore()
	if err != nil {
		return 0, err
	}
	id, err := c.AddWatcher(a.ctx, core.AddWatcherRequest{
		AgentID: agentID, Path: path, Workflow: workflow, Patterns: patterns,
	})
	if err == nil {
		a.restartWatchers()
	}
	return id, err
}

func (a *App) SetWatcherEnabled(id int64, enabled bool) error {
	c, err := a.requireCore()
	if err != nil {
		return err
	}
	if err := c.SetWatcherEnabled(a.ctx, id, enabled); err != nil {
		return err
	}
	a.restartWatchers()
	return nil
}

func (a *App) DeleteWatcher(id int64) error {
	c, err := a.requireCore()
	if err != nil {
		return err
	}
	if err := c.DeleteWatcher(a.ctx, id); err != nil {
		return err
	}
	a.restartWatchers()
	return nil
}

// restartWatchers rebuilds the fsnotify daemon after watcher rows
// change so newly authored paths are observed immediately.
func (a *App) restartWatchers() {
	if a.core == nil {
		return
	}
	a.daemonMu.Lock()
	defer a.daemonMu.Unlock()
	if a.watchers != nil {
		a.watchers.Stop()
		a.watchers = nil
	}
	a.watchers, _ = a.core.StartWatcherDaemon(context.Background(), core.WatcherDaemonOptions{
		WatcherDispatchOptions: core.WatcherDispatchOptions{CLI: "claude"},
	})
}

func (a *App) ListSchedules() ([]core.Schedule, error) {
	c, err := a.requireCore()
	if err != nil {
		return nil, err
	}
	return c.ListSchedules(a.ctx, false)
}

func (a *App) AddCronSchedule(agentID, cron, workflow string) (int64, error) {
	c, err := a.requireCore()
	if err != nil {
		return 0, err
	}
	return c.AddSchedule(a.ctx, core.AddScheduleRequest{
		AgentID: agentID, Cron: cron, Workflow: workflow,
	})
}

func (a *App) SetScheduleEnabled(id int64, enabled bool) error {
	c, err := a.requireCore()
	if err != nil {
		return err
	}
	return c.SetScheduleEnabled(a.ctx, id, enabled)
}

func (a *App) DeleteSchedule(id int64) error {
	c, err := a.requireCore()
	if err != nil {
		return err
	}
	return c.DeleteSchedule(a.ctx, id)
}

// --- Privacy patterns --------------------------------------------------------

func (a *App) ListPrivacyPatterns() ([]string, error) {
	c, err := a.requireCore()
	if err != nil {
		return nil, err
	}
	return c.ListPrivacyPatterns(a.ctx)
}

func (a *App) AddPrivacyPattern(pattern string) error {
	c, err := a.requireCore()
	if err != nil {
		return err
	}
	return c.AddPrivacyPattern(a.ctx, pattern)
}

func (a *App) DeletePrivacyPattern(index int) error {
	c, err := a.requireCore()
	if err != nil {
		return err
	}
	return c.DeletePrivacyPattern(a.ctx, index)
}

// --- GUI settings -----------------------------------------------------------

func (a *App) GetGUISetting(key string) (string, error) {
	c, err := a.requireCore()
	if err != nil {
		return "", err
	}
	raw, err := c.GetSetting(a.ctx, core.ScopeGUI, key)
	if err != nil || raw == nil {
		return "", err
	}
	return string(raw), nil
}

func (a *App) SetGUISetting(key, valueJSON string) error {
	c, err := a.requireCore()
	if err != nil {
		return err
	}
	return c.SetSetting(a.ctx, core.ScopeGUI, key, []byte(valueJSON))
}

// RenameChat changes the title of an existing chat.
func (a *App) RenameChat(chatID, newTitle string) error {
	c, err := a.requireCore()
	if err != nil {
		return err
	}
	return c.RenameChat(a.ctx, chatID, newTitle)
}
