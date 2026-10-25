#!/usr/bin/env bash
# Run from Git Bash with MinGW-w64 (w64devkit) installed.
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
toolchain="${DICAM_MINGW_BIN:-/c/w64devkit/bin}"
export PATH="$root/.deps/toolchain/go/bin:/usr/bin:$toolchain:$PATH"
export CC=gcc
cd "$root"
exec make SHELL="$(cygpath -m /usr/bin/bash)" "$@"
