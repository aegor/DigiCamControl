"""Package existing, checksummed desktop binaries and Android APK locally.

Run from any directory: python go/scripts/package-release.py --version v0.2.1
No build, camera connection, Git mutation, or network access is performed.
"""
import argparse
import gzip
import hashlib
import io
from pathlib import Path
import re
import struct
import tarfile
import zipfile

ROOT = Path(__file__).resolve().parents[2]
FFMPEG = "ffmpeg-8.0.3.tar.xz"
FFMPEG_SHA = "6136812ea6d4e68bdba27e33c2a94382711cdf4f8602ffef056ff792bd6f9818"


def digest(path):
    with path.open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def ffmpeg_source():
    # Only raw, checksummed sources are portable; never reuse compiled libraries.
    candidates = sorted((ROOT / "go/.deps").glob(f"*/downloads/{FFMPEG}"))
    candidates += sorted((ROOT / ".cache").glob(f"*/native/downloads/{FFMPEG}"))
    candidates.append(ROOT / "go/.deps/downloads" / FFMPEG)  # older release caches
    for candidate in candidates:
        if candidate.is_file() and digest(candidate) == FFMPEG_SHA:
            return candidate
    raise ValueError("No verified FFmpeg source archive; build the Go backend first")


def source_files():
    # Explicit source roots prevent caches, local SDK paths, keys and AppleDouble
    # metadata from entering a release, including when Git files are untracked.
    files = [ROOT / "LICENSE"]
    for name in ("Makefile", "README.md", "go.mod", ".gitignore"):
        files.append(ROOT / "go" / name)
    for pattern in ("*.go", "*.c", "*.h"):
        files.extend((ROOT / "go").glob(pattern))
    for folder in ("go/scripts", "go/web", "go/third_party", "android/app/src",
                   "android/gradle/wrapper", "android/scripts"):
        files.extend(p for p in (ROOT / folder).rglob("*") if p.is_file())
    for name in ("README.md", ".gitignore", "build.gradle", "settings.gradle",
                 "gradle.properties", "gradlew", "gradlew.bat", "app/build.gradle"):
        files.append(ROOT / "android" / name)
    return sorted({p for p in files if not any(
        part.startswith("._") or part in {"__pycache__", ".DS_Store"}
        for part in p.relative_to(ROOT).parts
    )})


def tar_gz(path, entries):
    temporary = path.with_name(path.name + ".part")
    with temporary.open("wb") as raw, gzip.GzipFile(filename="", fileobj=raw, mode="wb", mtime=0) as gz:
        with tarfile.open(fileobj=gz, mode="w") as archive:
            for name, data, mode in sorted(entries):
                entry = tarfile.TarInfo(name)
                entry.size, entry.mode = len(data), mode
                archive.addfile(entry, io.BytesIO(data))
    temporary.replace(path)


