"""Recording routes with simulated camera control (no hardware required)."""
import unittest
from unittest.mock import Mock, patch

try:
    from webui import app as ui
    from webui.worker import LarkflyWorker
except ModuleNotFoundError as exc:
    if exc.name not in ('flask', 'cv2'):
        raise
    ui = None


@unittest.skipIf(ui is None, 'Web UI dependencies are not installed')
class RecordingTests(unittest.TestCase):
    def setUp(self):
        self.worker = LarkflyWorker(0, '192.168.1.1')
        self.worker.state = self.worker.RUNNING
        self.worker._camera = Mock()
        workers = patch.object(ui, 'WORKERS', [self.worker])
        workers.start()
        self.addCleanup(workers.stop)
        self.client = ui.app.test_client()

    def test_record_and_stop_update_status(self):
        for command, recording in [('start', True), ('stop', False)]:
            response = self.client.post('/api/record/' + command)
            self.assertTrue(response.json['results']['0']['ok'])
            status = self.client.get('/api/status').json['slots'][0]
            self.assertEqual(status['recording'], recording)
        self.worker._camera.start_recording.assert_called_once()
        self.worker._camera.stop_recording.assert_called_once()

    def test_disconnected_camera_reports_error(self):
        self.worker.state = self.worker.DEAD
        self.worker.error = 'Connection lost'
        for command in ('start', 'stop'):
            result = self.client.post('/api/record/' + command).json['results']['0']
            self.assertFalse(result['ok'])
            self.assertEqual(result['error'], 'Connection lost')
        self.worker._camera.start_recording.assert_not_called()
        self.worker._camera.stop_recording.assert_not_called()

    def test_failed_command_does_not_claim_recording(self):
        self.worker._camera.start_recording.side_effect = RuntimeError('PTP failed')
        result = self.client.post('/api/record/start').json['results']['0']
        self.assertFalse(result['ok'])
        self.assertEqual(result['error'], 'PTP failed')
        self.assertFalse(self.client.get('/api/status').json['slots'][0]['recording'])


@unittest.skipIf(ui is None, 'Web UI dependencies are not installed')
class ReconnectionTests(unittest.TestCase):
    def test_ping_loss_clears_stale_preview_and_recording(self):
        worker = LarkflyWorker(0, '192.168.1.1')
        worker.state = worker.RUNNING
        worker.mode = 17
        worker.last_frame_jpeg = b'old frame'
        worker.last_frame_ts = 123
        worker._online.set()
        with patch.object(worker, '_ping', return_value=False), patch.object(
                worker._stop, 'wait', side_effect=lambda _: worker._stop.set()):
            worker._monitor_connection()
        self.assertFalse(worker.reachable)
        self.assertFalse(worker._online.is_set())
        self.assertTrue(worker._reconnect.is_set())
        self.assertEqual(worker.state, worker.RECONNECTING)
        self.assertIsNone(worker.get_jpeg())
        self.assertFalse(worker.is_recording())

    def test_ping_recovery_wakes_reconnection(self):
        worker = LarkflyWorker(0, '192.168.1.1')
        worker._reconnect.set()
        with patch.object(worker, '_ping', return_value=True), patch.object(
                worker._stop, 'wait', side_effect=lambda _: worker._stop.set()):
            worker._monitor_connection()
        self.assertTrue(worker.reachable)
        self.assertTrue(worker._online.is_set())
        # Keep the loss latched until the old RTSP stream has exited.
        self.assertTrue(worker._reconnect.is_set())

    def test_reconnect_continues_past_old_retry_limit(self):
        worker = LarkflyWorker(0, '192.168.1.1', max_retries=5)
        worker._online.set()
        with patch.object(worker, '_connect_ptp', side_effect=[RuntimeError('offline')] * 7 + [None]) as connect, patch.object(
                worker, '_stream_rtsp', side_effect=worker._stop.set) as stream, patch.object(
                worker._stop, 'wait', return_value=False):
            worker._run()
        self.assertEqual(connect.call_count, 8)
        stream.assert_called_once()

    def test_offline_camera_does_not_attempt_ptp(self):
        worker = LarkflyWorker(0, '192.168.1.1')
        with patch.object(worker, '_connect_ptp') as connect, patch.object(
                worker._stop, 'wait', side_effect=lambda _: worker._stop.set()):
            worker._run()
        connect.assert_not_called()

    def test_first_recovered_frame_updates_stream_generation(self):
        import numpy as np
        from webui import worker as worker_module
        worker = LarkflyWorker(0, '192.168.1.1')
        capture = Mock()
        def read():
            if capture.read.call_count > 1:
                worker._stop.set()
                return False, None
            return True, np.zeros((4, 4, 3), dtype=np.uint8)
        capture.read.side_effect = read
        with patch.object(worker_module.cv2, 'VideoCapture', return_value=capture):
            worker._stream_rtsp()
        self.assertEqual(worker.stream_generation, 1)
        self.assertEqual(worker.state, worker.RUNNING)
        self.assertIsNotNone(worker.get_jpeg())
        capture.release.assert_called_once()

    def test_ping_timeout_is_unreachable(self):
        import subprocess
        worker = LarkflyWorker(0, '192.168.1.1', bind='192.168.1.10')
        with patch('webui.worker.subprocess.run', side_effect=subprocess.TimeoutExpired('ping', .9)) as run:
            self.assertFalse(worker._ping())
        self.assertIn('192.168.1.10', run.call_args.args[0])
        self.assertEqual(run.call_args.args[0][-1], '192.168.1.1')
