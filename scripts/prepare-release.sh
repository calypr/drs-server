#!/usr/bin/env bash
set -euo pipefail

git config user.name  "github-actions[bot]"
git config user.email "github-actions[bot]@users.noreply.github.com"

if [[ "$EVENT_NAME" == 'push' ]]; then
  {
    echo "root_tag=$REF_NAME"
    echo "apigen_tag="
    echo "client_tag="
    echo "sha=$HEAD_SHA"
  } >> "$GITHUB_OUTPUT"
  echo "Using existing root tag: $REF_NAME"
  exit 0
fi

merged_pr_number="$(gh api \
  -H 'Accept: application/vnd.github+json' \
  "/repos/${GITHUB_REPOSITORY}/commits/${HEAD_SHA}/pulls" \
  --jq '.[] | select(.base.ref == "development" and .merged_at != null) | .number' \
  | head -n1 || true)"
if [[ -z "$merged_pr_number" ]]; then
  {
    echo "root_tag="
    echo "apigen_tag="
    echo "client_tag="
    echo "sha=$HEAD_SHA"
  } >> "$GITHUB_OUTPUT"
  echo "Commit $HEAD_SHA is not associated with a merged PR into development; skipping auto release."
  exit 0
fi
echo "Preparing auto release for merged PR #${merged_pr_number} at ${HEAD_SHA}"

write_outputs() {
  {
    echo "root_tag=$root_tag"
    echo "apigen_tag=$apigen_tag"
    echo "client_tag=$client_tag"
    echo "sha=$release_sha"
  } >> "$GITHUB_OUTPUT"
}

publish_tags() {
  local tag
  for tag in "$apigen_tag" "$client_tag" "$root_tag"; do
    [[ -n "$tag" ]] || continue
    if git rev-parse -q --verify "refs/tags/$tag" >/dev/null; then
      [[ "$(git rev-parse "$tag^{commit}")" == "$release_sha" ]] || {
        echo "Release tag $tag points at another commit" >&2
        return 1
      }
    else
      git tag "$tag" "$release_sha"
    fi
    git push origin "refs/tags/$tag"
  done
  write_outputs
}

release_trailer() {
  git show -s --format="%(trailers:key=Syfon-Release-$1,valueonly)" "$release_sha"
}

# A sibling tag may be the only published reference after an interrupted run.
release_sha="$(git log --all --format='%H%x09%(trailers:key=Syfon-Release-Source,valueonly)' \
  | awk -v source="$HEAD_SHA" '$2 == source && !found {print $1; found=1}')"
if [[ -n "$release_sha" ]]; then
  [[ "$(git rev-parse "$release_sha^")" == "$HEAD_SHA" ]] || {
    echo 'Prepared release has an unexpected source commit' >&2
    exit 1
  }
  root_tag="$(release_trailer Root)"
  client_tag="$(release_trailer Client)"
  apigen_tag="$(release_trailer Apigen)"
  [[ "$client_tag" != none ]] || client_tag=""
  [[ "$apigen_tag" != none ]] || apigen_tag=""
  [[ -n "$root_tag" ]] || { echo 'Prepared release is missing its root tag' >&2; exit 1; }
  publish_tags
  echo "Resuming prepared release $root_tag at $release_sha"
  exit 0
fi

latest_tag_for() {
  local pattern="$1"
  local regex="$2"
  git tag --list "$pattern" --sort=-version:refname | grep -E "$regex" | head -n1 || true
}

