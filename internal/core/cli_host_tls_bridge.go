package core

// Bun versions differ in their support for partial certificate chains. Route
// explicitly trusted local providers through the same Go verifier as discovery
// and praimate-cli, rather than relying on NODE_EXTRA_CA_CERTS to trust a leaf.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/sPROFFEs/PrAImate/internal/hosttls"
	"github.com/sPROFFEs/PrAImate/internal/ollama"
)

const cliTLSHeader = "X-Praimate-Host-TLS"

type cliHostTLSBridge struct {
	core            *Core
	server          *http.Server
	endpoint, token string
	mu              sync.RWMutex
	origins         map[string]string
}

func (c *Core) localTLSBridge() (*cliHostTLSBridge, error) {
	c.cliTLSMu.Lock()
	defer c.cliTLSMu.Unlock()
	if c.cliTLSClosed {
		return nil, errors.New("local TLS transport is closed")
	}
	if c.cliTLSBridge != nil {
		return c.cliTLSBridge, nil
	}
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	b := &cliHostTLSBridge{core: c, endpoint: "http://" + listener.Addr().String(), token: hex.EncodeToString(secret[:]), origins: map[string]string{}}
	b.server = &http.Server{Handler: b, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: time.Minute, MaxHeaderBytes: 64 << 10}
	c.cliTLSBridge = b
	go func() { _ = b.server.Serve(listener) }()
	return b, nil
}

func (c *Core) closeCLIHostTLS() error {
	c.cliTLSMu.Lock()
	c.cliTLSClosed = true
	b := c.cliTLSBridge
	c.cliTLSBridge = nil
	c.cliTLSMu.Unlock()
	if b != nil {
		return b.server.Close()
	}
	return nil
}

func (b *cliHostTLSBridge) route(endpoint string) string {
	origin, _ := hosttls.Origin(endpoint)
	id := fmt.Sprintf("%x", sha256.Sum256([]byte(origin)))
	b.mu.Lock()
	b.origins[id] = origin
	b.mu.Unlock()
	u, _ := url.Parse(endpoint) // Origin already validated this HTTPS URL.
	u.Scheme, u.Host = "http", strings.TrimPrefix(b.endpoint, "http://")
	u.Path = "/" + id + u.Path
	if u.RawPath != "" {
		u.RawPath = "/" + id + u.RawPath
	}
	u.Fragment = ""
	return u.String()
}

func (b *cliHostTLSBridge) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Origin") != "" || subtle.ConstantTimeCompare([]byte(r.Header.Get(cliTLSHeader)), []byte(b.token)) != 1 {
		http.Error(w, "unauthorized local TLS request", http.StatusUnauthorized)
		return
	}
	id, path, _ := strings.Cut(strings.TrimPrefix(r.URL.EscapedPath(), "/"), "/")
	b.mu.RLock()
	origin := b.origins[id]
	b.mu.RUnlock()
	if origin == "" {
		http.NotFound(w, r)
		return
	}
	// Re-read consent for every request: removing trust also stops existing CLIs.
	material, err := b.core.LocalHostTLSCertificate(r.Context(), origin)
	if err != nil || material == "" {
		http.Error(w, "Local LLM certificate trust is unavailable or was revoked; review Local LLM settings", http.StatusForbidden)
		return
	}
	client, err := hosttls.Client(origin, material, 0)
	if err != nil {
		http.Error(w, "Local LLM certificate trust is invalid or expired; review Local LLM settings", http.StatusBadGateway)
		return
	}
	defer client.CloseIdleConnections()
	target, _ := url.Parse(origin)
	decodedPath, err := url.PathUnescape("/" + path)
	if err != nil {
		http.Error(w, "invalid local provider path", http.StatusBadRequest)
		return
	}
	proxy := &httputil.ReverseProxy{
		Transport: client.Transport, FlushInterval: -1,
		Rewrite: func(req *httputil.ProxyRequest) {
			req.Out.URL.Scheme, req.Out.URL.Host = target.Scheme, target.Host
			req.Out.URL.Path, req.Out.URL.RawPath = decodedPath, "/"+path
			req.Out.Host = target.Host
			req.Out.Header.Del(cliTLSHeader)
		},
		ModifyResponse: func(res *http.Response) error {
			if res.StatusCode >= 300 && res.StatusCode < 400 && res.Header.Get("Location") != "" {
				return errors.New("local LLM redirects are blocked; configure and trust the final endpoint")
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			http.Error(w, "Local LLM TLS connection failed: "+err.Error(), http.StatusBadGateway)
		},
	}
	proxy.ServeHTTP(w, r)
}

