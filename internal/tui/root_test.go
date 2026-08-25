package tui

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/victorruiz/ssm-manager/internal/aws"
	"github.com/victorruiz/ssm-manager/internal/cache"
	"github.com/victorruiz/ssm-manager/internal/ssm"
)

type fakeInventory struct{}

func (fakeInventory) Profiles(context.Context) ([]aws.Profile, error) {
	return []aws.Profile{{Name: "prod", Region: "us-east-1"}}, nil
}
func (fakeInventory) Regions(context.Context, string) ([]string, error) {
	return []string{"us-east-1"}, nil
}
func (fakeInventory) Instances(context.Context, string, string) ([]aws.Instance, error) {
	return []aws.Instance{{ID: "i-1", Name: "web", State: "running", SSMAvailable: true}}, nil
}
func (fakeInventory) DiscoverRemoteResources(context.Context, string, string, string) aws.DiscoveryResult {
	return aws.DiscoveryResult{Hosts: []aws.RemoteResource{}}
}

func newTestRoot(t *testing.T) *Root {
	t.Helper()
	return NewRoot(context.Background(), Options{
		Inventory: fakeInventory{}, Sessions: NewTestSessionManager(),
		Cache:  cache.Store{Dir: t.TempDir(), TTL: time.Minute},
		Output: &bytes.Buffer{}, Env: []string{"TERM=dumb"},
	})
}

func NewTestSessionManager() *ssm.Manager { return ssm.NewManager(nil) }

func TestRootUsesViewStackAndSkipsDefaultRegionStep(t *testing.T) {
	t.Parallel()
	root := newTestRoot(t)
	if len(root.stack) != 1 {
		t.Fatalf("initial stack length = %d", len(root.stack))
	}
	root.Update(profileChosen(aws.Profile{Name: "prod", Region: "us-east-1"}))
	if len(root.stack) != 2 {
		t.Fatalf("stack length = %d, want 2", len(root.stack))
	}
	if _, ok := root.top().(*InstanceList); !ok {
		t.Fatalf("top = %T, want *InstanceList", root.top())
	}
	root.Update(popRequested{})
	if len(root.stack) != 1 {
		t.Fatalf("stack length after pop = %d", len(root.stack))
	}
}

func TestRootPushesRegionWhenProfileHasNoDefault(t *testing.T) {
	t.Parallel()
	root := newTestRoot(t)
	root.Update(profileChosen(aws.Profile{Name: "dev"}))
	if _, ok := root.top().(*RegionSelect); !ok {
		t.Fatalf("top = %T, want *RegionSelect", root.top())
	}
}

func TestSessionsPanelToggleDoesNotChangeStack(t *testing.T) {
	t.Parallel()
	root := newTestRoot(t)
	root.Update(profileChosen(aws.Profile{Name: "prod", Region: "us-east-1"}))
	before := len(root.stack)
	root.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if !root.panelVisible || len(root.stack) != before {
		t.Fatalf("panel=%v stack=%d, want visible and %d", root.panelVisible, len(root.stack), before)
	}
}

func TestInstanceFilterStartsWithSlash(t *testing.T) {
	t.Parallel()
	view := NewInstanceList(
		context.Background(), fakeInventory{}, cache.Store{Dir: t.TempDir(), TTL: time.Minute},
		newTestRoot(t).theme, "prod", "us-east-1",
	)
	view.Update(instancesLoaded{instances: []aws.Instance{
		{ID: "i-1", Name: "web", SSMAvailable: true}, {ID: "i-2", Name: "worker", SSMAvailable: true},
	}})
	view.Update(tea.KeyPressMsg{Code: '/'})
	if !view.list.SettingFilter() {
		t.Fatal("slash did not activate fuzzy filtering")
	}
	if strings.Contains(view.View().Content, "Filtro:") {
		t.Fatal("instance list still renders a duplicate custom filter")
	}
}

func TestRegionShortcutReplacesInstanceListAndForcesRefresh(t *testing.T) {
	t.Parallel()
	root := newTestRoot(t)
	root.Update(profileChosen(aws.Profile{Name: "prod", Region: "us-east-1"}))
	root.Update(tea.KeyPressMsg{Code: 'R', Text: "R"})
	if _, ok := root.top().(*RegionSelect); !ok {
		t.Fatalf("top = %T, want *RegionSelect", root.top())
	}
	root.Update(regionChosen("eu-west-1"))
	instances, ok := root.top().(*InstanceList)
	if !ok {
		t.Fatalf("top = %T, want *InstanceList", root.top())
	}
	if root.region != "eu-west-1" || !instances.refreshOnInit {
		t.Fatalf("region=%q refreshOnInit=%v, want eu-west-1/true", root.region, instances.refreshOnInit)
	}
}

func TestKilledSessionIsRemovedFromPanelImmediately(t *testing.T) {
	t.Parallel()
	root := newTestRoot(t)
	root.panel.Add(ssm.Session{ID: "session-1"}, "web")
	root.Update(sessionKilled{id: "session-1"})
	if got := root.panel.SelectedID(); got != "" {
		t.Fatalf("selected session = %q, want no selected session", got)
	}
}

func TestProfileAndRegionSelectorsUseFullTerminalSize(t *testing.T) {
	t.Parallel()
	root := newTestRoot(t)
	root.Update(profilesLoaded{profiles: []aws.Profile{{Name: "prod"}, {Name: "staging"}}})
	root.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	profile := root.top().(*ProfileSelect)
	if profile.list.Width() != 120 || profile.list.Height() != 40 {
		t.Fatalf("profile list size = %dx%d, want 120x40", profile.list.Width(), profile.list.Height())
	}
	root.Update(profileChosen(aws.Profile{Name: "prod"}))
	root.Update(regionsLoaded{regions: []string{"us-east-1", "eu-west-1", "mx-central-1"}})
	region := root.top().(*RegionSelect)
	if region.list.Width() != 120 || region.list.Height() != 40 {
		t.Fatalf("region list size = %dx%d, want 120x40", region.list.Width(), region.list.Height())
	}
}

func TestResponsiveLayoutsStayWithinTerminalWidth(t *testing.T) {
	t.Parallel()
	for _, size := range []struct {
		width  int
		height int
	}{{width: 80, height: 24}, {width: 120, height: 40}, {width: 200, height: 60}} {
		root := newTestRoot(t)
		root.Update(profileChosen(aws.Profile{Name: "prod", Region: "us-east-1"}))
		root.Update(tea.WindowSizeMsg{Width: size.width, Height: size.height})
		instances := root.top().(*InstanceList)
		instances.Update(instancesLoaded{instances: []aws.Instance{{
			ID: "i-0123456789", Name: "a-very-long-production-instance-name",
			Type: "t3.medium", State: "running", SSMAvailable: true,
		}}})
		root.panelVisible = true
		view := root.render()
		if height := lipgloss.Height(view); height > size.height {
			t.Fatalf("%dx%d rendered height = %d", size.width, size.height, height)
		}
		for lineNumber, line := range strings.Split(view, "\n") {
			if width := lipgloss.Width(line); width > size.width {
				t.Fatalf("%dx%d line %d width = %d", size.width, size.height, lineNumber, width)
			}
		}
	}
}
