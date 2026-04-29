# Contributing

## Pull Requests

Pull Request titles must follow the follow the [Conventional Commits](https://www.conventionalcommits.org). When merged to `main`, the title will end up as the commit title, and this is used when releasing new versions of the CLI.

For this reason, please keep PRs focussed, and stick to one feature or fix per pull request.

As a quick introduction:

- `feat:` triggers a minor version bump (e.g. `0.1.0` → `0.2.0`)
- `fix:` triggers a patch version bump (e.g. `0.1.0` → `0.1.1`)
- Breaking changes are marked with `!` after the type (e.g. `feat!:`) and trigger a major version bump
- Titles should be lowercase and in the imperative mood (e.g. `feat: add login command`).

## Releasing

Releases are fully automated. Merging to `main` triggers the release pipeline. We use `semantic-release` to analyse commits since the last release and determine the next version automatically.

A GitHub release is then created with generated release notes. Binaries for Linux, macOS, and Windows are then built by GoReleaser and attached to the release. No manual version bumping or tagging is required.

## Configuration

You can point to a different API URL by setting the `SEMALOOP_API_URL` environment variable. This is only useful if you are able to have the Semaloop API running locally.

## Client generation

To regenerate the `internal/api` package, you'll need access to Semaloop's OpenAPI spec. You'll only have this if you work at Semaloop.

If you do, you can regenerate the types by running:

```
mise run generate-client -- path/to/public-api-schema.json
```
