package core

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sPROFFEs/PrAImate/internal/hosttls"
	"github.com/sPROFFEs/PrAImate/internal/launcher"
)

func (c *Core) prepareCLIHostTLS(ctx context.Context, cfg *EffectiveExecutionConfig) error {
	if cfg.CLI != "praimate-code" && cfg.CLI != "opencode" {
		return nil
	}
	hosts, err := c.ListLocalHosts(ctx)
	if err != nil {
		return err
	}
	endpoints := make([]string, 0, len(hosts)+1)
	for _, host := range hosts {
		endpoints = append(endpoints, host.Endpoint)
	}
	if cfg.Local != nil {
		endpoints = append(endpoints, cfg.Local.Endpoint)
	}
	global, err := launcher.LoadConfig()
	if err != nil {
		return err
	}
	if global != nil {
		endpoints = append(endpoints, global.DefaultLocalEndpoint)
	}
	trusted := map[string]string{}
	for _, endpoint := range endpoints {
		certificate, err := c.LocalHostTLSCertificate(ctx, endpoint)
		if err != nil {
			return err
		}
		if certificate == "" {
			continue
		}
		origin, err := hosttls.Origin(endpoint)
		if err != nil {
			return err
		}
		if _, _, err := hosttls.ParseTrust(endpoint, certificate); err != nil {
			selected := ""
			if cfg.Local != nil {
				selected, _ = hosttls.Origin(cfg.Local.Endpoint)
			}
			if origin == selected {
				return err
			}
			// An expired exception for another host must not block this CLI.
			continue
		}
		trusted[origin] = certificate
	}
	raw, err := json.Marshal(trusted)
	if err != nil {
		return err
	}
	if cfg.Env == nil {
		cfg.Env = map[string]string{}
	}
	// Also overwrite an inherited stale exception after consent is removed.
	cfg.Env["PRAIMATE_HOST_TLS"] = string(raw)
	// OpenCode's unmodified Node/Bun runtime reads extra public CA certificates
	// at startup. Never disable certificate or hostname verification.
	var bundle strings.Builder
	origins := make([]string, 0, len(trusted))
	for origin := range trusted {
		origins = append(origins, origin)
	}
	sort.Strings(origins)
	for _, origin := range origins {
		pem, err := hosttls.PublicBundle(origin, trusted[origin])
		if err != nil {
			return err
		}
		bundle.WriteString(pem)
	}
	dir, _, err := launcher.ConfigPaths()
	if err != nil {
		return err
	}
	dir = filepath.Join(dir, "tls")
	if bundle.Len() > 0 {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return err
		}
		hash := sha256.Sum256([]byte(bundle.String()))
		path := filepath.Join(dir, fmt.Sprintf("cli-ca-%x.pem", hash[:16]))
		if err := os.WriteFile(path, []byte(bundle.String()), 0600); err != nil {
			return err
		}
		cfg.Env["NODE_EXTRA_CA_CERTS"] = path
	} else if filepath.Dir(cfg.Env["NODE_EXTRA_CA_CERTS"]) == dir && strings.HasPrefix(filepath.Base(cfg.Env["NODE_EXTRA_CA_CERTS"]), "cli-ca-") {
		cfg.Env["NODE_EXTRA_CA_CERTS"] = ""
	}
	return c.prepareCLIHostTLSRoutes(ctx, cfg, trusted)
}
