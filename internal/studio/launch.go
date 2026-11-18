package studio

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/sPROFFEs/PrAImate/internal/core"
	"github.com/sPROFFEs/PrAImate/internal/installer"
	"github.com/sPROFFEs/PrAImate/internal/orchestrator"
)

var desktopServers struct {
	sync.Mutex
	core            *core.Core
	server          *Server
	endpoint, token string
	window          *DesktopWindowHooks
	workers         *orchestrator.Manager
	assistant       *AssistantHooks
	assistantCore   *core.Core
}

// SetDesktopWorkerManager shares live worker chats between Wails and Studio.
func SetDesktopWorkerManager(c *core.Core, manager *orchestrator.Manager) {
	desktopServers.Lock()
	defer desktopServers.Unlock()
	desktopServers.core = c
	desktopServers.workers = manager
	if desktopServers.server != nil && desktopServers.server.core == c {
		desktopServers.server.workers = manager
	}
}

// SetDesktopWindowHooks attaches the Wails window controller to the shared
// authenticated Studio server. It is intentionally separate from Core.
func SetDesktopWindowHooks(c *core.Core, hooks *DesktopWindowHooks) {
	desktopServers.Lock()
	defer desktopServers.Unlock()
	desktopServers.window = hooks
	if desktopServers.server != nil && desktopServers.server.core == c {
		desktopServers.server.setDesktopWindowHooks(hooks)
	}
}

func desktopEndpoint(c *core.Core) (string, string, error) {
	desktopServers.Lock()
	defer desktopServers.Unlock()
	if desktopServers.server != nil && desktopServers.server.core == c {
		return desktopServers.endpoint, desktopServers.token, nil
	}
	srv := NewServer(c)
	if desktopServers.assistantCore == c {
		srv.assistant = desktopServers.assistant
	}
	if desktopServers.workers != nil && desktopServers.core == c {
		srv.workers = desktopServers.workers
	}
	srv.setDesktopWindowHooks(desktopServers.window)
	endpoint, token, err := srv.StartLocal()
	if err != nil {
		return "", "", err
	}
	if desktopServers.server != nil {
		_ = desktopServers.server.Close()
	}
	desktopServers.core, desktopServers.server, desktopServers.endpoint, desktopServers.token = c, srv, endpoint, token
	return endpoint, token, nil
}

func backendExecutable() (string, error) {
	return installer.ResolvePraimateBinary()
}

func studioEnvironment(base []string, overrides map[string]string) []string {
	out := make([]string, 0, len(base)+len(overrides))
	for _, item := range base {
		key, _, _ := strings.Cut(item, "=")
		replaced := false
		for candidate := range overrides {
			if strings.EqualFold(key, candidate) {
				replaced = true
				break
			}
		}
		if !replaced {
			out = append(out, item)
		}
	}
	for key, value := range overrides {
		// Electron on Windows tests whether ELECTRON_RUN_AS_NODE exists,
		// even when empty. Empty overrides remove inherited startup flags.
		if value != "" {
			out = append(out, key+"="+value)
		}
	}
	return out
}

