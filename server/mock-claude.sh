#!/usr/bin/env bash
# Mock Claude Code for exercising the SHRIMP pipeline without a real install.
# Reads the prompt from stdin and streams a few lines to stdout, honoring
# SIGTERM so cancellation can be tested.
#
#   SHRIMP_CLAUDE_CMD=$(pwd)/mock-claude.sh go run .
set -euo pipefail

trap 'echo "[mock] received SIGTERM, exiting"; exit 143' TERM

prompt="$(cat)"
echo "[mock] task=${SHRIMP_TASK_ID:-?} agent=${SHRIMP_AGENT:-?}"
echo "[mock] prompt: ${prompt}"
for i in 1 2 3; do
  echo "[mock] working step ${i}/3 ..."
  sleep 1
done
echo "[mock] done: produced result for '${prompt}'"
