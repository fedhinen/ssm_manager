package awscli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

type Client struct {
	AWSPath string
	Stdin   io.Reader
	Stdout  io.Writer
	Stderr  io.Writer
}

type Instance struct {
	ID        string
	Name      string
	PrivateIP string
	State     string
}

func New() (*Client, error) {
	awsPath, err := exec.LookPath("aws")
	if err != nil {
		return nil, errors.New("AWS CLI is not installed or is not in PATH")
	}
	if _, err := exec.LookPath("session-manager-plugin"); err != nil {
		return nil, errors.New("Session Manager Plugin is not installed or is not in PATH")
	}
	return &Client{
		AWSPath: awsPath,
		Stdin:   os.Stdin,
		Stdout:  os.Stdout,
		Stderr:  os.Stderr,
	}, nil
}

func (c *Client) Profiles(ctx context.Context) ([]string, error) {
	output, err := c.output(ctx, "configure", "list-profiles")
	if err != nil {
		return nil, err
	}
	profiles := strings.Fields(string(output))
	if len(profiles) == 0 {
		return nil, errors.New("no AWS profiles were found")
	}
	return profiles, nil
}

func (c *Client) Regions(ctx context.Context, profile string) ([]string, error) {
	args := []string{
		"ec2", "describe-regions",
		"--profile", profile,
		"--all-regions",
		"--query", "Regions[?OptInStatus!='not-opted-in'].RegionName",
		"--output", "json",
	}
	output, err := c.output(ctx, args...)
	if err != nil {
		return nil, fmt.Errorf("listing regions for profile %q: %w", profile, err)
	}
	regions := []string{}
	if err := json.Unmarshal(output, &regions); err != nil {
		return nil, fmt.Errorf("decoding AWS regions: %w", err)
	}
	sort.Strings(regions)
	return regions, nil
}

func (c *Client) Instances(ctx context.Context, profile, region string) ([]Instance, error) {
	managedIDs, err := c.managedInstanceIDs(ctx, profile, region)
	if err != nil {
		return nil, err
	}
	if len(managedIDs) == 0 {
		return []Instance{}, nil
	}

	const query = "Reservations[].Instances[].[InstanceId, Tags[?Key=='Name']|[0].Value, PrivateIpAddress, State.Name]"
	args := []string{
		"ec2", "describe-instances",
		"--profile", profile,
		"--region", region,
		"--filters", "Name=instance-state-name,Values=running",
		"--query", query,
		"--output", "json",
	}
	output, err := c.output(ctx, args...)
	if err != nil {
		return nil, fmt.Errorf("listing instances in %s: %w", region, err)
	}

	rows := [][]any{}
	if err := json.Unmarshal(output, &rows); err != nil {
		return nil, fmt.Errorf("decoding EC2 instances: %w", err)
	}
	instances := make([]Instance, 0, len(rows))
	for _, row := range rows {
		if len(row) != 4 {
			continue
		}
		instance := Instance{
			ID:        stringValue(row[0]),
			Name:      stringValue(row[1]),
			PrivateIP: stringValue(row[2]),
			State:     stringValue(row[3]),
		}
		if _, managed := managedIDs[instance.ID]; managed {
			instances = append(instances, instance)
		}
	}
	sort.Slice(instances, func(i, j int) bool {
		return instances[i].Name < instances[j].Name
	})
	return instances, nil
}

func (c *Client) managedInstanceIDs(
	ctx context.Context,
	profile string,
	region string,
) (map[string]struct{}, error) {
	args := []string{
		"ssm", "describe-instance-information",
		"--profile", profile,
		"--region", region,
		"--filters", "Key=PingStatus,Values=Online",
		"--query", "InstanceInformationList[].InstanceId",
		"--output", "json",
	}
	output, err := c.output(ctx, args...)
	if err != nil {
		return nil, fmt.Errorf("listing online SSM instances in %s: %w", region, err)
	}
	ids := []string{}
	if err := json.Unmarshal(output, &ids); err != nil {
		return nil, fmt.Errorf("decoding SSM instances: %w", err)
	}
	managed := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		managed[id] = struct{}{}
	}
	return managed, nil
}

func (c *Client) Shell(ctx context.Context, profile, region, instanceID string) error {
	return c.runSession(ctx, profile, region, instanceID, "", nil)
}

func (c *Client) Forward(
	ctx context.Context,
	profile string,
	region string,
	instanceID string,
	remotePort int,
	localPort int,
) error {
	parameters := map[string][]string{
		"portNumber":      {strconv.Itoa(remotePort)},
		"localPortNumber": {strconv.Itoa(localPort)},
	}
	return c.runSession(
		ctx,
		profile,
		region,
		instanceID,
		"AWS-StartPortForwardingSession",
		parameters,
	)
}

func (c *Client) ForwardHost(
	ctx context.Context,
	profile string,
	region string,
	instanceID string,
	host string,
	remotePort int,
	localPort int,
) error {
	parameters := map[string][]string{
		"host":            {host},
		"portNumber":      {strconv.Itoa(remotePort)},
		"localPortNumber": {strconv.Itoa(localPort)},
	}
	return c.runSession(
		ctx,
		profile,
		region,
		instanceID,
		"AWS-StartPortForwardingSessionToRemoteHost",
		parameters,
	)
}

func (c *Client) runSession(
	ctx context.Context,
	profile string,
	region string,
	instanceID string,
	document string,
	parameters map[string][]string,
) error {
	args := []string{
		"ssm", "start-session",
		"--profile", profile,
		"--region", region,
		"--target", instanceID,
	}
	if document != "" {
		encoded, err := json.Marshal(parameters)
		if err != nil {
			return fmt.Errorf("encoding session parameters: %w", err)
		}
		args = append(args, "--document-name", document, "--parameters", string(encoded))
	}

	cmd := exec.CommandContext(ctx, c.AWSPath, args...)
	cmd.Stdin = c.Stdin
	cmd.Stdout = c.Stdout
	cmd.Stderr = c.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("session ended: %w", err)
	}
	return nil
}

func (c *Client) output(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, c.AWSPath, args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return nil, errors.New(message)
	}
	return stdout.Bytes(), nil
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	text, _ := value.(string)
	return text
}
