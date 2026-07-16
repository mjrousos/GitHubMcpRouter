package downstream

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseAllowedOrgs(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want map[string]struct{}
	}{
		{"empty means all", "", nil},
		{"whitespace means all", "   ", nil},
		{"malformed denies all", "   ,  ,", map[string]struct{}{}},
		{"single", "Octo-Org", map[string]struct{}{"octo-org": {}}},
		{"multiple mixed case + spaces", " Org1, org2 ,ORG3 ", map[string]struct{}{"org1": {}, "org2": {}, "org3": {}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseAllowedOrgs(tc.raw); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("parseAllowedOrgs(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}

func TestResolveBinary_EnvOverride(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "github-mcp-server.exe")
	if err := os.WriteFile(bin, []byte("stub"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvServerPath, bin)

	got, err := ResolveBinary()
	if err != nil {
		t.Fatalf("ResolveBinary: %v", err)
	}
	if got != bin {
		t.Errorf("ResolveBinary = %q, want %q", got, bin)
	}
}

func TestResolveBinary_EnvOverrideMissing(t *testing.T) {
	t.Setenv(EnvServerPath, filepath.Join(t.TempDir(), "does-not-exist"))
	if _, err := ResolveBinary(); err == nil {
		t.Fatal("expected an error when the override path does not exist")
	}
}
