const fs = require('node:fs');
const path = require('node:path');
const { createHash } = require('node:crypto');
const { execFileSync } = require('node:child_process');
const { layout, environment, assertDependencies } = require('./layout.cjs');
const { prepareBackend, run, runGo } = require('./prepare-backend.cjs');
function npmCLI() {
  if (process.env.npm_execpath) return process.env.npm_execpath;
  const candidates = [
    path.join(path.dirname(process.execPath), 'node_modules/npm/bin/npm-cli.js'),
    path.resolve(path.dirname(process.execPath), '../lib/node_modules/npm/bin/npm-cli.js')
  ];
  const bundled = candidates.find(file => fs.existsSync(file));
  if (bundled) return bundled;
  const command = process.platform === 'win32' ? 'where.exe' : 'which';
  const found = execFileSync(command, [process.platform === 'win32' ? 'npm.cmd' : 'npm'], { encoding: 'utf8' }).trim().split(/\r?\n/)[0];
  return process.platform === 'win32' ? path.join(path.dirname(found), 'node_modules/npm/bin/npm-cli.js') : fs.realpathSync(found);
}
async function main(action) {
  const paths = layout();
  const env = environment(paths);
  Object.assign(process.env, env);
  fs.mkdirSync(paths.cache, { recursive: true });
  if (action === 'install') {
    run(process.execPath, [npmCLI(), 'ci'], { cwd: paths.electron, env });
    return;
  }
  if (action === 'paths') { console.log(JSON.stringify({ ...paths, env: Object.fromEntries(['npm_config_cache', 'ELECTRON_CACHE', 'ELECTRON_BUILDER_CACHE', 'GOCACHE', 'GOMODCACHE', 'DICAM_NATIVE_CACHE'].map(key => [key, env[key]])) }, null, 2)); return; }
  if (action === 'backend' || action === 'prepare:backend') { prepareBackend(action === 'backend', paths); return; }
  if (action === 'test:go' || action === 'vet:go') { runGo(action === 'test:go' ? 'test' : 'vet', paths); return; }
  assertDependencies(paths);
  if (action === 'start' || action === 'test:smoke') {
    run(process.execPath, [require.resolve('electron/cli.js'), action === 'start' ? '.' : 'scripts/smoke.cjs'], { cwd: paths.electron, env });
    return;
  }
  if (action === 'test') {
    const tests = fs.readdirSync(path.join(paths.electron, 'test')).filter(file => file.endsWith('.test.cjs')).map(file => path.join('test', file));
    run(process.execPath, ['--test', ...tests], { cwd: paths.electron, env });
    return;
  }
  if (!['pack', 'dist', 'dmg:arm64'].includes(action)) throw new Error('Usage: node scripts/build.cjs install|backend|prepare:backend|start|test|test:go|vet:go|test:smoke|pack|dist|dmg:arm64|paths');
  if (action === 'dmg:arm64' && paths.target !== 'darwin-arm64') throw new Error('dmg:arm64 requires a native macOS ARM64 checkout');
  const { build, createTargets, Platform } = require('electron-builder');
  const platform = { darwin: Platform.MAC, win32: Platform.WINDOWS, linux: Platform.LINUX }[paths.platform];
  const type = action === 'pack' ? 'dir' : { darwin: 'dmg', win32: 'nsis', linux: 'AppImage' }[paths.platform];
  // Builder loads package.json itself and concatenates arrays from overrides.
  // Keep extraResources/files/targets there to avoid concurrent duplicate copies.
  const config = {
    directories: { output: paths.release },
    electronDownload: { cache: env.ELECTRON_CACHE },
    beforePack: path.join(paths.electron, 'scripts/before-pack.cjs'),
    afterPack: path.join(paths.electron, 'scripts/mac-identity.cjs'),
    artifactName: `DiCam-Controller-\${version}-${{darwin:'macos',win32:'windows',linux:'linux'}[paths.platform]}-${paths.arch}${paths.platform === 'win32' ? '-setup' : ''}.\${ext}` };
  const options = { projectDir: paths.electron, targets: createTargets([platform], type, paths.arch), config, publish: 'never' };
  if (action === 'dmg:arm64') {
    const bundle = path.join(paths.release, 'mac-arm64/DiCam Controller.app');
    if (!fs.existsSync(path.join(bundle, 'Contents/Info.plist'))) throw new Error('Missing native .app. Run npm run pack first.');
    options.prepackaged = bundle;
  } else { prepareBackend(false, paths); }
  const artifacts = await build(options);
  if (action !== 'pack') {
    const installers = artifacts.filter(file => /\.(dmg|exe|AppImage)$/.test(file));
    if (!installers.length) throw new Error('Builder returned no installer artifacts');
    fs.mkdirSync(paths.dist, { recursive: true });
    for (const file of installers) {
      const destination = path.join(paths.dist, path.basename(file));
      const temporary = `${destination}.${process.pid}.part`;
      try {
        fs.copyFileSync(file, temporary);
        fs.renameSync(temporary, destination);
      } finally { fs.rmSync(temporary, { force: true }); }
      const hash = createHash('sha256').update(fs.readFileSync(destination)).digest('hex');
      fs.writeFileSync(`${destination}.sha256`, `${hash}  ${path.basename(destination)}\n`);
      console.log(`Installer: ${destination}`);
    }
  }
}
if (require.main === module) main(process.argv[2]).catch(error => { console.error(error.message); process.exitCode = 1; });
module.exports = { main };
