#!/usr/bin/env bash
# Written by mise-check: don't edit it. After upgrading, mise run mise-check:init writes it again.
# A Claude Code cloud session starts on a bare Linux machine with only the repo cloned. Install mise, then every tool
# the repo pins (Go from go.mod), then run each folder's own setup task if it has one, so `mise run …` works straight
# away. Does nothing on your own machine.
# Usage: session-start.sh [folder …], folders relative to the repo; with none, the repo's own folder.
set -euo pipefail
[ "${CLAUDE_CODE_REMOTE:-}" = true ] || exit 0
export PATH="$HOME/.local/bin:$PATH"
# A cloud session's GitHub token can see only the session's own repos. mise sends it when it downloads tools'
# files from other repos (usage's SKILL.md from jdx/usage), and GitHub answers 404, so the install fails.
# The installs below need no token, so hide it from them.
unset GITHUB_TOKEN GH_TOKEN
command -v mise > /dev/null || curl -fsSL https://mise.run | sh
cd "$CLAUDE_PROJECT_DIR"
[ $# -gt 0 ] || set -- .
for dir in "$@"; do
  (
    cd "$dir"
    mise trust -q --all # mise.toml, and .mise/config.toml with mise-check's pin
    mise install
    if mise tasks info setup > /dev/null 2>&1; then mise run setup; fi
  )
done
# Keep mise on PATH for the rest of the session's commands.
[ -n "${CLAUDE_ENV_FILE:-}" ] && echo "export PATH=\"$HOME/.local/bin:\$PATH\"" >> "$CLAUDE_ENV_FILE"
echo "mise $(mise --version | cut -d' ' -f1) and every pinned tool installed"
