const { isIP } = require('node:net');
const path = require('node:path');
function validateConfig(value, platform = process.platform) {
  if (!value || typeof value !== 'object') throw new Error('Некорректные настройки');
  const cameras = String(value.cameras ?? '').split(/[\s,;]+/).filter(Boolean);
  const usb = String(value.usb ?? '').split(/[\s,;]+/).filter(Boolean);
  const bind = String(value.bind ?? '').trim();
  const recordDir = String(value.recordDir ?? '').trim();
  if (!cameras.length && !usb.length) throw new Error('Укажите хотя бы одну камеру');
  if (cameras.length + usb.length > 16) throw new Error('Максимум 16 камер');
  if (cameras.some(ip => isIP(ip) !== 4)) throw new Error('Укажите IPv4-адреса камер');
  if (bind && isIP(bind) !== 4) throw new Error('Локальный IP должен быть IPv4 или пустым');
  if (usb.length && platform !== 'linux') throw new Error('USB поддерживается только на Linux');
  if (usb.some(device => device !== 'auto' && !/^\/dev\/video\d+$/.test(device))) throw new Error('USB: auto или /dev/videoN');
  if (!path.isAbsolute(recordDir)) throw new Error('Выберите абсолютный путь для записей');
  return { cameras: [...new Set(cameras)].join('\n'), usb: [...new Set(usb)].join('\n'), bind, recordDir };
}
function backendArgs(config) {
  return ['--desktop', '--port', '0', '--bind', config.bind, '--record-dir', config.recordDir,
    ...config.cameras.split('\n').filter(Boolean).flatMap(ip => ['--camera', ip]),
    ...config.usb.split('\n').filter(Boolean).flatMap(device => ['--usb', device])];
}
module.exports = { validateConfig, backendArgs };
