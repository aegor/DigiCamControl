#!/usr/bin/env bash
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
source "$root/scripts/build-env.sh"
if [[ ! -f "$prefix/.complete-v2" ]]; then
    echo 'Static FFmpeg is not built. Run: make ffmpeg' >&2
    exit 1
fi
export CGO_ENABLED=1
export PKG_CONFIG="$root/scripts/pkg-config-static.sh"
if [[ "$(go env GOOS)" == windows ]]; then
    pkg_tool="$(command -v pkg-config)"
    export DICAM_PKG_CONFIG="$(cygpath -m "${pkg_tool%.exe}.exe")"
    gcc -O2 -static "$root/scripts/pkg-config-static-windows.c" -o "$native_cache/pkg-config-static-$fingerprint.exe"
    export PKG_CONFIG="\"$(cygpath -m "$native_cache/pkg-config-static-$fingerprint.exe")\""
    export CGO_LDFLAGS="${CGO_LDFLAGS:-} -static"
    prefix="$(cygpath -m "$prefix")"
fi
export PKG_CONFIG_LIBDIR="$prefix/lib/pkgconfig"
unset PKG_CONFIG_PATH
export GOCACHE="${GOCACHE:-$native_cache/go-build-$fingerprint}"
export GOMODCACHE="${GOMODCACHE:-$native_cache/go-mod}"
if [[ "$(go env GOOS)" == windows ]]; then
    export GOCACHE="$(cygpath -m "$GOCACHE")"
    export GOMODCACHE="$(cygpath -m "$GOMODCACHE")"
fi
cd "$root"
exec go "$@"
