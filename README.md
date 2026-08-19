# ssm-manager

Interactive CLI for opening AWS Systems Manager sessions without remembering the
full `aws ssm start-session` syntax.

## Requirements

- AWS CLI v2
- Session Manager Plugin
- At least one configured AWS profile
- Permissions to describe EC2 regions and instances and start SSM sessions

## Build and run

```sh
go build -o ssm-manager ./cmd/ssm-manager
./ssm-manager
```

The wizard selects an AWS profile, enabled region, running EC2 instance that is
online in SSM, and one of:

1. Interactive terminal
2. Port forwarding to the selected instance
3. Port forwarding through the instance to a remote host

Instances can be filtered by Name tag, instance ID, or private IP.

## Remote targets

Copy `config.example.yaml` to the user config directory:

- macOS: `~/Library/Application Support/ssm-manager/config.yaml`
- Linux: `~/.config/ssm-manager/config.yaml`

Targets with an empty `profile` or `region` apply to every profile or region.
Use `--config path/to/config.yaml` to load another file.

When no configured target is suitable, choose **Other host** and enter its host,
remote port, and local port interactively.
