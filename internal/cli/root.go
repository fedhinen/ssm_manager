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
	profile, err := a.chooseProfile(ctx)
	if err != nil {
		return err
	}
	region, err := a.chooseRegion(ctx, profile)
	if err != nil {
		return err
	}
	instance, err := a.chooseInstance(ctx, profile, region)
	if err != nil {
		return err
	}

	action, err := a.prompter.Select("Action", []string{
		"Open terminal",
		"Forward a port to the instance",
		"Forward a port to a remote host",
	})
	if err != nil {
		return err
	}
	switch action {
	case 0:
		return a.client.Shell(ctx, profile, region, instance.ID)
	case 1:
		return a.forward(ctx, profile, region, instance.ID)
	case 2:
		return a.forwardHost(ctx, profile, region, instance.ID)
	default:
		return errors.New("unknown action")
	}
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
		return awscli.Instance{}, fmt.Errorf("no running EC2 instances were found in %s", region)
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

func (a application) forward(ctx context.Context, profile, region, instanceID string) error {
	remotePort, err := a.prompter.Port("Remote port", 8080)
	if err != nil {
		return err
	}
	localPort, err := a.prompter.Port("Local port", remotePort)
	if err != nil {
		return err
	}
	if err := ensureLocalPortAvailable(localPort); err != nil {
		return err
	}
	return a.client.Forward(ctx, profile, region, instanceID, remotePort, localPort)
}

func (a application) forwardHost(ctx context.Context, profile, region, instanceID string) error {
	cfg, err := appconfig.Load(a.configPath)
	if err != nil {
		return err
	}
	targets := cfg.TargetsFor(profile, region)
	labels := make([]string, 0, len(targets)+1)
	for _, target := range targets {
		labels = append(labels, fmt.Sprintf("%s (%s:%d)", target.Name, target.Host, target.RemotePort))
	}
	labels = append(labels, "Other host")
	choice, err := a.prompter.Select("Remote target", labels)
	if err != nil {
		return err
	}

	var host string
	var remotePort int
	if choice == len(targets) {
		host, err = a.prompter.Text("Remote host", "")
		if err != nil {
			return err
		}
		if host == "" {
			return errors.New("remote host cannot be empty")
		}
		remotePort, err = a.prompter.Port("Remote port", 5432)
		if err != nil {
			return err
		}
	} else {
		host = targets[choice].Host
		remotePort = targets[choice].RemotePort
	}

	localPort, err := a.prompter.Port("Local port", remotePort)
	if err != nil {
		return err
	}
	if err := ensureLocalPortAvailable(localPort); err != nil {
		return err
	}
	fmt.Fprintf(a.out, "Forwarding localhost:%d to %s:%d through %s\n", localPort, host, remotePort, instanceID)
	return a.client.ForwardHost(ctx, profile, region, instanceID, host, remotePort, localPort)
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
