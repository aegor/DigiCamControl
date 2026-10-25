const { test } = require('node:test');
const assert = require('node:assert/strict');
const { validateConfig, backendArgs } = require('../src/config.cjs');
const path = require('node:path');
const recordDir = path.join(require('node:os').tmpdir(), 'DiCam recordings');
const base = { cameras: '192.168.1.1', bind: '', usb: '', recordDir };
test('multiple cameras and source binding remain separate argv values', () => {
  const config = validateConfig({ ...base, cameras: '192.168.1.1, 192.168.1.2\n192.168.1.1', bind: '192.168.1.10' });
  assert.deepEqual(backendArgs(config), ['--desktop', '--port', '0', '--bind', '192.168.1.10', '--record-dir', recordDir, '--camera', '192.168.1.1', '--camera', '192.168.1.2']);
});
test('reject invalid addresses, flags, empty camera list and relative directories', () => {
  for (const change of [{ cameras: '--host 0.0.0.0' }, { cameras: '' }, { bind: 'invalid' }, { recordDir: 'relative' }]) {
    assert.throws(() => validateConfig({ ...base, ...change }));
  }
});
test('USB is allowed only on Linux, with safe device paths', () => {
  assert.throws(() => validateConfig({ ...base, usb: 'auto' }, 'darwin'));
  assert.throws(() => validateConfig({ ...base, usb: '/etc/passwd' }, 'linux'));
  assert.equal(validateConfig({ ...base, cameras: '', usb: '/dev/video0 auto' }, 'linux').usb, '/dev/video0\nauto');
});
