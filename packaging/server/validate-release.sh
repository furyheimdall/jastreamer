#!/usr/bin/env bash
set -euo pipefail
tag=${1:?tag required}
root=$(cd "$(dirname "$0")/../.." && pwd)
repo=${GITHUB_REPOSITORY:-}
[[ $repo == furyheimdall/jastreamer ]] || { echo CANONICAL_REPOSITORY_REQUIRED >&2; exit 65; }
[[ ${GITHUB_REF_TYPE:-} == tag ]] || { echo TAG_ONLY >&2; exit 65; }
[[ $tag =~ ^server-v([0-9]+\.[0-9]+\.[0-9]+)$ ]] || { echo PROTECTED_SERVER_TAG_REQUIRED >&2; exit 65; }
version=$(<"$root/apps/server/VERSION")
[[ ${BASH_REMATCH[1]} == "$version" ]] || { echo TAG_VERSION_MISMATCH >&2; exit 65; }
printf 'validated server %s in %s\n' "$version" "$repo"
