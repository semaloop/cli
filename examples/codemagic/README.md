# Codemagic

This example shows how to build an iOS app on [Codemagic] and push the resulting `.ipa` to Semaloop for continuous testing.

The resulting workflow will build a signed `.ipa` using `xcode-project build-ipa`. It will install our CLI using Homebrew, and run `semaloop build push` to upload the artifact to semaloop.

## Setup

1. Create an environment variable group named `semaloop`, and add a secure `SEMALOOP_API_KEY` variable.
2. Generate the API key from the "Settings" section of Semaloop.
3. Copy [`codemagic.yaml`](./codemagic.yaml) into the root of your repository
   and update the `bundle_identifier`, `XCODE_WORKSPACE`, and `XCODE_SCHEME`
   values to match your project.
4. Connect the repository to Codemagic and trigger a build.

## Existing workflows

If you have an existing workflow, the key parts are the following entries in scripts:

```yaml
- name: Install Semaloop CLI
  script: |
    brew tap semaloop/tap
    brew install semaloop
- name: Push build to Semaloop
  script: |
    IPA_PATH=$(find build/ios/ipa -name "*.ipa" | head -n 1)
    semaloop build push "$IPA_PATH"
```

[Codemagic]: https://codemagic.io
