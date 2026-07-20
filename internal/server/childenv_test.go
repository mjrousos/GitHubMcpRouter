package server

import (
	"slices"
	"testing"

	"github.com/mjrousos/GitHubMcpRouter/internal/githubapp"
)

func aliasConfig() *githubapp.Config {
	return &githubapp.Config{
		AppID:            42,
		PrivateKey:       []byte("PEM"),
		AppIDSource:      githubapp.EnvAppIDAlias,
		PrivateKeySource: githubapp.EnvPrivateKeyAlias,
	}
}

// TestChildBaseEnv_InjectsCanonicalFromAliases verifies that when the router was
// configured via the MCP_ROUTER_* aliases (so the canonical GITHUB_ names are
// absent), childBaseEnv adds them for the spawned children.
func TestChildBaseEnv_InjectsCanonicalFromAliases(t *testing.T) {
	got := childBaseEnv([]string{"PATH=/usr/bin"}, aliasConfig())

	if !slices.Contains(got, "GITHUB_APP_ID=42") {
		t.Errorf("expected GITHUB_APP_ID=42 to be injected, got %v", got)
	}
	if !slices.Contains(got, "GITHUB_APP_PRIVATE_KEY=PEM") {
		t.Errorf("expected GITHUB_APP_PRIVATE_KEY=PEM to be injected, got %v", got)
	}
	if !slices.Contains(got, "PATH=/usr/bin") {
		t.Errorf("expected the original environment to be preserved, got %v", got)
	}
}

// TestChildBaseEnv_DoesNotOverwriteCanonical verifies that existing canonical
// values (including a key supplied via the path variable) are left untouched.
func TestChildBaseEnv_DoesNotOverwriteCanonical(t *testing.T) {
	env := []string{"GITHUB_APP_ID=7", "GITHUB_APP_PRIVATE_KEY_PATH=/k.pem"}
	got := childBaseEnv(slices.Clone(env), aliasConfig())

	if !slices.Equal(got, env) {
		t.Errorf("expected env to be unchanged, got %v", got)
	}
}

// TestChildBaseEnv_InjectsOnlyMissing verifies each canonical variable is
// injected independently: here the app ID is already present but the key is not.
func TestChildBaseEnv_InjectsOnlyMissing(t *testing.T) {
	got := childBaseEnv([]string{"GITHUB_APP_ID=7"}, aliasConfig())

	if slices.Contains(got, "GITHUB_APP_ID=42") {
		t.Errorf("must not override an existing GITHUB_APP_ID, got %v", got)
	}
	if !slices.Contains(got, "GITHUB_APP_ID=7") {
		t.Errorf("existing GITHUB_APP_ID=7 should remain, got %v", got)
	}
	if !slices.Contains(got, "GITHUB_APP_PRIVATE_KEY=PEM") {
		t.Errorf("expected the missing private key to be injected, got %v", got)
	}
}

// TestChildBaseEnv_NilConfig verifies a nil config is a no-op.
func TestChildBaseEnv_NilConfig(t *testing.T) {
	env := []string{"PATH=/usr/bin"}
	got := childBaseEnv(slices.Clone(env), nil)
	if !slices.Equal(got, env) {
		t.Errorf("expected env to be unchanged for a nil config, got %v", got)
	}
}

func TestEnvHas(t *testing.T) {
	env := []string{"GITHUB_APP_ID=5", "EMPTY=", "SPACES=  ", "GITHUB_APP_PRIVATE_KEY_PATH=/k.pem"}
	cases := []struct {
		key  string
		want bool
	}{
		{"GITHUB_APP_ID", true},
		{"EMPTY", false},   // present but empty counts as absent
		{"SPACES", false},  // whitespace-only counts as absent
		{"MISSING", false}, // not present at all
		// A prefix must match up to '=', so this must not match the _PATH entry.
		{"GITHUB_APP_PRIVATE_KEY", false},
		{"GITHUB_APP_PRIVATE_KEY_PATH", true},
	}
	for _, tc := range cases {
		if got := envHas(env, tc.key); got != tc.want {
			t.Errorf("envHas(%q) = %v, want %v", tc.key, got, tc.want)
		}
	}
}
