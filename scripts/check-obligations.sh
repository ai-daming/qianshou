#!/bin/sh
# Obligations manifest check: every ENFORCED entry must cite tests that
# (1) exist in the package test binaries and (2) pass when selected.
# This makes the falsification matrix mechanically binding: deleting or
# renaming a cited test fails CI instead of silently leaving an ENFORCED
# claim behind.
set -eu
root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root/apps/control"
manifest="internal/ghfacts/obligations.json"

listed=$(go test -list '.*' ./internal/ghfacts ./internal/scope 2>/dev/null | grep '^Test' | sort -u)

names=$(jq -r '.[] | select(.status == "ENFORCED") | .tests[]' "$manifest" | sort -u)
[ -n "$names" ] || { echo "check-obligations: manifest cites no ENFORCED tests" >&2; exit 1; }

status=0
for name in $names; do
  if ! echo "$listed" | grep -qx "$name"; then
    echo "check-obligations: ENFORCED entry cites unknown test: $name" >&2
    status=1
  fi
done
[ "$status" -eq 0 ] || exit 1

pattern=$(printf '%s\n' "$names" | sed 's/^/^/; s/$/$/' | paste -sd'|' -)
go test -count=1 -run "$pattern" ./internal/ghfacts ./internal/scope
echo "check-obligations: all ENFORCED citations exist and pass"
