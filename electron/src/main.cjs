const { app, BrowserWindow, ipcMain, dialog, Menu, shell } = require('electron');
const path = require('node:path');
const fs = require('node:fs/promises');
const { pathToFileURL } = require('node:url');
const { Backend } = require('./backend.cjs');
const { validateConfig } = require('./config.cjs');
let window, backend, config, busy = false, closing = false, mayQuit = false;
const page = pathToFileURL(path.join(__dirname, 'index.html')).href;
const settingsPath = () => path.join(app.getPath('userData'), 'settings.json');
async function stopRecording() {
  if (!backend.url) return;
  const status = await backend.request('/api/status');
  if (status.burst.active || status.slots.some(slot => slot.recording)) {
    const result = await backend.request('/api/record/stop', {});
    const failures = Object.values(result.results || {}).filter(item => !item.ok);
    if (failures.length) throw new Error(failures.map(item => item.error).join('; '));
  }
}
function handle(channel, fn) {
  ipcMain.handle(channel, async (event, ...args) => {
    if (event.sender !== window.webContents || event.senderFrame !== window.webContents.mainFrame || event.senderFrame.url !== page) {
      throw new Error('Недопустимый источник команды');
    }
    return fn(...args);
  });
}
async function connect(value) {
  if (busy || closing) throw new Error('Подождите завершения текущей операции');
  const next = validateConfig(value);
  busy = true;
  try {
    await stopRecording();
    await backend.stop();
    const url = await backend.start(next);
    config = next;
    await fs.mkdir(app.getPath('userData'), { recursive: true });
    await fs.writeFile(`${settingsPath()}.tmp`, JSON.stringify(config, null, 2));
    await fs.rename(`${settingsPath()}.tmp`, settingsPath());
    return { url };
  } finally { busy = false; }
}
async function openRecordings() {
  await fs.mkdir(config.recordDir, { recursive: true });
  const error = await shell.openPath(config.recordDir);
  if (error) throw new Error(error);
}
async function quit() {
  if (closing) return;
  if (busy) {
    await dialog.showMessageBox(window, { message: 'Дождитесь завершения команды или подключения.', type: 'info' });
    return;
  }
  closing = true;
  try {
    await stopRecording();
  } catch (error) {
    const answer = await dialog.showMessageBox(window, {
      type: 'warning', message: 'Не удалось подтвердить остановку записи',
      detail: `${error.message}\nЗапись на SD-карту может продолжаться.`,
      buttons: ['Остаться', 'Выйти'], defaultId: 0, cancelId: 0
    });
    if (answer.response === 0) { closing = false; return; }
  }
  await backend.stop();
  mayQuit = true;
  app.quit();
}
if (!app.requestSingleInstanceLock()) app.quit();
else {
  app.on('second-instance', () => { if (window) { if (window.isMinimized()) window.restore(); window.show(); window.focus(); } });
  app.whenReady().then(async () => {
    config = { cameras: '192.168.1.1', bind: '', usb: '', recordDir: path.join(app.getPath('videos'), 'DiCam') };
    let settingsError = '';
    try { config = validateConfig(JSON.parse(await fs.readFile(settingsPath(), 'utf8'))); }
    catch (error) { if (error.code !== 'ENOENT') settingsError = `Не удалось прочитать настройки: ${error.message}`; }
    const resources = app.isPackaged ? path.join(process.resourcesPath, 'backend') : path.join(__dirname, '../resources', `${process.platform}-${process.arch}`);
    backend = new Backend(path.join(resources, process.platform === 'win32' ? 'dicam.exe' : 'dicam'));
    window = new BrowserWindow({ width: 1200, height: 800, minWidth: 720, minHeight: 520,
      title: 'DiCam Controller', backgroundColor: '#101217', show: false,
      webPreferences: { preload: path.join(__dirname, 'preload.cjs'), contextIsolation: true, sandbox: true, nodeIntegration: false }
    });
    window.webContents.setWindowOpenHandler(() => ({ action: 'deny' }));
    window.webContents.on('will-navigate', event => event.preventDefault());
    const canAccessLoopback = (contents, permission) => contents === window.webContents &&
      contents.getURL() === page && ['loopback-network', 'local-network-access'].includes(permission);
    window.webContents.session.setPermissionRequestHandler((contents, permission, callback) => callback(canAccessLoopback(contents, permission)));
    window.webContents.session.setPermissionCheckHandler(canAccessLoopback);
    window.on('close', event => { if (!mayQuit) { event.preventDefault(); void quit(); } });
    handle('open-network-settings', async () => {
      if (process.platform === 'darwin') await shell.openExternal('x-apple.systempreferences:com.apple.preference.security?Privacy_LocalNetwork');
    });
    handle('settings', () => ({ config, platform: process.platform, error: settingsError }));
    handle('connect', connect);
    handle('status', async () => ({ ...await backend.request('/api/status'), url: backend.url }));
    handle('command', async (name, duration) => {
      if (busy || closing) throw new Error('Подождите завершения текущей операции');
      const routes = { start: '/api/record/start', stop: '/api/record/stop', burst: '/api/burst' };
      if (!Object.hasOwn(routes, name)) throw new Error('Неизвестная команда');
      if (name === 'burst' && (!Number.isInteger(duration) || duration < 1 || duration > 60)) throw new Error('Длительность: 1–60 секунд');
      busy = true;
      try { return await backend.request(routes[name], name === 'burst' ? { duration } : {}); }
      finally { busy = false; }
    });
    handle('choose-directory', async () => {
      const result = await dialog.showOpenDialog(window, { properties: ['openDirectory', 'createDirectory'], defaultPath: config.recordDir });
      return result.canceled ? null : result.filePaths[0];
    });
    handle('open-recordings', openRecordings);
    Menu.setApplicationMenu(Menu.buildFromTemplate([
      ...(process.platform === 'darwin' ? [{ label: app.name, submenu: [{ role: 'about' }, { type: 'separator' }, { role: 'quit' }] }] : []),
      { label: 'Файл', submenu: [{ label: 'Папка записей', click: () => openRecordings().catch(error => dialog.showErrorBox('DiCam', error.message)) }, { role: 'quit', label: 'Выход' }] },
      { label: 'Правка', submenu: [{ role: 'undo' }, { role: 'redo' }, { type: 'separator' }, { role: 'cut' }, { role: 'copy' }, { role: 'paste' }, { role: 'selectAll' }] },
      { label: 'Вид', submenu: [{ role: 'togglefullscreen', label: 'Полный экран' }, { role: 'resetZoom' }, { role: 'zoomIn' }, { role: 'zoomOut' }] }
    ]));
    await window.loadFile(path.join(__dirname, 'index.html'));
    window.show();
  }).catch(error => { dialog.showErrorBox('DiCam', error.message); mayQuit = true; app.quit(); });
  app.on('before-quit', event => { if (!mayQuit && backend) { event.preventDefault(); void quit(); } });
}
