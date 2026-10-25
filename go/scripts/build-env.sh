#!/usr/bin/env bash
# Sourced by build-ffmpeg.sh and go.sh. Keep their toolchain selection identical.
version=8.0.3
platform="$(go env GOOS)-$(go env GOARCH)"
host_platform="$(go env GOHOSTOS)-$(go env GOHOSTARCH)"
[[ "$platform" == "$host_platform" ]] || { echo 'Build natively on the target OS/architecture; cross-compilation is not configured' >&2; exit 1; }
case "$platform" in darwin-*|linux-*|windows-amd64) ;; *) echo 'Supported build hosts: macOS, Linux and Windows amd64' >&2; exit 1 ;; esac
native_cache="${DICAM_NATIVE_CACHE:-$root/.deps/$platform}"
if [[ "$platform" == windows-* ]]; then native_cache="$(cygpath -u "$native_cache")"; fi
mkdir -p "$native_cache"
native_cache="$(cd "$native_cache" && pwd)"
compiler="${CC:-cc}"
compiler_path="$(command -v "$compiler")"
# Changing compiler, SDK, compilation flags or recipes cannot reuse old libraries.
fingerprint_input="$(
    printf '%s\n' "$platform" "$compiler_path" "${CFLAGS:-}" "${CPPFLAGS:-}" "${LDFLAGS:-}" "${CGO_CFLAGS:-}" "${CGO_LDFLAGS:-}" "${SDKROOT:-}"
    "$compiler" --version
    if [[ "$platform" == darwin-* ]]; then xcrun --show-sdk-path; xcrun --show-sdk-version; fi
    cat "$root/scripts/build-env.sh" "$root/scripts/build-ffmpeg.sh"
)"
if command -v sha256sum >/dev/null; then
    fingerprint="$(printf '%s' "$fingerprint_input" | sha256sum | cut -c 1-20)"
else
    fingerprint="$(printf '%s' "$fingerprint_input" | shasum -a 256 | cut -c 1-20)"
fi
prefix="$native_cache/ffmpeg-$version-$fingerprint"
build="$native_cache/build-$version-$fingerprint"
archive="$native_cache/downloads/ffmpeg-$version.tar.xz"
source_dir="$native_cache/source-$version-$fingerprint"
