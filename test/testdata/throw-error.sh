#!/usr/bin/env bash
set -euo pipefail

input=$(cat)

if ! echo "$input" | grep -q "THROW ERROR"; then
  echo "[]"
  exit 1
fi

echo '[{"rule_id":"throw-error","severity":"error","message":"Object contains THROW ERROR"}]'
