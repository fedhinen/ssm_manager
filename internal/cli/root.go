package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	appaws "github.com/victorruiz/ssm-manager/internal/aws"
	"github.com/victorruiz/ssm-manager/internal/awscli"
	"github.com/victorruiz/ssm-manager/internal/cache"
	appconfig "github.com/victorruiz/ssm-manager/internal/config"
	"github.com/victorruiz/ssm-manager/internal/ssm"
	appTUI "github.com/victorruiz/ssm-manager/internal/tui"
)

type sessionClient interface {
	Profiles(context.Context) ([]string, error)
	Regions(context.Context, string) ([]string, error)
	Instances(context.Context, string, string) ([]awscli.Instance, error)
	DiscoverRemoteHosts(context.Context, string, string, string) awscli.DiscoveryResult
	Shell(context.Context, string, string, string) error
	Forward(context.Context, string, string, string, int, int) error
	ForwardHost(context.Context, string, string, string, string, int, int) error
	SessionCommand(context.Context, awscli.SessionSpec) (*exec.Cmd, error)
	StartBackground(context.Context, awscli.SessionSpec) (*awscli.BackgroundSession, error)
}

type application struct {
	client     sessionClient
	prompter   *Prompter
	in         io.Reader
	out        io.Writer
	configPath string
	plain      bool
	dryRun     bool
	noColor    bool
	cacheTTL   time.Duration
}

func NewRootCommand() *cobra.Command {
	var configPath string
	var plain bool
	var dryRun bool
	var noColor bool
	var cacheTTL time.Duration
	defaultConfigPath, _ := appconfig.DefaultPath()

	cmd := &cobra.Command{
		Use:           "ssm-manager",
		Short:         "Connect to EC2 instances using AWS Systems Manager",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := awscli.New()
			if err != nil {
				return err
			}
			in := cmd.InOrStdin()
			out := cmd.OutOrStdout()
			app := application{
				client:     client,
				prompter:   NewPrompter(in, out),
				in:         in,
				out:        out,
				configPath: configPath,
				plain:      plain || !isInteractiveTerminal(in, out),
				dryRun:     dryRun,
				noColor:    noColor,
				cacheTTL:   cacheTTL,
			}
			return app.run(cmd.Context())
		},
	}
	cmd.SetIn(os.Stdin)
	cmd.SetOut(os.Stdout)
	cmd.SetErr(os.Stderr)
	cmd.Flags().StringVar(&configPath, "config", defaultConfigPath, "path to the YAML configuration file")
	cmd.Flags().BoolVar(&plain, "plain", false, "use numbered prompts instead of the terminal UI")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the exact AWS command without starting a session")
	cmd.Flags().BoolVar(&noColor, "no-color", false, "disable colors and terminal styling")
	cmd.Flags().DurationVar(&cacheTTL, "cache-ttl", 5*time.Minute, "instance and resource cache lifetime")
	return cmd
}

func (a application) run(ctx context.Context) error {
	cfg, err := appconfig.Load(a.configPath)
	if err != nil {
		return err
	}
	if !a.plain {
		return a.runTUI(ctx, cfg)
	}
	modeOptions := []string{"Explore dynamically"}
	if len(cfg.Bookmarks) > 0 {
		modeOptions = append(modeOptions, "Bookmarks")
	}
	mode, err := a.prompter.Select("How do you want to connect?", modeOptions)
	if err != nil {
		return err
	}
	if mode == 1 {
		bookmark, err := a.chooseBookmark(cfg.Bookmarks)
		if err != nil {
			return err
		}
		return a.runBookmark(ctx, bookmark)
	}

	bookmark, err := a.buildDynamicSession(ctx, cfg)
	if err != nil {
		return err
	}
	if err := a.runBookmark(ctx, bookmark); err != nil {
		return err
	}
	return nil
}

func (a application) runTUI(ctx context.Context, cfg appconfig.Config) error {
	client, ok := a.client.(*awscli.Client)
	if !ok {
		return appTUI.Run(ctx, a.client, cfg, a.configPath, a.dryRun, a.in, a.out)
	}
	store, err := cache.New("ssm-manager", a.cacheTTL)
	if err != nil {
		return err
	}
	return appTUI.RunWithOptions(ctx, appTUI.Options{
		Inventory: appaws.NewService(client, appaws.DefaultConfigPath()),
		Sessions:  ssm.NewManager(client), Cache: store, NoColor: a.noColor,
		Input: a.in, Output: a.out, DryRun: a.dryRun,
	})
}

