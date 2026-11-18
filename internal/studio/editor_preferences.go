package studio

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// A directory switch must not interleave provisioning, connection publication
// and editor launch. Managers are recreated by GUI requests.
var editorProfileMu sync.Mutex

type EditorPreferences struct {
	ExtensionsDirectory string `json:"extensionsDirectory"`
	ManagedDirectory    string `json:"managedDirectory"`
}

// EditorExtensionPreferences does not require an unlocked database or editor.
func EditorExtensionPreferences() (*EditorPreferences, error) {
	root, err := StudioDir()
	if err != nil {
		return nil, err
	}
	prefs := &EditorPreferences{ManagedDirectory: filepath.Join(root, "extensions")}
	body, err := os.ReadFile(filepath.Join(root, "editor-preferences.json"))
	if errors.Is(err, os.ErrNotExist) {
		return prefs, nil
	}
	if err != nil {
		return nil, err
	}
	var saved struct {
		ExtensionsDirectory string `json:"extensionsDirectory"`
	}
	if err := json.Unmarshal(body, &saved); err != nil {
		return nil, fmt.Errorf("read Studio editor preferences: %w", err)
	}
	if saved.ExtensionsDirectory != "" && !filepath.IsAbs(saved.ExtensionsDirectory) {
		return nil, errors.New("Studio extensions directory must be an absolute path")
	}
	prefs.ExtensionsDirectory = saved.ExtensionsDirectory
	return prefs, nil
}

// Called under editorProfileMu. Only the user's chosen extension directory is
// shared with their editor; settings, history and backend state stay private.
func saveEditorExtensions(directory string) error {
	root, err := StudioDir()
	if err != nil {
		return err
	}
	directory = strings.TrimSpace(directory)
	if directory != "" {
		if !filepath.IsAbs(directory) {
			return errors.New("choose an absolute extensions directory")
		}
		info, err := os.Stat(directory)
		if err != nil || !info.IsDir() {
			return errors.New("the extensions directory must already exist")
		}
		directory = filepath.Clean(directory)
		if directory == filepath.Join(root, "extensions") {
			directory = ""
		}
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(root, ".editor-preferences-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := json.NewEncoder(f).Encode(struct {
		ExtensionsDirectory string `json:"extensionsDirectory"`
	}{directory}); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), filepath.Join(root, "editor-preferences.json"))
}

// ConfigureExtensions also refreshes the integration and standalone launcher
// when an editor is available. No other extensions are installed or removed.
func (m *Manager) ConfigureExtensions(ctx context.Context, directory string) (*EditorPreferences, error) {
	editorProfileMu.Lock()
	defer editorProfileMu.Unlock()
	if err := saveEditorExtensions(directory); err != nil {
		return nil, err
	}
	if bin, _ := DetectBinary(); bin != "" {
		if _, err := m.install(ctx); err != nil {
			return nil, err
		}
	}
	return EditorExtensionPreferences()
}

func editorDataDirectory(root, customExtensions string) string {
	if customExtensions == "" {
		return filepath.Join(root, "data")
	}
	// Distinct user-data directories keep Electron from handing a directory
	// switch to an existing window that still uses the previous extensions.
	sum := sha256.Sum256([]byte(customExtensions))
	return filepath.Join(root, fmt.Sprintf("data-%x", sum[:8]))
}
