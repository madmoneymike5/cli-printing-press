#!/bin/sh
# Portable SoC checker runner, shared by .githooks/* and usable from CI.
#
# Resolves the checker pinned by checker_commit in soc-policy.toml (the same
# pin CI downloads in .github/workflows/soc-check.yml) and runs it. The
# pinned script is cached under .git/soc-check/<commit>/ and only downloaded
# when the pin changes, so hooks stay fast after first use.
set -eu

mode="${1:-changed}"
shift 2>/dev/null || true
case "$mode" in (changed|all|no-growth|explain) ;; (*)
  echo "soc-check: unknown mode: $mode" >&2
  exit 2
  ;;
esac

root="$(git rev-parse --show-toplevel)"
if ! commit="$(python3 -c 'import tomllib; print(tomllib.load(open("'"$root"'/soc-policy.toml", "rb"))["checker_commit"])' 2>/dev/null)"; then
  echo "soc-check: cannot read checker_commit from $root/soc-policy.toml (repository not enrolled?)" >&2
  exit 2
fi
case "$commit" in (""|*[!0-9a-fA-F]*)
  echo "soc-check: invalid checker_commit in soc-policy.toml" >&2
  exit 2
  ;;
esac

gitdir="$(git rev-parse --git-common-dir)"
case "$gitdir" in /*) ;; *) gitdir="$root/$gitdir" ;; esac
cache="$gitdir/soc-check/$commit/soc_check.py"
if [ ! -f "$cache" ]; then
  mkdir -p "$(dirname "$cache")"
  curl --fail --location --silent --show-error \
    "https://raw.githubusercontent.com/madmoneymike5/soc-check/$commit/soc_check.py" \
    --output "$cache"
fi

exec python3 "$cache" --root "$root" --policy "$root/soc-policy.toml" --mode "$mode" "$@"
