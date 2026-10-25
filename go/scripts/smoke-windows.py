"""Optional Windows smoke check: localhost HTTP, empty PATH, real Ctrl+C.

Uses only Python's standard library; Python is not needed by the application.
Run: python go/scripts/smoke-windows.py go/dicam.exe
"""
import ctypes
import json
import os
from pathlib import Path
import socket
import subprocess
import sys
import tempfile
import time
import urllib.request


def main():
    if sys.platform != "win32":
        raise SystemExit("This check requires Windows")
    binary = Path(sys.argv[1]).resolve(strict=True)
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        port = listener.getsockname()[1]
    startup = subprocess.STARTUPINFO()
    startup.dwFlags |= subprocess.STARTF_USESHOWWINDOW
    startup.wShowWindow = 0
    env = dict(os.environ, PATH="")
    subprocess.run([str(binary), "--help"], env=env, check=True, capture_output=True)
    with tempfile.TemporaryFile() as output:
        child = subprocess.Popen(
            [str(binary), "--no-browser", "--camera", "127.0.0.1", "--bind", "", "--port", str(port)],
            env=env, stdout=output, stderr=output, startupinfo=startup,
            creationflags=subprocess.CREATE_NEW_CONSOLE,
        )
        try:
            opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
            deadline = time.monotonic() + 10
            while True:
                try:
                    with opener.open(f"http://127.0.0.1:{port}/api/status", timeout=1) as response:
                        status = json.load(response)
                    with opener.open(f"http://127.0.0.1:{port}/", timeout=1) as response:
                        assert response.status == 200
                    break
                except OSError:
                    if child.poll() is not None or time.monotonic() >= deadline:
                        raise
                    time.sleep(0.1)
            print("Local HTTP and embedded UI OK; status:", status)
            kernel = ctypes.WinDLL("kernel32", use_last_error=True)
            # Attach only to the dedicated hidden child console. No camera or
            # other running controller receives this console event.
            kernel.FreeConsole()
            if not kernel.AttachConsole(child.pid):
                raise ctypes.WinError(ctypes.get_last_error())
            kernel.SetConsoleCtrlHandler(None, True)
            if not kernel.GenerateConsoleCtrlEvent(0, 0):
                raise ctypes.WinError(ctypes.get_last_error())
            code = child.wait(timeout=8)
            kernel.FreeConsole()
            if code != 0:
                raise RuntimeError(f"Ctrl+C exit code: {code}")
            print("Empty PATH startup and Ctrl+C graceful exit OK")
        finally:
            if child.poll() is None:
                child.kill()
                child.wait()
            output.seek(0)
            print(output.read().decode("utf-8", errors="replace"))


if __name__ == "__main__":
    main()
