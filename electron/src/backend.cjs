const { spawn } = require('node:child_process');
const { EventEmitter } = require('node:events');
const { backendArgs } = require('./config.cjs');
class Backend extends EventEmitter {
  constructor(executable, options = {}) {
    super();
    this.executable = executable;
    this.options = options;
    this.child = null;
    this.url = null;
    this.logs = '';
  }
  async start(config) {
    if (this.child) throw new Error('Движок уже запущен');
    this.logs = '';
    const child = spawn(this.executable, [...(this.options.prefixArgs || []), ...backendArgs(config)], {
      stdio: ['pipe', 'pipe', 'pipe'], windowsHide: true,
      env: { ...process.env, PATH: `${process.env.PATH || ''}${process.platform === 'win32' ? ';' : ':'}/sbin:/usr/sbin` }
    });
    this.child = child;
    child.stdin.on('error', () => {});
    this.exited = new Promise(resolve => child.once('close', () => {
      this.child = null;
      this.url = null;
      this.emit('stopped');
      resolve();
    }));
    try {
      const url = await new Promise((resolve, reject) => {
        let output = '';
        const timer = setTimeout(() => reject(new Error('Движок не запустился за 15 секунд')), 15000);
        const finish = (error, value) => { clearTimeout(timer); error ? reject(error) : resolve(value); };
        child.once('error', error => finish(error));
        child.once('exit', (code, signal) => finish(new Error(`Движок завершился (${code ?? signal}). ${this.logs.slice(-2000)}`)));
        child.stderr.on('data', data => { this.logs = (this.logs + data).slice(-16000); });
        child.stdout.on('data', data => {
          output = (output + data).slice(-4096);
          const match = output.match(/^DICAM_READY (http:\/\/127\.0\.0\.1:\d+\/)\r?$/m);
          if (match) finish(null, match[1]);
        });
      });
      if (this.child !== child || child.exitCode !== null) throw new Error('Движок остановлен');
      this.url = url;
      await this.request('/api/status');
      return url;
    } catch (error) {
      await this.stop();
      throw error;
    }
  }
  async request(route, body) {
    if (!this.url) throw new Error('Движок отключён. Нажмите «Подключить».');
    const response = await fetch(new URL(route, this.url), {
      method: body === undefined ? 'GET' : 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: body === undefined ? undefined : JSON.stringify(body),
      signal: AbortSignal.timeout(12000)
    });
    const result = await response.json();
    if (!response.ok || result.error) throw new Error(result.error || `HTTP ${response.status}`);
    return result;
  }
  async stop() {
    if (!this.child) return;
    const child = this.child;
    // EOF also works on Windows and when the Electron parent dies unexpectedly.
    child.stdin.end();
    const timer = setTimeout(() => child.kill('SIGKILL'), 12000);
    await this.exited;
    clearTimeout(timer);
  }
}
module.exports = { Backend };
