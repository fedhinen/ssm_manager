package tui

import (
	"context"
	"fmt"
	"io"
	"strings"

	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/victorruiz/ssm-manager/internal/aws"
	"github.com/victorruiz/ssm-manager/internal/cache"
	"github.com/victorruiz/ssm-manager/internal/theme"
)

type instanceItem struct{ instance aws.Instance }

func (i instanceItem) FilterValue() string {
	return i.instance.ID + " " + i.instance.Name + " " + i.instance.PrivateIP + " " + i.instance.Type
}

type instanceDelegate struct{ theme theme.Theme }

func (d instanceDelegate) Height() int                         { return 1 }
func (d instanceDelegate) Spacing() int                        { return 0 }
func (d instanceDelegate) Update(tea.Msg, *list.Model) tea.Cmd { return nil }
func (d instanceDelegate) Render(w io.Writer, m list.Model, index int, raw list.Item) {
	item, ok := raw.(instanceItem)
	if !ok {
		return
	}
	instance := item.instance
	stateSymbol := "○"
	ssm := "SSM ✗"
	ssmStyle := d.theme.Danger
	stateStyle := d.theme.Warning
	if instance.State == "running" {
		stateStyle = d.theme.Success
	}
	if instance.State == "stopped" || instance.State == "terminated" {
		stateStyle = d.theme.Danger
	}
	if instance.SSMAvailable {
		stateSymbol, ssm = "●", "SSM ✓"
		ssmStyle = d.theme.Success
	}
	line := fmt.Sprintf(
		" %s  %-19s %-24s %s %s %s",
		ssmStyle.Render(stateSymbol), instance.ID, fit(displayInstance(instance), 24),
		d.theme.Muted.Render(fmt.Sprintf("%-12s", fit(instance.Type, 12))),
		stateStyle.Render(fmt.Sprintf("%-10s", fit(instance.State, 10))),
		ssmStyle.Render(ssm),
	)
	if index == m.Index() {
		line = d.theme.Selected.Render(line)
	}
	fmt.Fprint(w, line)
}

type InstanceList struct {
	ctx           context.Context
	inventory     aws.Inventory
	cache         cache.Store
	theme         theme.Theme
	profile       string
	region        string
	list          list.Model
	spinner       spinner.Model
	loading       bool
	cached        bool
	err           error
	width         int
	height        int
	refreshOnInit bool
}

func NewInstanceList(
	ctx context.Context,
	inventory aws.Inventory,
	store cache.Store,
	palette theme.Theme,
	profile string,
	region string,
) *InstanceList {
	return newInstanceList(ctx, inventory, store, palette, profile, region, false)
}

func NewFreshInstanceList(
	ctx context.Context,
	inventory aws.Inventory,
	store cache.Store,
	palette theme.Theme,
	profile string,
	region string,
) *InstanceList {
	return newInstanceList(ctx, inventory, store, palette, profile, region, true)
}

func newInstanceList(
	ctx context.Context,
	inventory aws.Inventory,
	store cache.Store,
	palette theme.Theme,
	profile string,
	region string,
	refreshOnInit bool,
) *InstanceList {
	model := list.New([]list.Item{}, instanceDelegate{theme: palette}, 1, 1)
	model.SetShowTitle(false)
	model.SetShowStatusBar(false)
	model.SetShowPagination(false)
	model.SetShowHelp(false)
	model.SetFilteringEnabled(true)
	model.Styles.Filter.Focused.Prompt = palette.Header
	model.Styles.Filter.Blurred.Prompt = palette.Header
	model.Styles.Filter.Focused.Text = palette.Input
	model.Styles.Filter.Blurred.Text = palette.Input
	model.Styles.Filter.Cursor.Color = palette.Input.GetForeground()
	model.DisableQuitKeybindings()
	return &InstanceList{
		ctx: ctx, inventory: inventory, cache: store, theme: palette,
		profile: profile, region: region, list: model, refreshOnInit: refreshOnInit,
		spinner: spinner.New(spinner.WithSpinner(spinner.MiniDot), spinner.WithStyle(palette.Header)),
		loading: true,
	}
}

func (m *InstanceList) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, m.load(m.refreshOnInit))
}

