package awscli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestClient_DiscoverRemoteHosts(t *testing.T) {
	t.Parallel()
	script := `#!/bin/sh
case "$1 $2" in
  "rds describe-db-instances")
    echo '[["orders","postgres","orders.internal",5432,"vpc-1"],["other","mysql","other.internal",3306,"vpc-2"]]'
    ;;
  "rds describe-db-subnet-groups")
    echo '[["rds-vpc-1","vpc-1"]]'
    ;;
  "rds describe-db-clusters")
    echo '[["orders-cluster","aurora-postgresql","orders-w.internal","orders-r.internal",5432,"rds-vpc-1"]]'
    ;;
  "docdb describe-db-instances")
    echo '[["documents","docdb","documents.internal",27017,"vpc-1"],["wrong-aurora","aurora-postgresql","wrong.internal",5432,"vpc-1"]]'
    ;;
  "docdb describe-db-subnet-groups")
    echo '[["docdb-vpc-1","vpc-1"]]'
    ;;
  "docdb describe-db-clusters")
    echo '[["documents-cluster","docdb","documents-w.internal","documents-r.internal",27017,"docdb-vpc-1"],["wrong-cluster","aurora-postgresql","wrong-w.internal","wrong-r.internal",5432,"docdb-vpc-1"]]'
    ;;
  "elasticache describe-cache-subnet-groups")
    echo '[["cache-vpc-1","vpc-1"]]'
    ;;
  "elasticache describe-cache-clusters")
    echo '[["redis-main","redis","cache-vpc-1",null,null,"redis.internal",6379]]'
    ;;
  "mq list-brokers")
    echo '[["broker-1","events","RabbitMQ"]]'
    ;;
  "mq describe-broker")
    echo '{"BrokerInstances":[{"ConsoleURL":"https://events.mq.internal","Endpoints":["amqps://events.mq.internal:5671"]}],"SubnetIds":["subnet-1"]}'
    ;;
  "ec2 describe-subnets")
    echo '[["vpc-1"]]'
    ;;
  *)
    echo "unexpected command: $1 $2" >&2
    exit 1
    ;;
esac
`
	client := newScriptClient(t, script)

	result := client.DiscoverRemoteHosts(context.Background(), "dev", "us-east-1", "vpc-1")
	if len(result.Warnings) != 0 {
		t.Fatalf("DiscoverRemoteHosts() warnings = %v, want none", result.Warnings)
	}
	if len(result.Hosts) != 9 {
		t.Fatalf("DiscoverRemoteHosts() hosts = %#v, want 9", result.Hosts)
	}
	foundServices := map[string]bool{}
	foundNames := map[string]bool{}
	for _, host := range result.Hosts {
		foundServices[host.Service] = true
		foundNames[host.Name] = true
		if host.VPCID != "vpc-1" {
			t.Errorf("discovered host %q VPC = %q, want vpc-1", host.Name, host.VPCID)
		}
		isDocumentDB := host.Service == "DocumentDB" || host.Service == "DocumentDB cluster"
		if isDocumentDB && host.Engine != "docdb" {
			t.Errorf("DocumentDB host %q has engine %q, want docdb", host.Name, host.Engine)
		}
		if host.Service == "Amazon MQ console" && host.Port != 15671 {
			t.Errorf("Amazon MQ console port = %d, want 15671", host.Port)
		}
	}
	for _, name := range []string{"orders-cluster (W)", "orders-cluster (R1)"} {
		if !foundNames[name] {
			t.Errorf("DiscoverRemoteHosts() did not return Aurora endpoint %q", name)
		}
	}
	for _, service := range []string{"RDS instance", "Aurora cluster", "Amazon MQ", "Amazon MQ console"} {
		if !foundServices[service] {
			t.Errorf("DiscoverRemoteHosts() did not return service %q", service)
		}
	}
}

func TestClient_ShellExitCode(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		exitCode  string
		wantError bool
	}{
		{name: "session plugin closes with one", exitCode: "1", wantError: false},
		{name: "unexpected failure", exitCode: "2", wantError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			client := newScriptClient(t, "#!/bin/sh\nexit "+tt.exitCode+"\n")
			err := client.Shell(context.Background(), "dev", "us-east-1", "i-001")
			if (err != nil) != tt.wantError {
				t.Errorf("Shell() error = %v, wantError %v", err, tt.wantError)
			}
		})
	}
}

