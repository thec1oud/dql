#!/usr/bin/env bash
# Merge a branch into its target with a signed merge commit.
#   scripts/merge.sh feature/shape-presets dev
set -euo pipefail
branch=$1; target=$2

git fetch origin
git checkout -q "$target"
git merge -q --ff-only "origin/$target"
git merge --no-ff --gpg-sign -m "Merge branch '$branch' into $target" "origin/$branch"
git push origin "$target"
