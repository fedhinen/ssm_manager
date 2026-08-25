package tui

import (
	"context"
	"fmt"
	"io"
	"strconv"

	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/victorruiz/ssm-manager/internal/aws"
	"github.com/victorruiz/ssm-manager/internal/cache"
	"github.com/victorruiz/ssm-manager/internal/ssm"
	"github.com/victorruiz/ssm-manager/internal/theme"
)

type resourceItem struct{ resource aws.RemoteResource }

func (i resourceItem) FilterValue() string {
	return i.resource.Service + " " + i.resource.Name + " " + i.resource.Engine + " " + i.resource.Host
}

type resourceDelegate struct{ theme theme.Theme }

func (d resourceDelegate) Height() int                         { return 1 }
func (d resourceDelegate) Spacing() int                        { return 0 }
func (d resourceDelegate) Update(tea.Msg, *list.Model) tea.Cmd { return nil }
func (d resourceDelegate) Render(w io.Writer, m list.Model, index int, raw list.Item) {
	item, ok := raw.(resourceItem)
	if !ok {
		return
	}
	resource := item.resource
	line := fmt.Sprintf(
		" %-13s %-24s %-16s %-28s %5d",
		fit(resource.Service, 13), fit(resource.Name, 24), fit(resource.Engine, 16),
		fit(resource.Host, 28), resource.Port,
	)
	if index == m.Index() {
		line = d.theme.Selected.Render(line)
	}
	fmt.Fprint(w, line)
}

type RemoteResourceScan struct {
	ctx       context.Context
	inventory aws.Inventory
	cache     cache.Store
	theme     theme.Theme
	instance  aws.Instance
	profile   string
	region    string
	list      list.Model
	localPort textinput.Model
	spinner   spinner.Model
	loading   bool
	focusPort bool
	width     int
	height    int
	warnings  []string
	err       string
}

func NewRemoteResourceScan(
	ctx context.Context,
	inventory aws.Inventory,
	store cache.Store,
	palette theme.Theme,
	instance aws.Instance,
	profile string,
	region string,
) *RemoteResourceScan {
	model := list.New([]list.Item{}, resourceDelegate{theme: palette}, 1, 1)
	model.SetShowTitle(false)
	model.SetShowStatusBar(false)
	model.SetShowHelp(false)
	model.SetFilteringEnabled(true)
	model.Styles.Filter.Focused.Prompt = palette.Header
	model.Styles.Filter.Blurred.Prompt = palette.Header
	model.Styles.Filter.Focused.Text = palette.Input
	model.Styles.Filter.Blurred.Text = palette.Input
	model.Styles.Filter.Cursor.Color = palette.Input.GetForeground()
	model.DisableQuitKeybindings()
	input := newPortInput("local", "", palette)
	return &RemoteResourceScan{
		ctx: ctx, inventory: inventory, cache: store, theme: palette,
		instance: instance, profile: profile, region: region, list: model,
		localPort: input,
		spinner:   spinner.New(spinner.WithSpinner(spinner.MiniDot), spinner.WithStyle(palette.Header)),
		loading:   true,
	}
}

func (m *RemoteResourceScan) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, m.load(false))
}

func (m *RemoteResourceScan) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch value := msg.(type) {
	case resourcesLoaded:
		m.loading = false
		m.warnings = value.result.Warnings
		items := make([]list.Item, 0, len(value.result.Hosts))
		for _, resource := range value.result.Hosts {
			items = append(items, resourceItem{resource: resource})
		}
		cmd := m.list.SetItems(items)
		m.suggestPort()
		return m, cmd
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(value)
		return m, cmd
	case tea.WindowSizeMsg:
		m.width, m.height = value.Width, value.Height
		m.list.SetSize(max(1, value.Width-2), max(1, value.Height-4))
	case tea.KeyPressMsg:
		if value.String() == "esc" {
			if m.list.SettingFilter() {
				break
			}
			return m, func() tea.Msg { return popRequested{} }
		}
		if value.String() == "ctrl+r" && !m.focusPort {
			m.loading = true
			return m, tea.Batch(m.spinner.Tick, m.load(true))
		}
		if value.String() == "tab" && !m.list.SettingFilter() {
			m.focusPort = !m.focusPort
			if m.focusPort {
				return m, m.localPort.Focus()
			}
			m.localPort.Blur()
			return m, nil
		}
		if value.String() == "enter" && !m.list.SettingFilter() {
			return m, m.start()
		}
	}
	if m.focusPort {
		var cmd tea.Cmd
		m.localPort, cmd = m.localPort.Update(msg)
		return m, cmd
	}
	before := m.list.Index()
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	if before != m.list.Index() {
		m.suggestPort()
	}
	return m, cmd
}

