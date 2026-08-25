package tui

import (
	"time"

	"github.com/victorruiz/ssm-manager/internal/aws"
	"github.com/victorruiz/ssm-manager/internal/ssm"
)

type profilesLoaded struct {
	profiles []aws.Profile
	err      error
}

type regionsLoaded struct {
	regions []string
	err     error
}

type instancesLoaded struct {
	instances []aws.Instance
	cached    bool
	err       error
}

type resourcesLoaded struct {
	result aws.DiscoveryResult
	cached bool
}

type profileChosen aws.Profile
type regionChosen string
type actionsRequested aws.Instance
type localForwardRequested aws.Instance
type remoteScanRequested aws.Instance
type popRequested struct{}

type sessionRequested struct {
	instance aws.Instance
	spec     ssm.Spec
}

type sessionStarted struct {
	instance aws.Instance
	session  ssm.Session
	err      error
}

type sessionFinished struct {
	id  string
	err error
}

type sessionKilled struct{ id string }

type shellFinished struct{ err error }
type tickMsg time.Time
