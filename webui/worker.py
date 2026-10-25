"""Per-camera worker: owns a Camera (PTP control) + an RTSP frame grabber
(OpenCV VideoCapture). Runs a background thread that keeps the latest
JPEG-encoded preview frame available for the Flask MJPEG endpoint.

Mirrors the design of the parent ClaudesWorld/webcam/app.py CameraWorker
state machine but targets PTP-IP + RTSP instead of V4L2.
"""
from __future__ import annotations

import os
import subprocess
import threading
import time
from typing import Optional

import cv2

import sys
sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
from larkfly import Camera, PtpError, TransportError, types as t


# Larkfly RTSP preview URL. Resolution 720x400 @ 5 Mbps is the low-rate
# preview format the camera advertises; the camera uses MJPEG (not H264)
# for this stream so latency is low and OpenCV decodes it directly.
RTSP_URL_TMPL = "rtsp://{host}/MJPG?W=720&H=400&Q=50&BR=5000000"

PREVIEW_JPEG_QUALITY = 70
PREVIEW_MAX_WIDTH = 1280
CONNECTION_CHECK_INTERVAL = 1.0
RTSP_TIMEOUT_MS = 2000


class LarkflyWorker:
    """Background-managed connection to one camera.

    Reachability is checked independently of blocking video reads. Failed
    connections are retried indefinitely, including after a camera power cycle.
    """

    STARTING     = 'starting'
    RUNNING      = 'running'
    RECONNECTING = 'reconnecting'
    DEAD         = 'dead'

    def __init__(self, slot: int, host: str,
                 bind: Optional[str] = None,
                 name: Optional[str] = None,
                 max_retries: int = 5):
        self.slot = slot
        self.host = host
        self.bind = bind
        self.name = name or f"cam{slot}"
        self.max_retries = max_retries  # retained for caller compatibility; retries are unlimited

        self.state = self.STARTING
        self.error: Optional[str] = None
        self.model = ''
        self.fw = ''
        self.last_frame_jpeg: Optional[bytes] = None
        self.last_frame_ts: float = 0.0
        self.fps_actual = 0.0
        self.mode: int = 0  # current D604 value

        self._frame_lock = threading.Lock()
        self._stop = threading.Event()
        self.reachable: Optional[bool] = None
        self.stream_generation = 0
        self._online = threading.Event()
        self._reconnect = threading.Event()
        self._monitor_thread = threading.Thread(target=self._monitor_connection,
                                                daemon=True, name=f"CameraPing-{slot}")
        self._cmd_lock = threading.Lock()   # serialize PTP commands
        self._camera: Optional[Camera] = None
        self._thread = threading.Thread(target=self._run, daemon=True,
                                         name=f"LarkflyWorker-{slot}")

    # ---- lifecycle -----------------------------------------------
    def start(self):
        self._monitor_thread.start()
        self._thread.start()

    def stop(self):
        self._stop.set()
        if self._monitor_thread.is_alive():
            self._monitor_thread.join(timeout=2.0)
        if self._thread.is_alive():
            self._thread.join(timeout=3.0)
        # The streaming thread closes its own connection in finally. Do not
        # close sockets concurrently with a PTP command or reconnection.
        if not self._thread.is_alive():
            self._close_camera()

    # ---- PTP control API (called from Flask handlers) ------------
    def start_recording(self):
        with self._cmd_lock:
            if not self._camera:
                raise RuntimeError(f"slot {self.slot}: not connected")
            self._camera.start_recording()
            self.mode = t.MODE_VIDEO_ON

    def stop_recording(self):
        with self._cmd_lock:
            if not self._camera:
                raise RuntimeError(f"slot {self.slot}: not connected")
            self._camera.stop_recording()
            self.mode = t.MODE_VIDEO_OFF

    def is_recording(self) -> bool:
        return self.mode == t.MODE_VIDEO_ON

    def get_status(self) -> dict:
        return {
            'slot': self.slot,
            'host': self.host,
            'name': self.name,
            'state': self.state,
            'reachable': self.reachable,
            'stream_generation': self.stream_generation,
            'error': self.error,
            'model': self.model,
            'firmware': self.fw,
            'mode': self.mode,
            'mode_name': t.MODE_NAMES.get(self.mode, '?'),
            'recording': self.is_recording(),
            'fps_actual': round(self.fps_actual, 1),
            'has_preview': self.last_frame_jpeg is not None,
            'last_frame_age_s': round(time.time() - self.last_frame_ts, 2)
                                if self.last_frame_ts else None,
        }

    def get_jpeg(self) -> Optional[bytes]:
        with self._frame_lock:
            return self.last_frame_jpeg

    # ---- background loop ----------------------------------------
    def _ping(self) -> bool:
        command = ['ping', '-n', '-c', '1']
        if self.bind:
            command += ['-S' if sys.platform == 'darwin' else '-I', self.bind]
        command.append(self.host)
        try:
            return subprocess.run(command, stdout=subprocess.DEVNULL,
                                  stderr=subprocess.DEVNULL, timeout=0.9,
                                  check=False).returncode == 0
        except subprocess.TimeoutExpired:
            return False

    def _monitor_connection(self):
        while not self._stop.is_set():
            started = time.monotonic()
            try:
                reachable = self._ping()
            except OSError as e:
                reachable = False
                self.error = f'Camera availability check failed: {e}'
            self.reachable = reachable
            if reachable:
                self._online.set()
            else:
                self._online.clear()
                self._reconnect.set()
                self._mark_disconnected()
            self._stop.wait(max(0, CONNECTION_CHECK_INTERVAL - (time.monotonic() - started)))

    def _mark_disconnected(self):
        self.state = self.RECONNECTING
        self.mode = 0
        self.fps_actual = 0.0
        with self._frame_lock:
            self.last_frame_jpeg = None
            self.last_frame_ts = 0.0

    def _close_camera(self):
        with self._cmd_lock:
            if self._camera:
                try:
                    self._camera.close()
                except Exception:
                    pass
                self._camera = None

    def _run(self):
        try:
            while not self._stop.is_set():
                if not self._online.is_set():
                    self._stop.wait(0.1)
                    continue
                self._reconnect.clear()
                try:
                    with self._cmd_lock:
                        self._connect_ptp()
                    if self._reconnect.is_set() or self._stop.is_set():
                        continue
                    self._stream_rtsp()
                except (TransportError, PtpError, OSError, RuntimeError, cv2.error) as e:
                    self.error = str(e)
                finally:
                    self._mark_disconnected()
                    self._close_camera()
                self._stop.wait(CONNECTION_CHECK_INTERVAL)
        finally:
            self._close_camera()

    def _connect_ptp(self):
        if self._camera:
            try: self._camera.close()
            except Exception: pass
            self._camera = None
        self.state = self.STARTING
        cam = Camera(self.host, bind=self.bind, timeout=2.0)
        self._camera = cam
        cam.connect()
        try:
            info = cam.device_info()
            self.model = info.get('model') or 'V11'  # camera leaves model empty
            self.fw = cam.get_prop_value(0x501F)  # FwVersion
            self.mode = cam.get_mode()
        except Exception as e:
            self.error = f"DeviceInfo failed: {e}"

    def _stream_rtsp(self):
        """Open the RTSP MJPEG preview and pump frames into last_frame_jpeg."""
        url = RTSP_URL_TMPL.format(host=self.host)
        cap = cv2.VideoCapture(url, cv2.CAP_FFMPEG, [
            cv2.CAP_PROP_OPEN_TIMEOUT_MSEC, RTSP_TIMEOUT_MS,
            cv2.CAP_PROP_READ_TIMEOUT_MSEC, RTSP_TIMEOUT_MS,
        ])
        try:
            if not cap.isOpened():
                raise RuntimeError(f"RTSP open failed: {url}")
            first_frame = True
            t_window = time.time()
            n_window = 0
            while not self._stop.is_set() and not self._reconnect.is_set():
                ok, frame = cap.read()
                if self._reconnect.is_set() or self._stop.is_set():
                    break
                if not ok:
                    raise RuntimeError("RTSP read returned not-ok (camera disconnect?)")
                # Downscale if needed
                h, w = frame.shape[:2]
                if w > PREVIEW_MAX_WIDTH:
                    scale = PREVIEW_MAX_WIDTH / w
                    frame = cv2.resize(frame, (int(w * scale), int(h * scale)))
                ok, jpeg = cv2.imencode('.jpg', frame,
                                         [cv2.IMWRITE_JPEG_QUALITY, PREVIEW_JPEG_QUALITY])
                if ok:
                    if first_frame:
                        self.stream_generation += 1
                        self.state = self.RUNNING
                        self.error = None
                        first_frame = False
                    with self._frame_lock:
                        self.last_frame_jpeg = jpeg.tobytes()
                        self.last_frame_ts = time.time()
                # FPS metering
                n_window += 1
                now = time.time()
                if now - t_window >= 1.0:
                    self.fps_actual = n_window / (now - t_window)
                    n_window = 0
                    t_window = now
        finally:
            try: cap.release()
            except Exception: pass
