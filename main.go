package main

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type config struct {
	url     string
	timeout time.Duration
	caFile  string
}

func loadConfig(getenv func(string) string) (config, error) {
	cfg := config{
		url:     strings.TrimSpace(getenv("HEALTHCHECK_URL")),
		timeout: 5 * time.Second,
		caFile:  strings.TrimSpace(getenv("HEALTHCHECK_CA_FILE")),
	}

	u, err := url.Parse(cfg.url)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return config{}, fmt.Errorf("HEALTHCHECK_URL must be an absolute http:// or https:// URL")
	}

	if value := strings.TrimSpace(getenv("HEALTHCHECK_TIMEOUT")); value != "" {
		cfg.timeout, err = time.ParseDuration(value)
		if err != nil || cfg.timeout <= 0 {
			return config{}, fmt.Errorf("HEALTHCHECK_TIMEOUT must be a positive duration, e.g. 5s")
		}
	}

	return cfg, nil
}

func newHTTPClient(cfg config) (*http.Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if cfg.caFile != "" {
		pem, err := os.ReadFile(cfg.caFile)
		if err != nil {
			return nil, fmt.Errorf("read HEALTHCHECK_CA_FILE: %w", err)
		}
		roots, err := x509.SystemCertPool()
		if err != nil {
			// A scratch container may not have any system certificates.
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("HEALTHCHECK_CA_FILE contains no usable PEM certificates")
		}
		transport.TLSClientConfig = &tls.Config{RootCAs: roots}
	}

	return &http.Client{
		Timeout:   cfg.timeout,
		Transport: transport,
		// A redirect to a login or error page must not report a healthy service.
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}, nil
}

func check(client *http.Client, endpoint string) error {
	response, err := client.Get(endpoint)
	if err != nil {
		return fmt.Errorf("HTTP request failed: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("unexpected HTTP status: %s (expected 200–299)", response.Status)
	}
	return nil
}

func run(getenv func(string) string, stderr io.Writer) int {
	cfg, err := loadConfig(getenv)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	client, err := newHTTPClient(cfg)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer client.CloseIdleConnections()

	if err := check(client, cfg.url); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func main() {
	os.Exit(run(os.Getenv, os.Stderr))
}
