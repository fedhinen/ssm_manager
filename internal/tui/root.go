package tui

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/victorruiz/ssm-manager/internal/aws"
	"github.com/victorruiz/ssm-manager/internal/awscli"
	"github.com/victorruiz/ssm-manager/internal/cache"
	appconfig "github.com/victorruiz/ssm-manager/internal/config"
	"github.com/victorruiz/ssm-manager/internal/ssm"
	"github.com/victorruiz/ssm-manager/internal/theme"
)

const minimumWidth, minimumHeight = 80, 24

type legacyClient interface {
	Profiles(context.Context) ([]string, error)
	Regions(context.Context, string) ([]string, error)
	Instances(context.Context, string, string) ([]awscli.Instance, error)
	DiscoverRemoteHosts(context.Context, string, string, string) awscli.DiscoveryResult
	SessionCommand(context.Context, awscli.SessionSpec) (*exec.Cmd, error)
	StartBackground(context.Context, awscli.SessionSpec) (*awscli.BackgroundSession, error)
}

type inventoryAdapter struct{ client legacyClient }

func (a inventoryAdapter) Profiles(ctx context.Context) ([]aws.Profile, error) {
	names, err := a.client.Profiles(ctx)
	profiles := make([]aws.Profile, 0, len(names))
	for _, name := range names {
		profiles = append(profiles, aws.Profile{Name: name})
	}
	return profiles, err
}
func (a inventoryAdapter) Regions(ctx context.Context, profile string) ([]string, error) {
	return a.client.Regions(ctx, profile)
}
func (a inventoryAdapter) Instances(ctx context.Context, profile, region string) ([]aws.Instance, error) {
	return a.client.Instances(ctx, profile, region)
}
func (a inventoryAdapter) DiscoverRemoteResources(
	ctx context.Context,
	profile string,
	region string,
	vpcID string,
) aws.DiscoveryResult {
	return a.client.DiscoverRemoteHosts(ctx, profile, region, vpcID)
}

type Options struct {
	Inventory aws.Inventory
	Sessions  *ssm.Manager
	Cache     cache.Store
	NoColor   bool
	Input     io.Reader
	Output    io.Writer
	Env       []string
	DryRun    bool
}

type Root struct {
	ctx          context.Context
	inventory    aws.Inventory
	sessions     *ssm.Manager
	cache        cache.Store
	theme        theme.Theme
	env          []string
	output       io.Writer
	noColor      bool
	dryRun       bool
	width        int
	height       int
	dark         bool
	stack        []tea.Model
	profile      aws.Profile
	region       string
	panel        SessionsPanel
	panelVisible bool
	status       string
	now          time.Time
	stopping     map[string]bool
	starting     bool
}

// Run preserves the public entrypoint while routing presentation through the
// new aws and ssm core boundaries. config/configPath remain accepted for CLI
// compatibility; bookmarks and history continue to be handled by plain mode.
func Run(
	ctx context.Context,
	client legacyClient,
	_ appconfig.Config,
	_ string,
	dryRun bool,
	in io.Reader,
	out io.Writer,
) error {
	store, err := cache.New("ssm-manager", 5*time.Minute)
	if err != nil {
		return err
	}
	return RunWithOptions(ctx, Options{
		Inventory: inventoryAdapter{client: client}, Sessions: ssm.NewManager(client),
		Cache: store, Input: in, Output: out, Env: os.Environ(), DryRun: dryRun,
	})
}

func RunWithOptions(ctx context.Context, options Options) error {
	root := NewRoot(ctx, options)
	program := tea.NewProgram(root, tea.WithInput(options.Input), tea.WithOutput(options.Output))
	_, err := program.Run()
	closeErr := options.Sessions.Close()
	if err != nil {
		return fmt.Errorf("running TUI: %w", err)
	}
	return closeErr
}

func NewRoot(ctx context.Context, options Options) *Root {
	if options.Env == nil {
		options.Env = os.Environ()
	}
	if options.Output == nil {
		options.Output = os.Stdout
	}
	root := &Root{
		ctx: ctx, inventory: options.Inventory, sessions: options.Sessions,
		cache: options.Cache, env: options.Env, output: options.Output, noColor: options.NoColor,
		dryRun: options.DryRun, dark: true, now: time.Now(), stopping: map[string]bool{},
	}
	root.theme = theme.New(options.Output, options.Env, options.NoColor, root.dark)
	root.panel = NewSessionsPanel(root.theme)
	root.stack = []tea.Model{NewProfileSelect(ctx, options.Inventory, root.theme)}
	return root
}

func (r *Root) Init() tea.Cmd {
	return tea.Batch(r.top().Init(), tick())
}

