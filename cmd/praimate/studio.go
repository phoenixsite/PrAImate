package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/sPROFFEs/PrAImate/internal/studio"
	"github.com/sPROFFEs/PrAImate/internal/version"
)

func runStudio(args []string) int {
	if len(args) > 0 {
		switch args[0] {
		case "status":
			return runStudioStatus()
		case "install":
			return runStudioInstall()
		case "update":
			return runStudioUpdate()
		case "repair":
			return runStudioRepair()
		case "extensions":
			return runStudioExtensions(args[1:])
		case "-h", "--help", "help":
			printStudioHelp()
			return 0
		}
	}

	flags := flag.NewFlagSet("praimate studio", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	cliFlag := flags.String("cli", "", "CLI used in Studio (default: praimate-code)")
	modelFlag := flags.String("model", "", "Model override for Studio")
	agentFlag := flags.String("agent", "", "Agent persona ID for Studio")
	toolsFlag := flags.String("tools", "safe", "Tool permission level: safe, ask, edits, or full")

	if err := flags.Parse(args); err != nil {
		return 2
	}

	targetPath := "."
	rest := flags.Args()
	if len(rest) > 0 {
		targetPath = rest[0]
	}

	absPath, err := filepath.Abs(targetPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: resolve path %s: %v\n", targetPath, err)
		return 1
	}

	c, cleanup, err := openCore()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: open database: %v\n", err)
		return 1
	}
	defer cleanup()

	mgr := studio.NewManager(c)
	ctx := context.Background()

	// Ensure studio environment is prepared
	status, err := mgr.GetStatus(ctx)
	if err != nil || status.State != studio.StateInstalled {
		fmt.Println("Setting up PrAImate Studio environment...")
		if _, err := mgr.Install(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "error: install studio environment: %v\n", err)
			return 1
		}
	}

	fmt.Printf("Opening PrAImate Studio in %s...\n", absPath)

	// The extension owns its stdio backend; it survives this launcher exiting.
	err = studio.NewManager(nil).Launch(ctx, studio.LaunchOptions{
		WorkspacePath: absPath,
		CLI:           *cliFlag,
		Model:         *modelFlag,
		AgentID:       *agentFlag,
		Tools:         *toolsFlag,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: launch studio: %v\n", err)
		return 1
	}
	if err := mgr.SaveRecentProjectConfig(studio.RecentProject{Path: absPath, CLI: *cliFlag, Model: *modelFlag, AgentID: *agentFlag, Tools: *toolsFlag}); err != nil {
		fmt.Fprintf(os.Stderr, "warning: Studio opened, but recent project history could not be saved: %v\n", err)
	}

	return 0
}

func runStudioExtensions(args []string) int {
	if len(args) > 1 {
		fmt.Fprintln(os.Stderr, "Usage: praimate studio extensions [absolute-directory|--managed]")
		return 2
	}
	var prefs *studio.EditorPreferences
	var err error
	if len(args) == 0 {
		prefs, err = studio.EditorExtensionPreferences()
	} else {
		directory := args[0]
		if directory == "--managed" {
			directory = ""
		}
		prefs, err = studio.NewManager(nil).ConfigureExtensions(context.Background(), directory)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "Studio extensions:", err)
		return 1
	}
	directory := prefs.ExtensionsDirectory
	if directory == "" {
		directory = prefs.ManagedDirectory
	}
	fmt.Fprintln(os.Stdout, directory)
	return 0
}

func runStudioStatus() int {
	mgr := studio.NewManager(nil)
	status, err := mgr.GetStatus(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "error checking status: %v\n", err)
		return 1
	}

	fmt.Println(version.Banner)
	fmt.Printf("\nPrAImate Studio Status:\n")
	fmt.Printf("  State:            %s\n", status.State)
	fmt.Printf("  Version:          %s\n", status.Version)
	fmt.Printf("  Code-OSS Base:    %s\n", status.CodeOSSVersion)
	fmt.Printf("  Protocol Version: %s\n", status.ProtocolVersion)
	fmt.Printf("  Binary:           %s\n", status.BinaryPath)
	fmt.Printf("  Extension:        %s\n", status.ExtensionPath)
	fmt.Printf("  Socket:           %s\n", status.SocketPath)
	fmt.Printf("  Backend Daemon:   %v\n", status.BackendRunning)

	return 0
}

func runStudioInstall() int {
	mgr := studio.NewManager(nil)
	fmt.Println("Installing PrAImate Studio integration and extension...")
	status, err := mgr.Install(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	fmt.Printf("✓ Studio installation completed (State: %s)\n", status.State)
	return 0
}

func runStudioUpdate() int {
	mgr := studio.NewManager(nil)
	fmt.Println("Updating PrAImate Studio integration...")
	status, err := mgr.Update(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	fmt.Printf("✓ Studio updated to %s\n", status.Version)
	return 0
}

func runStudioRepair() int {
	mgr := studio.NewManager(nil)
	fmt.Println("Repairing PrAImate Studio installation...")
	status, err := mgr.Repair(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	fmt.Printf("✓ Studio repair complete (State: %s)\n", status.State)
	return 0
}

func runServe(args []string) int {
	flags := flag.NewFlagSet("praimate serve", flag.ContinueOnError)
	stdioFlag := flags.Bool("stdio", false, "Use stdio for JSON-RPC communication")

	if err := flags.Parse(args); err != nil {
		return 2
	}

	c, cleanup, err := openCore()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	defer cleanup()

	srv := studio.NewServer(c)
	defer srv.Close()

	if *stdioFlag {
		return runServeStdio(srv)
	}

	sockPath, _ := studio.SocketPath()
	fmt.Printf("PrAImate Core JSON-RPC daemon listening on %s...\n", sockPath)
	if err := srv.ServeSocket(); err != nil {
		fmt.Fprintf(os.Stderr, "error serving socket: %v\n", err)
		return 1
	}
	return 0
}

func runServeStdio(srv *studio.Server) int {
	if err := srv.ServeStdio(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "error in stdio rpc: %v\n", err)
		return 1
	}
	return 0
}

func printStudioHelp() {
	fmt.Fprintln(os.Stderr, version.Banner)
	fmt.Fprintf(os.Stderr, "\nUsage: praimate studio [command|path] [options]\n\n")
	fmt.Fprintln(os.Stderr, "Commands:")
	fmt.Fprintln(os.Stderr, "  <path>                     Open directory or project in PrAImate Studio (default: .)")
	fmt.Fprintln(os.Stderr, "  status                     Display Studio installation and daemon status")
	fmt.Fprintln(os.Stderr, "  install                    Install Studio integration and built-in extension")
	fmt.Fprintln(os.Stderr, "  update                     Update Studio integration to matching version")
	fmt.Fprintln(os.Stderr, "  repair                     Repair Studio integration, permissions and configs")
	fmt.Fprintln(os.Stderr, "  extensions [dir|--managed]  Show or choose the extensions directory")
	fmt.Fprintln(os.Stderr, "\nOptions:")
	fmt.Fprintln(os.Stderr, "  --cli <name>               CLI engine (praimate-code, claude, codex, opencode)")
	fmt.Fprintln(os.Stderr, "  --model <model>            Model override")
	fmt.Fprintln(os.Stderr, "  --agent <id>               Agent persona ID")
	fmt.Fprintln(os.Stderr, "  --tools <level>            Tool permission level (safe, edits, full)")
}
