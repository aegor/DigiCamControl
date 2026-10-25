const fs = require('node:fs');
const path = require('node:path');
const { createHash } = require('node:crypto');

// The stock Electron launcher is reused by many apps. macOS network privacy
// also uses LC_UUID, so CFBundleIdentifier alone does not distinguish them.
// Run only on fresh native packages, BEFORE electron-builder signs the bundle.
function uniqueUUID(input, identity) {
  const data = Buffer.from(input);
  if (data.length < 32 || data.readUInt32LE(0) !== 0xfeedfacf) {
    throw new Error('Expected a native 64-bit little-endian Mach-O executable');
  }
  const count = data.readUInt32LE(16);
  const end = 32 + data.readUInt32LE(20);
  if (end > data.length || count > (end - 32) / 8) throw new Error('Invalid Mach-O load commands');
  let offset = 32, uuidOffset;
  for (let i = 0; i < count; i++) {
    if (offset + 8 > end) throw new Error('Truncated Mach-O load command');
    const command = data.readUInt32LE(offset);
    const size = data.readUInt32LE(offset + 4);
    if (size < 8 || offset + size > end) throw new Error('Invalid Mach-O load command size');
    if (command === 0x1b) {
      if (size !== 24 || uuidOffset !== undefined) throw new Error('Invalid LC_UUID');
      uuidOffset = offset + 8;
    }
    offset += size;
  }
  if (offset !== end || uuidOffset === undefined) throw new Error('Missing LC_UUID or invalid load command table');
  const original = data.subarray(uuidOffset, uuidOffset + 16);
  const uuid = createHash('sha256').update(identity).update('\0').update(original).digest().subarray(0, 16);
  uuid[6] = (uuid[6] & 0x0f) | 0x50;
  uuid[8] = (uuid[8] & 0x3f) | 0x80;
  uuid.copy(data, uuidOffset);
  return data;
}
async function afterPack(context) {
  if (context.electronPlatformName !== 'darwin') return;
  const name = context.packager.appInfo.productFilename;
  const bundle = path.join(context.appOutDir, `${name}.app`);
  const executables = [path.join(bundle, 'Contents/MacOS', name), path.join(bundle, 'Contents/Resources/backend/dicam')];
  const frameworks = path.join(bundle, 'Contents/Frameworks');
  for (const entry of fs.readdirSync(frameworks)) {
    if (!entry.endsWith('.app')) continue;
    const macos = path.join(frameworks, entry, 'Contents/MacOS');
    for (const file of fs.readdirSync(macos)) executables.push(path.join(macos, file));
  }
  for (const file of executables) {
    const identity = `${context.packager.appInfo.id}/${path.relative(bundle, file)}`;
    fs.writeFileSync(file, uniqueUUID(fs.readFileSync(file), identity));
  }
  console.log(`Assigned app-specific Mach-O UUIDs to ${executables.length} executables before signing`);
}
module.exports = afterPack;
module.exports.uniqueUUID = uniqueUUID;
