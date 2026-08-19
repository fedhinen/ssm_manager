package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/victorruiz/ssm-manager/internal/awscli"
	appconfig "github.com/victorruiz/ssm-manager/internal/config"
)

type sessionClient interface {
	Profiles(context.Context) ([]string, error)
	Regions(context.Context, string) ([]string, error)
	Instances(context.Context, string, string) ([]awscli.Instance, error)
	DiscoverRemoteHosts(context.Context, string, string, string) awscli.DiscoveryResult
	Shell(context.Context, string, string, string) error
	Forward(context.Context, string, string, string, int, int) error
	ForwardHost(context.Context, string, string, string, string, int, int) error
}

type application struct {
	client     sessionClient
	prompter   *Prompter
	out        io.Writer
	configPath string
}

func NewRootCommand() *cobra.Command {
	var configPath string
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
			app := application{
				client:     client,
				prompter:   NewPrompter(cmd.InOrStdin(), cmd.OutOrStdout()),
				out:        cmd.OutOrStdout(),
				configPath: configPath,
			}
			return app.run(cmd.Context())
		},
	}
	cmd.SetIn(os.Stdin)
	cmd.SetOut(os.Stdout)
	cmd.SetErr(os.Stderr)
	cmd.Flags().StringVar(&configPath, "config", defaultConfigPath, "path to the YAML configuration file")
	return cmd
}

func (a application) run(ctx context.Context) error {
	cfg, err := appconfig.Load(a.configPath)
	if err != nil {
		return err
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
	return a.offerBookmark(cfg, bookmark)
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
	if bookmark.Type != appconfig.SessionTypeShell {
		if err := ensureLocalPortAvailable(bookmark.LocalPort); err != nil {
			return err
		}
	}
	instanceName := bookmark.InstanceName
	if instanceName == "" {
		instanceName = bookmark.InstanceID
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

func (a application) offerBookmark(cfg appconfig.Config, bookmark appconfig.Bookmark) error {
	save, err := a.prompter.Confirm("Save this session as a bookmark?", false)
	if err != nil || !save {
		return err
	}
	name, err := a.prompter.Text("Bookmark name", "")
	if err != nil {
		return err
	}
	bookmark.Name = strings.TrimSpace(name)
	if err := cfg.AddBookmark(bookmark); err != nil {
		return err
	}
	if err := appconfig.Save(a.configPath, cfg); err != nil {
		return err
	}
	fmt.Fprintf(a.out, "Bookmark %q saved in %s\n", bookmark.Name, a.configPath)
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
