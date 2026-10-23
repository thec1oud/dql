#!/usr/bin/env bash
# Re-sign every commit above <base> on the given branches and push them.
# List branches bottom to top; for a single branch, list just that one.
#   scripts/resign.sh origin/dev feature/shape-presets
#   scripts/resign.sh origin/dev feature/a feature/b feature/c
set -euo pipefail

base=$1; shift
top=${!#}

git fetch origin
for b in "$@"; do
  git checkout -q "$b"
  git reset -q --hard "origin/$b"
done

git checkout -q "$top"
# --force-rebase rewrites every commit, --gpg-sign signs each one with the
# owner's configured key, --update-refs moves the lower branches of a stack.
git rebase --force-rebase --gpg-sign --update-refs "$base"

git log --format='%h %G? %an  %s' "$base".."$top"
git push --force-with-lease origin "$@"
