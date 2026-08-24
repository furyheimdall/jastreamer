#!/usr/bin/env bash
set -euo pipefail
output=${1:?output directory required}
mkdir -p "$output"
curl -fsSL https://github.com/jqlang/jq/releases/download/jq-1.7.1/jq-linux-amd64 -o "$output/jq"
printf '%s  %s\n' 5942c9b0934e510ee61eb3e30273f1b3fe2590df93933a93d7c58b81d19c8ff5 "$output/jq" | sha256sum --check --status
curl -fsSL https://github.com/cli/cli/releases/download/v2.76.2/gh_2.76.2_linux_amd64.tar.gz -o "$output/gh.tar.gz"
printf '%s  %s\n' 62544b0f3759bbf1155c0ac3d75838b5fe23d66dfb75cf8368f84fff8f82b93e "$output/gh.tar.gz" | sha256sum --check --status
tar -xzf "$output/gh.tar.gz" -C "$output" --strip-components=2 gh_2.76.2_linux_amd64/bin/gh
rm "$output/gh.tar.gz"
chmod 0755 "$output/jq" "$output/gh"
test "$("$output/jq" --version)" = jq-1.7.1
"$output/gh" --version | grep -F 'gh version 2.76.2 '
