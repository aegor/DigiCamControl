#!/usr/bin/env bash
# LibVLC and its dependencies are packaged into the APK by Gradle.
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
exec "$root/gradlew" -p "$root" assembleDebug "$@"