// Generated launchers must treat directory names as data, including quotes and
// shell substitutions in a user-selected extensions path.
func quoteEditorShellArgument(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

// Keep startup diagnostics bounded while a long-lived editor runs. Read only
// after Wait returns, when exec has finished copying the stderr pipe.
type editorStartupOutput struct{ data []byte }

func (b *editorStartupOutput) Write(p []byte) (int, error) {
	n := len(p)
	remaining := 2048 - len(b.data)
	if len(p) > remaining {
		p = p[:remaining]
	}
	b.data = append(b.data, p...)
	return n, nil
}

func startEditor(cmd *exec.Cmd) error {
	output := &editorStartupOutput{}
	cmd.Stderr = output
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	timer := time.NewTimer(750 * time.Millisecond)
	defer timer.Stop()
	select {
	case err := <-done:
		if err != nil {
			if details := strings.TrimSpace(string(output.data)); details != "" {
				return fmt.Errorf("editor exited during startup: %w: %s", err, details)
			}
			return fmt.Errorf("editor exited during startup: %w", err)
		}
		// CLI launchers may successfully hand off to an existing GUI process.
		return nil
	case <-timer.C:
		// A running process is not proof that a window opened. The goroutine
		// still reaps it on exit without blocking the application's UI.
		return nil
	}
}

func launchConfiguration(opts LaunchOptions, workspace string) string {
	cli, tools := opts.CLI, opts.Tools
	if cli == "" {
		cli = "praimate-code"
	}
	if tools == "" {
		tools = "safe"
	}
	body, _ := json.Marshal(sessionConfig{CLI: cli, Tools: tools, Model: opts.Model, AgentID: opts.AgentID,
		Workspace: workspace, LocalEndpoint: opts.LocalEndpoint, LocalModel: opts.LocalModel, MCPServers: opts.MCPServers})
	return string(body)
}

func extensionFiles() map[string]string {
	return map[string]string{"package.json": ExtensionPackageJSON, "extension.js": ExtensionJS, "rpc.js": ExtensionRPCJS, "terminal.js": ExtensionTerminalJS,
		"resources/chat.html": ExtensionChatHTML, "resources/assistant.js": ExtensionAssistantJS, "resources/monke-mascot.png": ExtensionMascot, "resources/monke.svg": ExtensionSVG}
}
func extensionCurrent(dir string) bool {
	for name, want := range extensionFiles() {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return false
		}
		if name == "package.json" {
			var actual, expected map[string]any
			if json.Unmarshal(data, &actual) != nil || json.Unmarshal([]byte(want), &expected) != nil {
				return false
			}
			// The editor adds installation metadata and reformats package.json.
			delete(actual, "__metadata")
			if !reflect.DeepEqual(actual, expected) {
				return false
			}
		} else if string(data) != want {
			return false
		}
	}
	return true
}
func provisionExtension(dir string) error {
	if extensionCurrent(dir) {
		return nil
	}
	if err := os.MkdirAll(filepath.Join(dir, "resources"), 0755); err != nil {
		return err
	}
	for name, body := range extensionFiles() {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0644); err != nil {
			return err
		}
	}
	return nil
}

// Older Studio builds bundled a second copy inside Code-OSS. Upgrade it too:
// an existing extension host may resolve that copy instead of the user copy.
// Never modify extensions in a system-installed editor.
func extensionLocations() ([]string, error) {
	ext, err := ExtensionsDir()
	if err != nil {
		return nil, err
	}
	app, err := AppDir()
	if err != nil {
		return nil, err
	}
	primary, _, err := registeredExtension(ext)
	if err != nil {
		return nil, err
	}
	dirs := []string{primary}
	legacy := filepath.Join(ext, "praimate")
	if primary != legacy {
		if _, err := os.Stat(filepath.Join(legacy, "package.json")); err == nil {
			dirs = append(dirs, legacy)
		}
	}
	roots := []string{app}
	entries, _ := os.ReadDir(app)
	for _, entry := range entries {
		if entry.IsDir() {
			roots = append(roots, filepath.Join(app, entry.Name()))
		}
	}
	for _, root := range roots {
		dir := filepath.Join(root, "resources", "app", "extensions", "praimate")
		body, err := os.ReadFile(filepath.Join(dir, "package.json"))
		var manifest struct{ Name string }
		if err == nil && json.Unmarshal(body, &manifest) == nil && manifest.Name == "praimate-studio" {
			dirs = append(dirs, dir)
		}
	}
	return dirs, nil
}

func provisionExtensions() error {
	dirs, err := extensionLocations()
	if err != nil {
		return err
	}
	for _, dir := range dirs {
		if err := provisionExtension(dir); err != nil {
			return err
		}
	}
	return nil
}

// Code-OSS keeps its first process environment across launches, including after
// Desktop restarts. The private, atomically replaced descriptor lets both old
// and new windows discover the current backend without logging credentials.
func publishConnection(endpoint, token, backend, config string) error {
	dirs, err := extensionLocations()
	if err != nil {
		return err
	}
	body, err := json.Marshal(map[string]any{"endpoint": endpoint, "token": token, "backend": backend,
		"config": json.RawMessage(config), "launchId": fmt.Sprint(time.Now().UnixNano())})
	if err != nil {
		return err
	}
	for _, dir := range dirs {
		if err := writeConnection(dir, body); err != nil {
			return err
		}
	}
	return nil
}

func writeConnection(dir string, body []byte) error {
	f, err := os.CreateTemp(dir, ".connection-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(body); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), filepath.Join(dir, "connection.json"))
}
