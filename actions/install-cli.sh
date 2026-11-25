#!/usr/bin/env bash
# Installs the semaloop CLI for the actions in this directory, and adds it to
# the job's PATH. SEMALOOP_VERSION is a version such as "1.6.0", or "latest".
# SEMALOOP_ACTION names the action in error messages.
set -euo pipefail

case "$RUNNER_OS" in
  Linux) os=Linux ;;
  macOS) os=Darwin ;;
  *)
    echo "::error::The semaloop/cli ${SEMALOOP_ACTION} action does not support ${RUNNER_OS} runners yet. Use Linux or macOS." >&2
    exit 1
    ;;
esac

case "$RUNNER_ARCH" in
  X64) arch=x86_64 ;;
  ARM64) arch=arm64 ;;
  *)
    echo "::error::The semaloop/cli ${SEMALOOP_ACTION} action does not support ${RUNNER_ARCH} runners yet." >&2
    exit 1
    ;;
esac

if [ "$SEMALOOP_VERSION" = "latest" ]; then
  # Resolve the redirect from /releases/latest to /releases/tag/<tag> rather
  # than calling the GitHub API, which is subject to a much lower unauthenticated
  # rate limit on shared GitHub-hosted runners.
  tag=$(curl -sSfL -o /dev/null -w '%{url_effective}' "https://github.com/semaloop/cli/releases/latest")
  tag="${tag##*/}"
else
  tag="v${SEMALOOP_VERSION#v}"
fi
version="${tag#v}"
archive="cli_${os}_${arch}.tar.gz"
checksums="cli_${version}_checksums.txt"
base_url="https://github.com/semaloop/cli/releases/download/${tag}"

install_dir="${RUNNER_TEMP}/semaloop-cli"
mkdir -p "$install_dir"
curl -sSfL "${base_url}/${archive}" -o "$install_dir/cli.tar.gz"
curl -sSfL "${base_url}/${checksums}" -o "$install_dir/checksums.txt"

expected=$(grep " ${archive}\$" "$install_dir/checksums.txt" | awk '{print $1}')
if [ -z "$expected" ]; then
  echo "::error::Could not find a checksum for ${archive} in ${checksums}." >&2
  exit 1
fi
actual=$(shasum -a 256 "$install_dir/cli.tar.gz" | awk '{print $1}')
if [ "$expected" != "$actual" ]; then
  echo "::error::Checksum mismatch for ${archive}: expected $expected, got $actual." >&2
  exit 1
fi

tar -xzf "$install_dir/cli.tar.gz" -C "$install_dir" semaloop
chmod +x "$install_dir/semaloop"
echo "$install_dir" >> "$GITHUB_PATH"
