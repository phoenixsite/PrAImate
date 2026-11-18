package studio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/sPROFFEs/PrAImate/internal/appdata"
	"github.com/sPROFFEs/PrAImate/internal/core"
	"github.com/sPROFFEs/PrAImate/internal/version"
)

// Manager manages PrAImate Studio lifecycle, extensions, and process launches.
type Manager struct {
	core *core.Core
	mu   sync.Mutex
}

// NewManager creates a Studio manager.
func NewManager(c *core.Core) *Manager {
	return &Manager{core: c}
}

// StudioDir returns <config>/praimate/tools/praimate-studio.
func StudioDir() (string, error) {
	root, err := appdata.Root()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "tools", "praimate-studio"), nil
}

// AppDir returns <config>/praimate/tools/praimate-studio/app.
func AppDir() (string, error) {
	sDir, err := StudioDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(sDir, "app"), nil
}

// SocketPath returns the default JSON-RPC socket path.
func SocketPath() (string, error) {
	if s := os.Getenv("PRAIMATE_SOCK"); s != "" {
		return s, nil
	}
	if runtime.GOOS == "windows" {
		return "", nil // Windows Studio uses authenticated loopback or stdio.
	}
	root, err := appdata.Root()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "run", "praimate.sock"), nil
}

// ExtensionsDir uses the managed directory unless the user selects their own.
func ExtensionsDir() (string, error) {
	prefs, err := EditorExtensionPreferences()
	if err != nil {
		return "", err
	}
	if prefs.ExtensionsDirectory != "" {
		if info, err := os.Stat(prefs.ExtensionsDirectory); err != nil || !info.IsDir() {
			return "", errors.New("the configured Studio extensions directory is unavailable; choose another folder or restore the managed directory")
		}
		return prefs.ExtensionsDirectory, nil
	}
	return prefs.ManagedDirectory, nil
}

// UserDataDir returns <config>/praimate/tools/praimate-studio/data.
func UserDataDir() (string, error) {
	sDir, err := StudioDir()
	if err != nil {
		return "", err
	}
	prefs, err := EditorExtensionPreferences()
	if err != nil {
		return "", err
	}
	return editorDataDirectory(sDir, prefs.ExtensionsDirectory), nil
}

// DetectBinary finds the Code-OSS / Studio binary to use.
func DetectBinary() (string, string) {
	// 1. Check managed downloaded app under ~/.config/praimate/tools/praimate-studio/app/
	sDir, _ := StudioDir()
	if sDir != "" {
		if runtime.GOOS == "darwin" {
			candidates := []string{
				filepath.Join(sDir, "app", "VSCodium.app", "Contents", "Resources", "app", "bin", "codium"),
				filepath.Join(sDir, "app", "VSCodium.app", "Contents", "MacOS", "Electron"),
				filepath.Join(sDir, "app", "bin", "codium"),
			}
			candidates = append(candidates, managedCandidates(filepath.Join(sDir, "app"), "codium")...)
			for _, c := range candidates {
				if usableManagedEditor(c) {
					return c, "managed-codium"
				}
			}
		} else if runtime.GOOS != "windows" {
			candidates := []string{
				filepath.Join(sDir, "app", "bin", "codium"),
				filepath.Join(sDir, "app", "codium"),
				filepath.Join(sDir, "app", "bin", "code-oss"),
			}
			candidates = append(candidates, managedCandidates(filepath.Join(sDir, "app"), "codium")...)
			candidates = append(candidates, managedCandidates(filepath.Join(sDir, "app"), "code-oss")...)
			for _, c := range candidates {
				if usableManagedEditor(c) {
					return c, "managed-codium"
				}
			}
		} else {
			candidates := []string{
				filepath.Join(sDir, "app", "VSCodium.exe"),
				filepath.Join(sDir, "app", "Code.exe"),
			}
			candidates = append(candidates, managedCandidates(filepath.Join(sDir, "app"), "VSCodium.exe")...)
			candidates = append(candidates, managedCandidates(filepath.Join(sDir, "app"), "Code.exe")...)
			for _, c := range candidates {
				if usableManagedEditor(c) {
					return c, "managed-codium"
				}
			}
		}
	}

	// 2. Look for system installed binaries in order of preference
	candidates := []string{"code-oss", "codium", "vscodium", "code"}
	for _, c := range candidates {
		if path, err := exec.LookPath(c); err == nil && path != "" {
			if runtime.GOOS == "windows" && strings.EqualFold(filepath.Ext(path), ".cmd") {
				// Launch the sibling executable directly. exec.Command cannot
				// execute a batch shim; a shell would also reinterpret paths.
				for _, name := range []string{"VSCodium.exe", "Code.exe", "code-oss.exe"} {
					exe := filepath.Join(filepath.Dir(filepath.Dir(path)), name)
					if info, err := os.Stat(exe); err == nil && !info.IsDir() {
						return exe, c
					}
				}
				continue
			}
			return path, c
		}
	}

	return "", ""
}

