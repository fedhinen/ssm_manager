package awscli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
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
	VPCID     string
}

type RemoteHost struct {
	Name    string
	Service string
	Engine  string
	Host    string
	Port    int
	VPCID   string
}

type DiscoveryResult struct {
	Hosts    []RemoteHost
	Warnings []string
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

	const query = "Reservations[].Instances[].[InstanceId, Tags[?Key=='Name']|[0].Value, PrivateIpAddress, State.Name, VpcId]"
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
		if len(row) != 5 {
			continue
		}
		instance := Instance{
			ID:        stringValue(row[0]),
			Name:      stringValue(row[1]),
			PrivateIP: stringValue(row[2]),
			State:     stringValue(row[3]),
			VPCID:     stringValue(row[4]),
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

func (c *Client) DiscoverRemoteHosts(
	ctx context.Context,
	profile string,
	region string,
	vpcID string,
) DiscoveryResult {
	discoverers := []struct {
		name string
		fn   func(context.Context, string, string, string) ([]RemoteHost, error)
	}{
		{name: "RDS/Aurora", fn: c.discoverRDS},
		{name: "DocumentDB", fn: c.discoverDocumentDB},
		{name: "ElastiCache", fn: c.discoverElastiCache},
		{name: "Amazon MQ", fn: c.discoverMQ},
	}
	result := DiscoveryResult{
		Hosts:    []RemoteHost{},
		Warnings: []string{},
	}
	for _, discoverer := range discoverers {
		hosts, err := discoverer.fn(ctx, profile, region, vpcID)
		if err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("%s: %v", discoverer.name, err))
			continue
		}
		result.Hosts = append(result.Hosts, hosts...)
	}
	sort.Slice(result.Hosts, func(i, j int) bool {
		if result.Hosts[i].Service == result.Hosts[j].Service {
			return result.Hosts[i].Name < result.Hosts[j].Name
		}
		return result.Hosts[i].Service < result.Hosts[j].Service
	})
	return result
}

func (c *Client) discoverRDS(
	ctx context.Context,
	profile string,
	region string,
	vpcID string,
) ([]RemoteHost, error) {
	const query = "DBInstances[].[DBInstanceIdentifier,Engine,Endpoint.Address,Endpoint.Port,DBSubnetGroup.VpcId]"
	instances, err := c.discoverDatabaseRows(ctx, "rds", "RDS", profile, region, vpcID, query)
	if err != nil {
		return nil, err
	}
	clusters, err := c.discoverDatabaseClusters(ctx, "rds", "RDS/Aurora", profile, region, vpcID)
	if err != nil {
		return nil, err
	}
	for index := range instances {
		instances[index].Service = databaseServiceLabel(instances[index].Engine, false)
	}
	for index := range clusters {
		clusters[index].Service = databaseServiceLabel(clusters[index].Engine, true)
	}
	return append(instances, clusters...), nil
}

func (c *Client) discoverDocumentDB(
	ctx context.Context,
	profile string,
	region string,
	vpcID string,
) ([]RemoteHost, error) {
	const query = "DBInstances[].[DBInstanceIdentifier,Engine,Endpoint.Address,Endpoint.Port,DBSubnetGroup.VpcId]"
	instances, err := c.discoverDatabaseRows(ctx, "docdb", "DocumentDB", profile, region, vpcID, query)
	if err != nil {
		return nil, err
	}
	instances = filterHostsByEngine(instances, "docdb")
	clusters, err := c.discoverDatabaseClusters(ctx, "docdb", "DocumentDB cluster", profile, region, vpcID)
	if err != nil {
		return nil, err
	}
	clusters = filterHostsByEngine(clusters, "docdb")
	return append(instances, clusters...), nil
}

func (c *Client) discoverDatabaseClusters(
	ctx context.Context,
	service string,
	label string,
	profile string,
	region string,
	vpcID string,
) ([]RemoteHost, error) {
	subnetRows, err := c.outputRows(
		ctx,
		service, "describe-db-subnet-groups",
		"--profile", profile,
		"--region", region,
		"--query", "DBSubnetGroups[].[DBSubnetGroupName,VpcId]",
		"--output", "json",
	)
	if err != nil {
		return nil, err
	}
	subnetVPCs := map[string]string{}
	for _, row := range subnetRows {
		if len(row) == 2 {
			subnetVPCs[stringValue(row[0])] = stringValue(row[1])
		}
	}
	const query = "DBClusters[].[DBClusterIdentifier,Engine,Endpoint,Port,DBSubnetGroup]"
	rows, err := c.outputRows(
		ctx,
		service, "describe-db-clusters",
		"--profile", profile,
		"--region", region,
		"--query", query,
		"--output", "json",
	)
	if err != nil {
		return nil, err
	}
	hosts := []RemoteHost{}
	for _, row := range rows {
		if len(row) != 5 || stringValue(row[2]) == "" || intValue(row[3]) == 0 {
			continue
		}
		hostVPC := subnetVPCs[stringValue(row[4])]
		if vpcID != "" && hostVPC != vpcID {
			continue
		}
		hosts = append(hosts, RemoteHost{
			Name:    stringValue(row[0]),
			Service: label,
			Engine:  stringValue(row[1]),
			Host:    stringValue(row[2]),
			Port:    intValue(row[3]),
			VPCID:   hostVPC,
		})
	}
	return hosts, nil
}