func cliTLSObject(parent map[string]any, key string) (map[string]any, error) {
	if parent[key] == nil {
		return map[string]any{}, nil
	}
	object, ok := parent[key].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("OpenCode local TLS configuration %q must be an object", key)
	}
	return object, nil
}

func (c *Core) prepareCLIHostTLSRoutes(ctx context.Context, cfg *EffectiveExecutionConfig, trusted map[string]string) error {
	routes, _, err := ollama.ConfiguredOpenCodeRoutes(cfg.Cwd)
	if err != nil {
		return err
	}
	config := map[string]any{}
	raw := cfg.Env["OPENCODE_CONFIG_CONTENT"]
	if raw == "" {
		raw = os.Getenv("OPENCODE_CONFIG_CONTENT")
	}
	if raw != "" && (json.Unmarshal([]byte(raw), &config) != nil || config == nil) {
		return errors.New("invalid OpenCode launch configuration for local TLS")
	}
	providers, err := cliTLSObject(config, "provider")
	if err != nil {
		return err
	}
	for name, value := range providers {
		provider, _ := value.(map[string]any)
		options, _ := provider["options"].(map[string]any)
		if endpoint, _ := options["baseURL"].(string); endpoint != "" {
			routes[name] = ollama.Settings{Endpoint: endpoint}
		}
	}
	if cfg.Local != nil {
		routes["praimate-local"] = ollama.Settings{Endpoint: ollama.OpenAIEndpoint(cfg.Local.Endpoint)}
	}
	changed := false
	for name, route := range routes {
		origin, err := hosttls.Origin(route.Endpoint)
		if err != nil {
			continue
		}
		if trusted[origin] == "" {
			material, err := c.LocalHostTLSCertificate(ctx, origin)
			if err != nil {
				return err
			}
			if material == "" {
				continue
			}
			if _, _, err := hosttls.ParseTrust(origin, material); err != nil {
				selected, _, _ := strings.Cut(cfg.Model, "/")
				if name == selected || (cfg.Local != nil && name == "praimate-local") {
					return err
				}
				continue
			}
		}
		provider, err := cliTLSObject(providers, name)
		if err != nil {
			return err
		}
		options, err := cliTLSObject(provider, "options")
		if err != nil {
			return err
		}
		headers, err := cliTLSObject(options, "headers")
		if err != nil {
			return err
		}
		bridge, err := c.localTLSBridge()
		if err != nil {
			return err
		}
		headers[cliTLSHeader] = bridge.token
		options["baseURL"], options["headers"] = bridge.route(route.Endpoint), headers
		provider["options"], providers[name] = options, provider
		changed = true
	}
	if changed {
		config["provider"] = providers
		raw, err := json.Marshal(config)
		if err != nil {
			return err
		}
		cfg.Env["OPENCODE_CONFIG_CONTENT"] = string(raw)
		// Bun honors proxy environment variables even for loopback. Keep the
		// authenticated local request inside this machine, preserving every
		// existing proxy exclusion and leaving the upstream proxy unchanged.
		for _, key := range []string{"NO_PROXY", "no_proxy"} {
			value, exists := cfg.Env[key]
			if !exists {
				value = os.Getenv(key)
			}
			excluded := false
			for _, host := range strings.Split(value, ",") {
				if host = strings.TrimSpace(host); host == "127.0.0.1" || host == "*" {
					excluded = true
				}
			}
			if !excluded {
				if strings.TrimSpace(value) != "" {
					value += ","
				}
				value += "127.0.0.1"
			}
			cfg.Env[key] = value
		}
	}
	return nil
}