func usableManagedEditor(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	if info.Mode().Perm()&0111 == 0 {
		return false
	}
	if filepath.Base(filepath.Dir(path)) != "bin" {
		return true
	}
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	var prefix [2]byte
	n, _ := file.Read(prefix[:])
	file.Close()
	if n != 2 || string(prefix[:]) != "#!" {
		return true
	}
	// Archive launch scripts depend on the Electron executable next to bin/.
	// Removing that executable must not leave a "detected" but unusable editor.
	native := filepath.Join(filepath.Dir(filepath.Dir(path)), filepath.Base(path))
	if strings.Contains(filepath.ToSlash(path), ".app/Contents/Resources/app/bin/") {
		native = filepath.Join(filepath.Dir(path), "..", "..", "..", "MacOS", "Electron")
	}
	info, err = os.Stat(native)
	return err == nil && !info.IsDir() && info.Mode().Perm()&0111 != 0
}

func managedCandidates(root, executable string) []string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			out = append(out, filepath.Join(root, entry.Name(), executable))
		}
	}
	return out
}

// GetStatus checks and returns the status of PrAImate Studio.
func (m *Manager) GetStatus(ctx context.Context) (*StudioStatus, error) {
	editorProfileMu.Lock()
	defer editorProfileMu.Unlock()
	return m.getStatus(ctx)
}

func (m *Manager) getStatus(ctx context.Context) (*StudioStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	sockPath, _ := SocketPath()
	binPath, flavor := DetectBinary()
	extDir, err := ExtensionsDir()
	if err != nil {
		return nil, err
	}
	praimateExtDir, registered, err := registeredExtension(extDir)
	if err != nil {
		return nil, err
	}

	backendRunning := false
	if sockPath != "" {
		if runtime.GOOS != "windows" {
			if c, err := net.DialTimeout("unix", sockPath, 200*time.Millisecond); err == nil {
				backendRunning = true
				_ = c.Close()
			}
		}
	}

	desktopServers.Lock()
	if desktopServers.server != nil && desktopServers.core == m.core {
		backendRunning = true
		sockPath = desktopServers.endpoint
	}
	desktopServers.Unlock()
	extInstalled := registered
	dirs, err := extensionLocations()
	if err != nil {
		return nil, err
	}
	for _, dir := range dirs {
		extInstalled = extInstalled && extensionCurrent(dir)
	}

	status := &StudioStatus{
		Version:         version.Current,
		CodeOSSVersion:  "1.96.4 (Code-OSS)",
		ProtocolVersion: "1",
		Platform:        fmt.Sprintf("%s-%s", runtime.GOOS, runtime.GOARCH),
		BinaryPath:      binPath,
		ExtensionPath:   praimateExtDir,
		SocketPath:      sockPath,
		BackendRunning:  backendRunning,
	}

	if binPath != "" && extInstalled {
		status.State = StateInstalled
		if flavor != "" {
			status.CodeOSSVersion = fmt.Sprintf("Code-OSS base (%s)", flavor)
		}
	} else if binPath != "" && !extInstalled {
		status.State = StateUpdateAvailable
	} else {
		status.State = StateNotInstalled
	}

	return status, nil
}

// Install sets up Studio directories, provisions the built-in extension, and downloads Code-OSS if needed.
func (m *Manager) Install(ctx context.Context) (*StudioStatus, error) {
	editorProfileMu.Lock()
	defer editorProfileMu.Unlock()
	return m.install(ctx)
}

