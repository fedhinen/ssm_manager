package awscli

import (
	"context"
	"os"
	"path/filepath"
	"testing"
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
    echo '[["orders-cluster","aurora-postgresql","orders-cluster.internal",5432,"rds-vpc-1"]]'
    ;;
  "docdb describe-db-instances")
    echo '[["documents","docdb","documents.internal",27017,"vpc-1"],["wrong-aurora","aurora-postgresql","wrong.internal",5432,"vpc-1"]]'
    ;;
  "docdb describe-db-subnet-groups")
    echo '[["docdb-vpc-1","vpc-1"]]'
    ;;
  "docdb describe-db-clusters")
    echo '[["documents-cluster","docdb","documents-cluster.internal",27017,"docdb-vpc-1"],["wrong-cluster","aurora-postgresql","wrong-cluster.internal",5432,"docdb-vpc-1"]]'
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
	if len(result.Hosts) != 7 {
		t.Fatalf("DiscoverRemoteHosts() hosts = %#v, want 7", result.Hosts)
	}
	foundServices := map[string]bool{}
	for _, host := range result.Hosts {
		foundServices[host.Service] = true
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