func (m *InstanceList) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch value := msg.(type) {
	case instancesLoaded:
		m.loading, m.err, m.cached = false, value.err, value.cached
		items := make([]list.Item, 0, len(value.instances))
		for _, instance := range value.instances {
			items = append(items, instanceItem{instance: instance})
		}
		return m, m.list.SetItems(items)
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(value)
		return m, cmd
	case tea.WindowSizeMsg:
		m.SetSize(value.Width, value.Height)
	case tea.KeyPressMsg:
		if m.list.SettingFilter() {
			break
		}
		switch value.String() {
		case "esc":
			return m, func() tea.Msg { return popRequested{} }
		case "q":
			return m, tea.Quit
		case "ctrl+r":
			m.loading = true
			return m, tea.Batch(m.spinner.Tick, m.load(true))
		case "enter":
			return m, m.action()
		case "p":
			if instance, ok := m.selected(); ok && instance.SSMAvailable {
				return m, func() tea.Msg { return localForwardRequested(instance) }
			}
		case "r":
			if instance, ok := m.selected(); ok && instance.SSMAvailable {
				return m, func() tea.Msg { return remoteScanRequested(instance) }
			}
		}
	}
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m *InstanceList) View() tea.View {
	cacheLabel := ""
	if m.cached {
		cacheLabel = " " + m.theme.Muted.Render("(cache)")
	}
	header := m.theme.Header.Render(fmt.Sprintf("perfil: %s │ región: %s", m.profile, m.region)) + cacheLabel
	body := ""
	switch {
	case m.loading:
		body = m.spinner.View() + " consultando instancias…"
	case m.err != nil:
		body = m.theme.Danger.Render("Error: " + m.err.Error())
	case len(m.list.Items()) == 0:
		body = m.theme.Muted.Render("Sin instancias para mostrar")
	default:
		body = m.list.View()
	}
	help := "[enter] acciones  [p] port-fwd local  [r] port-fwd remoto  [R] cambiar región  [/] filtro  [ctrl+r] refresh  [q] salir"
	content := lipgloss.JoinVertical(lipgloss.Left, header, body, m.theme.HelpBar.Render(help))
	content = fitBlock(content, max(1, m.width-2))
	if m.theme.Plain {
		return tea.NewView(content)
	}
	return tea.NewView(m.theme.Border.Width(max(1, m.width-2)).Render(content))
}

func (m *InstanceList) SetTheme(value theme.Theme) {
	m.theme = value
	m.list.SetDelegate(instanceDelegate{theme: value})
}

func (m *InstanceList) SetSize(width, height int) {
	m.width, m.height = width, height
	contentWidth := max(1, width-2)
	headerHeight := lipgloss.Height(m.theme.Header.Render(fmt.Sprintf("perfil: %s │ región: %s", m.profile, m.region)))
	helpHeight := lipgloss.Height(m.theme.HelpBar.Render("[enter] acciones  [p] port-fwd local  [r] port-fwd remoto  [R] cambiar región  [/] filtro  [ctrl+r] refresh  [q] salir"))
	listHeight := height - headerHeight - helpHeight - 2
	m.list.SetSize(contentWidth, max(1, listHeight))
}

func (m *InstanceList) selected() (aws.Instance, bool) {
	item, ok := m.list.SelectedItem().(instanceItem)
	return item.instance, ok
}

func (m *InstanceList) action() tea.Cmd {
	instance, ok := m.selected()
	if !ok || !instance.SSMAvailable {
		return nil
	}
	return func() tea.Msg { return actionsRequested(instance) }
}

func (m *InstanceList) load(refresh bool) tea.Cmd {
	return func() tea.Msg {
		key := "instances-" + m.region
		instances := []aws.Instance{}
		if !refresh {
			if hit, err := m.cache.Load(key, &instances); hit || err != nil {
				return instancesLoaded{instances: instances, cached: hit, err: err}
			}
		}
		instances, err := m.inventory.Instances(m.ctx, m.profile, m.region)
		if err == nil {
			err = m.cache.Save(key, instances)
		}
		return instancesLoaded{instances: instances, err: err}
	}
}

func fit(value string, width int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= width {
		return string(runes)
	}
	if width <= 1 {
		return "…"
	}
	return string(runes[:width-1]) + "…"
}

func fitBlock(value string, width int) string {
	lines := strings.Split(value, "\n")
	for index, line := range lines {
		lines[index] = ansi.Truncate(line, width, "…")
	}
	return strings.Join(lines, "\n")
}
