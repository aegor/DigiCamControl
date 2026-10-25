# FFmpeg

DiCam statically links an unmodified, minimal build of FFmpeg 8.0.3.

- Upstream source: https://ffmpeg.org/releases/ffmpeg-8.0.3.tar.xz
- SHA-256: `6136812ea6d4e68bdba27e33c2a94382711cdf4f8602ffef056ff792bd6f9818`
- License: GNU LGPL version 2.1 or later; see `COPYING.LGPLv2.1`.
- Configuration and build recipe: `scripts/build-ffmpeg.sh`.
- GPL, nonfree and external codec components are not enabled.

This software is based in part on the work of the Independent JPEG Group.
FFmpeg's IJG-derived files have not been modified by this project.

The controller's own source remains under the repository's MIT license.
When distributing a binary, include this notice, the FFmpeg license, the
corresponding FFmpeg source archive and controller source/build scripts so
recipients can rebuild and relink the application with a modified FFmpeg.
The standalone Go build retains the archive under
`.deps/<GOOS>-<GOARCH>/downloads/ffmpeg-8.0.3.tar.xz`. Electron builds use
`.cache/<target>/native/downloads/` at the repository root and include the archive
and Go sources under `backend/source/` in the packaged application.
To relink from the included Go sources, run `make ffmpeg`, then
`./scripts/go.sh build -a -o dicam .` (use `dicam.exe` on Windows).
`scripts/build-env.sh` selects a platform- and toolchain-specific library prefix;
`DICAM_NATIVE_CACHE` can override the cache root.