func (r *Root) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch value := msg.(type) {
	case tea.WindowSizeMsg:
		r.width, r.height = value.Width, value.Height
		r.resizeViews(value)
	case tea.BackgroundColorMsg:
		r.dark = value.IsDark()
		r.theme = theme.New(r.output, r.env, r.noColor, r.dark)
		r.rethemeViews()
	case tea.KeyPressMsg:
		if value.String() == "ctrl+c" {
			return r, tea.Quit
		}
		if r.isInstanceTop() && value.String() == "tab" {
			r.panelVisible = !r.panelVisible
			return r, nil
		}
		if r.isInstanceTop() && value.String() == "R" && !r.top().(*InstanceList).list.SettingFilter() {
			return r, r.changeRegion()
		}
		if r.panelVisible && r.isInstanceTop() {
			switch value.String() {
			case "x":
				return r, r.killSelected()
			case "j", "down", "k", "up":
				r.panel, _ = r.panel.Update(value)
				return r, nil
			}
		}
	case profileChosen:
		r.profile = aws.Profile(value)
		if r.profile.Region != "" {
			r.region = r.profile.Region
			return r, r.push(NewInstanceList(r.ctx, r.inventory, r.cache, r.theme, r.profile.Name, r.region))
		}
		return r, r.push(NewRegionSelect(r.ctx, r.inventory, r.theme, r.profile.Name))
	case regionChosen:
		r.region = string(value)
		if r.isInstanceTop() {
			r.pop()
		}
		if len(r.stack) > 1 {
			r.pop()
		}
		return r, r.push(NewFreshInstanceList(r.ctx, r.inventory, r.cache, r.theme, r.profile.Name, r.region))
	case actionsRequested:
		return r, r.push(NewActionMenu(r.theme, aws.Instance(value), r.profile.Name, r.region))
	case localForwardRequested:
		menu := NewActionMenu(r.theme, aws.Instance(value), r.profile.Name, r.region)
		menu.OpenLocal()
		return r, r.push(menu)
	case remoteScanRequested:
		return r, r.push(NewRemoteResourceScan(
			r.ctx, r.inventory, r.cache, r.theme, aws.Instance(value), r.profile.Name, r.region,
		))
	case popRequested:
		return r, r.pop()
	case sessionRequested:
		if r.starting {
			return r, nil
		}
		r.starting = true
		cmd := r.startSession(value)
		if r.dryRun {
			r.starting = false
		}
		return r, cmd
	case sessionStarted:
		r.starting = false
		if value.err != nil {
			r.status = value.err.Error()
			return r, nil
		}
		r.panel.Add(value.session, displayInstance(value.instance))
		r.panelVisible = true
		r.status = fmt.Sprintf("sesión iniciada (PID %d)", value.session.PID)
		for !r.isInstanceTop() && len(r.stack) > 1 {
			r.pop()
		}
		return r, waitSession(value.session)
	case sessionFinished:
		r.sessions.Forget(value.id)
		r.panel.Remove(value.id)
		stopped := r.stopping[value.id]
		delete(r.stopping, value.id)
		if stopped && value.err == nil {
			r.status = "sesión detenida"
		} else if value.err != nil {
			r.status = "la sesión terminó con error: " + value.err.Error()
		} else {
			r.status = "sesión finalizada"
		}
	case sessionKilled:
		r.sessions.Forget(value.id)
		r.panel.Remove(value.id)
		r.status = "sesión detenida"
	case shellFinished:
		if value.err != nil && !awscli.IsNormalSessionExit(value.err) {
			r.status = value.err.Error()
		} else {
			r.status = "shell finalizada"
		}
	case tickMsg:
		r.now = time.Time(value)
		r.panel.SetNow(r.now)
		return r, tick()
	}

	top, cmd := r.top().Update(msg)
	r.stack[len(r.stack)-1] = top
	return r, cmd
}

func (r *Root) View() tea.View {
	view := tea.NewView(r.render())
	view.AltScreen = true
	return view
}

func (r *Root) render() string {
	if r.width > 0 && (r.width < minimumWidth || r.height < minimumHeight) {
		return fmt.Sprintf("Terminal demasiado pequeña: %dx%d; mínimo %dx%d", r.width, r.height, minimumWidth, minimumHeight)
	}
	r.layoutViews()
	baseIndex := len(r.stack) - 1
	if _, modal := r.stack[baseIndex].(*ActionMenu); modal && baseIndex > 0 {
		baseIndex--
	}
	base := r.stack[baseIndex].View().Content
	if r.panelVisible && r.isInstanceAt(baseIndex) {
		base = lipgloss.JoinVertical(lipgloss.Left, base, r.panel.View().Content)
	}
	if len(r.stack)-1 != baseIndex {
		dialog := r.top().View().Content
		return overlay(base, dialog, r.width, r.height)
	}
	if r.status != "" {
		base = lipgloss.JoinVertical(lipgloss.Left, base, r.theme.Muted.Render(r.status))
	}
	return base
}

