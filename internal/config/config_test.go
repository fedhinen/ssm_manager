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
