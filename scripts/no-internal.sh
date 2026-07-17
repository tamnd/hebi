#!/usr/bin/env bash
# Fail if any internal/ directory exists. hebi does not use Go's internal/
# visibility mechanism; packages stay importable and the boundaries are kept
# by discipline and review, not by the compiler hiding them.
set -euo pipefail

found=$(find . -type d -name internal -not -path './.git/*' || true)
if [ -n "$found" ]; then
  echo "internal/ directories are not allowed:"
  echo "$found"
  exit 1
fi
echo "no internal/ directories, good"