func (c *Client) discoverDatabaseRows(
	ctx context.Context,
	service string,
	label string,
	profile string,
	region string,
	vpcID string,
	query string,
) ([]RemoteHost, error) {
	rows, err := c.outputRows(
		ctx,
		service, "describe-db-instances",
		"--profile", profile,
		"--region", region,
		"--query", query,
		"--output", "json",
	)
	if err != nil {
		return nil, err
	}
	hosts := []RemoteHost{}
	for _, row := range rows {
		if len(row) != 5 || stringValue(row[2]) == "" || intValue(row[3]) == 0 {
			continue
		}
		hostVPC := stringValue(row[4])
		if vpcID != "" && hostVPC != vpcID {
			continue
		}
		hosts = append(hosts, RemoteHost{
			Name:    stringValue(row[0]),
			Service: label,
			Engine:  stringValue(row[1]),
			Host:    stringValue(row[2]),
			Port:    intValue(row[3]),
			VPCID:   hostVPC,
		})
	}
	return hosts, nil
}

func (c *Client) discoverElastiCache(
	ctx context.Context,
	profile string,
	region string,
	vpcID string,
) ([]RemoteHost, error) {
	subnetRows, err := c.outputRows(
		ctx,
		"elasticache", "describe-cache-subnet-groups",
		"--profile", profile,
		"--region", region,
		"--query", "CacheSubnetGroups[].[CacheSubnetGroupName,VpcId]",
		"--output", "json",
	)
	if err != nil {
		return nil, err
	}
	subnetVPCs := map[string]string{}
	for _, row := range subnetRows {
		if len(row) == 2 {
			subnetVPCs[stringValue(row[0])] = stringValue(row[1])
		}
	}
	const query = "CacheClusters[].[CacheClusterId,Engine,CacheSubnetGroupName,ConfigurationEndpoint.Address,ConfigurationEndpoint.Port,CacheNodes[0].Endpoint.Address,CacheNodes[0].Endpoint.Port]"
	rows, err := c.outputRows(
		ctx,
		"elasticache", "describe-cache-clusters",
		"--show-cache-node-info",
		"--profile", profile,
		"--region", region,
		"--query", query,
		"--output", "json",
	)
	if err != nil {
		return nil, err
	}
	hosts := []RemoteHost{}
	for _, row := range rows {
		if len(row) != 7 {
			continue
		}
		hostVPC := subnetVPCs[stringValue(row[2])]
		if vpcID != "" && hostVPC != vpcID {
			continue
		}
		host := stringValue(row[3])
		port := intValue(row[4])
		if host == "" {
			host = stringValue(row[5])
			port = intValue(row[6])
		}
		if host == "" || port == 0 {
			continue
		}
		hosts = append(hosts, RemoteHost{
			Name:    stringValue(row[0]),
			Service: "ElastiCache",
			Engine:  stringValue(row[1]),
			Host:    host,
			Port:    port,
			VPCID:   hostVPC,
		})
	}
	return hosts, nil
}

func (c *Client) discoverMQ(
	ctx context.Context,
	profile string,
	region string,
	vpcID string,
) ([]RemoteHost, error) {
	rows, err := c.outputRows(
		ctx,
		"mq", "list-brokers",
		"--profile", profile,
		"--region", region,
		"--query", "BrokerSummaries[].[BrokerId,BrokerName,EngineType]",
		"--output", "json",
	)
	if err != nil {
		return nil, err
	}
	hosts := []RemoteHost{}
	for _, row := range rows {
		if len(row) != 3 {
			continue
		}
		brokerHosts, err := c.describeMQBroker(
			ctx,
			profile,
			region,
			vpcID,
			stringValue(row[0]),
			stringValue(row[1]),
			stringValue(row[2]),
		)
		if err != nil {
			return nil, err
		}
		hosts = append(hosts, brokerHosts...)
	}
	return hosts, nil
}

