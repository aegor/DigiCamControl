// Run with Electron. Uses isolated settings and the real Go backend on loopback.
const { app } = require('electron');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const assert = require('node:assert/strict');
const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'dicam-electron-smoke-'));
app.setPath('userData', dir);
app.setPath('sessionData', dir);
fs.writeFileSync(path.join(dir, 'settings.json'), JSON.stringify({ cameras: '127.0.0.1', bind: '', usb: '', recordDir: dir }));
require('../src/main.cjs');
let finished = false;
const deadline = setTimeout(() => { console.error('Smoke test timeout'); app.exit(1); }, 30000);
app.on('browser-window-created', (_event, window) => {
  window.webContents.once('did-finish-load', async () => {
    try {
      const result = await window.webContents.executeJavaScript(`(async () => {
        const settings = await window.dicam.settings();
        // No click: the saved camera must connect automatically.
        const deadline = Date.now() + 15000;
        while (!document.querySelector('.camera')) {
          if (Date.now() > deadline) throw new Error(document.getElementById('error').textContent || 'No camera card');
          await new Promise(resolve => setTimeout(resolve, 100));
        }
        const first = await window.dicam.status();
        document.getElementById('config-form').requestSubmit();
        while (document.getElementById('connect').disabled) await new Promise(resolve => setTimeout(resolve, 100));
        const status = await window.dicam.status();
        if (status.url === first.url) throw new Error('Manual reconnect did not restart the backend');
        let rejected = false;
        try { await window.dicam.command('invalid'); } catch { rejected = true; }
        return { host: status.slots[0].host, rejected, node: typeof require,
          title: document.querySelector('.camera-title').textContent,
          error: document.getElementById('error').textContent };
      })()`);
      assert.equal(result.host, '127.0.0.1');
      assert.equal(result.rejected, true);
      assert.equal(result.node, 'undefined');
      assert.match(result.title, /127\.0\.0\.1/);
      assert.equal(result.error, '');
      await window.webContents.executeJavaScript('new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)))');
      await new Promise(resolve => setTimeout(resolve, 250));
      const image = await window.webContents.capturePage();
      fs.mkdirSync(path.join(__dirname, '../release', `${process.platform}-${process.arch}`), { recursive: true });
      fs.writeFileSync(path.join(__dirname, '../release', `${process.platform}-${process.arch}`, 'smoke.png'), image.toPNG());
      console.log('Electron smoke passed:', JSON.stringify(result));
      finished = true;
      clearTimeout(deadline);
      window.close();
    } catch (error) { console.error(error); app.exit(1); }
  });
});
app.on('will-quit', () => { if (!finished) process.exitCode = 1; });
