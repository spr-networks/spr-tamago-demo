#!/usr/bin/env bash
set -euo pipefail

export GENERATE_SOURCEMAP=false
craco build
node ./move-inline-scripts.js

# CRACO inlines all JavaScript and CSS. Assert that the kernel will not need
# to serve any secondary frontend asset.
if grep -Eq '<(script|link)[^>]+(src|href)=' build/index.html; then
  echo "frontend bundle contains an external asset reference" >&2
  exit 1
fi