bump_patch() {
  local latest="$1"
  if [[ -z "$latest" ]]; then
    echo "v0.1.0"
    return 0
  fi

  local base major minor patch
  base="${latest#*/}"
  base="${base#v}"
  IFS=. read -r major minor patch <<<"$base"
  patch=$((10#$patch + 1))
  echo "v${major}.${minor}.${patch}"
}

bump_breaking() {
  local base="${1:-v0.0.0}" major minor patch
  base="${base#v}"
  IFS=. read -r major minor patch <<<"$base"
  if ((10#$major == 0)); then
    printf 'v0.%d.0\n' "$((10#$minor + 1))"
  else
    printf 'v%d.0.0\n' "$((10#$major + 1))"
  fi
}

changed_files_from() {
  local base="$1"
  shift
  if [[ -n "$base" ]]; then
    git diff --name-only "${base}..${HEAD_SHA}" -- "$@"
  else
    git diff --name-only "${HEAD_SHA}^..${HEAD_SHA}" -- "$@"
  fi
}

root_latest=$(latest_tag_for 'v[0-9]*' '^v[0-9]+\.[0-9]+\.[0-9]+$')
apigen_latest=$(latest_tag_for 'apigen/v[0-9]*' '^apigen/v[0-9]+\.[0-9]+\.[0-9]+$')
client_latest=$(latest_tag_for 'client/v[0-9]*' '^client/v[0-9]+\.[0-9]+\.[0-9]+$')

root_existing=$(git tag --points-at "$HEAD_SHA" | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' | head -1 || true)
if [[ -n "$root_existing" ]]; then
  root_tag="$root_existing"
  apigen_tag=$(git tag --points-at "$HEAD_SHA" | grep -E '^apigen/v[0-9]+\.[0-9]+\.[0-9]+$' | head -1 || true)
  client_tag=$(git tag --points-at "$HEAD_SHA" | grep -E '^client/v[0-9]+\.[0-9]+\.[0-9]+$' | head -1 || true)
  release_sha="$HEAD_SHA"
  publish_tags
  exit 0
fi

if [[ -n "$root_latest" ]] && git merge-base --is-ancestor "$HEAD_SHA" "$root_latest"; then
  root_tag=""
  apigen_tag=""
  client_tag=""
  release_sha="$HEAD_SHA"
  write_outputs
  echo "Source $HEAD_SHA is already included in newer release $root_latest"
  exit 0
fi

root_files=$(changed_files_from "$root_latest")
apigen_files=$(changed_files_from "$apigen_latest" apigen)
client_files=$(changed_files_from "$client_latest" client)

root_base_changed=false
apigen_base_changed=false
client_base_changed=false

if printf '%s\n' "$root_files" \
  | grep -Ev '^(apigen/|client/)' \
  | grep -Ev '(_test\.go$|^$)' \
  | grep -Eq '(^|/).+\.go$|^go\.(mod|sum)$|^Makefile$|^Dockerfile$|^install\.sh$|^\.github/workflows/release\.yaml$|^scripts/prepare-release\.sh$'; then
  root_base_changed=true
fi

if printf '%s\n' "$apigen_files" \
  | grep -Ev '(_test\.go$|^$)' \
  | grep -Eq '^apigen/.+\.go$|^apigen/go\.(mod|sum)$'; then
  apigen_base_changed=true
fi

if printf '%s\n' "$client_files" \
  | grep -Ev '(_test\.go$|^$)' \
  | grep -Eq '^client/.+\.go$|^client/go\.(mod|sum)$'; then
  client_base_changed=true
fi

apigen_existing=$(git tag --points-at "$HEAD_SHA" | grep -E '^apigen/v[0-9]+\.[0-9]+\.[0-9]+$' | head -1 || true)
client_existing=$(git tag --points-at "$HEAD_SHA" | grep -E '^client/v[0-9]+\.[0-9]+\.[0-9]+$' | head -1 || true)

if [[ -n "$apigen_existing" ]]; then
  apigen_tag="$apigen_existing"
elif [[ "$apigen_base_changed" == true ]]; then
  apigen_tag="apigen/$(bump_patch "$apigen_latest")"
else
  apigen_tag=""
fi

if [[ -n "$client_existing" ]]; then
  client_tag="$client_existing"
elif [[ "$client_base_changed" == true || -n "$apigen_tag" ]]; then
  client_tag="client/$(bump_patch "$client_latest")"
else
  client_tag=""
fi

if [[ "$root_base_changed" == true || -n "$client_tag" || -n "$apigen_tag" ]]; then
  history_range="${root_latest:+${root_latest}..}${HEAD_SHA}"
  if git log --format=%B "$history_range" | grep '^BREAKING CHANGE:' >/dev/null; then
    root_tag="$(bump_breaking "$root_latest")"
  else
    root_tag="$(bump_patch "$root_latest")"
  fi
else
  root_tag=""
fi

if [[ -z "$root_tag" ]]; then
  {
    echo "root_tag="
    echo "apigen_tag=$apigen_tag"
    echo "client_tag=$client_tag"
    echo "sha=$HEAD_SHA"
  } >> "$GITHUB_OUTPUT"
  echo "No release needed."
  exit 0
fi

[[ "$(git rev-parse HEAD)" == "$HEAD_SHA" ]] || {
  echo 'Release preparation must run from the source commit' >&2
  exit 1
}
git diff --exit-code
git diff --cached --exit-code
if [[ -n "$apigen_tag" ]]; then
  go mod edit -require="github.com/calypr/syfon/apigen@${apigen_tag#apigen/}"
  go -C client mod edit -require="github.com/calypr/syfon/apigen@${apigen_tag#apigen/}"
fi
if [[ -n "$client_tag" ]]; then
  go mod edit -require="github.com/calypr/syfon/client@${client_tag#client/}"
fi
git add go.mod client/go.mod
git commit --allow-empty -m "chore: prepare release $root_tag" \
  -m "Syfon-Release-Source: $HEAD_SHA
Syfon-Release-Root: $root_tag
Syfon-Release-Client: ${client_tag:-none}
Syfon-Release-Apigen: ${apigen_tag:-none}"
release_sha="$(git rev-parse HEAD)"

publish_tags
printf 'Prepared %s at %s\n' "$root_tag" "$release_sha"
