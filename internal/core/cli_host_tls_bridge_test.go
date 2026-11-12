package core

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sPROFFEs/PrAImate/internal/hosttls"
)

// Serve a CA-issued leaf without its issuer, as real private gateways often do.
func missingIssuerServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, ca, ca, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	ca, err = x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), NotBefore: ca.NotBefore, NotAfter: ca.NotAfter, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err = x509.CreateCertificate(rand.Reader, leaf, ca, &leafKey.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	s := httptest.NewUnstartedServer(handler)
	s.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: leafKey}}}
	s.StartTLS()
	t.Cleanup(s.Close)
	return s
}

func bridgeProvider(t *testing.T, cfg *EffectiveExecutionConfig) (string, http.Header) {
	t.Helper()
	var config struct {
		Providers map[string]struct {
			Options struct {
				BaseURL string            `json:"baseURL"`
				Headers map[string]string `json:"headers"`
			} `json:"options"`
		} `json:"provider"`
	}
	if err := json.Unmarshal([]byte(cfg.Env["OPENCODE_CONFIG_CONTENT"]), &config); err != nil {
		t.Fatal(err)
	}
	options := config.Providers["praimate-local"].Options
	headers := http.Header{}
	for key, value := range options.Headers {
		headers.Set(key, value)
	}
	return options.BaseURL, headers
}

func trustedBridgeConfig(t *testing.T, c *Core, endpoint, cli string) *EffectiveExecutionConfig {
	t.Helper()
	inspected, err := hosttls.Inspect(context.Background(), endpoint)
	if err != nil {
		t.Fatal(err)
	}
	// Exercise saved legacy pins, without importing a CA or inspecting an issuer.
	if err := c.TrustLocalHostCertificate(context.Background(), endpoint, inspected.PEM); err != nil {
		t.Fatal(err)
	}
	cfg := &EffectiveExecutionConfig{CLI: cli, Cwd: t.TempDir(), Local: &ChatLocalEndpoint{Endpoint: endpoint}, Env: map[string]string{
		"OPENCODE_CONFIG_CONTENT": `{"plugin":["existing"],"provider":{"praimate-local":{"options":{"headers":{"X-Fixture":"kept"}}},"untrusted":{"options":{"baseURL":"https://untrusted.example/v1"}}}}`,
	}}
	if err := c.prepareCLIHostTLS(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestCLIHostTLSBridgeAuthenticatesStreamsAndRevokes(t *testing.T) {
	for _, cli := range []string{"praimate-code", "opencode"} {
		t.Run(cli, func(t *testing.T) {
			c := nativeTestCore(t)
			release := make(chan struct{})
			defer func() { close(release) }()
			backend := missingIssuerServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get(cliTLSHeader) != "" || r.Header.Get("Authorization") != "Bearer fixture-key" || r.Header.Get("X-Fixture") != "kept" {
					t.Error("bridge leaked its own token or changed provider headers")
				}
				if r.Method != "POST" || r.URL.Path != "/custom/v1/chat/completions" || r.URL.RawQuery != "q=fixture" {
					t.Error("bridge changed the provider request target")
				}
				body, _ := io.ReadAll(r.Body)
				if string(body) != `{"stream":true}` {
					t.Error("bridge changed the request body")
				}
				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, "data: first\n\n")
				w.(http.Flusher).Flush()
				select {
				case <-release:
				case <-r.Context().Done():
				}
			}))
			cfg := trustedBridgeConfig(t, c, backend.URL+"/custom/v1", cli)
			endpoint, headers := bridgeProvider(t, cfg)
			if !strings.HasPrefix(endpoint, "http://127.0.0.1:") {
				t.Fatal("trusted local provider was not routed through loopback")
			}
			if !strings.Contains(cfg.Env["OPENCODE_CONFIG_CONTENT"], `"plugin":["existing"]`) || !strings.Contains(cfg.Env["OPENCODE_CONFIG_CONTENT"], `"baseURL":"https://untrusted.example/v1"`) {
				t.Fatal("unrelated CLI configuration changed")
			}
			client := &http.Client{Timeout: 5 * time.Second}
			request := func(token, origin string) *http.Response {
				req, _ := http.NewRequest("POST", endpoint+"/chat/completions?q=fixture", strings.NewReader(`{"stream":true}`))
				req.Header = headers.Clone()
				req.Header.Set(cliTLSHeader, token)
				req.Header.Set("Authorization", "Bearer fixture-key")
				if origin != "" {
					req.Header.Set("Origin", origin)
				}
				res, err := client.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { res.Body.Close() })
				return res
			}
			for _, bad := range []struct{ token, origin string }{{"", ""}, {"wrong", ""}, {headers.Get(cliTLSHeader), "https://browser.example"}} {
				if res := request(bad.token, bad.origin); res.StatusCode != http.StatusUnauthorized {
					t.Fatal("unauthorized bridge request was accepted")
				}
			}
			res := request(headers.Get(cliTLSHeader), "")
			if res.StatusCode != 200 {
				body, _ := io.ReadAll(res.Body)
				t.Fatalf("accepted leaf failed without issuer: %s", body)
			}
			line, err := bufio.NewReader(res.Body).ReadString('\n')
			if err != nil || line != "data: first\n" {
				t.Fatalf("response did not stream before completion: %q %v", line, err)
			}
			res.Body.Close()
			if err := c.RemoveLocalHostCertificate(context.Background(), backend.URL); err != nil {
				t.Fatal(err)
			}
			if res := request(headers.Get(cliTLSHeader), ""); res.StatusCode != http.StatusForbidden {
				t.Fatal("revocation did not stop an already configured CLI")
			}
			if err := c.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := c.localTLSBridge(); err == nil {
				t.Fatal("closed Core restarted its TLS bridge")
			}
		})
	}
}

