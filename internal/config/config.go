// Package config handles loading and generating sqlited.json configuration.
package config

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
)

const (
	DefaultConfigFile  = "sqlited.json"
	DefaultPort        = 4567
	DefaultBusyTimeout = 5000
	TokenLength        = 32
)

// DatabaseConfig describes a single managed SQLite database instance.
type DatabaseConfig struct {
	Path          string `json:"path"`
	BusyTimeoutMS int    `json:"busy_timeout_ms"`
	WALMode       bool   `json:"wal_mode"`
}

// Config is the top-level configuration loaded from sqlited.json.
type Config struct {
	Port      int                       `json:"port"`
	AuthToken string                    `json:"auth_token"`
	Databases map[string]DatabaseConfig `json:"databases"`
}

// Load reads and parses the configuration file at path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config %q: %w", path, err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config %q: %w", path, err)
	}
	if cfg.Port == 0 {
		cfg.Port = DefaultPort
	}
	if strings.TrimSpace(cfg.AuthToken) == "" {
		return nil, errors.New("config is missing a non-empty auth_token")
	}
	if len(cfg.Databases) == 0 {
		return nil, errors.New("config defines no databases")
	}
	return &cfg, nil
}

// GenerateToken returns a cryptographically secure random string of length n.
func GenerateToken(n int) (string, error) {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	// Rejection sampling avoids the modulo bias that would otherwise make the
	// first (256 mod len(alphabet)) characters slightly more frequent.
	limit := byte(256 - (256 % len(alphabet)))
	out := make([]byte, n)
	buf := make([]byte, 1)
	for i := 0; i < n; {
		if _, err := rand.Read(buf); err != nil {
			return "", err
		}
		if buf[0] >= limit {
			continue
		}
		out[i] = alphabet[int(buf[0])%len(alphabet)]
		i++
	}
	return string(out), nil
}

// Init writes a default configuration file to path unless it already exists.
func Init(path string) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("config file %q already exists; refusing to overwrite", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("checking config file %q: %w", path, err)
	}

	token, err := GenerateToken(TokenLength)
	if err != nil {
		return fmt.Errorf("generating auth token: %w", err)
	}

	cfg := Config{
		Port:      DefaultPort,
		AuthToken: token,
		Databases: map[string]DatabaseConfig{
			"app_prod": {
				Path:          "./data/app.db",
				BusyTimeoutMS: DefaultBusyTimeout,
				WALMode:       true,
			},
		},
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding default config: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("writing config file %q: %w", path, err)
	}

	log.Printf("Created %q with a freshly generated auth_token.", path)
	// Print the secret straight to stdout rather than through the logger so it
	// is not captured by structured/file-based log sinks.
	fmt.Fprintf(os.Stdout, "Auth token: %s\n", token)
	return nil
}
