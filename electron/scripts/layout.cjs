const path = require('node:path');
const fs = require('node:fs');
const root = path.resolve(__dirname, '../..');
function layout(platform = process.platform, arch = process.arch) {
  const target = `${platform}-${arch}`;
  if (!['darwin-arm64', 'darwin-x64', 'win32-x64', 'linux-x64', 'linux-arm64'].includes(target)) {
    throw new Error(`Unsupported native build target: ${target}`);
  }
  const electron = path.join(root, 'electron');
  const cache = path.join(root, '.cache', target);
  return { root, electron, platform, arch, target, cache,
    resources: path.join(electron, 'resources', target),
    release: path.join(electron, 'release', target),
    dist: path.join(root, 'dist'),
    binary: path.join(cache, 'bin', platform === 'win32' ? 'dicam.exe' : 'dicam'),
    name: platform === 'win32' ? 'dicam.exe' : 'dicam' };
}
function environment(paths, inherited = process.env) {
  return { ...inherited, DICAM_BUILD_TARGET: paths.target,
    npm_config_platform: paths.platform, npm_config_arch: paths.arch,
    npm_config_os: paths.platform, npm_config_cpu: paths.arch,
    ELECTRON_INSTALL_PLATFORM: paths.platform, ELECTRON_INSTALL_ARCH: paths.arch,
    npm_config_cache: path.join(paths.cache, 'npm'),
    ELECTRON_CACHE: path.join(paths.cache, 'electron'),
    electron_config_cache: path.join(paths.cache, 'electron'),
    ELECTRON_BUILDER_CACHE: path.join(paths.cache, 'electron-builder'),
    GOCACHE: path.join(paths.cache, 'go/build'),
    GOMODCACHE: path.join(paths.cache, 'go/mod'),
    DICAM_NATIVE_CACHE: path.join(paths.cache, 'native') };
}
function assertDependencies(paths) {
  const marker = path.join(paths.electron, 'node_modules/.dicam-platform.json');
  let installed;
  try { installed = JSON.parse(fs.readFileSync(marker, 'utf8')); } catch {}
  if (installed?.target !== paths.target) {
    throw new Error(`node_modules belongs to ${installed?.target || 'an unknown platform'}. Run: node scripts/build.cjs install. Do not share a checkout/node_modules between operating systems.`);
  }
}
module.exports = { layout, environment, assertDependencies };
