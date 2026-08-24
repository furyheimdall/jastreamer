#!/usr/bin/env bash
set -euo pipefail
tag=${1:?tag required}
[[ ${GITHUB_REPOSITORY:-furyheimdall/jastreamer} == furyheimdall/jastreamer ]] || { echo CANONICAL_REPOSITORY_REQUIRED >&2; exit 65; }
[[ ${GITHUB_REF_TYPE:-tag} == tag ]] || { echo PROTECTED_CONTROL_TAG_REQUIRED >&2; exit 65; }
[[ $tag =~ ^control-v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]] || { echo PROTECTED_CONTROL_TAG_REQUIRED >&2; exit 65; }
version=${tag#control-v}
declared=$(tr -d '[:space:]' < apps/control/VERSION)
pubspec=$(awk '/^version:/{print $2; exit}' apps/control/pubspec.yaml)
[[ $version == "$declared" && $version == "$pubspec" ]] || { echo TAG_VERSION_MISMATCH >&2; exit 65; }
