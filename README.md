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

### Shell completion

`semaloop completion <shell>` prints a completion script for `bash`, `zsh` or `fish`. Homebrew installs completions automatically. Otherwise, add the line for your shell to its startup file:

```
# ~/.bashrc
source <(semaloop completion bash)

# ~/.zshrc (after compinit)
source <(semaloop completion zsh)

# ~/.config/fish/config.fish
semaloop completion fish | source
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

Allows you to push a build artifact for testing: an `.app` or `.ipa` for iOS, or a standalone `.apk` for Android.

App bundles (`.aab`), XAPK or APK-set containers, and split APKs aren't accepted.

You can specify `--git-repo`, `--git-commit` and `--git-ref`, which allows Semaloop to report the results back as a status check on the commit or pull request. You must have connected your repository to Semaloop via our web dashboard for this to work. All three arguments must be specified.

### `semaloop test`

Lets you keep your tests as Markdown files in your app's repository, one file per test in `.semaloop/tests` (subdirectories are fine). The frontmatter holds the test's settings and the body is the test, written in natural language:

```markdown
---
identifier: sign-in
name: Sign in with email
folder: Smoke
draft: false
tags: [smoke, regression]
secrets: [test-account]
disabled_capabilities: [audio_input]
---

Open the app and sign in with the test account. Check that the home screen shows the account's first name.
```

`identifier` and `name` are required. Semaloop recognises a test by its `identifier`, so renaming the file is safe but changing `identifier` creates a new test. `secrets` lists secrets by slug. Leaving it out gives a new test the app's default secrets and keeps an existing test's secrets as they are. Leaving out `tags` or `disabled_capabilities` means the test has none.

- `semaloop test check` checks the files offline, without an API key, and reports every error with its file and line.
- `semaloop test sync` checks the files, then creates, updates and archives tests in Semaloop to match them. Deleting a file archives its test. A sync that would archive every test in code is refused unless you pass `--allow-archive-all`.

Both commands read `.semaloop/tests` by default, or the directory you pass.

## GitHub Actions

Some of the `semaloop cli` commands come with a pre-packaged GitHub Action that you can drop in to your GitHub workflows.

### `semaloop/cli/actions/build-push`

Allows a build to be pushed to Semaloop. Arguments like `--git-repo` are deduced automatically. See [`actions/build-push`](./actions/build-push/action.yml) for the full list of inputs and outputs.

#### iOS example

```yaml
- uses: semaloop/cli/actions/build-push@v1
  with:
    path: build/YourApp.app
    api-key: ${{ secrets.SEMALOOP_API_KEY }}
```

#### Android example

```yaml
- uses: semaloop/cli/actions/build-push@v1
  with:
    path: app/build/outputs/apk/release/app-release.apk
    api-key: ${{ secrets.SEMALOOP_API_KEY }}
```

## Contributing

Information on contributing can be found in [CONTRIBUTING.md](./CONTRIBUTING.md).

[semaloop]: https://semaloop.com