func TestCLIHostTLSBridgeBlocksRedirects(t *testing.T) {
	c := nativeTestCore(t)
	backend := missingIssuerServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://other.example/", http.StatusTemporaryRedirect)
	}))
	cfg := trustedBridgeConfig(t, c, backend.URL, "opencode")
	endpoint, headers := bridgeProvider(t, cfg)
	req, _ := http.NewRequest("GET", endpoint, nil)
	req.Header = headers
	res, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadGateway || res.Header.Get("Location") != "" {
		t.Fatal("redirect escaped the trusted origin")
	}
}

func TestCLIHostTLSBridgeConfiguredProviderWithoutLocalOverride(t *testing.T) {
	c := nativeTestCore(t)
	backend := missingIssuerServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "configured-ready") }))
	cert, err := hosttls.Inspect(context.Background(), backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.TrustLocalHostCertificate(context.Background(), backend.URL, cert.PEM); err != nil {
		t.Fatal(err)
	}
	project := t.TempDir()
	config, _ := json.Marshal(map[string]any{"provider": map[string]any{"saved": map[string]any{"options": map[string]any{"baseURL": backend.URL + "/api/v1", "apiKey": "{env:FIXTURE_API_KEY}"}}}})
	if err := os.WriteFile(filepath.Join(project, "opencode.json"), config, 0600); err != nil {
		t.Fatal(err)
	}
	cfg := &EffectiveExecutionConfig{CLI: "opencode", Cwd: project, Model: "saved/model"}
	if err := c.prepareCLIHostTLS(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	var launch struct {
		Providers map[string]struct {
			Options struct {
				BaseURL string            `json:"baseURL"`
				Headers map[string]string `json:"headers"`
			} `json:"options"`
		} `json:"provider"`
	}
	if err := json.Unmarshal([]byte(cfg.Env["OPENCODE_CONFIG_CONTENT"]), &launch); err != nil {
		t.Fatal(err)
	}
	route := launch.Providers["saved"].Options
	req, _ := http.NewRequest("GET", route.BaseURL, nil)
	req.Header.Set(cliTLSHeader, route.Headers[cliTLSHeader])
	res, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 || string(body) != "configured-ready" || !strings.HasSuffix(route.BaseURL, "/api/v1") {
		t.Fatalf("saved provider route failed: %d %s", res.StatusCode, body)
	}
	unchanged, err := os.ReadFile(filepath.Join(project, "opencode.json"))
	if err != nil || string(unchanged) != string(config) {
		t.Fatal("ephemeral bridge changed the saved project configuration")
	}
}

func TestCLIHostTLSBridgeWithBunMissingIssuer(t *testing.T) {
	bin := os.Getenv("PRAIMATE_TEST_BUN_BINARY")
	if bin == "" {
		t.Skip("set PRAIMATE_TEST_BUN_BINARY to exercise Bun fetch with missing issuer")
	}
	c := nativeTestCore(t)
	t.Setenv("NO_PROXY", "from-process.example")
	t.Setenv("no_proxy", "lowercase.example")
	backend := missingIssuerServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "bridge-ready") }))
	cfg := trustedBridgeConfig(t, c, backend.URL, "opencode")
	if cfg.Env["NO_PROXY"] != "from-process.example,127.0.0.1" || cfg.Env["no_proxy"] != "lowercase.example,127.0.0.1" {
		t.Fatal("existing proxy exclusions were not preserved")
	}
	var proxied atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxied.Add(1)
		io.WriteString(w, "local request must bypass the proxy")
	}))
	defer proxy.Close()
	endpoint, headers := bridgeProvider(t, cfg)
	flat := map[string]string{}
	for key := range headers {
		flat[key] = headers.Get(key)
	}
	raw, _ := json.Marshal(flat)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "-e", `fetch(process.env.FIXTURE_URL,{headers:JSON.parse(process.env.FIXTURE_HEADERS)}).then(async r=>{if(!r.ok)throw new Error(await r.text());console.log(await r.text())}).catch(e=>{console.error(e.message);process.exitCode=1})`)
	cmd.Env = []string{"FIXTURE_URL=" + endpoint, "FIXTURE_HEADERS=" + string(raw), "HTTP_PROXY=" + proxy.URL, "NO_PROXY=" + cfg.Env["NO_PROXY"], "no_proxy=" + cfg.Env["no_proxy"]}
	out, err := cmd.CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "bridge-ready" {
		t.Fatalf("Bun could not use the accepted leaf without its issuer: %s %v", out, err)
	}
	if proxied.Load() != 0 {
		t.Fatal("local transport request reached the configured proxy")
	}
}

func TestCLIHostTLSBridgeRejectsMalformedLaunchConfig(t *testing.T) {
	c := nativeTestCore(t)
	backend := missingIssuerServer(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	cert, err := hosttls.Inspect(context.Background(), backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.TrustLocalHostCertificate(context.Background(), backend.URL, cert.PEM); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`null`, `{"provider":[]}`, `{"provider":{"praimate-local":{"options":{"headers":[]}}}}`} {
		cfg := &EffectiveExecutionConfig{CLI: "opencode", Cwd: t.TempDir(), Local: &ChatLocalEndpoint{Endpoint: backend.URL}, Env: map[string]string{"OPENCODE_CONFIG_CONTENT": raw}}
		if err := c.prepareCLIHostTLS(context.Background(), cfg); err == nil {
			t.Fatal("invalid launch configuration was silently replaced")
		}
		if cfg.Env["OPENCODE_CONFIG_CONTENT"] != raw || c.cliTLSBridge != nil {
			t.Fatal("invalid launch configuration started or changed a provider route")
		}
	}
}
