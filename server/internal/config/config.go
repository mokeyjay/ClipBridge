// Package config loads the ClipBridge server's on-disk configuration. These are
// process-level knobs (listen addresses, runtime dir, trusted proxies). Business
// configuration that the Web console can change lives in SQLite, not here.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Config is the server process configuration, normally loaded from config.yaml.
type Config struct {
	// DeviceListenAddress hosts the self-signed HTTPS + WSS device API. Clients
	// pin this port's certificate fingerprint.
	DeviceListenAddress string `yaml:"device_listen_address"`
	// WebListenAddress hosts the HTTP + WSS Web console. A reverse proxy is
	// expected to terminate HTTPS in front of it for public exposure.
	WebListenAddress string `yaml:"web_listen_address"`
	// RuntimeDir holds the SQLite db, certificates, data/ and logs.
	RuntimeDir string `yaml:"runtime_dir"`
	// PublicBaseURL is the externally reachable base URL, shown during pairing.
	PublicBaseURL string `yaml:"public_base_url"`
	// TrustedProxyCIDRs lists proxy networks whose forwarded client IP is trusted
	// for pairing rate limiting on the Web port.
	TrustedProxyCIDRs []string `yaml:"trusted_proxy_cidrs"`
}

// Default returns the baseline configuration used when no file is present.
func Default() Config {
	return Config{
		DeviceListenAddress: ":8443",
		WebListenAddress:    ":8080",
		RuntimeDir:          "./runtime",
		PublicBaseURL:       "",
		TrustedProxyCIDRs:   nil,
	}
}

// Load reads config.yaml from dir, layering any present fields over Default().
// A missing file is not an error: defaults are returned so the server can boot
// on a fresh machine with zero configuration.
func Load(dir string) (Config, error) {
	cfg := Default()
	path := filepath.Join(dir, "config.yaml")
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("config: read %s: %w", path, err)
	}
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return cfg, fmt.Errorf("config: parse %s: %w", path, err)
	}
	return cfg, nil
}
