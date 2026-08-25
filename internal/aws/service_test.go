package aws

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseProfiles(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config")
	contents := []byte(`[default]
region = us-east-1

[profile prod-admin]
region=us-west-2

[profile dev]
output = json

[sso-session company]
sso_region = us-east-1
`)
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}

	profiles, err := ParseProfiles(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []Profile{
		{Name: "default", Region: "us-east-1"},
		{Name: "dev"},
		{Name: "prod-admin", Region: "us-west-2"},
	}
	if len(profiles) != len(want) {
		t.Fatalf("ParseProfiles() = %#v, want %#v", profiles, want)
	}
	for index := range want {
		if profiles[index] != want[index] {
			t.Errorf("profile %d = %#v, want %#v", index, profiles[index], want[index])
		}
	}
}
