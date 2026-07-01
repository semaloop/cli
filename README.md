# Semaloop CLI

This is the home of `semaloop`, a way of interacting with [Semaloop] from the command line.

> [!NOTE]
> The Semaloop CLI is currently in early access. If you're interested in using it, please get in touch with us.

## Installation

Download the latest binary for your platform from the [releases page](https://github.com/semaloop/cli/releases).

On macOS, you can also install via Homebrew:

```
brew tap semaloop/tap
brew install semaloop
```

## Usage

First, you'll need to authenticate using your Semaloop API key. You can specify this using the `SEMALOOP_API_KEY` environment variable, or you can run:

```
semaloop auth login
```

For help on using `semaloop`, run:

```
semaloop --help
```

## Features

The Semaloop CLI supports:

- `semaloop auth`: Authenticate with the Semaloop API.
- `semaloop build push`: Push an iOS build artifact (`.app` or `.ipa`) for testing.

### Continuous integration

Push builds from your CI pipeline with their git context so Semaloop can test each one and report the results back to your repository as a status check on the commit or pull request:

```
semaloop build push path/to/YourApp.app \
  --git-repo "owner/repo" \
  --git-commit "<commit-sha>" \
  --git-ref "refs/heads/your-branch"
```

`--git-repo`, `--git-commit`, and `--git-ref` tell Semaloop which commit the build came from, so the status check lands on the right place. `--git-repo` must be a repository you've connected in the Semaloop dashboard. Pass all three or none.

#### GitHub Actions example

```yaml
- run: brew install semaloop/tap/semaloop
- name: Push build to Semaloop
  env:
    SEMALOOP_API_KEY: ${{ secrets.SEMALOOP_API_KEY }}
  run: |
    semaloop build push "$PWD/build/YourApp.app" \
      --git-repo "${{ github.repository }}" \
      --git-commit "${{ github.event.pull_request.head.sha || github.sha }}" \
      --git-ref "${{ github.head_ref && format('refs/heads/{0}', github.head_ref) || github.ref }}"
```

This works for both pushes and pull requests — on a PR it stamps the PR's head commit so the check appears on the pull request.

## Contributing

Information on contributing can be found in [CONTRIBUTING.md](./CONTRIBUTING.md).

[semaloop]: https://semaloop.com