func (m *RemoteResourceScan) View() tea.View {
	title := fmt.Sprintf(
		"recursos alcanzables desde %s (%s)", m.instance.ID, displayInstance(m.instance),
	)
	body := ""
	if m.loading {
		body = m.spinner.View() + " explorando RDS/Aurora, ElastiCache y DocumentDB…"
	} else {
		header := " tipo          nombre                   engine           endpoint                     puerto"
		body = lipgloss.JoinVertical(lipgloss.Left, m.theme.Muted.Render(header), m.list.View())
	}
	footer := "puerto local: [ " + m.localPort.View() + " ]  (sugerido = puerto remoto)"
	if len(m.warnings) > 0 {
		footer += "\n" + m.theme.Warning.Render(fmt.Sprintf("%d consultas AWS fallaron", len(m.warnings)))
	}
	if m.err != "" {
		footer += "\n" + m.theme.Danger.Render(m.err)
	}
	footer += "\n" + m.theme.HelpBar.Render("[enter] iniciar forwarding  [tab] editar puerto  [/] filtro  [esc] volver")
	content := lipgloss.JoinVertical(lipgloss.Left, m.theme.Header.Render(title), body, footer)
	content = fitBlock(content, max(1, m.width-2))
	if m.theme.Plain {
		return tea.NewView(content)
	}
	return tea.NewView(m.theme.Border.Width(max(1, m.width-2)).Render(content))
}

func (m *RemoteResourceScan) SetTheme(value theme.Theme) {
	m.theme = value
	m.list.SetDelegate(resourceDelegate{theme: value})
}

func (m *RemoteResourceScan) suggestPort() {
	item, ok := m.list.SelectedItem().(resourceItem)
	if ok && !m.focusPort {
		m.localPort.SetValue(strconv.Itoa(item.resource.Port))
	}
}

func (m *RemoteResourceScan) start() tea.Cmd {
	item, ok := m.list.SelectedItem().(resourceItem)
	if !ok {
		return nil
	}
	local, err := parsePort(m.localPort.Value())
	if err != nil {
		m.err = err.Error()
		return nil
	}
	resource := item.resource
	spec := ssm.Spec{
		Type: "remote-host", Profile: m.profile, Region: m.region,
		InstanceID: m.instance.ID, Host: resource.Host,
		RemotePort: resource.Port, LocalPort: local,
	}
	return func() tea.Msg { return sessionRequested{instance: m.instance, spec: spec} }
}

func (m *RemoteResourceScan) load(refresh bool) tea.Cmd {
	return func() tea.Msg {
		key := "resources-" + m.region + "-" + m.instance.VPCID
		result := aws.DiscoveryResult{Hosts: []aws.RemoteResource{}, Warnings: []string{}}
		if !refresh {
			if hit, err := m.cache.Load(key, &result); hit {
				return resourcesLoaded{result: result, cached: true}
			} else if err != nil {
				result.Warnings = append(result.Warnings, err.Error())
			}
		}
		result = m.inventory.DiscoverRemoteResources(m.ctx, m.profile, m.region, m.instance.VPCID)
		if err := m.cache.Save(key, result); err != nil {
			result.Warnings = append(result.Warnings, err.Error())
		}
		return resourcesLoaded{result: result}
	}
}
