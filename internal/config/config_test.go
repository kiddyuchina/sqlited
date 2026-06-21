package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeConfig creates a config file in dir for use by a single test.
func writeConfig(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config %s: %v", path, err)
	}
	return path
}

func TestGenerateToken(t *testing.T) {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

	for _, length := range []int{0, 1, 32, 64} {
		t.Run(fmt.Sprintf("length-%d", length), func(t *testing.T) {
			tok, err := GenerateToken(length)
			if err != nil {
				t.Fatalf("GenerateToken failed: %v", err)
			}
			if len(tok) != length {
				t.Fatalf("expected token length %d, got %d", length, len(tok))
			}
			for i, ch := range tok {
				if !strings.ContainsRune(alphabet, ch) {
					t.Fatalf("token contains invalid char %q at position %d", ch, i)
				}
			}
		})
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()

	cases := []struct {
		name    string
		body    string
		wantErr string
		check   func(t *testing.T, cfg *Config)
	}{
		{
			name:    "missing file",
			body:    "",
			wantErr: "reading config",
		},
		{
			name:    "invalid json",
			body:    "{not json",
			wantErr: "parsing config",
		},
		{
			name:    "missing auth token",
			body:    `{"port":9090,"databases":{"a":{"path":"a.db"}}}`,
			wantErr: "auth_token",
		},
		{
			name:    "whitespace auth token",
			body:    `{"auth_token":"   ","databases":{"a":{"path":"a.db"}}}`,
			wantErr: "auth_token",
		},
		{
			name:    "no databases",
			body:    `{"auth_token":"secret"}`,
			wantErr: "no databases",
		},
		{
			name: "valid config fills defaults",
			body: `{"auth_token":"secret","databases":{"a":{"path":"a.db","wal_mode":false}}}`,
			check: func(t *testing.T, cfg *Config) {
				if cfg.Port != DefaultPort {
					t.Errorf("expected default port %d, got %d", DefaultPort, cfg.Port)
				}
				if cfg.AuthToken != "secret" {
					t.Errorf("expected token secret, got %s", cfg.AuthToken)
				}
				if len(cfg.Databases) != 1 {
					t.Errorf("expected 1 database, got %d", len(cfg.Databases))
				}
			},
		},
		{
			name: "explicit port preserved",
			body: `{"port":9090,"auth_token":"secret","databases":{"a":{"path":"a.db"}}}`,
			check: func(t *testing.T, cfg *Config) {
				if cfg.Port != 9090 {
					t.Errorf("expected port 9090, got %d", cfg.Port)
				}
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var path string
			if c.body != "" {
				path = writeConfig(t, dir, c.name+".json", c.body)
			} else {
				path = filepath.Join(dir, "does-not-exist.json")
			}

			cfg, err := Load(path)
			if c.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", c.wantErr)
				}
				if !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("expected error containing %q, got %v", c.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if c.check != nil {
				c.check(t, cfg)
			}
		})
	}
}

func TestInit(t *testing.T) {
	t.Run("creates valid config", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "sqlited.json")
		if err := Init(path); err != nil {
			t.Fatalf("Init failed: %v", err)
		}

		cfg, err := Load(path)
		if err != nil {
			t.Fatalf("load generated config: %v", err)
		}
		if cfg.Port != DefaultPort {
			t.Errorf("expected port %d, got %d", DefaultPort, cfg.Port)
		}
		if len(cfg.AuthToken) != TokenLength {
			t.Errorf("expected token length %d, got %d", TokenLength, len(cfg.AuthToken))
		}
		if _, ok := cfg.Databases["app_prod"]; !ok {
			t.Errorf("expected app_prod database in config")
		}
	})

	t.Run("refuses to overwrite", func(t *testing.T) {
		dir := t.TempDir()
		path := writeConfig(t, dir, "sqlited.json", "{}")
		if err := Init(path); err == nil {
			t.Fatal("expected error for existing config, got nil")
		}
	})
}
