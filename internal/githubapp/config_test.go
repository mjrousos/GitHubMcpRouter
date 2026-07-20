package githubapp

import (
	"os"
	"path/filepath"
	"testing"
)

// setEnv sets all GitHub App environment variables for a test, using empty
// strings for any that are not relevant. Empty values are treated as unset by
// LoadConfigFromEnv, which also isolates the test from the host environment.
// The MCP_ROUTER_ aliases are cleared here; tests that exercise them set them
// explicitly.
func setEnv(t *testing.T, appID, keyPath, keyInline, apiURL string) {
	t.Helper()
	t.Setenv(EnvAppID, appID)
	t.Setenv(EnvAppIDAlias, "")
	t.Setenv(EnvPrivateKeyPath, keyPath)
	t.Setenv(EnvPrivateKey, keyInline)
	t.Setenv(EnvPrivateKeyAlias, "")
	t.Setenv(EnvAPIURL, apiURL)
}

func TestLoadConfigFromEnv_Disabled(t *testing.T) {
	setEnv(t, "", "", "", "")

	cfg, err := LoadConfigFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg != nil {
		t.Fatalf("expected nil config when nothing is set, got %+v", cfg)
	}
}

func TestLoadConfigFromEnv_InlineKey(t *testing.T) {
	setEnv(t, "12345", "", "PEM-CONTENTS", "")

	cfg, err := LoadConfigFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected a config")
	}
	if cfg.AppID != 12345 {
		t.Errorf("AppID = %d, want 12345", cfg.AppID)
	}
	if string(cfg.PrivateKey) != "PEM-CONTENTS" {
		t.Errorf("PrivateKey = %q, want %q", cfg.PrivateKey, "PEM-CONTENTS")
	}
	if cfg.AppIDSource != EnvAppID {
		t.Errorf("AppIDSource = %q, want %q", cfg.AppIDSource, EnvAppID)
	}
	if cfg.PrivateKeySource != EnvPrivateKey {
		t.Errorf("PrivateKeySource = %q, want %q", cfg.PrivateKeySource, EnvPrivateKey)
	}
}

// TestLoadConfigFromEnv_Aliases verifies that the MCP_ROUTER_ aliases are used
// when the GITHUB_ names are unset, and that the sources are reported.
func TestLoadConfigFromEnv_Aliases(t *testing.T) {
	setEnv(t, "", "", "", "")
	t.Setenv(EnvAppIDAlias, "9001")
	t.Setenv(EnvPrivateKeyAlias, "ALIAS-PEM")

	cfg, err := LoadConfigFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected a config")
	}
	if cfg.AppID != 9001 {
		t.Errorf("AppID = %d, want 9001", cfg.AppID)
	}
	if string(cfg.PrivateKey) != "ALIAS-PEM" {
		t.Errorf("PrivateKey = %q, want %q", cfg.PrivateKey, "ALIAS-PEM")
	}
	if cfg.AppIDSource != EnvAppIDAlias {
		t.Errorf("AppIDSource = %q, want %q", cfg.AppIDSource, EnvAppIDAlias)
	}
	if cfg.PrivateKeySource != EnvPrivateKeyAlias {
		t.Errorf("PrivateKeySource = %q, want %q", cfg.PrivateKeySource, EnvPrivateKeyAlias)
	}
}

// TestLoadConfigFromEnv_PrimaryPreferredOverAlias verifies that when both a
// primary (GITHUB_) variable and its alias are set, the primary wins.
func TestLoadConfigFromEnv_PrimaryPreferredOverAlias(t *testing.T) {
	setEnv(t, "111", "", "PRIMARY-PEM", "")
	t.Setenv(EnvAppIDAlias, "222")
	t.Setenv(EnvPrivateKeyAlias, "ALIAS-PEM")

	cfg, err := LoadConfigFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.AppID != 111 {
		t.Errorf("AppID = %d, want 111 (GITHUB_APP_ID should win)", cfg.AppID)
	}
	if cfg.AppIDSource != EnvAppID {
		t.Errorf("AppIDSource = %q, want %q", cfg.AppIDSource, EnvAppID)
	}
	if string(cfg.PrivateKey) != "PRIMARY-PEM" {
		t.Errorf("PrivateKey = %q, want %q", cfg.PrivateKey, "PRIMARY-PEM")
	}
	if cfg.PrivateKeySource != EnvPrivateKey {
		t.Errorf("PrivateKeySource = %q, want %q", cfg.PrivateKeySource, EnvPrivateKey)
	}
}

func TestLoadConfigFromEnv_PathPreferredOverInline(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "key.pem")
	if err := os.WriteFile(keyPath, []byte("FROM-FILE"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Both path and inline are set; the path must win.
	setEnv(t, "42", keyPath, "FROM-INLINE", "https://ghe.example.com/api/v3")

	cfg, err := LoadConfigFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(cfg.PrivateKey) != "FROM-FILE" {
		t.Errorf("PrivateKey = %q, want file contents %q", cfg.PrivateKey, "FROM-FILE")
	}
	if cfg.PrivateKeySource != EnvPrivateKeyPath {
		t.Errorf("PrivateKeySource = %q, want %q", cfg.PrivateKeySource, EnvPrivateKeyPath)
	}
	if cfg.APIBaseURL != "https://ghe.example.com/api/v3" {
		t.Errorf("APIBaseURL = %q", cfg.APIBaseURL)
	}
}

func TestLoadConfigFromEnv_AppIDTrimmed(t *testing.T) {
	setEnv(t, "  12345  ", "", "PEM", "")

	cfg, err := LoadConfigFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.AppID != 12345 {
		t.Errorf("AppID = %d, want 12345", cfg.AppID)
	}
}

func TestLoadConfigFromEnv_Errors(t *testing.T) {
	t.Run("missing app id", func(t *testing.T) {
		setEnv(t, "", "", "PEM", "")
		if _, err := LoadConfigFromEnv(); err == nil {
			t.Fatal("expected error when app ID is missing")
		}
	})

	t.Run("non-numeric app id", func(t *testing.T) {
		setEnv(t, "not-a-number", "", "PEM", "")
		if _, err := LoadConfigFromEnv(); err == nil {
			t.Fatal("expected error for non-numeric app ID")
		}
	})

	t.Run("non-positive app id", func(t *testing.T) {
		for _, id := range []string{"0", "-5"} {
			setEnv(t, id, "", "PEM", "")
			if _, err := LoadConfigFromEnv(); err == nil {
				t.Errorf("expected error for app ID %q", id)
			}
		}
	})

	t.Run("insecure api url", func(t *testing.T) {
		setEnv(t, "123", "", "PEM", "http://ghe.example.com/api/v3")
		if _, err := LoadConfigFromEnv(); err == nil {
			t.Fatal("expected error for non-https GITHUB_API_URL")
		}
	})

	t.Run("malformed api url", func(t *testing.T) {
		setEnv(t, "123", "", "PEM", "https://ghe.example.com/api/v3?token=leak")
		if _, err := LoadConfigFromEnv(); err == nil {
			t.Fatal("expected error for GITHUB_API_URL with a query string")
		}
	})

	t.Run("missing key", func(t *testing.T) {
		setEnv(t, "123", "", "", "")
		if _, err := LoadConfigFromEnv(); err == nil {
			t.Fatal("expected error when no private key is provided")
		}
	})

	t.Run("unreadable key path", func(t *testing.T) {
		setEnv(t, "123", filepath.Join(t.TempDir(), "does-not-exist.pem"), "", "")
		if _, err := LoadConfigFromEnv(); err == nil {
			t.Fatal("expected error when key path cannot be read")
		}
	})
}
