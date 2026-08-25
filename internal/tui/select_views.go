package tui

import (
	"context"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/victorruiz/ssm-manager/internal/aws"
	"github.com/victorruiz/ssm-manager/internal/theme"
)

type profileItem struct{ profile aws.Profile }

func (i profileItem) Title() string       { return i.profile.Name }
func (i profileItem) Description() string { return optionalRegion(i.profile.Region) }
func (i profileItem) FilterValue() string { return i.profile.Name + " " + i.profile.Region }

type regionItem string

func (i regionItem) Title() string       { return string(i) }
func (i regionItem) Description() string { return "región AWS" }
func (i regionItem) FilterValue() string { return string(i) }

type ProfileSelect struct {
	ctx       context.Context
	inventory aws.Inventory
	theme     theme.Theme
	list      list.Model
	spinner   spinner.Model
	loading   bool
	err       error
}

func NewProfileSelect(ctx context.Context, inventory aws.Inventory, palette theme.Theme) *ProfileSelect {
	model := newList("Selecciona un perfil AWS", palette)
	return &ProfileSelect{
		ctx: ctx, inventory: inventory, theme: palette, list: model,
		spinner: spinner.New(spinner.WithSpinner(spinner.MiniDot), spinner.WithStyle(palette.Header)),
		loading: true,
	}
}

func (m *ProfileSelect) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, func() tea.Msg {
		profiles, err := m.inventory.Profiles(m.ctx)
		return profilesLoaded{profiles: profiles, err: err}
	})
}

func (m *ProfileSelect) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch value := msg.(type) {
	case profilesLoaded:
		m.loading, m.err = false, value.err
		items := make([]list.Item, 0, len(value.profiles))
		for _, profile := range value.profiles {
			items = append(items, profileItem{profile: profile})
		}
		return m, m.list.SetItems(items)
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(value)
		return m, cmd
	case tea.WindowSizeMsg:
		m.list.SetSize(max(1, value.Width), max(1, value.Height))
	case tea.KeyPressMsg:
		if value.String() == "esc" || value.String() == "q" {
			return m, tea.Quit
		}
		if value.String() == "enter" && !m.list.SettingFilter() {
			if selected, ok := m.list.SelectedItem().(profileItem); ok {
				return m, func() tea.Msg { return profileChosen(selected.profile) }
			}
		}
	}
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m *ProfileSelect) View() tea.View {
	if m.loading {
		return tea.NewView(m.spinner.View() + " leyendo ~/.aws/config…")
	}
	if m.err != nil {
		return tea.NewView("No se pudieron cargar los perfiles: " + m.err.Error())
	}
	return tea.NewView(m.list.View())
}

func (m *ProfileSelect) SetTheme(value theme.Theme) { m.theme = value }

type RegionSelect struct {
	ctx       context.Context
	inventory aws.Inventory
	profile   string
	theme     theme.Theme
	list      list.Model
	spinner   spinner.Model
	loading   bool
	err       error
}

func NewRegionSelect(
	ctx context.Context,
	inventory aws.Inventory,
	palette theme.Theme,
	profile string,
) *RegionSelect {
	return &RegionSelect{
		ctx: ctx, inventory: inventory, profile: profile, theme: palette,
		list:    newList("Selecciona una región para "+profile, palette),
		spinner: spinner.New(spinner.WithSpinner(spinner.MiniDot), spinner.WithStyle(palette.Header)),
		loading: true,
	}
}

func (m *RegionSelect) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, func() tea.Msg {
		regions, err := m.inventory.Regions(m.ctx, m.profile)
		return regionsLoaded{regions: regions, err: err}
	})
}

func (m *RegionSelect) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch value := msg.(type) {
	case regionsLoaded:
		m.loading, m.err = false, value.err
		items := make([]list.Item, 0, len(value.regions))
		for _, region := range value.regions {
			items = append(items, regionItem(region))
		}
		return m, m.list.SetItems(items)
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(value)
		return m, cmd
	case tea.WindowSizeMsg:
		m.list.SetSize(max(1, value.Width), max(1, value.Height))
	case tea.KeyPressMsg:
		if value.String() == "esc" {
			return m, func() tea.Msg { return popRequested{} }
		}
		if value.String() == "enter" && !m.list.SettingFilter() {
			if selected, ok := m.list.SelectedItem().(regionItem); ok {
				return m, func() tea.Msg { return regionChosen(selected) }
			}
		}
	}
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m *RegionSelect) View() tea.View {
	if m.loading {
		return tea.NewView(m.spinner.View() + " consultando regiones…")
	}
	if m.err != nil {
		return tea.NewView("No se pudieron cargar las regiones: " + m.err.Error())
	}
	return tea.NewView(m.list.View())
}

func (m *RegionSelect) SetTheme(value theme.Theme) { m.theme = value }

func newList(title string, palette theme.Theme) list.Model {
	delegate := list.NewDefaultDelegate()
	delegate.Styles.NormalTitle = lipgloss.NewStyle()
	delegate.Styles.NormalDesc = palette.Muted
	delegate.Styles.SelectedTitle = palette.Selected
	delegate.Styles.SelectedDesc = palette.Header
	delegate.Styles.FilterMatch = palette.Header.Underline(true)
	model := list.New([]list.Item{}, delegate, 1, 1)
	model.Title = title
	model.Styles.TitleBar = lipgloss.NewStyle()
	model.Styles.Title = palette.Header
	model.Styles.HelpStyle = lipgloss.NewStyle()
	model.Styles.Filter.Focused.Prompt = palette.Header
	model.Styles.Filter.Focused.Text = palette.Input
	model.SetShowHelp(true)
	model.SetFilteringEnabled(true)
	model.DisableQuitKeybindings()
	return model
}

func optionalRegion(region string) string {
	if strings.TrimSpace(region) == "" {
		return "sin región predeterminada"
	}
	return fmt.Sprintf("región predeterminada: %s", region)
}
