package studio

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCustomExtensionsPreserveUserPluginsAndPrivateData(t *testing.T) {
	t.Setenv("PRAIMATE_HOME", t.TempDir())
	managed, _ := ExtensionsDir()
	defaultData, _ := UserDataDir()
	custom := filepath.Join(t.TempDir(), "personal extensions")
	plugin := filepath.Join(custom, "example.personal-1.0.0")
	if err := os.MkdirAll(plugin, 0755); err != nil {
		t.Fatal(err)
	}
	manifest := []byte(`{"publisher":"example","name":"personal","version":"1.0.0"}`)
	if err := os.WriteFile(filepath.Join(plugin, "package.json"), manifest, 0644); err != nil {
		t.Fatal(err)
	}
	registry, _ := json.Marshal([]any{
		map[string]any{"identifier": map[string]string{"id": "example.personal"}, "version": "1.0.0", "relativeLocation": filepath.Base(plugin)},
		map[string]any{"identifier": map[string]string{"id": extensionID}, "version": embeddedExtensionVersion(), "relativeLocation": "praimate"},
	})
	if err := os.MkdirAll(filepath.Join(custom, "praimate"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(custom, "extensions.json"), registry, 0644); err != nil {
		t.Fatal(err)
	}
	if err := saveEditorExtensions(custom); err != nil {
		t.Fatal(err)
	}
	selected, err := ExtensionsDir()
	if err != nil || selected != custom {
		t.Fatalf("custom directory: %q %v", selected, err)
	}
	data, err := UserDataDir()
	root, _ := StudioDir()
	if err != nil || data == defaultData || filepath.Dir(data) != root {
		t.Fatalf("editor settings must stay private: %q %v", data, err)
	}
	if err := prepareExtensions(context.Background(), "unused-editor"); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string][]byte{filepath.Join(plugin, "package.json"): manifest, filepath.Join(custom, "extensions.json"): registry} {
		actual, err := os.ReadFile(name)
		if err != nil || string(actual) != string(want) {
			t.Fatalf("user extension data modified: %s %v", name, err)
		}
	}
	if !extensionCurrent(filepath.Join(custom, "praimate")) {
		t.Fatal("bundled integration not refreshed in custom directory")
	}
	if err := saveEditorExtensions(""); err != nil {
		t.Fatal(err)
	}
	selected, _ = ExtensionsDir()
	data, _ = UserDataDir()
	if selected != managed || data != defaultData {
		t.Fatal("reset did not restore the original managed profile")
	}
	if _, err := os.Stat(plugin); err != nil {
		t.Fatal("reset removed a personal extension")
	}
}

func TestConfiguredExtensionsReachLauncherAndEditor(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture editor uses a Unix launch script")
	}
	t.Setenv("PRAIMATE_HOME", t.TempDir())
	app, _ := AppDir()
	if err := os.MkdirAll(filepath.Join(app, "bin"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app, "codium"), []byte("native fixture"), 0755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$PRAIMATE_TEST_STUDIO_ARGS\"\n"
	if err := os.WriteFile(filepath.Join(app, "bin", "codium"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	backendDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(backendDir, "praimate"), []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", backendDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	argsFile := filepath.Join(t.TempDir(), "arguments")
	t.Setenv("PRAIMATE_TEST_STUDIO_ARGS", argsFile)
	custom := filepath.Join(t.TempDir(), "user extensions ' \" $(touch INJECTED)")
	if err := os.MkdirAll(custom, 0755); err != nil {
		t.Fatal(err)
	}
	m := NewManager(nil)
	if _, err := m.ConfigureExtensions(context.Background(), custom); err != nil {
		t.Fatal(err)
	}
	data, _ := UserDataDir()
	workspace := t.TempDir()
	if err := m.Launch(context.Background(), LaunchOptions{WorkspacePath: workspace}); err != nil {
		t.Fatal(err)
	}
	args, err := os.ReadFile(argsFile)
	want := "--new-window\n--extensions-dir\n" + custom + "\n--user-data-dir\n" + data + "\n" + workspace + "\n"
	if err != nil || string(args) != want {
		t.Fatalf("editor used a different profile: %q %v", args, err)
	}
	root, _ := StudioDir()
	wrapper, err := os.ReadFile(filepath.Join(root, "bin", "praimate-studio"))
	if err != nil || !strings.Contains(string(wrapper), quoteEditorShellArgument(custom)) || !strings.Contains(string(wrapper), data) {
		t.Fatalf("standalone launcher not refreshed: %v", err)
	}
	work := t.TempDir()
	cmd := exec.Command("bash", filepath.Join(root, "bin", "praimate-studio"), workspace)
	cmd.Dir = work
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("standalone launcher failed: %v %s", err, output)
	}
	args, err = os.ReadFile(argsFile)
	want = "--extensions-dir\n" + custom + "\n--user-data-dir\n" + data + "\n" + workspace + "\n"
	if err != nil || string(args) != want {
		t.Fatalf("launcher reinterpreted the extensions path: %q %v", args, err)
	}
	if _, err := os.Stat(filepath.Join(work, "INJECTED")); !os.IsNotExist(err) {
		t.Fatal("directory name executed as shell code")
	}
	if _, err := m.ConfigureExtensions(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	managed, _ := ExtensionsDir()
	wrapper, err = os.ReadFile(filepath.Join(root, "bin", "praimate-studio"))
	if err != nil || !strings.Contains(string(wrapper), managed) || strings.Contains(string(wrapper), custom) {
		t.Fatalf("reset left the old launcher profile: %v", err)
	}
}

func TestEditorPreferencesRejectInvalidPathsAndRecoverMissingFolders(t *testing.T) {
	t.Setenv("PRAIMATE_HOME", t.TempDir())
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("keep"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"relative/extensions", file, filepath.Join(t.TempDir(), "absent")} {
		if err := saveEditorExtensions(p); err == nil {
			t.Fatalf("accepted invalid directory %q", p)
		}
	}
	directory := t.TempDir()
	if err := saveEditorExtensions(directory); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(directory); err != nil {
		t.Fatal(err)
	}
	if _, err := ExtensionsDir(); err == nil {
		t.Fatal("missing external folder silently recreated")
	}
	// Preferences remain readable so the GUI can offer a reset after this error.
	if prefs, err := EditorExtensionPreferences(); err != nil || prefs.ExtensionsDirectory != directory {
		t.Fatalf("cannot recover preferences: %v %v", prefs, err)
	}
	if err := saveEditorExtensions(""); err != nil {
		t.Fatal(err)
	}
	if _, err := ExtensionsDir(); err != nil {
		t.Fatal(err)
	}
	root, _ := StudioDir()
	if err := os.WriteFile(filepath.Join(root, "editor-preferences.json"), []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ExtensionsDir(); err == nil {
		t.Fatal("corrupt preferences silently ignored")
	}
	if err := saveEditorExtensions(""); err != nil {
		t.Fatal(err)
	}
	if _, err := EditorExtensionPreferences(); err != nil {
		t.Fatal(err)
	}
}
