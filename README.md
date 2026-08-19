# ssm-manager

Interactive CLI for opening AWS Systems Manager sessions without remembering the
full `aws ssm start-session` syntax.

## Requirements

- AWS CLI v2
- Session Manager Plugin
- At least one configured AWS profile
- Permissions for EC2 and SSM discovery and sessions

Optional remote-host discovery uses read permissions for RDS, DocumentDB,
ElastiCache, Amazon MQ, and EC2 subnets. A missing permission only hides that
service and prints a warning; the remaining sources still work.

## Build and run

```sh
go build -o ssm-manager ./cmd/ssm-manager
./ssm-manager
```

At startup, choose between dynamic exploration and saved bookmarks. Dynamic
exploration selects an AWS profile, enabled region, running EC2 instance online
in SSM, and one of:

1. Interactive terminal
2. Port forwarding to the selected instance
3. Port forwarding through the instance to a remote host

Instances can be filtered by Name tag, instance ID, or private IP. After a
dynamic session ends, the CLI offers to save the complete session as a bookmark.

## Remote-host discovery

Remote-host forwarding combines three sources in a single selector:

- Resources discovered in the EC2 instance's VPC: RDS/Aurora instances and
  clusters, DocumentDB, ElastiCache, and Amazon MQ protocol and web-console
  endpoints
- Manually configured `targets`
- An arbitrary host and port entered interactively

The discovered endpoint supplies its remote port; only the local port is asked.
VPC filtering reduces irrelevant results but does not validate security groups,
DNS, routes, or actual network connectivity.

RabbitMQ web-console bookmarks use the traditional TLS management listener on
remote port `15671`. AMQPS continues to use the endpoint reported by AWS,
normally `5671`.

## Configuration and bookmarks

See `config.example.yaml` for the complete format. The default location is:

- macOS: `~/Library/Application Support/ssm-manager/config.yaml`
- Linux: `~/.config/ssm-manager/config.yaml`

Use `--config path/to/config.yaml` to load another file. The CLI creates the
parent directory and writes the file with mode `0600` when saving a bookmark.
Existing manual targets are preserved.

Targets with an empty `profile` or `region` apply to every profile or region.
Bookmarks support `shell`, `port-forward`, and `remote-host` session types.

## IAM read permissions

The exact policy can be narrowed to your environment. Discovery may call:

- `ec2:DescribeRegions`, `ec2:DescribeInstances`, `ec2:DescribeSubnets`
- `ssm:DescribeInstanceInformation`, `ssm:StartSession`
- `rds:DescribeDBInstances`, `rds:DescribeDBClusters`,
  `rds:DescribeDBSubnetGroups`
- `docdb:DescribeDBInstances`, `docdb:DescribeDBClusters`,
  `docdb:DescribeDBSubnetGroups`
- `elasticache:DescribeCacheClusters`, `elasticache:DescribeCacheSubnetGroups`
- `mq:ListBrokers`, `mq:DescribeBroker`
