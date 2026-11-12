// Package core is the single API surface used by the GUI and the
// maintenance CLI. No
// other package may import internal/launcher, internal/ollama, etc.
// directly; everything flows through Core.
//
// Today this package is a thin facade that delegates to
// internal/launcher. Over Phase 2+ the facade hardens and the
// launcher's exported surface shrinks until it is only consumed
// through Core.
//
// Core owns no concurrency primitives of its own. Callers are expected
// to be Wails-binding goroutines or synchronous maintenance CLI calls.
package core

import (
	"context"
	"errors"
	"sync"

	"github.com/sPROFFEs/PrAImate/internal/artifacts"
	"github.com/sPROFFEs/PrAImate/internal/launcher"
	"github.com/sPROFFEs/PrAImate/internal/store"
)

// Core is the facade. Constructed once at process start and passed to
// every page or command that needs to read or mutate state.
type Core struct {
	store *store.Store

	privacy *PrivacyScanner

	// workspacesRoot is the legacy <root>/chats/... layout used by
	// internal/launcher today. Phase 1 keeps using this so the existing
	// TUI keeps working unchanged; Phase 2+ will route writes through
	// the DB and leave only transcript slices on disk.
	workspacesRoot string

	// approvalProvider, when set (GUI only), supplies the per-chat
	// approval shim wiring for the "ask" Tools level. Nil = "ask"
	// degrades to the safe default. See SetApprovalProvider.
	approvalProvider func(chatID string) *ApprovalConfig

	managedMu        sync.Mutex
	managedActive    map[string]bool
	nativeLimits     nativeLimitCache
	nativeCatalogue  modelCatalogueCache
	cliTLSMu         sync.Mutex
	cliTLSBridge     *cliHostTLSBridge
	cliTLSClosed     bool
	artifactMu       sync.Mutex
	artifacts        *artifacts.Service
	artifactCancel   context.CancelFunc
	artifactProgress *artifacts.Progress
	artifactDone     chan struct{}
	artifactClosed   bool
}

// ApprovalConfig tells a CLI adapter how to spawn the approval shim —
// the tiny MCP server that forwards the CLI's mid-turn permission
// requests to the GUI's Allow/Deny dialog. Command+Args typically point
// back at the calling binary with a hidden flag (the endpoint URL and
// auth token ride in Args).
type ApprovalConfig struct {
	Command string
	Args    []string
	// Request is used by PrAImate's managed runtime. It blocks until the GUI
	// answers and fails closed when the broker is unavailable. Native Claude
	// runs continue using Command+Args through the approval MCP shim.
	Request func(ctx context.Context, tool string, input map[string]any) (bool, error)
}

// SetApprovalProvider registers the factory the chat layer calls when a
// chat with Tools=="ask" runs a turn. The GUI registers one at startup;
// the TUI leaves it nil ("ask" then behaves as safe — terminal chats
// already get the CLI's own native prompts).
func (c *Core) SetApprovalProvider(fn func(chatID string) *ApprovalConfig) {
	c.approvalProvider = fn
}

type approvalContextKey struct{}

// WithApprovalProvider scopes a frontend's broker to this execution, without
// replacing the desktop broker or another Studio window's permission handler.
func WithApprovalProvider(ctx context.Context, fn func(string) *ApprovalConfig) context.Context {
	return context.WithValue(ctx, approvalContextKey{}, fn)
}

func (c *Core) approvalForContext(ctx context.Context, scope string) *ApprovalConfig {
	if fn, ok := ctx.Value(approvalContextKey{}).(func(string) *ApprovalConfig); ok {
		return fn(scope)
	}
	if c.approvalProvider != nil {
		return c.approvalProvider(scope)
	}
	return nil
}

// Options bundles the inputs Core needs at construction time. Each is
// optional; leaving them zero gives a no-DB, launcher-only Core that
// behaves the same as today's TUI.
type Options struct {
	// Store, if non-nil, becomes the DB-backed source of truth for
	// agents, MCP, schedules, and watchers.
	Store *store.Store
	// WorkspacesRoot is the legacy on-disk workspace directory. Phase 1
	// continues to honour it so chats keep launching.
	WorkspacesRoot string
}

// New builds a Core. Either Store or WorkspacesRoot (or both) may be
// set. Passing neither returns an error because a Core that cannot
// read anything is useless.
func New(opts Options) (*Core, error) {
	if opts.Store == nil && opts.WorkspacesRoot == "" {
		return nil, errors.New("core.New: need Store or WorkspacesRoot")
	}
	c := &Core{
		store:          opts.Store,
		privacy:        NewPrivacyScanner(),
		workspacesRoot: opts.WorkspacesRoot,
		managedActive:  map[string]bool{},
	}
	c.loadPrivacyPatterns(context.Background())
	return c, nil
}

// Close releases any resources Core owns. The store is NOT closed here
// — callers own its lifetime since they supplied it.
func (c *Core) Close() error { return c.closeCLIHostTLS() }

// Store exposes the underlying store, if any. Used by tests and by
// migrations that haven't grown a typed Core method yet.
func (c *Core) Store() *store.Store { return c.store }

// WorkspacesRoot returns the on-disk workspaces directory the legacy
// launcher uses. Empty if Core was built without one.
func (c *Core) WorkspacesRoot() string { return c.workspacesRoot }

// PrivacyScanner exposes the process-wide scanner so settings screens
// can add custom patterns before workflow runs.
func (c *Core) PrivacyScanner() *PrivacyScanner {
	if c.privacy == nil {
		c.privacy = NewPrivacyScanner()
	}
	return c.privacy
}

// --- Legacy chat list ---------------------------------------------------

// ListLegacyChats returns every chat in the legacy on-disk workspaces
// layout (the workspaces-root scheme that predates the DB-backed chats
// added in Phase 3c). Used by the existing TUI's chat list.
//
// New code should use ListChats() instead, which reads from the
// chats table and is what Recipes / workflow runs persist to.
func (c *Core) ListLegacyChats(ctx context.Context) ([]launcher.Chat, error) {
	if c.workspacesRoot == "" {
		return nil, errors.New("core.ListLegacyChats: workspaces root not configured")
	}
	return launcher.ListChats(c.workspacesRoot)
}

// --- Agents (Phase 2 placeholder) ---------------------------------------

// ListAgents returns the user's agent catalogue from the DB. Phase 1
// returns an empty slice; Phase 2 wires it up.
func (c *Core) ListAgents(ctx context.Context) ([]Agent, error) {
	if c.store == nil {
		return nil, nil
	}
	return c.listAgentsFromDB(ctx)
}
