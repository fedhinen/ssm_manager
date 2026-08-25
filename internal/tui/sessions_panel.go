package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/victorruiz/ssm-manager/internal/ssm"
	"github.com/victorruiz/ssm-manager/internal/theme"
)

type panelSession struct {
	session      ssm.Session
	instanceName string
}

type SessionsPanel struct {
	theme    theme.Theme
	viewport viewport.Model
	sessions []panelSession
	cursor   int
	now      time.Time
	width    int
	height   int
}

func NewSessionsPanel(palette theme.Theme) SessionsPanel {
	return SessionsPanel{
		theme: palette, viewport: viewport.New(viewport.WithWidth(1), viewport.WithHeight(1)),
		sessions: []panelSession{}, now: time.Now(),
	}
}

func (m SessionsPanel) Init() tea.Cmd { return m.viewport.Init() }

func (m SessionsPanel) Update(msg tea.Msg) (SessionsPanel, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.String() {
		case "j", "down":
			m.cursor = min(m.cursor+1, max(0, len(m.sessions)-1))
			m.refresh()
			return m, nil
		case "k", "up":
			m.cursor = max(0, m.cursor-1)
			m.refresh()
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	return m, cmd
}

func (m SessionsPanel) View() tea.View {
	content := lipgloss.JoinVertical(
		lipgloss.Left,
		m.theme.Header.Render("sesiones activas"),
		m.viewport.View(),
		m.theme.HelpBar.Render("[x] kill  [j/k] seleccionar  [Tab] ocultar"),
	)
	content = fitBlock(content, max(1, m.width-2))
	if m.theme.Plain {
		return tea.NewView(content)
	}
	return tea.NewView(m.theme.Border.Width(max(1, m.width-2)).Render(content))
}

func (m *SessionsPanel) Add(session ssm.Session, instanceName string) {
	m.sessions = append(m.sessions, panelSession{session: session, instanceName: instanceName})
	m.cursor = len(m.sessions) - 1
	m.refresh()
}

func (m *SessionsPanel) Remove(id string) {
	for index, session := range m.sessions {
		if session.session.ID != id {
			continue
		}
		m.sessions = append(m.sessions[:index], m.sessions[index+1:]...)
		break
	}
	m.cursor = min(m.cursor, max(0, len(m.sessions)-1))
	m.refresh()
}

func (m *SessionsPanel) SelectedID() string {
	if len(m.sessions) == 0 {
		return ""
	}
	return m.sessions[m.cursor].session.ID
}

func (m *SessionsPanel) Done(id string) <-chan error {
	for _, entry := range m.sessions {
		if entry.session.ID == id {
			return entry.session.Done
		}
	}
	return nil
}

func (m *SessionsPanel) SetSize(width, height int) {
	m.width, m.height = width, height
	m.viewport.SetWidth(max(1, width-4))
	m.viewport.SetHeight(max(1, height-4))
	m.refresh()
}

func (m *SessionsPanel) SetNow(now time.Time) {
	m.now = now
	m.refresh()
}

func (m *SessionsPanel) SetTheme(value theme.Theme) {
	m.theme = value
	m.refresh()
}

func (m *SessionsPanel) refresh() {
	lines := make([]string, 0, max(1, len(m.sessions)))
	for index, entry := range m.sessions {
		spec := entry.session.Spec
		typeLabel := "shell"
		if spec.Type == "port-forward" {
			typeLabel = fmt.Sprintf("fwd %d→%d", spec.LocalPort, spec.RemotePort)
		}
		if spec.Type == "remote-host" {
			typeLabel = fmt.Sprintf("fwd %d→%s:%d", spec.LocalPort, spec.Host, spec.RemotePort)
		}
		line := fmt.Sprintf(
			"● %-20s %-30s %s    [x] kill",
			fit(entry.instanceName, 20), fit(typeLabel, 30), elapsed(m.now.Sub(entry.session.StartedAt)),
		)
		line = m.theme.Success.Render(line)
		if index == m.cursor {
			line = m.theme.Selected.Render(line)
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		lines = append(lines, m.theme.Muted.Render("No hay sesiones activas"))
	}
	m.viewport.SetContent(strings.Join(lines, "\n"))
}

func elapsed(duration time.Duration) string {
	if duration < 0 {
		duration = 0
	}
	total := int(duration.Round(time.Second).Seconds())
	return fmt.Sprintf("%02d:%02d:%02d", total/3600, (total/60)%60, total%60)
}
