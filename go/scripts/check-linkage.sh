#!/usr/bin/env bash
set -euo pipefail
binary="${1:-./dicam}"
case "$(uname -s)" in
    Darwin) dependencies="$(otool -L "$binary")" ;;
    Linux) dependencies="$(ldd "$binary")" ;;
    MINGW*|MSYS*)
        dependencies="$(objdump -p "$binary" | grep 'DLL Name:')"
        unexpected="$(printf '%s\n' "$dependencies" | sed 's/.*DLL Name: //' | grep -Eiv '^(KERNEL32|msvcrt|ucrtbase|WS2_32|ADVAPI32|SHELL32|USER32|OLE32|BCRYPT|CRYPT32|SECUR32|IPHLPAPI|WINMM|NTDLL)\.dll$|^api-ms-win-.*\.dll$' || true)"
        [[ -z "$unexpected" ]] || { echo "Unexpected DLL dependencies: $unexpected" >&2; exit 1; }
        ;;
    *) echo 'Linkage verification supports macOS, Linux and Windows'  >&2; exit 1 ;;
esac
printf '%s\n' "$dependencies"
if printf '%s\n' "$dependencies" | grep -E 'lib(avcodec|avformat|avdevice|avutil|swscale|swresample|avfilter)[. -]'; then
    echo 'ERROR: binary dynamically links FFmpeg' >&2
    exit 1
fi
echo 'OK: no FFmpeg shared-library dependencies'