func (a application) buildDynamicSession(
	ctx context.Context,
	cfg appconfig.Config,
) (appconfig.Bookmark, error) {
	profile, err := a.chooseProfile(ctx)
	if err != nil {
		return appconfig.Bookmark{}, err
	}
	region, err := a.chooseRegion(ctx, profile)
	if err != nil {
		return appconfig.Bookmark{}, err
	}
	instance, err := a.chooseInstance(ctx, profile, region)
	if err != nil {
		return appconfig.Bookmark{}, err
	}
	bookmark := appconfig.Bookmark{
		Profile:      profile,
		Region:       region,
		InstanceID:   instance.ID,
		InstanceName: instance.Name,
	}

	action, err := a.prompter.Select("Action", []string{
		"Open terminal",
		"Forward a port to the instance",
		"Forward a port to a remote host",
	})
	if err != nil {
		return appconfig.Bookmark{}, err
	}
	switch action {
	case 0:
		bookmark.Type = appconfig.SessionTypeShell
	case 1:
		bookmark.Type = appconfig.SessionTypeForward
		if err := a.completeForward(&bookmark); err != nil {
			return appconfig.Bookmark{}, err
		}
	case 2:
		bookmark.Type = appconfig.SessionTypeRemoteHost
		if err := a.completeRemoteHost(ctx, cfg, instance, &bookmark); err != nil {
			return appconfig.Bookmark{}, err
		}
	default:
		return appconfig.Bookmark{}, errors.New("unknown action")
	}
	return bookmark, nil
}

func (a application) chooseProfile(ctx context.Context) (string, error) {
	profiles, err := a.client.Profiles(ctx)
	if err != nil {
		return "", err
	}
	choice, err := a.prompter.Select("AWS profile", profiles)
	if err != nil {
		return "", err
	}
	return profiles[choice], nil
}

func (a application) chooseRegion(ctx context.Context, profile string) (string, error) {
	regions, err := a.client.Regions(ctx, profile)
	if err != nil {
		return "", err
	}
	choice, err := a.prompter.Select("AWS region", regions)
	if err != nil {
		return "", err
	}
	return regions[choice], nil
}

func (a application) chooseInstance(
	ctx context.Context,
	profile string,
	region string,
) (awscli.Instance, error) {
	instances, err := a.client.Instances(ctx, profile, region)
	if err != nil {
		return awscli.Instance{}, err
	}
	if len(instances) == 0 {
		return awscli.Instance{}, fmt.Errorf("no running EC2 instances online in SSM were found in %s", region)
	}
	filter, err := a.prompter.Text("Filter instances by name, ID, or private IP", "")
	if err != nil {
		return awscli.Instance{}, err
	}
	instances = filterInstances(instances, filter)
	if len(instances) == 0 {
		return awscli.Instance{}, fmt.Errorf("no instances match %q", filter)
	}

	labels := make([]string, 0, len(instances))
	for _, instance := range instances {
		name := instance.Name
		if name == "" {
			name = "(unnamed)"
		}
		labels = append(labels, fmt.Sprintf("%-28s %-20s %s", name, instance.ID, instance.PrivateIP))
	}
	choice, err := a.prompter.Select("EC2 instance", labels)
	if err != nil {
		return awscli.Instance{}, err
	}
	return instances[choice], nil
}

func (a application) completeForward(bookmark *appconfig.Bookmark) error {
	remotePort, err := a.prompter.Port("Remote port", 8080)
	if err != nil {
		return err
	}
	localPort, err := a.prompter.Port("Local port", remotePort)
	if err != nil {
		return err
	}
	bookmark.RemotePort = remotePort
	bookmark.LocalPort = localPort
	return nil
}

