#!/usr/bin/env bash
set -euo pipefail

arch=${1:?architecture required}
output=${2:?output directory required}
include_gh=${3:-false}
case "$arch" in
  amd64)
    jq_sha=5942c9b0934e510ee61eb3e30273f1b3fe2590df93933a93d7c58b81d19c8ff5
    gh_sha=62544b0f3759bbf1155c0ac3d75838b5fe23d66dfb75cf8368f84fff8f82b93e
    ;;
  arm64)
    jq_sha=4dd2d8a0661df0b22f1bb9a1f9830f06b6f3b8f7d91211a1ef5d7c4f06a8b4a5
    gh_sha=a77f6d709c5100cda8e9bbb8d8b7143120121233d9102ba2f2bc254134db18dc
    ;;
  *) printf 'unsupported architecture: %s\n' "$arch" >&2; exit 64 ;;
esac

mkdir -p "$output"
curl -fsSL "https://github.com/jqlang/jq/releases/download/jq-1.7.1/jq-linux-$arch" -o "$output/jq"
printf '%s  %s\n' "$jq_sha" "$output/jq" | sha256sum --check --status
chmod 0755 "$output/jq"
"$output/jq" --version | grep -Fx 'jq-1.7.1'

if [[ $include_gh == true ]]; then
  archive="$output/gh.tar.gz"
  curl -fsSL "https://github.com/cli/cli/releases/download/v2.76.2/gh_2.76.2_linux_$arch.tar.gz" -o "$archive"
  printf '%s  %s\n' "$gh_sha" "$archive" | sha256sum --check --status
  tar -xzf "$archive" -C "$output" --strip-components=2 "gh_2.76.2_linux_$arch/bin/gh"
  rm "$archive"
  "$output/gh" --version | grep -F 'gh version 2.76.2 '
fi
