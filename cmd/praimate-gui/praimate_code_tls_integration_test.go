package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sPROFFEs/PrAImate/internal/core"
	"github.com/sPROFFEs/PrAImate/internal/hosttls"
)

// Run the actual Code PTY with core routing, accepted TLS and the usage plugin.
// A headless JSON completion alone does not exercise the TUI's server worker.
func TestPraimateCodeTerminalAcceptedTLS(t *testing.T) {
	bin := os.Getenv("PRAIMATE_TEST_CODE_BINARY")
	if bin == "" {
		t.Skip("set PRAIMATE_TEST_CODE_BINARY to test the compiled Code terminal")
	}
	if runtime.GOOS == "windows" {
		t.Skip("isolated runtime fixture currently uses a POSIX symlink")
	}
	if !filepath.IsAbs(bin) {
		t.Fatal("PRAIMATE_TEST_CODE_BINARY must be absolute")
	}
	app := assistantAppFixture(t)
	root := t.TempDir()
	for key, value := range map[string]string{
		"OPENCODE_TEST_HOME": root, "OPENCODE_CONFIG_DIR": filepath.Join(root, "config"),
		"XDG_DATA_HOME": filepath.Join(root, "data"), "XDG_CACHE_HOME": filepath.Join(root, "cache"),
		"XDG_STATE_HOME": filepath.Join(root, "state"), "PWD": root,
		"OPENCODE_CONFIG": "", "OPENCODE_CONFIG_CONTENT": "", "OPENCODE_SERVER_PASSWORD": "",
		"OPENCODE_DISABLE_AUTOUPDATE": "1", "OPENCODE_DISABLE_MODELS_FETCH": "1",
		"OPENCODE_DISABLE_DEFAULT_PLUGINS": "1", "OPENCODE_DISABLE_EXTERNAL_SKILLS": "1",
		"OPENCODE_DISABLE_CLAUDE_CODE": "1", "OPENCODE_DISABLE_LSP_DOWNLOAD": "1", "TERM": "xterm-256color",
	} {
		t.Setenv(key, value)
	}
	managed := filepath.Join(os.Getenv("PRAIMATE_HOME"), "bin")
	if err := os.MkdirAll(managed, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(bin, filepath.Join(managed, "praimate-code")); err != nil {
		t.Fatal(err)
	}
	old, oldErr := core.GetCLIAdapter("praimate-code")
	core.RegisterCLIAdapter(core.NewPraimateCodeAdapter())
	t.Cleanup(func() {
		if oldErr == nil {
			core.RegisterCLIAdapter(old)
		} else {
			core.UnregisterCLIAdapter("praimate-code")
		}
	})
	var requests atomic.Int32
	backend := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		var request struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.Model != "openai/qwen3.8-vLLM" || r.Header.Get("Authorization") != "Bearer fixture-key" {
			t.Errorf("incorrect model, authorization or request: model=%q err=%v", request.Model, err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		requests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"id\":\"fixture\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"openai/qwen3.8-vLLM\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"trusted-terminal-ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer backend.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	cert, err := hosttls.Inspect(ctx, backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.core.TrustLocalHostCertificate(ctx, backend.URL, cert.PEM); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(root, "project")
	if err := os.Mkdir(project, 0700); err != nil {
		t.Fatal(err)
	}
	cfg, err := app.core.ResolveExecutionConfig(ctx, core.ExecutionRequest{Surface: core.SurfaceTerminal, CLI: "praimate-code", Cwd: project, Local: &core.ChatLocalEndpoint{Endpoint: backend.URL, Model: "openai/qwen3.8-vLLM", APIKey: "fixture-key"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := app.core.PrepareExecution(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	// Disable unrelated providers/title models without replacing the route
	// produced by PrepareExecution in the project configuration.
	config := map[string]any{}
	if raw := cfg.Env["OPENCODE_CONFIG_CONTENT"]; raw != "" {
		if err := json.Unmarshal([]byte(raw), &config); err != nil {
			t.Fatal(err)
		}
	}
	config["enabled_providers"] = []string{"praimate-local"}
	config["model"], config["small_model"], config["snapshot"] = cfg.Model, cfg.Model, false
	raw, _ := json.Marshal(config)
	cfg.Env["OPENCODE_CONFIG_CONTENT"] = string(raw)
	usage, err := app.core.BeginTerminalUsage(ctx, "praimate-code", cfg.Model, cfg.Env)
	if err != nil {
		t.Fatal(err)
	}
	defer usage.Close()
	env := appendEnvMap(appendEnvMap(nil, cfg.Env), usage.Env)
	name, args, err := terminalCommand("praimate-code", cfg.Model)
	if err != nil {
		t.Fatal(err)
	}
	args = append(args, "--prompt", "Reply with the fixture response.")
	manager := newTermManager()
	output := make(chan string, 512)
	id, err := manager.startWithCleanup(name, args, project, env, func(_ string, value any) {
		if data, ok := value.(TerminalData); ok {
			decoded, _ := base64.StdEncoding.DecodeString(data.Data)
			select {
			case output <- string(decoded):
			default:
			}
		}
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.close(id)
	var screen strings.Builder
	for {
		select {
		case chunk := <-output:
			screen.WriteString(chunk)
			if strings.Contains(screen.String(), "trusted-terminal-ok") {
				if requests.Load() == 0 {
					t.Fatal("terminal rendered a response without reaching the HTTPS fixture")
				}
				return
			}
		case <-ctx.Done():
			t.Fatalf("Code PTY did not complete over accepted HTTPS (requests=%d, server error=%v): %v", requests.Load(), strings.Contains(screen.String(), "Unexpected server error"), ctx.Err())
		}
	}
}