func (r *Root) top() tea.Model { return r.stack[len(r.stack)-1] }

func (r *Root) push(view tea.Model) tea.Cmd {
	r.stack = append(r.stack, view)
	if r.width > 0 {
		updated, cmd := view.Update(tea.WindowSizeMsg{Width: r.width, Height: r.height})
		r.stack[len(r.stack)-1] = updated
		return tea.Batch(view.Init(), cmd)
	}
	return view.Init()
}

func (r *Root) sessionsHeight() int {
	return max(4, min(8, r.height/3))
}

func (r *Root) layoutViews() {
	panelHeight := 0
	if r.panelVisible {
		r.panel.SetSize(max(1, r.width-2), r.sessionsHeight())
		panelHeight = lipgloss.Height(r.panel.View().Content)
	}
	if r.isInstanceTop() {
		instances := r.top().(*InstanceList)
		instances.SetSize(r.width, max(1, r.height-panelHeight))
	}
}

func (r *Root) pop() tea.Cmd {
	if len(r.stack) == 1 {
		return tea.Quit
	}
	r.stack = r.stack[:len(r.stack)-1]
	return nil
}

func (r *Root) resizeViews(msg tea.WindowSizeMsg) {
	for index, view := range r.stack {
		updated, _ := view.Update(msg)
		r.stack[index] = updated
	}
	r.layoutViews()
}

func (r *Root) rethemeViews() {
	for _, view := range r.stack {
		if themed, ok := view.(interface{ SetTheme(theme.Theme) }); ok {
			themed.SetTheme(r.theme)
		}
	}
	r.panel.SetTheme(r.theme)
}

func (r *Root) isInstanceTop() bool { return r.isInstanceAt(len(r.stack) - 1) }
func (r *Root) isInstanceAt(index int) bool {
	_, ok := r.stack[index].(*InstanceList)
	return ok
}

func (r *Root) startSession(request sessionRequested) tea.Cmd {
	if r.dryRun {
		command, err := awscli.SessionCommandString(request.spec)
		if err != nil {
			r.status = err.Error()
		} else {
			r.status = "dry-run: " + command
		}
		for !r.isInstanceTop() && len(r.stack) > 1 {
			r.pop()
		}
		return nil
	}
	if request.spec.Type == "shell" {
		command, err := r.sessions.ShellCommand(r.ctx, request.spec)
		if err != nil {
			return func() tea.Msg { return sessionStarted{instance: request.instance, err: err} }
		}
		r.starting = false
		r.pop()
		return tea.ExecProcess(command, func(err error) tea.Msg { return shellFinished{err: err} })
	}
	id := strconv.FormatInt(time.Now().UnixNano(), 36)
	return func() tea.Msg {
		session, err := r.sessions.Start(r.ctx, id, request.spec)
		return sessionStarted{instance: request.instance, session: session, err: err}
	}
}

func (r *Root) killSelected() tea.Cmd {
	id := r.panel.SelectedID()
	if id == "" {
		return nil
	}
	if r.stopping[id] {
		return nil
	}
	done := r.panel.Done(id)
	r.stopping[id] = true
	r.status = "deteniendo sesión…"
	return func() tea.Msg {
		err := r.sessions.Kill(id)
		if err != nil {
			return sessionFinished{id: id, err: err}
		}
		if done != nil {
			err = <-done
		}
		if err != nil {
			return sessionFinished{id: id, err: err}
		}
		return sessionKilled{id: id}
	}
}

func (r *Root) changeRegion() tea.Cmd {
	return r.push(NewRegionSelect(r.ctx, r.inventory, r.theme, r.profile.Name))
}

func waitSession(session ssm.Session) tea.Cmd {
	return func() tea.Msg { return sessionFinished{id: session.ID, err: <-session.Done} }
}

func tick() tea.Cmd {
	return tea.Tick(time.Second, func(now time.Time) tea.Msg { return tickMsg(now) })
}

func overlay(base, dialog string, width, height int) string {
	x := max(0, (width-lipgloss.Width(dialog))/2)
	y := max(0, (height-lipgloss.Height(dialog))/2)
	canvas := lipgloss.NewCanvas(width, height)
	canvas.Compose(lipgloss.NewCompositor(
		lipgloss.NewLayer(base), lipgloss.NewLayer(dialog).X(x).Y(y).Z(1),
	))
	return canvas.Render()
}

func displayInstance(instance aws.Instance) string {
	if instance.Name != "" {
		return instance.Name
	}
	return instance.ID
}
