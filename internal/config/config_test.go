package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	contents := []byte(`targets:
  - name: orders
    profile: development
    region: us-east-1
    host: orders.internal
    remote_port: 5432
`)
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(cfg.Targets) != 1 {
		t.Fatalf("Load() targets = %d, want 1", len(cfg.Targets))
	}
	if cfg.Targets[0].Host != "orders.internal" {
		t.Errorf("Load() host = %q, want orders.internal", cfg.Targets[0].Host)
	}
}

func TestConfig_TargetsFor(t *testing.T) {
	t.Parallel()
	cfg := Config{Targets: []Target{
		{Name: "global", Host: "global.internal", RemotePort: 443},
		{Name: "dev", Profile: "dev", Region: "us-east-1", Host: "dev.internal", RemotePort: 5432},
		{Name: "prod", Profile: "prod", Region: "us-east-1", Host: "prod.internal", RemotePort: 5432},
	}}

	targets := cfg.TargetsFor("dev", "us-east-1")
	if len(targets) != 2 {
		t.Fatalf("TargetsFor() returned %d targets, want 2", len(targets))
	}
}

func TestSave(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "nested", "config.yaml")
	cfg := Config{
		Targets: []Target{},
		Bookmarks: []Bookmark{
			{
				Name:         "orders",
				Type:         SessionTypeRemoteHost,
				Profile:      "dev",
				Region:       "us-east-1",
				InstanceID:   "i-001",
				InstanceName: "bastion",
				Host:         "orders.internal",
				RemotePort:   5432,
				LocalPort:    15432,
			},
		},
	}

	if err := Save(path, cfg); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(loaded.Bookmarks) != 1 || loaded.Bookmarks[0].Name != "orders" {
		t.Fatalf("saved bookmarks = %#v, want orders", loaded.Bookmarks)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("config permissions = %o, want 600", info.Mode().Perm())
	}
}

func TestConfig_AddBookmark(t *testing.T) {
	t.Parallel()
	bookmark := Bookmark{
		Name:       "api",
		Type:       SessionTypeShell,
		Profile:    "dev",
		Region:     "us-east-1",
		InstanceID: "i-001",
	}
	cfg := emptyConfig()
	if err := cfg.AddBookmark(bookmark); err != nil {
		t.Fatalf("AddBookmark() error = %v", err)
	}
	if err := cfg.AddBookmark(bookmark); err == nil {
		t.Fatal("AddBookmark() duplicate error = nil, want error")
	}
}
