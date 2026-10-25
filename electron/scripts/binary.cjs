const fs = require('node:fs');
const { createHash } = require('node:crypto');
function identify(data) {
  if (data.length >= 32 && data.readUInt32LE(0) === 0xfeedfacf) {
    const arch = { 0x1000007: 'x64', 0x100000c: 'arm64' }[data.readUInt32LE(4)];
    if (arch) return { platform: 'darwin', arch };
  }
  if (data.length >= 64 && data.toString('ascii', 0, 2) === 'MZ') {
    const offset = data.readUInt32LE(0x3c);
    if (offset + 24 <= data.length && data.readUInt32LE(offset) === 0x4550) {
      const arch = { 0x8664: 'x64', 0xaa64: 'arm64', 0x14c: 'ia32' }[data.readUInt16LE(offset + 4)];
      if (arch) return { platform: 'win32', arch };
    }
  }
  if (data.length >= 64 && data.subarray(0, 4).equals(Buffer.from([0x7f, 0x45, 0x4c, 0x46])) && data[4] === 2 && data[5] === 1) {
    const arch = { 62: 'x64', 183: 'arm64' }[data.readUInt16LE(18)];
    if (arch) return { platform: 'linux', arch };
  }
  throw new Error('Unsupported or malformed native executable (expected Mach-O, PE or ELF)');
}
function verifyBinary(file, expected) {
  const data = fs.readFileSync(file);
  const actual = identify(data);
  if (actual.platform !== expected.platform || actual.arch !== expected.arch) {
    throw new Error(`Wrong backend: ${actual.platform}/${actual.arch}; expected ${expected.platform}/${expected.arch}`);
  }
  return { ...actual, sha256: createHash('sha256').update(data).digest('hex') };
}
module.exports = { identify, verifyBinary };
