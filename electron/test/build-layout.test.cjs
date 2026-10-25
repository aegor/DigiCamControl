const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { layout, environment, assertDependencies } = require('../scripts/layout.cjs');
const { identify, verifyBinary } = require('../scripts/binary.cjs');
const beforePack = require('../scripts/before-pack.cjs');
function macho(arch) {
  const buffer = Buffer.alloc(64);
  buffer.writeUInt32LE(0xfeedfacf, 0);
  buffer.writeUInt32LE(arch === 'arm64' ? 0x100000c : 0x1000007, 4);
  return buffer;
}
function pe(arch = 'x64') {
  const buffer = Buffer.alloc(256);
  buffer.write('MZ'); buffer.writeUInt32LE(128, 0x3c);
  buffer.writeUInt32LE(0x4550, 128); buffer.writeUInt16LE(arch === 'x64' ? 0x8664 : 0xaa64, 132);
  return buffer;
}
test('all generated paths and all caches are disjoint across targets', () => {
  const targets = [layout('darwin', 'arm64'), layout('darwin', 'x64'), layout('win32', 'x64')];
  for (const key of ['resources', 'release', 'binary', 'cache']) assert.equal(new Set(targets.map(t => t[key])).size, targets.length);
  const inherited = { ELECTRON_INSTALL_PLATFORM: 'foreign', npm_config_arch: 'wrong', GOCACHE: 'wrong', GOMODCACHE: 'wrong', ELECTRON_CACHE: 'wrong', PATH: 'keep' };
  for (const target of targets) {
    const env = environment(target, inherited);
    for (const key of ['GOCACHE', 'GOMODCACHE', 'npm_config_cache', 'ELECTRON_CACHE', 'ELECTRON_BUILDER_CACHE', 'DICAM_NATIVE_CACHE']) {
      assert.ok(env[key].startsWith(target.cache + path.sep));
    }
    assert.equal(env.PATH, 'keep');
    assert.equal(env.ELECTRON_INSTALL_PLATFORM, target.platform);
    assert.equal(env.npm_config_arch, target.arch);
  }
  assert.throws(() => layout('win32', 'arm64'), /Unsupported/);
});
test('identify actual binary format and architecture, regardless of file extension', () => {
  assert.deepEqual(identify(macho('arm64')), { platform: 'darwin', arch: 'arm64' });
  assert.deepEqual(identify(macho('x64')), { platform: 'darwin', arch: 'x64' });
  assert.deepEqual(identify(pe()), { platform: 'win32', arch: 'x64' });
  const elf = Buffer.alloc(64); Buffer.from([0x7f, 0x45, 0x4c, 0x46, 2, 1]).copy(elf); elf.writeUInt16LE(62, 18);
  assert.deepEqual(identify(elf), { platform: 'linux', arch: 'x64' });
  assert.throws(() => identify(Buffer.alloc(4)), /malformed/);
  const bad = pe(); bad.writeUInt32LE(0xffffffff, 0x3c);
  assert.throws(() => identify(bad), /malformed/);
});
test('reject disguised foreign backend and detect byte changes', t => {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), 'dicam-build-test-'));
  t.after(() => fs.rmSync(directory, { recursive: true, force: true }));
  const file = path.join(directory, 'dicam.exe');
  fs.writeFileSync(file, macho('arm64'));
  assert.throws(() => verifyBinary(file, layout('win32', 'x64')), /Wrong backend/);
  fs.writeFileSync(file, pe());
  const first = verifyBinary(file, layout('win32', 'x64'));
  const changed = pe(); changed[250] = 1; fs.writeFileSync(file, changed);
  assert.notEqual(verifyBinary(file, layout('win32', 'x64')).sha256, first.sha256);
});
test('foreign or unknown node_modules cannot be used for a build', t => {
  const electron = fs.mkdtempSync(path.join(os.tmpdir(), 'dicam-dependencies-'));
  t.after(() => fs.rmSync(electron, { recursive: true, force: true }));
  const target = { ...layout(), electron };
  assert.throws(() => assertDependencies(target), /node_modules/);
  fs.mkdirSync(path.join(electron, 'node_modules'));
  const file = path.join(electron, 'node_modules/.dicam-platform.json');
  fs.writeFileSync(file, JSON.stringify({ target: 'foreign-target' }));
  assert.throws(() => assertDependencies(target), /foreign-target/);
  fs.writeFileSync(file, JSON.stringify({ target: target.target }));
  assert.doesNotThrow(() => assertDependencies(target));
});
test('packaging rejects a foreign target before using any resources', async () => {
  const foreign = process.platform === 'win32' ? 'darwin' : 'win32';
  await assert.rejects(beforePack({ electronPlatformName: foreign, arch: 1 }), /natively/);
});
