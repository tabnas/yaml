#!/usr/bin/env bash
# Fetch the official YAML Test Suite (https://github.com/yaml/yaml-test-suite)
# into test/yaml-test-suite/ so both the TypeScript and Go conformance runners
# can exercise the parser against it.
#
# The corpus is owned by the YAML Test Suite authors and is NOT redistributed
# as part of this repository (see .gitignore) — it is fetched on demand, at a
# PINNED commit, so the conformance numbers are reproducible.
#
# Pinned to the `data` branch (the flat one-directory-per-case layout), which
# is the layout both runners read:
#
#   repo:   https://github.com/yaml/yaml-test-suite
#   branch: data
#   commit: 6ad3d2c62885d82fc349026c136ef560838fdf3d
#
# 402 cases: 279 with in.json (value-checked), 94 with an `error` marker
# (must-fail), 29 with neither (parse-only; no expected value published).
#
# Usage:
#   scripts/fetch-yaml-test-suite.sh            # default location
#   scripts/fetch-yaml-test-suite.sh /some/dir  # custom destination
#
# Idempotent: if the destination is already checked out at the pinned commit
# it does nothing. If it is at a different commit it re-checks-out.
set -euo pipefail

REPO="https://github.com/yaml/yaml-test-suite"
COMMIT="6ad3d2c62885d82fc349026c136ef560838fdf3d"

REPO_ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
DEST="${1:-$REPO_ROOT/test/yaml-test-suite}"

if [ -d "$DEST/.git" ]; then
  have="$(git -C "$DEST" rev-parse HEAD 2>/dev/null || echo none)"
  if [ "$have" = "$COMMIT" ]; then
    echo "yaml-test-suite already at $COMMIT in $DEST"
    exit 0
  fi
  echo "yaml-test-suite in $DEST is at $have; re-fetching $COMMIT ..."
  git -C "$DEST" fetch --depth 1 origin "$COMMIT"
  git -C "$DEST" checkout --detach --force "$COMMIT"
  git -C "$DEST" clean -qfdx
  echo "yaml-test-suite now at $COMMIT in $DEST"
  exit 0
fi

# A non-git directory here means a stale hand-copied corpus (this repo used to
# vendor one). Replace it, so the pinned commit is the only source of truth.
if [ -d "$DEST" ]; then
  echo "Replacing non-git corpus at $DEST ..."
  rm -rf "$DEST"
fi

echo "Fetching $REPO @ $COMMIT into $DEST ..."
mkdir -p "$DEST"
git -C "$DEST" init -q
git -C "$DEST" remote add origin "$REPO"
git -C "$DEST" fetch -q --depth 1 origin "$COMMIT"
git -C "$DEST" checkout -q --detach "$COMMIT"

cases=$(find "$DEST" -name 'in.yaml' -not -path '*/.git/*' | wc -l | tr -d ' ')
withjson=$(find "$DEST" -name 'in.json' -not -path '*/.git/*' | wc -l | tr -d ' ')
errors=$(find "$DEST" -name 'error' -not -path '*/.git/*' | wc -l | tr -d ' ')
echo "Done. $cases cases ($withjson with in.json, $errors must-fail) at $COMMIT."
