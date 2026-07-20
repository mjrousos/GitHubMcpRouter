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

// TestChildBaseEnv_CanonicalSourcesNotInjected verifies that when the values
// came from canonical names (app ID from GITHUB_APP_ID, key from the path
// variable), nothing is injected — the children inherit those names directly.
// This also covers the case-sensitivity concern: the decision keys off the
// resolved source (via os.Getenv), not a case-sensitive scan of the env slice.
func TestChildBaseEnv_CanonicalSourcesNotInjected(t *testing.T) {
	cfg := &githubapp.Config{
		AppID:            7,
		PrivateKey:       []byte("FROM-FILE"),
		AppIDSource:      githubapp.EnvAppID,
		PrivateKeySource: githubapp.EnvPrivateKeyPath,
	}
	env := []string{"PATH=/usr/bin"}
	got := childBaseEnv(slices.Clone(env), cfg)

	if !slices.Equal(got, env) {
		t.Errorf("expected env to be unchanged for canonical sources, got %v", got)
	}
}

// TestChildBaseEnv_CanonicalInlineKeyNotInjected verifies a key from the
// canonical inline variable is not re-injected.
func TestChildBaseEnv_CanonicalInlineKeyNotInjected(t *testing.T) {
	cfg := &githubapp.Config{
		AppID:            7,
		PrivateKey:       []byte("PEM"),
		AppIDSource:      githubapp.EnvAppID,
		PrivateKeySource: githubapp.EnvPrivateKey,
	}
	if got := childBaseEnv(nil, cfg); len(got) != 0 {
		t.Errorf("expected no injection for canonical sources, got %v", got)
	}
}

// TestChildBaseEnv_InjectsOnlyAliasedValues verifies each variable is bridged
// independently: here the app ID came from the alias but the key from a path,
// so only the app ID is injected (never the path-sourced key inline).
func TestChildBaseEnv_InjectsOnlyAliasedValues(t *testing.T) {
	cfg := &githubapp.Config{
		AppID:            42,
		PrivateKey:       []byte("FROM-FILE"),
		AppIDSource:      githubapp.EnvAppIDAlias,
		PrivateKeySource: githubapp.EnvPrivateKeyPath,
	}
	got := childBaseEnv(nil, cfg)

	if !slices.Contains(got, "GITHUB_APP_ID=42") {
		t.Errorf("expected the aliased app ID to be injected, got %v", got)
	}
	if slices.Contains(got, "GITHUB_APP_PRIVATE_KEY=FROM-FILE") {
		t.Errorf("must not inject a path-sourced key inline, got %v", got)
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
