const { test } = require('node:test');
const assert = require('node:assert/strict');
const { uniqueUUID } = require('../scripts/mac-identity.cjs');
function executable() {
  const buffer = Buffer.alloc(80, 0);
  buffer.writeUInt32LE(0xfeedfacf, 0);
  buffer.writeUInt32LE(1, 16);
  buffer.writeUInt32LE(24, 20);
  buffer.writeUInt32LE(0x1b, 32);
  buffer.writeUInt32LE(24, 36);
  buffer.fill(0xab, 40, 56);
  buffer.fill(0xcd, 56);
  return buffer;
}
test('app-specific UUID is deterministic and changes no executable code or headers', () => {
  const original = executable();
  const a = uniqueUUID(original, 'dicam/main');
  assert.notDeepEqual(a.subarray(40, 56), original.subarray(40, 56));
  assert.deepEqual(a.subarray(0, 40), original.subarray(0, 40));
  assert.deepEqual(a.subarray(56), original.subarray(56));
  assert.deepEqual(a, uniqueUUID(original, 'dicam/main'));
  assert.notDeepEqual(a, uniqueUUID(original, 'dicam/helper'));
  assert.notDeepEqual(a, uniqueUUID(original, 'other-app/main'));
  assert.deepEqual(original, executable());
});
test('reject malformed or unsupported Mach-O instead of corrupting the package', () => {
  assert.throws(() => uniqueUUID(Buffer.alloc(20), 'app'));
  const truncated = executable(); truncated.writeUInt32LE(1000, 36);
  assert.throws(() => uniqueUUID(truncated, 'app'));
  const missing = executable(); missing.writeUInt32LE(0x19, 32);
  assert.throws(() => uniqueUUID(missing, 'app'));
});
