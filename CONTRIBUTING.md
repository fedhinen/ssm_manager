# Contributing to ssm-manager

Thank you for contributing. The project is a Go CLI, so changes should keep
the command usable both interactively and in plain, redirected-output mode.

## Development setup

Install Go, AWS CLI v2, and the Session Manager Plugin. Clone the repository,
check out `dev`, and run:

```sh
go test ./...
go build ./cmd/ssm-manager
```

The TUI requires a terminal of at least `80x24`. Use `--plain` or redirected
input/output when testing non-interactive behavior.

## Branches and pull requests

- Use `dev` for integration work and create feature branches from it.
- Keep commits focused and use a short imperative subject, for example
  `fix: stop forwarding child processes`.
- Rebase or update the branch before requesting review.
- Describe the problem, the solution, and how it was tested.
- Include screenshots or terminal recordings for meaningful TUI changes.
- Do not commit AWS credentials, local configuration, cache files, binaries,
  or generated indexes.

## Testing changes

Run the complete suite before submitting a pull request:

```sh
go test ./...
go test -race ./internal/tui ./internal/ssm ./internal/awscli
```

For TUI changes, verify resizing at `80x24`, `120x40`, and `200x60`, and test
keyboard paths for both success and cancellation. For session lifecycle
changes, verify that the process exits and that the session disappears from
the active-sessions panel.

## Release process

Merging a pull request into `main` runs the release workflow. It tests the
project, increments the patch component of the latest stable `vMAJOR.MINOR.PATCH`
tag (starting at `v0.1.0`), builds supported platform archives, creates
checksums, and publishes a GitHub Release.

Do not create development or pre-release tags manually. Release tags are
created by GitHub Actions after a successful merge to `main`.

## License

By contributing, you agree that your contribution is provided under the GNU
General Public License, version 3 or later, as described in [LICENSE](LICENSE).
