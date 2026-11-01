#!/usr/bin/env bash
# Print the next Sagalore release version (no leading v).
# Reuses a semver tag already on HEAD. Otherwise bumps the latest vX.Y.Z:
# major on BREAKING CHANGE / feat!: / fix!:, minor on feat:, else patch.
# First release is 0.1.0.
set -euo pipefail

if [[ "${GITHUB_ACTIONS:-}" != "true" ]]; then
  git fetch --tags --force origin >/dev/null 2>&1 || true
fi

semver_tag() {
  grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' || true
}

existing="$(git tag --points-at HEAD | semver_tag | sort -V | tail -n 1)"
if [[ -n "${existing}" ]]; then
  echo "${existing#v}"
  exit 0
fi

latest="$(git tag -l 'v*' | semver_tag | sort -V | tail -n 1)"
if [[ -z "${latest}" ]]; then
  echo "0.1.0"
  exit 0
fi

log="$(git log "${latest}..HEAD" --format='%s%n%b')"
bump=patch
if printf '%s\n' "${log}" | grep -Eq 'feat!:|fix!:|BREAKING CHANGE'; then
  bump=major
elif printf '%s\n' "${log}" | grep -Eq '^feat(\(|:)'; then
  bump=minor
fi

ver="${latest#v}"
IFS=. read -r major minor patch <<<"${ver}"
case "${bump}" in
  major)
    major=$((major + 1))
    minor=0
    patch=0
    ;;
  minor)
    minor=$((minor + 1))
    patch=0
    ;;
  patch) patch=$((patch + 1)) ;;
esac
echo "${major}.${minor}.${patch}"
