const fs = require('node:fs');
const path = require('node:path');
const { spawnSync } = require('node:child_process');
const { createHash } = require('node:crypto');
const { layout, environment } = require('./layout.cjs');
const { verifyBinary } = require('./binary.cjs');
function run(command, args, options) {
  const result = spawnSync(command, args, { stdio: 'inherit', ...options });
  if (result.error) throw result.error;
  if (result.status !== 0) throw new Error(`${command} failed (${result.status ?? result.signal})`);
}
function gitBash(env) {
  const candidates = [env.DICAM_GIT_BASH,
    env.ProgramFiles && path.join(env.ProgramFiles, 'Git/bin/bash.exe'),
    env.LOCALAPPDATA && path.join(env.LOCALAPPDATA, 'Programs/Git/bin/bash.exe')].filter(Boolean);
  const executable = candidates.find(file => fs.existsSync(file));
  if (!executable) throw new Error('Install Git for Windows or set DICAM_GIT_BASH to its bin/bash.exe. WSL bash is not supported.');
  return executable;
}
function runGo(action, paths = layout()) {
  const env = environment(paths);
  const args = [action];
  if (action === 'build') args.push(`BINARY=${paths.binary.replaceAll('\\', '/')}`);
  const cwd = path.join(paths.root, 'go');
  if (paths.platform === 'win32') run(gitBash(env), ['scripts/windows.sh', ...args], { cwd, env });
  else run('make', args, { cwd, env });
}
function prepareBackend(build = false, paths = layout()) {
  if (paths.platform !== process.platform || paths.arch !== process.arch) throw new Error('Build natively on the target OS/architecture');
  const env = environment(paths);
  fs.mkdirSync(path.dirname(paths.binary), { recursive: true });
  if (build) runGo('build', paths);
  const source = process.env.DICAM_BACKEND || paths.binary;
  if (!fs.existsSync(source)) throw new Error(`Missing ${source}. Run npm run backend, or set DICAM_BACKEND to a compatible native executable.`);
  const identity = verifyBinary(source, paths);
  const check = spawnSync(source, ['--help'], { encoding: 'utf8', env, timeout: 10000 });
  if (check.error || check.status !== 0 || !`${check.stdout}${check.stderr}`.includes('-desktop')) {
    throw new Error('Backend does not support --desktop. Rebuild with npm run backend.');
  }
  const archive = path.join(env.DICAM_NATIVE_CACHE, 'downloads/ffmpeg-8.0.3.tar.xz');
  if (!fs.existsSync(archive)) throw new Error('Missing platform-local FFmpeg source archive. Run npm run backend.');
  const archiveHash = createHash('sha256').update(fs.readFileSync(archive)).digest('hex');
  if (archiveHash !== '6136812ea6d4e68bdba27e33c2a94382711cdf4f8602ffef056ff792bd6f9818') throw new Error('FFmpeg source checksum mismatch');
  // Recreate only this generated target directory: no stale binaries or sources.
  fs.mkdirSync(path.dirname(paths.resources), { recursive: true });
  const staging = fs.mkdtempSync(`${paths.resources}-staging-`);
  try {
    fs.copyFileSync(source, path.join(staging, paths.name));
    fs.chmodSync(path.join(staging, paths.name), 0o755);
    fs.cpSync(path.join(paths.root, 'go/third_party/ffmpeg'), path.join(staging, 'ffmpeg'), { recursive: true });
    fs.copyFileSync(path.join(paths.root, 'LICENSE'), path.join(staging, 'LICENSE'));
    const sources = path.join(staging, 'source/go');
    fs.mkdirSync(sources, { recursive: true });
    for (const entry of fs.readdirSync(path.join(paths.root, 'go'))) {
      if (/\.(go|c|h)$/.test(entry) || ['go.mod', 'Makefile', 'scripts', 'web', 'third_party'].includes(entry)) {
        fs.cpSync(path.join(paths.root, 'go', entry), path.join(sources, entry), { recursive: true });
      }
    }
    fs.copyFileSync(archive, path.join(staging, 'source/ffmpeg-8.0.3.tar.xz'));
    fs.writeFileSync(path.join(staging, 'platform.json'), JSON.stringify({ ...identity, target: paths.target }, null, 2));
    fs.rmSync(paths.resources, { recursive: true, force: true });
    fs.renameSync(staging, paths.resources);
  } finally { fs.rmSync(staging, { recursive: true, force: true }); }
  console.log(`Backend verified: ${paths.target} → ${paths.resources}`);
  return paths;
}
if (require.main === module) prepareBackend(process.argv.includes('--build'));
module.exports = { prepareBackend, run, runGo };
