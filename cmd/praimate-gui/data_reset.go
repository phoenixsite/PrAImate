package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/sPROFFEs/PrAImate/internal/appdata"
	"github.com/sPROFFEs/PrAImate/internal/backup"
	"github.com/sPROFFEs/PrAImate/internal/launcher"
	"github.com/sPROFFEs/PrAImate/internal/ollama"
	"github.com/sPROFFEs/PrAImate/internal/store"
)

const deleteAllDataPhrase = "DELETE ALL PRAIMATE DATA"

// StoredDataInfo describes the exact destructive scope shown in Settings.
type StoredDataInfo struct {
	DataRoot     string `json:"dataRoot"`
	ProjectsRoot string `json:"projectsRoot"`
	Phrase       string `json:"phrase"`
}

func (a *App) StoredDataInfo() (*StoredDataInfo, error) {
	root, err := appdata.Root()
	if err != nil {
		return nil, err
	}
	cfg, err := launcher.LoadConfig()
	if err != nil {
		return nil, err
	}
	projects := ""
	if cfg != nil {
		projects = cfg.WorkspacesRoot
	}
	return &StoredDataInfo{DataRoot: root, ProjectsRoot: projects, Phrase: deleteAllDataPhrase}, nil
}

// DeleteAllStoredData removes PrAImate-owned application state and, when
// requested, the configured projects root. The frontend supplies two human
// confirmations; the backend independently pins the typed phrase and exact
// configured projects path so a forged or stale UI call cannot widen scope.
func (a *App) DeleteAllStoredData(projectsRoot, phrase string) error {
	if a.detached != nil {
		if windows := a.detached.list(); len(windows) > 0 {
			return fmt.Errorf("close all %d secondary window(s) before deleting PrAImate data", len(windows))
		}
	}
	if phrase != deleteAllDataPhrase {
		return errors.New("confirmation phrase does not match")
	}
	info, err := a.StoredDataInfo()
	if err != nil {
		return err
	}
	projectsRoot = strings.TrimSpace(projectsRoot)
	if projectsRoot != "" {
		if info.ProjectsRoot == "" || !samePath(projectsRoot, info.ProjectsRoot) {
			return errors.New("projects folder must exactly match the folder configured in PrAImate")
		}
		if err := validateDeletionRoot(projectsRoot, info.DataRoot); err != nil {
			return fmt.Errorf("refuse to delete projects folder: %w", err)
		}
	}
	if err := validateDeletionRoot(info.DataRoot, ""); err != nil {
		return fmt.Errorf("refuse to delete data folder: %w", err)
	}

	a.resetMu.Lock()
	if a.dataReset {
		a.resetMu.Unlock()
		return errors.New("data deletion is already in progress")
	}
	a.dataReset = true
	a.resetMu.Unlock()

	// Stop anything that can write state back while deletion runs.
	stopCtxAssistant, cancelAssistant := context.WithTimeout(context.Background(), 10*time.Second)
	assistantErr := a.stopAssistantServices(stopCtxAssistant)
	cancelAssistant()
	if assistantErr != nil {
		return fmt.Errorf("stop Assistant before deleting data: %w", assistantErr)
	}
	if a.core != nil {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := a.core.StopManagedArtifactInstalls(stopCtx)
		cancel()
		if err != nil {
			return fmt.Errorf("stop model installation before deleting data: %w", err)
		}
	}
	backup.SetStateSyncer(nil)
	a.backupRestoreMu.Lock()
	a.backupRestoreDir, a.backupRestoreErr = "", nil
	a.backupRestoreMu.Unlock()
	a.stopBackgroundWork()
	if a.terms != nil {
		a.terms.closeAll()
	}
	_, _ = ollama.DisableCodex()
	_, _ = ollama.DisableOpenCode()
	_, _ = ollama.DisableDeepSeek()
	if a.dbPath != "" {
		_ = store.ForgetRememberedPassword(a.dbPath)
	}

	dbPath := ""
	if a.st != nil {
		dbPath = a.st.Path()
		if a.core != nil {
			_ = a.core.Close()
		}
		if err := a.st.Close(); err != nil {
			return fmt.Errorf("close database: %w", err)
		}
		a.st = nil
		a.core = nil
	}

	// Delete the explicitly confirmed external projects tree first. If this
	// fails, retain the app root so the user can retry with intact settings.
	if projectsRoot != "" {
		if err := os.RemoveAll(projectsRoot); err != nil {
			return fmt.Errorf("delete projects folder: %w", err)
		}
	}
	if dbPath != "" && !pathWithin(dbPath, info.DataRoot) {
		for _, suffix := range []string{"", ".key", "-wal", "-shm"} {
			if err := os.Remove(dbPath + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("delete custom database: %w", err)
			}
		}
	}
	if err := os.RemoveAll(info.DataRoot); err != nil {
		return fmt.Errorf("delete PrAImate data folder: %w", err)
	}
	for _, root := range appdata.LegacyRoots() {
		if samePath(root, info.DataRoot) {
			continue
		}
		if err := os.RemoveAll(root); err != nil {
			return fmt.Errorf("delete legacy PrAImate data %s: %w", root, err)
		}
	}

	// Let the Wails call resolve so the frontend can show completion, then
	// exit. A later launch recreates a clean database and first-run setup.
	go func() {
		time.Sleep(250 * time.Millisecond)
		if a.quit != nil {
			a.quit(a.ctx)
		}
	}()
	return nil
}

func (a *App) stopBackgroundWork() {
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

	a.chatCancelMu.Lock()
	for _, cancel := range a.chatCancels {
		cancel()
	}
	a.chatCancels = map[string]context.CancelFunc{}
	a.chatCancelIDs = map[string]uint64{}
	a.chatCancelMu.Unlock()
	a.managedCancelMu.Lock()
	for _, cancel := range a.managedCancels {
		cancel()
	}
	a.managedCancels = map[string]context.CancelFunc{}
	a.managedCancelMu.Unlock()

	a.ragCancelMu.Lock()
	for _, run := range a.ragCancels {
		run.cancel()
	}
	a.ragCancels = map[string]*ragRun{}
	a.ragCancelMu.Unlock()

	a.requirementsCancelMu.Lock()
	for _, run := range a.requirementsCancels {
		run.cancel()
	}
	a.requirementsCancels = map[string]*requirementsRun{}
	a.requirementsCancelMu.Unlock()
}

func validateDeletionRoot(path, appRoot string) error {
	clean, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return err
	}
	volume := filepath.VolumeName(clean) + string(filepath.Separator)
	home, _ := os.UserHomeDir()
	cwd, _ := os.Getwd()
	for _, protected := range []string{volume, home, os.TempDir(), cwd, appRoot, filepath.Dir(appRoot)} {
		if protected != "" && samePath(clean, protected) {
			return fmt.Errorf("%s is a protected broad folder", clean)
		}
	}
	return nil
}

func samePath(a, b string) bool {
	aa, errA := filepath.Abs(filepath.Clean(a))
	bb, errB := filepath.Abs(filepath.Clean(b))
	if errA != nil || errB != nil {
		return false
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(aa, bb)
	}
	return aa == bb
}

func pathWithin(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
