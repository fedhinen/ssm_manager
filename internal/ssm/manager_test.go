package ssm

import (
	"context"
	"os/exec"
	"sync/atomic"
	"testing"
	"time"

	"github.com/victorruiz/ssm-manager/internal/awscli"
)

type fakeLauncher struct {
	stopped *atomic.Bool
	done    chan error
}

func (f fakeLauncher) SessionCommand(context.Context, Spec) (*exec.Cmd, error) {
	return exec.Command("true"), nil
}

func (f fakeLauncher) StartBackground(context.Context, Spec) (*Process, error) {
	return awscli.NewBackgroundSession(42, time.Now(), f.done, func() error {
		f.stopped.Store(true)
		return nil
	}), nil
}

func TestManagerKillIsIndividual(t *testing.T) {
	t.Parallel()
	firstStopped := &atomic.Bool{}
	secondStopped := &atomic.Bool{}
	firstDone := make(chan error, 1)
	secondDone := make(chan error, 1)
	manager := NewManager(fakeLauncher{stopped: firstStopped, done: firstDone})
	if _, err := manager.Start(context.Background(), "one", Spec{Type: "port-forward"}); err != nil {
		t.Fatal(err)
	}
	manager.launcher = fakeLauncher{stopped: secondStopped, done: secondDone}
	if _, err := manager.Start(context.Background(), "two", Spec{Type: "port-forward"}); err != nil {
		t.Fatal(err)
	}

	if err := manager.Kill("one"); err != nil {
		t.Fatal(err)
	}
	if !firstStopped.Load() || secondStopped.Load() {
		t.Fatalf("Kill(one): first=%v second=%v", firstStopped.Load(), secondStopped.Load())
	}
}