def zip_archive(path, entries):
    temporary = path.with_name(path.name + ".part")
    with zipfile.ZipFile(temporary, "w", compression=zipfile.ZIP_DEFLATED) as archive:
        for name, data, mode in sorted(entries):
            entry = zipfile.ZipInfo(name)
            entry.create_system = 3
            entry.external_attr = (0o100000 | mode) << 16
            entry.compress_type = zipfile.ZIP_DEFLATED
            archive.writestr(entry, data)
    temporary.replace(path)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--version", default="v0.2.1")
    args = parser.parse_args()
    if not re.fullmatch(r"v\d+\.\d+\.\d+", args.version):
        parser.error("version must have the form v0.2.1")
    tag = args.version
    stem = "dicam-controller-" + tag
    output = ROOT / "dist" / tag
    windows = output / (stem + "-windows-amd64.exe")
    macos = output / (stem + "-macos-arm64")
    apk = output / (stem + "-android-universal-debug.apk")
    sums = {}
    for line in (output / "SHA256SUMS").read_text().splitlines():
        checksum, name = line.split("  ", 1)
        sums[name] = checksum
    for binary in (windows, macos, apk):
        if binary.name not in sums or digest(binary) != sums[binary.name]:
            raise ValueError(f"Missing or mismatched input checksum: {binary.name}")
    pe = windows.read_bytes()
    offset = struct.unpack_from("<I", pe, 0x3c)[0]
    if pe[:2] != b"MZ" or pe[offset:offset+6] != b"PE\0\0\x64\x86":
        raise ValueError("Windows input is not PE AMD64")
    macho = macos.read_bytes()
    if struct.unpack_from("<II", macho) != (0xFEEDFACF, 0x0100000C):
        raise ValueError("macOS input is not Mach-O ARM64")
    with zipfile.ZipFile(apk) as archive:
        for name in ("assets/licenses/NOTICE.txt", "assets/licenses/LGPL-2.1.txt"):
            if not archive.read(name):
                raise ValueError(f"APK license missing: {name}")
    ffmpeg = ffmpeg_source()

    rebuilding = f"""# Пересборка и перелинковка {tag}

Архив исходников — снимок Go/Android на момент упаковки. Кеши, SDK, ключи,
локальная конфигурация и файлы AppleDouble не включены. Gradle Wrapper включён.
Windows-бинарник собран из этих Go-исходников (Go 1.27.3, GCC 14.2.0).
macOS-бинарник перенесён с Mac (Go 1.24.5); исходники в пакете уже содержат
добавленную Windows-поддержку. Побайтовая воспроизводимость старой Mac-сборки
не заявляется. APK перенесён без пересборки, версия 0.2.1/versionCode 3, debug.

## Desktop

Распакуйте `source/{stem}-source.tar.gz` в каталог без пробелов.
Возьмите отдельный файл `{FFMPEG}` рядом с desktop-пакетами в каталоге релиза
и скопируйте его в `go/.deps/<GOOS>-<GOARCH>/downloads/` внутри исходников
(например, `darwin-arm64` или `windows-amd64`).
SHA-256 FFmpeg: `{FFMPEG_SHA}`.

На macOS/Linux установите Go >= 1.24, C-компилятор, make, pkg-config, curl,
tar/xz и sha256sum/shasum. Из каталога `go` выполните `make build`.
На Windows установите Go >= 1.24, Git Bash и MinGW-w64/w64devkit; из корня
исходников в Git Bash выполните `bash go/scripts/windows.sh build`.
Подробности настройки инструментов и проверки: `go/README.md`.

FFmpeg собирается в `go/.deps/<GOOS>-<GOARCH>/ffmpeg-8.0.3-<fingerprint>`.
Отпечаток учитывает компилятор, SDK, флаги и скрипты сборки; точный prefix
выводит `make ffmpeg`.
Чтобы использовать изменённый FFmpeg, соберите его статические библиотеки и
заголовки в этот prefix, сохранив pkg-config metadata. Затем из `go` выполните:
`bash scripts/go.sh build -a -o dicam .` (Windows: `-o dicam.exe`, тот же PATH
Go/GCC/Git Bash, что задаёт `scripts/windows.sh`). Ключ `-a` нужен, поскольку
кеш Go не отслеживает изменения внешних архивов. Маркер `.complete-v2` должен
сохраняться после исходной сборки. Контроллер можно полностью пересобрать и
перелинковать с изменёнными библиотеками; исходники самого контроллера — MIT.

## Android

Инструкции и версии JDK/SDK: `android/README.md`. Из `android` выполните
`./gradlew assembleDebug testDebugUnitTest lintDebug` (Windows: `gradlew.bat`).
LibVLC 3.7.7 динамически включён в APK. Чтобы заменить его, измените зависимость
в `android/app/build.gradle` на свой AAR. Лицензия и ссылки на исходники:
`android/app/src/main/assets/licenses/`. APK не требует desktop-сервера.
"""
    source_name = stem + "-source.tar.gz"
    source_entries = []
    for path in source_files():
        name = path.relative_to(ROOT).as_posix()
        mode = 0o755 if path.suffix == ".sh" or path.name == "gradlew" else 0o644
        source_entries.append((stem + "-source/" + name, path.read_bytes(), mode))
    source_entries.append((stem + "-source/REBUILD.md", rebuilding.encode(), 0o644))
    tar_gz(output / source_name, source_entries)
    (output / FFMPEG).write_bytes(ffmpeg.read_bytes())

    packaged = []
    for platform, binary in (("windows-amd64", windows), ("macos-arm64", macos)):
        command = (f'.\\{binary.name} --bind= --port 5001' if platform.startswith("windows")
                   else f'./{binary.name} --bind "" --port 5001')
        guide = f"""# DiCam Controller {tag} — {platform}

1. Распакуйте весь архив в отдельную папку.
2. Подключите компьютер к Wi-Fi камеры и остановите другие контроллеры камеры.
3. Откройте терминал в распакованной папке и выполните:

```
{command}
```

4. Откройте http://127.0.0.1:5001/ в браузере. Record/Stop управляют записью
на SD-карту камеры. Перед выходом остановите запись кнопкой Stop; затем Ctrl+C.

Готовому приложению не нужны Go, Python или установленный FFmpeg.
Камера по умолчанию: 192.168.1.1. `--bind=` использует маршрутизацию ОС.
Для выбора локального интерфейса укажите `--bind <его IPv4>`: адрес должен
быть назначен этому компьютеру. Без этого аргумента применяется 192.168.1.10.
`--host`/`--port` задают HTTP-сервер; `--bind` относится к PTP/ICMP.
Если нужны все параметры, запустите приложение с `--help`.

Windows-пакет предназначен для x64; macOS-пакет — для Apple Silicon ARM64.
macOS использует системный ping. USB/V4L2 поддерживается только на Linux.
Windows: пройдены 24 Go-теста с race detector, vet, UI-тесты, запуск с пустым
PATH и завершение по Ctrl+C. Физическая камера на Windows пока не проверена.
Mac-бинарник перенесён без пересборки; запуск на Windows не проверялся.

Лицензии: LICENSE (контроллер, MIT), COPYING.LGPLv2.1 и FFMPEG-NOTICE.md.
Снимок исходников контроллера находится в source/. Архив исходников FFmpeg
`{FFMPEG}` поставляется отдельно рядом с desktop-пакетами в каталоге релиза.
Для запуска он не нужен: библиотеки FFmpeg статически встроены в приложение.
Инструкции замены FFmpeg и перелинковки — REBUILD.md.
"""
        folder = stem + "-" + platform
        entries = [
            (folder + "/" + binary.name, binary.read_bytes(), 0o755),
            (folder + "/README.md", guide.encode(), 0o644),
            (folder + "/REBUILD.md", rebuilding.encode(), 0o644),
            (folder + "/LICENSE", (ROOT / "LICENSE").read_bytes(), 0o644),
            (folder + "/COPYING.LGPLv2.1", (ROOT / "go/third_party/ffmpeg/COPYING.LGPLv2.1").read_bytes(), 0o644),
            (folder + "/FFMPEG-NOTICE.md", (ROOT / "go/third_party/ffmpeg/NOTICE.md").read_bytes(), 0o644),
            (folder + "/source/" + source_name, (output / source_name).read_bytes(), 0o644),
        ]
        package = output / (folder + (".zip" if platform.startswith("windows") else ".tar.gz"))
        (zip_archive if platform.startswith("windows") else tar_gz)(package, entries)
        packaged.append(package)

    notes = f"""# DiCam Controller {tag} — локальный релиз

Desktop-пакеты для запуска:
- `{stem}-windows-amd64.zip` — Windows x64.
- `{stem}-macos-arm64.tar.gz` — macOS Apple Silicon.

Распакуйте пакет и следуйте README.md внутри. Каждый desktop-пакет содержит
исполняемый файл, инструкции, лицензии и исходники контроллера для пересборки.
Архив исходников FFmpeg поставляется отдельно и для запуска не нужен.
Рядом сохранены отдельные EXE/macOS-бинарник, `{source_name}` и `{FFMPEG}`.

Android: `{apk.name}` — самостоятельное приложение Digma DiCam Contoller,
тестовая debug-подпись; desktop-сервер не требуется. Инструкции и исходники Android
в архиве исходников. Лицензии LibVLC включены в APK и исходники.

SHA256SUMS содержит суммы всех файлов локальной поставки, кроме самого SHA256SUMS.
Windows: 24 теста с -race, vet, UI, пустой PATH и Ctrl+C прошли. Физическая камера
на Windows не проверена. Mac/APK перенесены без пересборки. Android остаётся debug.
На некоторых Xiaomi ранее наблюдались Wi-Fi-разрывы; при зависании камеры
остановите контроллер, не продолжайте циклы реконнекта.

{tag} — обозначение локальной поставки, не встроенная версия Go и не Git-тег.
На GitHub ничего не опубликовано; Git-коммиты и теги не создавались.
"""
    (output / "README.md").write_text(notes, encoding="utf-8", newline="\n")
    assets = [windows, macos, apk, *packaged, output / source_name, output / FFMPEG, output / "README.md"]
    (output / "SHA256SUMS").write_text("".join(
        f"{digest(path)}  {path.name}\n" for path in sorted(assets)
    ), encoding="ascii", newline="\n")
    for path in assets:
        print(f"{path.name}: {path.stat().st_size} bytes")


if __name__ == "__main__":
    main()
