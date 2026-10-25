const fs = require('node:fs');
const path = require('node:path');
const { layout, assertDependencies } = require('./layout.cjs');
const { verifyBinary } = require('./binary.cjs');
module.exports = async context => {
  const arch = { 0: 'ia32', 1: 'x64', 2: 'arm', 3: 'arm64', 4: 'universal' }[context.arch];
  const paths = layout(context.electronPlatformName, arch);
  if (paths.platform !== process.platform || paths.arch !== process.arch) throw new Error('Build natively on the target OS/architecture');
  if (process.env.DICAM_BUILD_TARGET !== paths.target) throw new Error('Use npm run pack/dist so the resource target and cache environment match the build');
  assertDependencies(paths);
  const manifest = JSON.parse(fs.readFileSync(path.join(paths.resources, 'platform.json'), 'utf8'));
  const actual = verifyBinary(path.join(paths.resources, paths.name), paths);
  if (manifest.target !== paths.target || manifest.platform !== actual.platform || manifest.arch !== actual.arch || manifest.sha256 !== actual.sha256) {
    throw new Error('Backend manifest does not match the executable. Run npm run prepare:backend.');
  }
};
