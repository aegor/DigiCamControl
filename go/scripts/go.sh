#!/usr/bin/env bash
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
prefix="$root/.deps/ffmpeg-8.0.3-$(go env GOOS)-$(go env GOARCH)"
if [[ ! -f "$prefix/.complete-v2" ]]; then
    echo 'Static FFmpeg is not built. Run: make ffmpeg' >&2
    exit 1
fi
export CGO_ENABLED=1
export PKG_CONFIG="$root/scripts/pkg-config-static.sh"
if [[ "$(go env GOOS)" == windows ]]; then
    pkg_tool="$(command -v pkg-config)"
    export DICAM_PKG_CONFIG="$(cygpath -m "${pkg_tool%.exe}.exe")"
    gcc -O2 -static "$root/scripts/pkg-config-static-windows.c" -o "$root/.deps/pkg-config-static.exe"
    export PKG_CONFIG="\"$(cygpath -m "$root/.deps/pkg-config-static.exe")\""
    export CGO_LDFLAGS="${CGO_LDFLAGS:-} -static"
    prefix="$(cygpath -m "$prefix")"
fi
export PKG_CONFIG_LIBDIR="$prefix/lib/pkgconfig"
unset PKG_CONFIG_PATH
export GOCACHE="${GOCACHE:-$root/.deps/go-cache-$(go env GOOS)-$(go env GOARCH)}"
if [[ "$(go env GOOS)" == windows ]]; then export GOCACHE="$(cygpath -m "$GOCACHE")"; fi
cd "$root"
exec go "$@"
