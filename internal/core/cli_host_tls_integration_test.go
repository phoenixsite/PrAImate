package core

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sPROFFEs/PrAImate/internal/hosttls"
)

// Exercises the actual compiled fork and core consent, without real providers.
// Unit tests of the transport helper cannot catch bundle initialization defects.
func TestPraimateCodeInstalledHostTLS(t *testing.T) {
	bin := os.Getenv("PRAIMATE_TEST_CODE_BINARY")
	if bin == "" {
		t.Skip("set PRAIMATE_TEST_CODE_BINARY to test the compiled fork with HTTPS")
	}
	if !filepath.IsAbs(bin) {
		t.Fatal("PRAIMATE_TEST_CODE_BINARY must be absolute")
	}
	for _, mode := range []string{"self-signed", "chain", "ca", "leaf-only", "missing-root", "legacy-pin"} {
		t.Run(mode, func(t *testing.T) { testInstalledHostTLS(t, bin, "praimate-code", mode) })
	}
}

func TestOpenCodeInstalledHostTLS(t *testing.T) {
	bin := os.Getenv("PRAIMATE_TEST_OPENCODE_BINARY")
	if bin == "" {
		t.Skip("set PRAIMATE_TEST_OPENCODE_BINARY to test unmodified OpenCode with HTTPS")
	}
	if !filepath.IsAbs(bin) {
		t.Fatal("PRAIMATE_TEST_OPENCODE_BINARY must be absolute")
	}
	for _, mode := range []string{"self-signed", "chain", "ca", "leaf-only", "missing-root", "legacy-pin"} {
		t.Run(mode, func(t *testing.T) { testInstalledHostTLS(t, bin, "opencode", mode) })
	}
}

func testInstalledHostTLS(t *testing.T, bin, cli, mode string) {
	root := t.TempDir()
	isolateOpenCodeFixture(t, root)
	c := nativeTestCore(t)
	var calls atomic.Int32
	s := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture-key" || r.Header.Get(cliTLSHeader) != "" {
			t.Error("provider credentials were changed or local transport authentication leaked upstream")
			http.Error(w, "invalid fixture credentials", http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(nativeSSE("trusted-model-ok", "stop")))
	}))
	if mode != "self-signed" {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		root := &x509.Certificate{Subject: pkix.Name{CommonName: "PrAImate fixture CA"}, SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
		rootDER, err := x509.CreateCertificate(rand.Reader, root, root, &key.PublicKey, key)
		if err != nil {
			t.Fatal(err)
		}
		root, err = x509.ParseCertificate(rootDER)
		if err != nil {
			t.Fatal(err)
		}
		if mode == "missing-root" || mode == "legacy-pin" {
			intermediateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			intermediate := &x509.Certificate{Subject: pkix.Name{CommonName: "PrAImate fixture intermediate"}, SerialNumber: big.NewInt(3), NotBefore: root.NotBefore, NotAfter: root.NotAfter, IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
			rootDER, err = x509.CreateCertificate(rand.Reader, intermediate, root, &intermediateKey.PublicKey, key)
			if err != nil {
				t.Fatal(err)
			}
			root, err = x509.ParseCertificate(rootDER)
			if err != nil {
				t.Fatal(err)
			}
			key = intermediateKey
		}
		leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		leaf := &x509.Certificate{Subject: pkix.Name{CommonName: "PrAImate fixture endpoint"}, SerialNumber: big.NewInt(2), NotBefore: root.NotBefore, NotAfter: root.NotAfter, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
		leafDER, err := x509.CreateCertificate(rand.Reader, leaf, root, &leafKey.PublicKey, key)
		if err != nil {
			t.Fatal(err)
		}
		s.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{leafDER, rootDER}, PrivateKey: leafKey}}}
		if mode == "leaf-only" {
			s.TLS.Certificates[0].Certificate = [][]byte{leafDER}
		}
	}
	s.StartTLS()
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cert, err := hosttls.Inspect(ctx, s.URL)
	if err != nil {
		t.Fatal(err)
	}
	material := cert.Trust
	if mode == "legacy-pin" {
		material = cert.PEM
	}
	if mode == "ca" {
		trust, _, err := hosttls.ParseTrust(s.URL, material)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(hosttls.Trust{Authorities: trust.Authorities})
		if err != nil {
			t.Fatal(err)
		}
		material = string(raw)
	}
	if err := c.TrustLocalHostCertificate(ctx, s.URL, material); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(root, "project")
	if err := os.Mkdir(project, 0700); err != nil {
		t.Fatal(err)
	}
	cfg, err := c.ResolveExecutionConfig(ctx, ExecutionRequest{Surface: SurfaceChat, CLI: cli, Cwd: project, Local: &ChatLocalEndpoint{Endpoint: s.URL, Model: "fixture", APIKey: "fixture-key"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.PrepareExecution(ctx, cfg); err != nil {
		t.Fatal(err)
	}
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
	adapter := NewPraimateCodeAdapter()
	if cli == "opencode" {
		adapter = NewOpenCodeAdapter()
	}
	adapter.bin = bin
	reply, err := adapter.SingleShotStream(ctx, SingleShotOpts{Cwd: project, Message: "Reply with the fixture response", Model: cfg.Model, Env: cfg.Env}, nil)
	if err != nil || reply == nil || reply.ExitCode != 0 || reply.SessionID == "" || !strings.Contains(reply.Text, "trusted-model-ok") || calls.Load() == 0 {
		t.Fatalf("accepted HTTPS completion: reply=%+v requests=%d err=%v", reply, calls.Load(), err)
	}
	resumed, err := adapter.ResumeStream(ctx, reply.SessionID, ResumeOpts{Cwd: project, Message: "Reply again", Model: cfg.Model, Env: cfg.Env}, nil)
	if err != nil || resumed == nil || resumed.ExitCode != 0 || resumed.SessionID != reply.SessionID || !strings.Contains(resumed.Text, "trusted-model-ok") {
		t.Fatalf("resumed HTTPS completion: reply=%+v err=%v", resumed, err)
	}
}
