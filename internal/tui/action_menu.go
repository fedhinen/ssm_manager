package tui

import (
	"fmt"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/victorruiz/ssm-manager/internal/aws"
	"github.com/victorruiz/ssm-manager/internal/ssm"
	"github.com/victorruiz/ssm-manager/internal/theme"
)

type actionItem struct {
	name string
	kind string
}

func (i actionItem) Title() string       { return i.name }
func (i actionItem) Description() string { return "" }
func (i actionItem) FilterValue() string { return i.name }

type ActionMenu struct {
	theme      theme.Theme
	instance   aws.Instance
	profile    string
	region     string
	list       list.Model
	remotePort textinput.Model
	localPort  textinput.Model
	form       bool
	focus      int
	width      int
	err        string
	pending    bool
}

func NewActionMenu(
	palette theme.Theme,
	instance aws.Instance,
	profile string,
	region string,
) *ActionMenu {
	items := []list.Item{
		actionItem{name: "Shell", kind: "shell"},
		actionItem{name: "Port-forward a la instancia", kind: "port-forward"},
		actionItem{name: "Port-forward a host remoto", kind: "remote-host"},
	}
	menu := newList("Acción para "+displayInstance(instance), palette)
	menu.SetItems(items)
	menu.SetShowFilter(false)
	menu.SetShowStatusBar(false)
	menu.SetShowHelp(false)
	remote := newPortInput("puerto remoto", "8080", palette)
	local := newPortInput("puerto local", "8080", palette)
	return &ActionMenu{
		theme: palette, instance: instance, profile: profile, region: region,
		list: menu, remotePort: remote, localPort: local, width: 58,
	}
}

func (m *ActionMenu) OpenLocal() {
	m.form = true
	m.focus = 0
	m.remotePort.Focus()
}

func (m *ActionMenu) Init() tea.Cmd { return nil }

func (m *ActionMenu) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch value := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = min(64, max(40, value.Width-8))
		m.list.SetSize(m.width-6, 8)
	case tea.KeyPressMsg:
		if value.String() == "esc" {
			if m.form {
				m.form = false
				m.remotePort.Blur()
				m.localPort.Blur()
				return m, nil
			}
			return m, func() tea.Msg { return popRequested{} }
		}
		if m.form {
			return m.updateForm(value)
		}
		if value.String() == "enter" {
			if m.pending {
				return m, nil
			}
			selected, ok := m.list.SelectedItem().(actionItem)
			if !ok {
				return m, nil
			}
			switch selected.kind {
			case "shell":
				return m, m.request("shell", 0, 0)
			case "port-forward":
				m.OpenLocal()
				return m, nil
			case "remote-host":
				m.pending = true
				return m, func() tea.Msg { return remoteScanRequested(m.instance) }
			}
		}
	}
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m *ActionMenu) updateForm(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "tab", "shift+tab", "up", "down":
		m.focus = (m.focus + 1) % 2
		if m.focus == 0 {
			m.remotePort.Focus()
			m.localPort.Blur()
		} else {
			m.remotePort.Blur()
			m.localPort.Focus()
		}
		return m, nil
	case "enter":
		if m.pending {
			return m, nil
		}
		remote, remoteErr := parsePort(m.remotePort.Value())
		local, localErr := parsePort(m.localPort.Value())
		if remoteErr != nil || localErr != nil {
			m.err = "Los puertos deben estar entre 1 y 65535"
			return m, nil
		}
		return m, m.request("port-forward", remote, local)
	}
	var cmd tea.Cmd
	if m.focus == 0 {
		m.remotePort, cmd = m.remotePort.Update(msg)
	} else {
		m.localPort, cmd = m.localPort.Update(msg)
	}
	return m, cmd
}

func (m *ActionMenu) request(kind string, remote, local int) tea.Cmd {
	m.pending = true
	spec := ssm.Spec{
		Type: kind, Profile: m.profile, Region: m.region, InstanceID: m.instance.ID,
		RemotePort: remote, LocalPort: local,
	}
	return func() tea.Msg { return sessionRequested{instance: m.instance, spec: spec} }
}

func (m *ActionMenu) View() tea.View {
	body := m.list.View()
	if m.form {
		body = lipgloss.JoinVertical(
			lipgloss.Left,
			"Port-forward a la instancia",
			"puerto remoto: "+m.remotePort.View(),
			"puerto local:  "+m.localPort.View(),
			m.theme.Danger.Render(m.err),
			m.theme.HelpBar.Render("[enter] iniciar  [tab] cambiar campo  [esc] volver"),
		)
	}
	if m.theme.Plain {
		return tea.NewView(body)
	}
	return tea.NewView(m.theme.Border.Width(m.width).Padding(1, 2).Render(body))
}

func (m *ActionMenu) SetTheme(value theme.Theme) { m.theme = value }

func newPortInput(placeholder, value string, palette theme.Theme) textinput.Model {
	input := textinput.New()
	input.Placeholder = placeholder
	input.CharLimit = 5
	input.SetWidth(8)
	input.SetValue(value)
	styles := input.Styles()
	styles.Focused.Text = palette.Input
	styles.Focused.Prompt = palette.Input
	input.SetStyles(styles)
	return input
}

func parsePort(value string) (int, error) {
	port, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("invalid port %q", value)
	}
	return port, nil
}
