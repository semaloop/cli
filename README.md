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

The following sections describe the commands supported by the `semaloop` CLI. For a full description of each command, run `semaloop --help`.

### `semaloop auth`

Allows you to authenticate with Semaloop using an API key, and remove any existing stored credentials.

### `semaloop build push`

Allows you to push an iOS build artifact (`.app` or `.ipa`) for testing.

You can specify `--git-repo`, `--git-commit` and `--git-ref`, which allows Semaloop to report the results back as a status check on the commit or pull request. You must have connected your repository to Semaloop via our web dashboard for this to work. All three arguments must be specified.

## GitHub Actions

Some of the `semaloop cli` commands come with a pre-packaged GitHub Action that you can drop in to your GitHub workflows.

## `semaloop/cli/actions/build-push`

Allows a build to be pushed to Semaloop. Arguments like `--git-repo` are deduced automatically. See [`actions/build-push`](./actions/build-push/action.yml) for the full list of inputs and outputs.

### Example

```yaml
- uses: semaloop/cli/actions/build-push@v1
  with:
    path: build/YourApp.app
    api-key: ${{ secrets.SEMALOOP_API_KEY }}
```

## Contributing

Information on contributing can be found in [CONTRIBUTING.md](./CONTRIBUTING.md).

[semaloop]: https://semaloop.com