func (a application) completeRemoteHost(
	ctx context.Context,
	cfg appconfig.Config,
	instance awscli.Instance,
	bookmark *appconfig.Bookmark,
) error {
	targets := cfg.TargetsFor(bookmark.Profile, bookmark.Region)
	result := a.client.DiscoverRemoteHosts(ctx, bookmark.Profile, bookmark.Region, instance.VPCID)
	for _, warning := range result.Warnings {
		fmt.Fprintf(a.out, "Warning discovering %s\n", warning)
	}

	labels := make([]string, 0, len(result.Hosts)+len(targets)+1)
	for _, host := range result.Hosts {
		labels = append(labels, fmt.Sprintf(
			"[discovered] %-16s %-18s %-14s %s:%d",
			host.Service,
			host.Name,
			host.Engine,
			host.Host,
			host.Port,
		))
	}
	for _, target := range targets {
		labels = append(labels, fmt.Sprintf("[configured] %-20s %s:%d", target.Name, target.Host, target.RemotePort))
	}
	labels = append(labels, "Other host")
	choice, err := a.prompter.Select("Remote target", labels)
	if err != nil {
		return err
	}

	switch {
	case choice < len(result.Hosts):
		host := result.Hosts[choice]
		bookmark.Host = host.Host
		bookmark.RemotePort = host.Port
	case choice < len(result.Hosts)+len(targets):
		target := targets[choice-len(result.Hosts)]
		bookmark.Host = target.Host
		bookmark.RemotePort = target.RemotePort
	default:
		bookmark.Host, err = a.prompter.Text("Remote host", "")
		if err != nil {
			return err
		}
		if bookmark.Host == "" {
			return errors.New("remote host cannot be empty")
		}
		bookmark.RemotePort, err = a.prompter.Port("Remote port", 5432)
		if err != nil {
			return err
		}
	}
	bookmark.LocalPort, err = a.prompter.Port("Local port", bookmark.RemotePort)
	return err
}

func (a application) chooseBookmark(bookmarks []appconfig.Bookmark) (appconfig.Bookmark, error) {
	labels := make([]string, 0, len(bookmarks))
	for _, bookmark := range bookmarks {
		labels = append(labels, bookmarkLabel(bookmark))
	}
	choice, err := a.prompter.Select("Bookmark", labels)
	if err != nil {
		return appconfig.Bookmark{}, err
	}
	return bookmarks[choice], nil
}

func (a application) runBookmark(ctx context.Context, bookmark appconfig.Bookmark) error {
	if len(bookmark.Tunnels) > 0 {
		return a.runBookmarkGroup(ctx, bookmark)
	}
	instanceName := bookmark.InstanceName
	if instanceName == "" {
		instanceName = bookmark.InstanceID
	}
	spec := awscli.SessionSpec{
		Type:       string(bookmark.Type),
		Profile:    bookmark.Profile,
		Region:     bookmark.Region,
		InstanceID: bookmark.InstanceID,
		Host:       bookmark.Host,
		RemotePort: bookmark.RemotePort,
		LocalPort:  bookmark.LocalPort,
	}
	if a.dryRun {
		command, err := awscli.SessionCommandString(spec)
		if err != nil {
			return err
		}
		fmt.Fprintln(a.out, command)
		return nil
	}
	if bookmark.Type != appconfig.SessionTypeShell {
		if err := ensureLocalPortAvailable(bookmark.LocalPort); err != nil {
			return err
		}
	}
	fmt.Fprintf(a.out, "\nStarting %s through %s...\n", bookmark.Type, instanceName)

	switch bookmark.Type {
	case appconfig.SessionTypeShell:
		return a.client.Shell(ctx, bookmark.Profile, bookmark.Region, bookmark.InstanceID)
	case appconfig.SessionTypeForward:
		return a.client.Forward(
			ctx,
			bookmark.Profile,
			bookmark.Region,
			bookmark.InstanceID,
			bookmark.RemotePort,
			bookmark.LocalPort,
		)
	case appconfig.SessionTypeRemoteHost:
		fmt.Fprintf(
			a.out,
			"Forwarding localhost:%d to %s:%d\n",
			bookmark.LocalPort,
			bookmark.Host,
			bookmark.RemotePort,
		)
		return a.client.ForwardHost(
			ctx,
			bookmark.Profile,
			bookmark.Region,
			bookmark.InstanceID,
			bookmark.Host,
			bookmark.RemotePort,
			bookmark.LocalPort,
		)
	default:
		return fmt.Errorf("unsupported session type %q", bookmark.Type)
	}
}

