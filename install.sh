#!/usr/bin/env bash
set -euo pipefail

fail() { printf 'syfon install: %s\n' "$*" >&2; exit 1; }

show_help() {
  cat <<EOF
Usage: $0 [version] [install_path]
       $0 --version <version> --dest <install_path>
       $0 --list | --help

Install the latest release into \$HOME/.local/bin by default.
EOF
}

api_get() {
  local headers=(-H 'Accept: application/vnd.github+json')
  if [[ -n "${GITHUB_TOKEN:-}" ]]; then
    headers+=(-H "Authorization: Bearer $GITHUB_TOKEN")
  fi
  curl --fail --silent --show-error --location --retry 3 "${headers[@]}" \
    "https://api.github.com/repos/calypr/syfon/releases$1"
}

release_tags() {
  sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p'
}

version=""
dest="${HOME}/.local/bin"
dest_set=false
while (($#)); do
  case "$1" in
    --help|-h) show_help; exit 0 ;;
    --list|-l) api_get '?per_page=20' | release_tags; exit 0 ;;
    --version|-v)
      [[ $# -ge 2 && -n "$2" && "$2" != -* ]] || fail "$1 requires a version"
      version="$2"
      shift 2
      ;;
    --dest|-d)
      [[ $# -ge 2 && -n "$2" && "$2" != -* ]] || fail "$1 requires a directory"
      dest="$2"
      dest_set=true
      shift 2
      ;;
    -*) fail "unknown option: $1" ;;
    *)
      if [[ -z "$version" ]]; then
        version="$1"
      elif [[ "$dest_set" == false ]]; then
        dest="$1"
        dest_set=true
      else
        fail "too many arguments"
      fi
      shift
      ;;
  esac
done

case "$(uname -s)" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) fail 'supported operating systems are Linux and macOS' ;;
esac
case "$(uname -m)" in
  x86_64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) fail 'supported architectures are amd64 and arm64' ;;
esac

if [[ -z "$version" ]]; then
  version="$(api_get /latest | release_tags)"
fi
[[ "$version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9.-]+)?$ ]] || fail "invalid release version: $version"

archive="syfon-${os}-${arch}-${version}.tar.gz"
checksums="syfon-${version}-checksums.txt"
release_url="https://github.com/calypr/syfon/releases/download/${version}"
work_dir="$(mktemp -d "${TMPDIR:-/tmp}/syfon-install.XXXXXX")"
candidate=""
trap 'rm -rf "$work_dir"; if [[ -n "$candidate" ]]; then rm -f "$candidate"; fi' EXIT

printf 'Downloading Syfon %s for %s/%s...\n' "$version" "$os" "$arch"
curl --fail --silent --show-error --location --retry 3 \
  "$release_url/$archive" -o "$work_dir/$archive"
curl --fail --silent --show-error --location --retry 3 \
  "$release_url/$checksums" -o "$work_dir/$checksums"

expected="$(awk -v name="$archive" '$2 == name {print $1}' "$work_dir/$checksums")"
[[ "$expected" =~ ^[[:xdigit:]]{64}$ ]] || fail "missing or invalid checksum for $archive"
if command -v sha256sum >/dev/null 2>&1; then
  actual="$(sha256sum "$work_dir/$archive")"
else
  actual="$(shasum -a 256 "$work_dir/$archive")"
fi
[[ "${actual%% *}" == "$expected" ]] || fail "checksum verification failed for $archive"

tar -xzf "$work_dir/$archive" -C "$work_dir" syfon
[[ -f "$work_dir/syfon" && ! -L "$work_dir/syfon" ]] || fail 'archive does not contain a regular syfon binary'
mkdir -p "$dest"
[[ ! -d "$dest/syfon" ]] || fail "$dest/syfon is a directory"
candidate="$(mktemp "$dest/.syfon-install.XXXXXX")"
command install -m 755 "$work_dir/syfon" "$candidate"
version_output="$("$candidate" version)"
mv -f "$candidate" "$dest/syfon"
printf 'Installed %s/syfon\n%s\n' "$dest" "$version_output"