func (m *Manager) install(ctx context.Context) (*StudioStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	extDir, err := ExtensionsDir()
	if err != nil {
		return nil, err
	}
	dataDir, err := UserDataDir()
	if err != nil {
		return nil, err
	}
	sDir, err := StudioDir()
	if err != nil {
		return nil, err
	}
	appDir, err := AppDir()
	if err != nil {
		return nil, err
	}

	if err := os.MkdirAll(filepath.Join(sDir, "bin"), 0755); err != nil {
		return nil, fmt.Errorf("create studio bin dir: %w", err)
	}
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, fmt.Errorf("create studio data dir: %w", err)
	}
	if err := os.MkdirAll(appDir, 0755); err != nil {
		return nil, fmt.Errorf("create studio app dir: %w", err)
	}

	// If no Code-OSS binary is installed on system or app directory, download standalone VSCodium
	binPath, _ := DetectBinary()
	if binPath == "" {
		if err := m.downloadAndExtractCodeOSS(ctx, appDir); err != nil {
			return nil, fmt.Errorf("download Code-OSS runtime: %w", err)
		}
	}
	binPath, _ = DetectBinary()
	if binPath == "" {
		return nil, errors.New("Studio editor runtime is missing after setup")
	}
	if err := prepareExtensions(ctx, binPath); err != nil {
		return nil, err
	}

	// Create launcher wrapper script
	if runtime.GOOS != "windows" {
		wrapperPath := filepath.Join(sDir, "bin", "praimate-studio")
		binPath, _ = DetectBinary()
		script := fmt.Sprintf(`#!/usr/bin/env bash
# PrAImate Studio Launcher
EXT_DIR=%s
DATA_DIR=%s
TARGET=%s
if [ -z "$TARGET" ]; then
    if command -v code-oss >/dev/null 2>&1; then TARGET="code-oss";
    elif command -v codium >/dev/null 2>&1; then TARGET="codium";
    elif command -v vscodium >/dev/null 2>&1; then TARGET="vscodium";
    elif command -v code >/dev/null 2>&1; then TARGET="code"; fi
fi

if [ -n "$TARGET" ]; then
    exec "$TARGET" --extensions-dir "$EXT_DIR" --user-data-dir "$DATA_DIR" "$@"
else
    echo "No Code-OSS found. Run 'praimate studio install' to download." >&2
    exit 1
fi
`, quoteEditorShellArgument(extDir), quoteEditorShellArgument(dataDir), quoteEditorShellArgument(binPath))
		if err := os.WriteFile(wrapperPath, []byte(script), 0755); err != nil {
			return nil, fmt.Errorf("write Studio launcher: %w", err)
		}
	}

	// Ensure run directory exists for sockets
	root, _ := appdata.Root()
	_ = os.MkdirAll(filepath.Join(root, "run"), 0755)

	m.mu.Unlock()
	status, err := m.getStatus(ctx)
	m.mu.Lock()
	return status, err
}

func (m *Manager) downloadAndExtractCodeOSS(ctx context.Context, destDir string) error {
	var downloadURL string
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "linux/amd64":
		downloadURL = "https://github.com/VSCodium/vscodium/releases/download/1.96.4.25017/VSCodium-linux-x64-1.96.4.25017.tar.gz"
	case "linux/arm64":
		downloadURL = "https://github.com/VSCodium/vscodium/releases/download/1.96.4.25017/VSCodium-linux-arm64-1.96.4.25017.tar.gz"
	case "darwin/arm64":
		downloadURL = "https://github.com/VSCodium/vscodium/releases/download/1.96.4.25017/VSCodium-darwin-arm64-1.96.4.25017.zip"
	case "windows/amd64":
		downloadURL = "https://github.com/VSCodium/vscodium/releases/download/1.96.4.25017/VSCodium-win32-x64-1.96.4.25017.zip"
	case "windows/arm64":
		downloadURL = "https://github.com/VSCodium/vscodium/releases/download/1.96.4.25017/VSCodium-win32-arm64-1.96.4.25017.zip"
	default:
		return fmt.Errorf("unsupported platform for automatic download: %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", downloadURL, nil)
	if err != nil {
		return err
	}
	resp, err := (&http.Client{Timeout: 15 * time.Minute}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: HTTP %d %s", resp.StatusCode, resp.Status)
	}

	if strings.HasSuffix(downloadURL, ".tar.gz") {
		return extractTar(resp.Body, destDir)
	}
	if strings.HasSuffix(downloadURL, ".zip") {
		return extractZip(resp.Body, destDir)
	}

	return nil
}

// Update re-provisions the extension and wrappers.
func (m *Manager) Update(ctx context.Context) (*StudioStatus, error) {
	return m.Install(ctx)
}

// Repair fixes missing files and directories.
func (m *Manager) Repair(ctx context.Context) (*StudioStatus, error) {
	return m.Install(ctx)
}

