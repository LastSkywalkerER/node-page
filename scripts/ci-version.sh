#!/usr/bin/env bash
#
# ci-version.sh — print the version string CI stamps into the binary / uses for
# release tags. Shared by docker.yml and release.yml so the Docker image and the
# native packages built from the SAME commit get IDENTICAL versions.
#
#   • on a v* tag ref            → the clean release version WITHOUT the leading
#                                  "v" (e.g. 0.8.2)
#   • otherwise (a main push)    → the next-patch beta:
#                                  <maj>.<min>.<patch+1>-beta.<N>
#     where the base is the latest STABLE tag and N = commits since it. So beta
#     sorts after the last release and before the next one
#     (0.8.2 < 0.8.3-beta.14 < 0.8.3) and bumps on every push.
#
# Output has NO leading "v" — callers that need a tag/asset name add it. Requires
# full git history + tags (actions/checkout with fetch-depth: 0).
set -euo pipefail

ref="${GITHUB_REF:-}"
if [[ "$ref" == refs/tags/v* ]]; then
  printf '%s\n' "${GITHUB_REF_NAME#v}"
  exit 0
fi

# Latest STABLE tag only — the per-push beta tags we publish must NOT become the
# base, or the version would drift upward (0.8.3-beta.N → 0.8.4-beta.N → …) every
# beta. Version-sort all vX.Y.Z tags, drop any with a "-" (prereleases), take the
# top.
latest="$(git tag --list 'v[0-9]*' --sort=-v:refname | grep -v -- '-' | head -n1 || true)"
[ -n "$latest" ] || latest="v0.0.0"

base="${latest#v}"
IFS='.' read -r major minor patch <<<"${base%%-*}"
major="${major:-0}"; minor="${minor:-0}"; patch="${patch:-0}"

count="$(git rev-list "${latest}..HEAD" --count 2>/dev/null || git rev-list HEAD --count)"

printf '%s\n' "${major}.${minor}.$((patch + 1))-beta.${count}"
