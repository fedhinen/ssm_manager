package cli

import (
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
