package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/victorruiz/ssm-manager/internal/awscli"
	appconfig "github.com/victorruiz/ssm-manager/internal/config"
)

func TestFilterInstances(t *testing.T) {
	t.Parallel()
	instances := []awscli.Instance{
		{ID: "i-001", Name: "api-dev", PrivateIP: "10.0.1.4"},
		{ID: "i-002", Name: "worker", PrivateIP: "10.0.2.5"},
	}
	tests := []struct {
		name   string
		filter string
		wantID string
	}{
		{name: "by name", filter: "API", wantID: "i-001"},
		{name: "by ID", filter: "002", wantID: "i-002"},
		{name: "by IP", filter: "10.0.1", wantID: "i-001"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := filterInstances(instances, tt.filter)
			if len(got) != 1 || got[0].ID != tt.wantID {
				t.Fatalf("filterInstances(%q) = %#v, want ID %s", tt.filter, got, tt.wantID)
			}
		})
	}
}

func TestIsInteractiveTerminal(t *testing.T) {
	t.Parallel()
	var input bytes.Buffer
	var output bytes.Buffer
	if isInteractiveTerminal(&input, &output) {
		t.Fatal("isInteractiveTerminal() = true for buffers, want false")
	}
}

func TestBookmarkLabel(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		bookmark appconfig.Bookmark
		contains string
	}{
		{
			name: "shell",
			bookmark: appconfig.Bookmark{
				Name: "api", Type: appconfig.SessionTypeShell, InstanceName: "api-dev",
			},
			contains: "api-dev",
		},
		{
			name: "remote host",
			bookmark: appconfig.Bookmark{
				Name: "orders", Type: appconfig.SessionTypeRemoteHost,
				InstanceName: "bastion", Host: "db.internal", RemotePort: 5432, LocalPort: 15432,
			},
			contains: "localhost:15432 -> db.internal:5432 via bastion",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := bookmarkLabel(tt.bookmark); !strings.Contains(got, tt.contains) {
				t.Errorf("bookmarkLabel() = %q, want to contain %q", got, tt.contains)
			}
		})
	}
}

func TestApplicationRunBookmarkDryRun(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	app := application{out: &output, dryRun: true}
	bookmark := appconfig.Bookmark{
		Name: "database", Type: appconfig.SessionTypeRemoteHost,
		Profile: "dev team", Region: "mx-central-1", InstanceID: "i-001",
		Host: "db.internal", RemotePort: 5432, LocalPort: 15432,
	}
	if err := app.runBookmark(context.Background(), bookmark); err != nil {
		t.Fatal(err)
	}
	if got := output.String(); !strings.Contains(got, "aws ssm start-session") || !strings.Contains(got, "'dev team'") {
		t.Fatalf("dry-run output = %q", got)
	}
}

func TestApplicationRunBookmarkGroupDryRun(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	app := application{out: &output, dryRun: true}
	bookmark := appconfig.Bookmark{
		Name: "stack", Profile: "dev", Region: "mx-central-1", InstanceID: "i-001",
		Tunnels: []appconfig.Tunnel{
			{Name: "db", Type: appconfig.SessionTypeRemoteHost, Host: "db.internal", RemotePort: 5432, LocalPort: 15432},
			{Name: "redis", Type: appconfig.SessionTypeRemoteHost, Host: "redis.internal", RemotePort: 6379, LocalPort: 16379},
		},
	}
	if err := app.runBookmark(context.Background(), bookmark); err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(output.String(), "aws ssm start-session"); got != 2 {
		t.Fatalf("group dry-run commands = %d, want 2: %q", got, output.String())
	}
}
