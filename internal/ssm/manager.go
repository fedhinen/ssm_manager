// Package ssm owns session process lifecycle independently from any UI.
package ssm

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/victorruiz/ssm-manager/internal/awscli"
)

type Spec = awscli.SessionSpec
type Process = awscli.BackgroundSession

type Launcher interface {
	SessionCommand(context.Context, Spec) (*exec.Cmd, error)
	StartBackground(context.Context, Spec) (*Process, error)
}

type Session struct {
	ID        string
	Spec      Spec
	PID       int
	StartedAt time.Time
	Done      <-chan error
}

type tracked struct {
	session Session
	process *Process
	cancel  context.CancelFunc
}

type Manager struct {
	launcher Launcher
	mu       sync.RWMutex
	sessions map[string]tracked
}

func NewManager(launcher Launcher) *Manager {
	return &Manager{launcher: launcher, sessions: map[string]tracked{}}
}

func (m *Manager) ShellCommand(ctx context.Context, spec Spec) (*exec.Cmd, error) {
	return m.launcher.SessionCommand(ctx, spec)
}

func (m *Manager) Start(ctx context.Context, id string, spec Spec) (Session, error) {
	// Each tunnel gets its own cancellation branch. WithoutCancel lets a tunnel
	// survive a temporary TUI operation while Kill still has precise ownership.
	sessionCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	process, err := m.launcher.StartBackground(sessionCtx, spec)
	if err != nil {
		cancel()
		return Session{}, err
	}
	session := Session{
		ID: id, Spec: spec, PID: process.PID, StartedAt: process.StartedAt, Done: process.Done,
	}
	m.mu.Lock()
	m.sessions[id] = tracked{session: session, process: process, cancel: cancel}
	m.mu.Unlock()
	return session, nil
}

func (m *Manager) Kill(id string) error {
	m.mu.RLock()
	entry, ok := m.sessions[id]
	m.mu.RUnlock()
	if !ok {
		return errors.New("SSM session not found")
	}
	entry.cancel()
	err := entry.process.Stop()
	if errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}

func (m *Manager) Forget(id string) {
	m.mu.Lock()
	entry, ok := m.sessions[id]
	delete(m.sessions, id)
	m.mu.Unlock()
	if ok {
		entry.cancel()
	}
}

func (m *Manager) Sessions() []Session {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Session, 0, len(m.sessions))
	for _, entry := range m.sessions {
		out = append(out, entry.session)
	}
	return out
}

func (m *Manager) Close() error {
	m.mu.RLock()
	ids := make([]string, 0, len(m.sessions))
	for id := range m.sessions {
		ids = append(ids, id)
	}
	m.mu.RUnlock()
	errs := []error{}
	for _, id := range ids {
		if err := m.Kill(id); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
