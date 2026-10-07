#!/usr/bin/env bash
set -euo pipefail

project_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
release_dir="${HERDR_SPACE_BIN_DIR:-$HOME/.local/lib/herdr-space}"
install -d -m 700 "$release_dir"
staging="$(mktemp "$release_dir/.herdr-space.XXXXXX")"
previous=""
trap 'rm -f -- "$staging" "${previous:-}"' EXIT

install -m 700 "$project_root/bin/herdr-space" "$staging"
install -m 644 "$project_root/bin/LICENSE" "$project_root/bin/NOTICE.md" "$project_root/bin/THIRD_PARTY_LICENSES.md" "$release_dir/"
sync "$staging"
if [[ -f "$release_dir/herdr-space" ]]; then
  previous="$(mktemp "$release_dir/.herdr-space-previous.XXXXXX")"
  install -m 700 "$release_dir/herdr-space" "$previous"
  sync "$previous"
  mv -f -- "$previous" "$release_dir/herdr-space.previous"
fi
mv -f -- "$staging" "$release_dir/herdr-space"
sync "$release_dir"
printf 'Installed HERDR Space binary in %s\n' "$release_dir"
