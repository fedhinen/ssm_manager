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

To print the embedded version:

```sh
./ssm-manager --version
```

Unreleased local builds report `dev`. Stable release binaries are published
from merged pull requests to `main` and are available from the repository's
GitHub Releases page.

## Terminal UI

Interactive terminals use a drill-down view stack: profile, optional region,
instances, action overlay, and remote-resource scan. AWS requests run in the
background and the UI remains responsive.

- `j`/`k` or up/down moves the current selection; `Enter` selects
- `Esc` pops the current view
- `/` starts fuzzy filtering without disabling the other instance shortcuts
- `p` opens direct port forwarding; `r` scans remote resources
- `Tab` toggles the active-session split without replacing the current view
- `x` kills the selected session while that split is visible
- `Ctrl+R` bypasses the cache and refreshes the current inventory
- `q` exits from the instance list

The theme follows the terminal's light/dark background and detected color
profile, degrading from TrueColor to ANSI. `NO_COLOR`, `--no-color`, and
`TERM=dumb` produce an unstyled text fallback. Rounded borders are used only
when styling is available. The minimum supported size is `80x24`.
When stdin or stdout is redirected, the CLI automatically falls back to numbered
prompts so ANSI control sequences never leak into pipelines. Use `--plain` to
request that mode explicitly.

Select an AWS profile, enabled region, and running EC2 instance online in SSM,
then start one of:

1. Interactive terminal
2. Port forwarding to the selected instance
3. Port forwarding through the instance to a remote host

Instances can be filtered by Name tag, instance ID, or private IP. Every session
is added to persistent history. Bookmarks are created explicitly from that
history instead of being prompted when a session closes.

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
They may also contain a `tunnels` list; every entry is started concurrently
through the bookmark's instance and grouped under its name in the sessions
panel. See `config.example.yaml` for the format.

Use `--dry-run` to print (plain mode) or preview (TUI) the exact shell-safe AWS
CLI command without starting a session. This also previews every command in a
multi-tunnel bookmark.

Instance and remote-resource responses are cached under the user's cache
directory. Set the lifetime with `--cache-ttl` (default `5m`) and use `Ctrl+R`
for a manual refresh.

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

## Development

Create a development branch from `dev` or `main`, then run the test suite
before opening a pull request:

```sh
go test ./...
```

Pull requests should explain the user-visible change, include tests for new
behavior where practical, and keep unrelated refactors out of the same
change. See [CONTRIBUTING.md](CONTRIBUTING.md) for the complete workflow.

## License

ssm-manager is distributed under the GNU General Public License, version 3 or
later. See [LICENSE](LICENSE).