func TestClient_StartBackgroundReportsCompletion(t *testing.T) {
	t.Parallel()
	client := newScriptClient(t, "#!/bin/sh\nexit 0\n")
	session, err := client.StartBackground(context.Background(), SessionSpec{
		Type: "port-forward", Profile: "dev", Region: "us-east-1",
		InstanceID: "i-001", RemotePort: 8080, LocalPort: 8080,
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-session.Done:
		if err != nil {
			t.Fatalf("background completion: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("background process did not report completion")
	}
}

func TestClient_StopBackgroundReportsCompletion(t *testing.T) {
	t.Parallel()
	client := newScriptClient(t, "#!/bin/sh\nexec sleep 10\n")
	session, err := client.StartBackground(context.Background(), SessionSpec{
		Type: "port-forward", Profile: "", Region: "us-east-1", InstanceID: "i-001",
		RemotePort: 8080, LocalPort: 8080,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Stop(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-session.Done:
	case <-time.After(2 * time.Second):
		t.Fatal("stopped background process did not report completion")
	}
}

func TestClient_ExpiredSSOSessionRequestsLoginAndRetries(t *testing.T) {
	t.Parallel()
	statePath := filepath.Join(t.TempDir(), "authorized")
	script := `#!/bin/sh
if [ "$1 $2" = "sso login" ]; then
  touch "` + statePath + `"
  exit 0
fi
if [ ! -f "` + statePath + `" ]; then
  echo "The SSO session associated with this profile has expired or is otherwise invalid." >&2
  exit 1
fi
echo '["mx-central-1"]'
`
	client := newScriptClient(t, script)

	regions, err := client.Regions(context.Background(), "dev-sso")
	if err != nil {
		t.Fatal(err)
	}
	if len(regions) != 1 || regions[0] != "mx-central-1" {
		t.Fatalf("Regions() = %v, want [mx-central-1]", regions)
	}
}

func TestClient_NonSSOAuthErrorDoesNotRequestLogin(t *testing.T) {
	t.Parallel()
	client := newScriptClient(t, "#!/bin/sh\necho 'Unable to locate credentials' >&2\nexit 1\n")

	_, err := client.Regions(context.Background(), "dev")
	if err == nil || !strings.Contains(err.Error(), "Unable to locate credentials") {
		t.Fatalf("Regions() error = %v, want original credential error", err)
	}
}

func TestClient_SessionCommandAuthorizesExpiredSSOBookmark(t *testing.T) {
	t.Parallel()
	statePath := filepath.Join(t.TempDir(), "authorized")
	script := `#!/bin/sh
case "$1 $2 $3" in
  "configure get sso_session") echo "my-sso"; exit 0 ;;
  "sso login --profile") touch "` + statePath + `"; exit 0 ;;
  "sts get-caller-identity")
    if [ ! -f "` + statePath + `" ]; then echo "SSO session expired" >&2; exit 1; fi
    echo '{}'; exit 0 ;;
esac
exit 0
`
	client := newScriptClient(t, script)
	if _, err := client.SessionCommand(context.Background(), SessionSpec{Type: "shell", Profile: "dev-sso", Region: "us-east-1", InstanceID: "i-001"}); err != nil {
		t.Fatal(err)
	}
}

func TestSessionCommandString(t *testing.T) {
	t.Parallel()
	command, err := SessionCommandString(SessionSpec{
		Type: "remote-host", Profile: "dev team", Region: "mx-central-1",
		InstanceID: "i-001", Host: "db.internal", RemotePort: 5432, LocalPort: 15432,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"aws ssm start-session", "--document-name AWS-StartPortForwardingSessionToRemoteHost", "'dev team'", "localPortNumber"} {
		if !strings.Contains(command, expected) {
			t.Errorf("SessionCommandString() = %q, want %q", command, expected)
		}
	}
}

func TestClient_SSOLoginPublishesProcessOutput(t *testing.T) {
	t.Parallel()
	client := newScriptClient(t, "#!/bin/sh\necho 'browser login URL'\n")
	client.events = make(chan ProcessEvent, 4)
	var stdout bytes.Buffer
	client.Stdout = &stdout
	if err := client.ssoLogin(context.Background(), "dev"); err != nil {
		t.Fatal(err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("ssoLogin wrote raw output to TUI writer: %q", stdout.String())
	}
	started := <-client.events
	output := <-client.events
	done := <-client.events
	if !strings.Contains(started.Message, "navegador") || output.Message != "browser login URL" || !done.Done {
		t.Fatalf("unexpected process events: %#v %#v %#v", started, output, done)
	}
}

func newScriptClient(t *testing.T, script string) *Client {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-aws")
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return &Client{
		AWSPath: path,
		Stdin:   nil,
		Stdout:  os.Stdout,
		Stderr:  os.Stderr,
	}
}
