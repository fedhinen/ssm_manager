package cli

import (
	"testing"

	"github.com/victorruiz/ssm-manager/internal/awscli"
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