func (a application) runBookmarkGroup(ctx context.Context, bookmark appconfig.Bookmark) error {
	specs := make([]awscli.SessionSpec, 0, len(bookmark.Tunnels))
	seenPorts := map[int]struct{}{}
	for _, tunnel := range bookmark.Tunnels {
		if _, duplicate := seenPorts[tunnel.LocalPort]; duplicate {
			return fmt.Errorf("local port %d is repeated in bookmark %q", tunnel.LocalPort, bookmark.Name)
		}
		seenPorts[tunnel.LocalPort] = struct{}{}
		spec := awscli.SessionSpec{
			Type: string(tunnel.Type), Profile: bookmark.Profile, Region: bookmark.Region,
			InstanceID: bookmark.InstanceID, Host: tunnel.Host,
			RemotePort: tunnel.RemotePort, LocalPort: tunnel.LocalPort,
		}
		specs = append(specs, spec)
	}
	if a.dryRun {
		for _, spec := range specs {
			command, err := awscli.SessionCommandString(spec)
			if err != nil {
				return err
			}
			fmt.Fprintln(a.out, command)
		}
		return nil
	}
	for _, spec := range specs {
		if err := ensureLocalPortAvailable(spec.LocalPort); err != nil {
			return err
		}
	}
	sessions := make([]*awscli.BackgroundSession, 0, len(specs))
	for _, spec := range specs {
		session, err := a.client.StartBackground(ctx, spec)
		if err != nil {
			for _, started := range sessions {
				_ = started.Stop()
			}
			return err
		}
		sessions = append(sessions, session)
		fmt.Fprintf(a.out, "Started localhost:%d (PID %d)\n", spec.LocalPort, session.PID)
	}
	done := make(chan error, len(sessions))
	for _, session := range sessions {
		go func() { done <- <-session.Done }()
	}
	for range sessions {
		select {
		case <-ctx.Done():
			for _, session := range sessions {
				_ = session.Stop()
			}
			return ctx.Err()
		case err := <-done:
			if err != nil {
				for _, session := range sessions {
					_ = session.Stop()
				}
				return err
			}
		}
	}
	return nil
}

func bookmarkLabel(bookmark appconfig.Bookmark) string {
	instance := bookmark.InstanceName
	if instance == "" {
		instance = bookmark.InstanceID
	}
	switch bookmark.Type {
	case appconfig.SessionTypeShell:
		return fmt.Sprintf("%-20s shell         %s", bookmark.Name, instance)
	case appconfig.SessionTypeForward:
		return fmt.Sprintf(
			"%-20s port-forward  localhost:%d -> %s:%d",
			bookmark.Name,
			bookmark.LocalPort,
			instance,
			bookmark.RemotePort,
		)
	case appconfig.SessionTypeRemoteHost:
		return fmt.Sprintf(
			"%-20s remote-host   localhost:%d -> %s:%d via %s",
			bookmark.Name,
			bookmark.LocalPort,
			bookmark.Host,
			bookmark.RemotePort,
			instance,
		)
	default:
		return bookmark.Name
	}
}

func filterInstances(instances []awscli.Instance, filter string) []awscli.Instance {
	filter = strings.ToLower(strings.TrimSpace(filter))
	if filter == "" {
		return instances
	}
	filtered := []awscli.Instance{}
	for _, instance := range instances {
		searchable := strings.ToLower(instance.Name + " " + instance.ID + " " + instance.PrivateIP)
		if strings.Contains(searchable, filter) {
			filtered = append(filtered, instance)
		}
	}
	return filtered
}

func ensureLocalPortAvailable(port int) error {
	listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return fmt.Errorf("local port %d is unavailable: %w", port, err)
	}
	if err := listener.Close(); err != nil {
		return fmt.Errorf("checking local port %d: %w", port, err)
	}
	return nil
}

func isInteractiveTerminal(in io.Reader, out io.Writer) bool {
	inFile, inputIsFile := in.(*os.File)
	outFile, outputIsFile := out.(*os.File)
	if !inputIsFile || !outputIsFile {
		return false
	}
	inInfo, err := inFile.Stat()
	if err != nil {
		return false
	}
	outInfo, err := outFile.Stat()
	if err != nil {
		return false
	}
	return inInfo.Mode()&os.ModeCharDevice != 0 && outInfo.Mode()&os.ModeCharDevice != 0
}
