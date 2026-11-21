package main

import (
	"fmt"
	"log"
	"path/filepath"

	"github.com/sPROFFEs/PrAImate/internal/studio"
)

func (a *App) StudioGetEditorPreferences() (*studio.EditorPreferences, error) {
	return studio.EditorExtensionPreferences()
}

func (a *App) StudioSaveEditorPreferences(directory string) (*studio.EditorPreferences, error) {
	return studio.NewManager(a.core).ConfigureExtensions(a.ctx, directory)
}

// StudioGetStatus returns the installation and daemon status for PrAImate Studio.
func (a *App) StudioGetStatus() (*studio.StudioStatus, error) {
	mgr := studio.NewManager(a.core)
	return mgr.GetStatus(a.ctx)
}

// StudioInstall provisions the built-in extension and environment for PrAImate Studio.
func (a *App) StudioInstall() (*studio.StudioStatus, error) {
	mgr := studio.NewManager(a.core)
	return mgr.Install(a.ctx)
}

// StudioUpdate refreshes the Studio extension and scripts.
func (a *App) StudioUpdate() (*studio.StudioStatus, error) {
	mgr := studio.NewManager(a.core)
	return mgr.Update(a.ctx)
}

// StudioRepair repairs the Studio environment and permissions.
func (a *App) StudioRepair() (*studio.StudioStatus, error) {
	mgr := studio.NewManager(a.core)
	return mgr.Repair(a.ctx)
}

// StudioOpenProject launches PrAImate Studio with the given configuration.
func (a *App) StudioOpenProject(workspacePath, cli, model, agentID, tools string) error {
	c, err := a.requireCore()
	if err != nil {
		return err
	}
	refreshManagedPaths()
	mgr := studio.NewManager(c)
	target := workspacePath
	if target == "" {
		target = "."
	}
	abs, err := filepath.Abs(target)
	if err != nil {
		return fmt.Errorf("resolve workspace path: %w", err)
	}

	if err := mgr.Launch(a.ctx, studio.LaunchOptions{
		WorkspacePath: abs,
		CLI:           cli,
		Model:         model,
		AgentID:       agentID,
		Tools:         tools,
	}); err != nil {
		return err
	}
	if err := mgr.SaveRecentProjectConfig(studio.RecentProject{Path: abs, CLI: cli, Model: model, AgentID: agentID, Tools: tools}); err != nil {
		log.Printf("Studio opened, but recent project history could not be saved: %v", err)
	}
	return nil
}

// StudioListRecentProjects returns recent workspace folders opened in Studio
// with the configuration last used to open each one.
func (a *App) StudioListRecentProjects() ([]studio.RecentProject, error) {
	mgr := studio.NewManager(a.core)
	return mgr.ListRecentProjectEntries()
}

// StudioRemoveRecentProject removes a workspace from Studio's recent projects.
// The project folder is not modified.
func (a *App) StudioRemoveRecentProject(workspacePath string) error {
	return studio.NewManager(a.core).RemoveRecentProject(workspacePath)
}
