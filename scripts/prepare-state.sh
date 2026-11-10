#!/usr/bin/env bash
set -euo pipefail
repo="${GITHUB_REPOSITORY:-$(node -p "require('./monitor.config.json').repository")}"
if [[ ! "$repo" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]]; then
  echo 'Invalid GitHub repository' >&2
  exit 1
fi
mkdir -p .state
if [[ ! -d .state/.git ]]; then
  git -C .state init -b monitor-state
  git -C .state remote add origin "https://github.com/$repo.git"
fi
expected="https://github.com/$repo.git"
[[ "$(git -C .state remote get-url origin)" == "$expected" ]]
set +e
git -C .state -c credential.helper='!gh auth git-credential' ls-remote --exit-code --heads origin monitor-state >/dev/null
status=$?
set -e
if [[ "$status" == 0 ]]; then
  git -C .state -c credential.helper='!gh auth git-credential' fetch --depth=1 origin monitor-state
  # Do not overwrite uncommitted checkpoint files on a developer machine.
  if [[ -n "$(git -C .state status --porcelain)" ]]; then
    echo 'Local state has uncommitted changes; commit it before restoring remote state.' >&2
    exit 1
  fi
  git -C .state checkout -B monitor-state FETCH_HEAD
  test -s .state/state/manifest.json
elif [[ "$status" != 2 ]]; then
  echo 'Cannot read state branch; refusing to silently reset the baseline.' >&2
  exit "$status"
fi
