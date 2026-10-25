const { test } = require('node:test');
const assert = require('node:assert/strict');
const path = require('node:path');
const fs = require('node:fs');
const { Backend } = require('../src/backend.cjs');
const executable = path.join(__dirname, '../resources', `${process.platform}-${process.arch}`, process.platform === 'win32' ? 'dicam.exe' : 'dicam');
const config = { cameras: '127.0.0.1', bind: '', usb: '', recordDir: require('node:os').tmpdir() };
test('missing backend reports an actionable spawn failure', async () => {
  const backend = new Backend(path.join(__dirname, 'missing-dicam'));
  await assert.rejects(backend.start(config), /ENOENT/);
  assert.equal(backend.child, null);
});
test('real Go backend: ephemeral port, API, commands and graceful EOF shutdown', { skip: !fs.existsSync(executable), timeout: 30000 }, async () => {
  const backend = new Backend(executable);
  try {
    const url = await backend.start(config);
    assert.match(url, /^http:\/\/127\.0\.0\.1:\d+\/$/);
    const status = await backend.request('/api/status');
    assert.equal(status.slots.length, 1);
    assert.equal(status.slots[0].host, '127.0.0.1');
    const result = await backend.request('/api/record/start', {});
    assert.equal(result.results['0'].ok, false);
    await assert.rejects(backend.request('/api/burst', { duration: 'bad' }), /expected JSON/);
    const child = backend.child;
    await backend.stop();
    assert.equal(child.exitCode, 0);
    assert.equal(backend.url, null);
    await assert.rejects(fetch(url));
    await backend.start(config);
    assert.equal((await backend.request('/api/status')).slots.length, 1);
  } finally { await backend.stop(); }
});
