#!/usr/bin/env bash
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
version=8.0.3
sha256=6136812ea6d4e68bdba27e33c2a94382711cdf4f8602ffef056ff792bd6f9818
platform="$(go env GOOS)-$(go env GOARCH)"
host_platform="$(go env GOHOSTOS)-$(go env GOHOSTARCH)"
[[ "$platform" == "$host_platform" ]] || { echo "Build natively on the target OS/architecture; cross-compilation is not configured" >&2; exit 1; }
case "$platform" in darwin-*|linux-*|windows-amd64) ;; *) echo "Supported build hosts: macOS, Linux and Windows amd64" >&2; exit 1 ;; esac
prefix="$root/.deps/ffmpeg-$version-$platform"
build="$root/.deps/build-$version-$platform-v2"
archive="$root/.deps/downloads/ffmpeg-$version.tar.xz"
source_dir="$root/.deps/source-$version-$platform"
if [[ -f "$prefix/.complete-v2" ]]; then exit 0; fi
for tool in curl tar make pkg-config; do command -v "$tool" >/dev/null || { echo "Build tool missing: $tool" >&2; exit 1; }; done
mkdir -p "$root/.deps/downloads" "$build" "$prefix"
if [[ ! -f "$archive" ]]; then
    curl --fail --location --retry 3 "https://ffmpeg.org/releases/ffmpeg-$version.tar.xz" -o "$archive.part"
    mv "$archive.part" "$archive"
fi
if command -v sha256sum >/dev/null; then actual="$(sha256sum "$archive" | cut -d ' ' -f 1)"; else actual="$(shasum -a 256 "$archive" | cut -d ' ' -f 1)"; fi
[[ "$actual" == "$sha256" ]] || { echo "FFmpeg archive checksum mismatch: $archive" >&2; exit 1; }
if [[ ! -d "$source_dir" ]]; then mkdir -p "$source_dir"; tar -xJf "$archive" --strip-components=1 -C "$source_dir"; fi
args=(
    --prefix="$prefix" --enable-static --disable-shared --enable-pic
    --disable-programs --disable-doc --disable-debug --disable-autodetect
    --disable-everything --disable-avfilter --disable-swresample
    --disable-x86asm --enable-network
    --enable-protocol=file,tcp,udp,rtp
    --enable-demuxer=rtsp,mjpeg,mov --enable-muxer=mp4
    --enable-decoder=mjpeg,h264 --enable-encoder=mpeg4,mjpeg
    --enable-parser=mjpeg,mpeg4video,h264 --enable-swscale --enable-avdevice
)
if [[ "$platform" == linux-* ]]; then args+=(--enable-indev=v4l2); fi
if [[ "$platform" == windows-* ]]; then
    prefix="$(cygpath -m "$prefix")"
    args[0]="--prefix=$prefix"
    args+=(--target-os=mingw32 --arch=x86_64 --cc=gcc --extra-ldflags=-static)
fi
cd "$build"
# The recipe is versioned with the build directory and completion marker.
# Keep successful configure results when compilation is interrupted.
if [[ ! -f .configured-v2 ]]; then
    "$source_dir/configure" "${args[@]}"
    touch .configured-v2
fi
if [[ "$platform" == windows-* ]]; then
    # Native MinGW make does not understand MSYS drive paths in include/vpath.
    native_source="../$(basename "$source_dir")"
    printf 'include %s/Makefile\n' "$native_source" > Makefile
    sed -i "s|^SRC_PATH=.*|SRC_PATH=$native_source|" ffbuild/config.mak
fi
jobs="${DICAM_BUILD_JOBS:-4}"
make -j"$jobs"
# Avoid duplicating the full header list in verbose progress commands on Windows.
make V=1 install
cp "$source_dir/COPYING.LGPLv2.1" "$prefix/"
printf '%s\n' "$version $platform $sha256" > "$prefix/.complete-v2"
echo "Static FFmpeg libraries: $prefix"