func (c *Client) describeMQBroker(
	ctx context.Context,
	profile string,
	region string,
	vpcID string,
	brokerID string,
	name string,
	engine string,
) ([]RemoteHost, error) {
	type brokerInstance struct {
		ConsoleURL string   `json:"ConsoleURL"`
		Endpoints  []string `json:"Endpoints"`
	}
	type brokerResponse struct {
		BrokerInstances []brokerInstance `json:"BrokerInstances"`
		SubnetIDs       []string         `json:"SubnetIds"`
	}
	output, err := c.output(
		ctx,
		"mq", "describe-broker",
		"--broker-id", brokerID,
		"--profile", profile,
		"--region", region,
		"--output", "json",
	)
	if err != nil {
		return nil, err
	}
	var broker brokerResponse
	if err := json.Unmarshal(output, &broker); err != nil {
		return nil, fmt.Errorf("decoding broker %q: %w", name, err)
	}
	brokerVPC := ""
	if len(broker.SubnetIDs) > 0 {
		brokerVPC, err = c.subnetVPC(ctx, profile, region, broker.SubnetIDs[0])
		if err != nil {
			return nil, err
		}
		if vpcID != "" && brokerVPC != vpcID {
			return []RemoteHost{}, nil
		}
	}
	hosts := []RemoteHost{}
	seen := map[string]struct{}{}
	for _, instance := range broker.BrokerInstances {
		if console, ok := parseRemoteEndpoint(instance.ConsoleURL); ok {
			if strings.EqualFold(engine, "RabbitMQ") {
				console.Port = 15671
			}
			key := console.Host + ":" + strconv.Itoa(console.Port)
			if _, exists := seen[key]; !exists {
				seen[key] = struct{}{}
				console.Name = name
				console.Service = "Amazon MQ console"
				console.Engine = engine + "/web-console"
				console.VPCID = brokerVPC
				hosts = append(hosts, console)
			}
		}
		for _, endpoint := range instance.Endpoints {
			remote, ok := parseRemoteEndpoint(endpoint)
			if !ok {
				continue
			}
			key := remote.Host + ":" + strconv.Itoa(remote.Port)
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			parsed, _ := url.Parse(endpoint)
			remote.Name = name
			remote.Service = "Amazon MQ"
			remote.Engine = engine + "/" + parsed.Scheme
			remote.VPCID = brokerVPC
			hosts = append(hosts, remote)
		}
	}
	return hosts, nil
}

func databaseServiceLabel(engine string, cluster bool) string {
	product := "RDS"
	if strings.HasPrefix(strings.ToLower(engine), "aurora") {
		product = "Aurora"
	}
	resourceType := "instance"
	if cluster {
		resourceType = "cluster"
	}
	return product + " " + resourceType
}

func parseRemoteEndpoint(endpoint string) (RemoteHost, bool) {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Hostname() == "" {
		return RemoteHost{}, false
	}
	portText := parsed.Port()
	if portText == "" {
		switch strings.ToLower(parsed.Scheme) {
		case "https":
			portText = "443"
		case "http":
			portText = "80"
		default:
			return RemoteHost{}, false
		}
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return RemoteHost{}, false
	}
	return RemoteHost{Host: parsed.Hostname(), Port: port}, true
}

func (c *Client) subnetVPC(
	ctx context.Context,
	profile string,
	region string,
	subnetID string,
) (string, error) {
	rows, err := c.outputRows(
		ctx,
		"ec2", "describe-subnets",
		"--subnet-ids", subnetID,
		"--profile", profile,
		"--region", region,
		"--query", "Subnets[].[VpcId]",
		"--output", "json",
	)
	if err != nil {
		return "", err
	}
	if len(rows) == 0 || len(rows[0]) == 0 {
		return "", nil
	}
	return stringValue(rows[0][0]), nil
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
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, os.Interrupt)
	defer signal.Stop(interrupts)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting session: %w", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	interrupted := false
	for {
		select {
		case <-interrupts:
			interrupted = true
		case err := <-done:
			if interrupted {
				return nil
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if isNormalSessionExit(err) {
				return nil
			}
			if err != nil {
				return fmt.Errorf("session ended: %w", err)
			}
			return nil
		}
	}
}

func filterHostsByEngine(hosts []RemoteHost, engine string) []RemoteHost {
	filtered := make([]RemoteHost, 0, len(hosts))
	for _, host := range hosts {
		if strings.EqualFold(host.Engine, engine) {
			filtered = append(filtered, host)
		}
	}
	return filtered
}

func isNormalSessionExit(err error) bool {
	if err == nil {
		return true
	}
	exitError, ok := errors.AsType[*exec.ExitError](err)
	if !ok {
		return false
	}
	return exitError.ExitCode() == 1 || exitError.ExitCode() == 130
}

func (c *Client) outputRows(ctx context.Context, args ...string) ([][]any, error) {
	output, err := c.output(ctx, args...)
	if err != nil {
		return nil, err
	}
	rows := [][]any{}
	if err := json.Unmarshal(output, &rows); err != nil {
		return nil, fmt.Errorf("decoding AWS response: %w", err)
	}
	return rows, nil
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

func intValue(value any) int {
	number, _ := value.(float64)
	return int(number)
}