// Launch opens a project in PrAImate Studio.
func (m *Manager) Launch(ctx context.Context, opts LaunchOptions) error {
	editorProfileMu.Lock()
	defer editorProfileMu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()

	targetDir := opts.WorkspacePath
	if targetDir == "" {
		targetDir = "."
	}
	targetDir, err := filepath.Abs(targetDir)
	if err != nil {
		return fmt.Errorf("resolve workspace path: %w", err)
	}
	if info, err := os.Stat(targetDir); err != nil || !info.IsDir() {
		return fmt.Errorf("workspace is not a directory: %s", targetDir)
	}

	extDir, err := ExtensionsDir()
	if err != nil {
		return err
	}
	dataDir, err := UserDataDir()
	if err != nil {
		return err
	}
	binPath, _ := DetectBinary()
	if binPath == "" {
		return errors.New("no Code-OSS or VSCodium runtime found. Please click 'Install' to set up PrAImate Studio.")
	}

	if err := prepareExtensions(ctx, binPath); err != nil {
		return err
	}
	// An already running Code instance keeps the first launch's environment.
	// Reuse one desktop server so its endpoint remains valid across windows.
	endpoint, token := "", ""
	if m.core != nil {
		endpoint, token, err = desktopEndpoint(m.core)
		if err != nil {
			return err
		}
	}
	backend, err := backendExecutable()
	if err != nil && endpoint == "" {
		return err
	}
	if err := publishConnection(endpoint, token, backend, launchConfiguration(opts, targetDir)); err != nil {
		return fmt.Errorf("publish Studio connection: %w", err)
	}
	cmd := exec.Command(binPath, "--new-window", "--extensions-dir", extDir, "--user-data-dir", dataDir, targetDir)
	cmd.Env = studioEnvironment(os.Environ(), map[string]string{
		"VSCODE_IPC_HOOK_CLI":    "",
		"ELECTRON_RUN_AS_NODE":   "",
		"PRAIMATE_SOCK":          endpoint,
		"PRAIMATE_STUDIO_TOKEN":  token,
		"PRAIMATE_BIN":           backend,
		"PRAIMATE_STUDIO_CONFIG": launchConfiguration(opts, targetDir),
	})
	cmd.Env = append(cmd.Env,
		fmt.Sprintf("PRAIMATE_STUDIO_WORKSPACE=%s", targetDir),
		fmt.Sprintf("PRAIMATE_STUDIO_CLI=%s", opts.CLI),
		fmt.Sprintf("PRAIMATE_STUDIO_MODEL=%s", opts.Model),
		fmt.Sprintf("PRAIMATE_STUDIO_AGENT=%s", opts.AgentID),
		fmt.Sprintf("PRAIMATE_STUDIO_TOOLS=%s", opts.Tools),
	)

	if err := startEditor(cmd); err != nil {
		return fmt.Errorf("launch studio (%s): %w", binPath, err)
	}
	return nil
}

// GUI bindings create a new Manager per request, so its mutex cannot protect
// the shared history file from concurrent launches.
var recentProjectsMu sync.Mutex

func readRecentProjects(file string) ([]string, error) {
	data, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read Studio history: %w", err)
	}
	var projects []string
	if err := json.Unmarshal(data, &projects); err != nil {
		return nil, fmt.Errorf("decode Studio history: %w", err)
	}
	return projects, nil
}

// SaveRecentProject records a project workspace as recently opened in Studio.
func (m *Manager) SaveRecentProject(workspacePath string) error {
	if workspacePath == "" {
		return nil
	}
	sDir, err := StudioDir()
	if err != nil {
		return err
	}
	recentsFile := filepath.Join(sDir, "recent_projects.json")
	if err := os.MkdirAll(sDir, 0700); err != nil {
		return err
	}
	recentProjectsMu.Lock()
	defer recentProjectsMu.Unlock()
	recents, err := readRecentProjects(recentsFile)
	if err != nil {
		return err
	}
	var updated []string
	clean := filepath.Clean(workspacePath)
	updated = append(updated, clean)
	for _, r := range recents {
		if filepath.Clean(r) != clean && len(updated) < 20 {
			updated = append(updated, r)
		}
	}
	data, _ := json.MarshalIndent(updated, "", "  ")
	f, err := os.CreateTemp(sDir, ".recent-projects-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), recentsFile)
}

// ListRecentProjects returns the list of recently opened Studio projects.
func (m *Manager) ListRecentProjects() ([]string, error) {
	sDir, err := StudioDir()
	if err != nil {
		return nil, err
	}
	recentsFile := filepath.Join(sDir, "recent_projects.json")
	recents, err := readRecentProjects(recentsFile)
	if err != nil {
		return nil, err
	}
	var existing []string
	seen := make(map[string]bool)
	for _, r := range recents {
		if info, err := os.Stat(r); err == nil && info.IsDir() && !seen[r] {
			seen[r] = true
			existing = append(existing, r)
		}
		if len(existing) == 20 {
			break
		}
	}
	return existing, nil
}
