package main

import (
	"bytes"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func env(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func TestLoadConfig(t *testing.T) {
	tests := []struct {
		name    string
		values  map[string]string
		wantErr bool
	}{
		{"http", map[string]string{"HEALTHCHECK_URL": "http://localhost/health"}, false},
		{"https", map[string]string{"HEALTHCHECK_URL": "https://localhost/health"}, false},
		{"missing URL", nil, true},
		{"relative URL", map[string]string{"HEALTHCHECK_URL": "/health"}, true},
		{"missing host", map[string]string{"HEALTHCHECK_URL": "http:///health"}, true},
		{"wrong scheme", map[string]string{"HEALTHCHECK_URL": "ftp://localhost"}, true},
		{"malformed URL", map[string]string{"HEALTHCHECK_URL": "http://%zz"}, true},
		{"zero timeout", map[string]string{"HEALTHCHECK_URL": "http://localhost", "HEALTHCHECK_TIMEOUT": "0s"}, true},
		{"negative timeout", map[string]string{"HEALTHCHECK_URL": "http://localhost", "HEALTHCHECK_TIMEOUT": "-1s"}, true},
		{"invalid timeout", map[string]string{"HEALTHCHECK_URL": "http://localhost", "HEALTHCHECK_TIMEOUT": "soon"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := loadConfig(env(tt.values))
			if (err != nil) != tt.wantErr {
				t.Fatalf("loadConfig() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}

	cfg, err := loadConfig(env(map[string]string{"HEALTHCHECK_URL": " http://localhost/health "}))
	if err != nil || cfg.url != "http://localhost/health" || cfg.timeout != 5*time.Second {
		t.Fatalf("unexpected defaults: %+v, %v", cfg, err)
	}
	cfg, err = loadConfig(env(map[string]string{
		"HEALTHCHECK_URL":     "https://localhost",
		"HEALTHCHECK_TIMEOUT": " 250ms ",
		"HEALTHCHECK_CA_FILE": " /certs/ca.pem ",
	}))
	if err != nil || cfg.timeout != 250*time.Millisecond || cfg.caFile != "/certs/ca.pem" {
		t.Fatalf("unexpected overrides: %+v, %v", cfg, err)
	}
}

func TestHTTPStatus(t *testing.T) {
	for _, status := range []int{200, 204, 299, 300, 301, 401, 404, 500, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Errorf("method = %s, want GET", r.Method)
				}
				w.WriteHeader(status)
			}))
			defer server.Close()

			var stderr bytes.Buffer
			code := run(env(map[string]string{"HEALTHCHECK_URL": server.URL}), &stderr)
			want := 1
			if status >= 200 && status < 300 {
				want = 0
			}
			if code != want {
				t.Fatalf("exit = %d, want %d; stderr: %s", code, want, &stderr)
			}
			if want == 0 && stderr.Len() != 0 {
				t.Fatalf("successful check wrote output: %s", &stderr)
			}
		})
	}
}

func TestRedirectIsNotFollowed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" {
			t.Error("redirect was followed")
			return
		}
		http.Redirect(w, r, "/login", http.StatusFound)
	}))
	defer server.Close()
	if code := run(env(map[string]string{"HEALTHCHECK_URL": server.URL}), io.Discard); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
}

func TestTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()
	var stderr bytes.Buffer
	code := run(env(map[string]string{
		"HEALTHCHECK_URL":     server.URL,
		"HEALTHCHECK_TIMEOUT": "50ms",
	}), &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "deadline exceeded") {
		t.Fatalf("exit = %d; stderr: %s", code, &stderr)
	}
}

func TestConnectionFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	endpoint := server.URL
	server.Close()
	if code := run(env(map[string]string{"HEALTHCHECK_URL": endpoint}), io.Discard); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
}

func TestHTTPS(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	values := map[string]string{"HEALTHCHECK_URL": server.URL}
	if code := run(env(values), io.Discard); code != 1 {
		t.Fatalf("untrusted certificate: exit = %d, want 1", code)
	}

	caPath := filepath.Join(t.TempDir(), "ca.pem")
	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	if err := os.WriteFile(caPath, cert, 0600); err != nil {
		t.Fatal(err)
	}
	values["HEALTHCHECK_CA_FILE"] = caPath
	var stderr bytes.Buffer
	if code := run(env(values), &stderr); code != 0 {
		t.Fatalf("trusted certificate: exit = %d; stderr: %s", code, &stderr)
	}

	// Trusting a CA must not disable hostname verification.
	client, err := newHTTPClient(config{timeout: time.Second, caFile: caPath})
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	client.Transport.(*http.Transport).TLSClientConfig.ServerName = "wrong-host.invalid"
	if err := check(client, server.URL); err == nil {
		t.Fatal("certificate with mismatched hostname was accepted")
	}
}

func TestConfigExitCode(t *testing.T) {
	if code := run(env(nil), io.Discard); code != 1 {
		t.Fatalf("missing URL: exit = %d, want 1", code)
	}
	missing := filepath.Join(t.TempDir(), "missing.pem")
	invalid := filepath.Join(t.TempDir(), "invalid.pem")
	if err := os.WriteFile(invalid, []byte("not a certificate"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{missing, invalid} {
		var stderr bytes.Buffer
		code := run(env(map[string]string{
			"HEALTHCHECK_URL":     "https://localhost",
			"HEALTHCHECK_CA_FILE": path,
		}), &stderr)
		if code != 1 || !strings.Contains(stderr.String(), "HEALTHCHECK_CA_FILE") {
			t.Fatalf("exit = %d; stderr: %s", code, &stderr)
		}
	}
}
