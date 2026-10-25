// Run with: node web/ui_test.cjs (no npm dependencies).
const fs = require('node:fs');
const vm = require('node:vm');
const assert = require('node:assert/strict');
const path = require('node:path');
const html = fs.readFileSync(path.join(__dirname, 'index.html'), 'utf8');
const nodes = {};
const buttons = [{disabled: false}, {disabled: false}];
let down = false;
let failCommand = false;
let slot = {slot: 0, host: '192.168.1.1', state: 'running', reachable: true,
  stream_generation: 'session-1', recording: false};
const context = {
  document: {
    getElementById(id) {
      return nodes[id] ||= {dataset: {}, style: {}, classList: {add() {}, remove() {}}};
    },
    querySelectorAll() { return buttons; },
  },
  setTimeout() {},
  async fetch(url) {
    if (down) throw new Error('Failed to fetch');
    if (url === '/api/status') {
      return {ok: true, async json() { return {slots: [slot], burst: {active: false}}; }};
    }
    if (!failCommand) slot.recording = url.endsWith('/start');
    return {ok: true, async json() {
      return {results: {'0': {ok: !failCommand, error: failCommand ? 'PTP failed' : undefined}}};
    }};
  },
};
vm.createContext(context);
vm.runInContext(html.split('<script>')[1].split('</script>')[0], context);
(async () => {
  await context.refresh();
  await context.post('/api/record/start');
  assert.match(nodes['rec-0'].innerHTML, /REC/);
  await context.post('/api/record/stop');
  assert.equal(nodes['rec-0'].innerHTML, '');
  failCommand = true;
  await context.post('/api/record/start');
  assert.match(nodes['command-error'].textContent, /PTP failed/);
  assert.ok(buttons.every(button => !button.disabled));
  slot = {...slot, state: 'reconnecting', reachable: false};
  await context.refresh();
  assert.equal(nodes['connection-status'].textContent, 'lost connection to camera');
  slot = {...slot, state: 'running', reachable: true, stream_generation: 'session-2'};
  await context.refresh();
  assert.equal(nodes['connection-status'].hidden, true);
  assert.match(nodes['preview-0'].src, /reconnect=/);
  nodes['preview-0'].src = 'unchanged';
  await context.refresh();
  assert.equal(nodes['preview-0'].src, 'unchanged');
  down = true;
  await context.refresh();
  assert.equal(nodes['status-error'].textContent, 'Server is down');
  down = false;
  await context.refresh();
  assert.equal(nodes['status-error'].hidden, true);
  assert.match(nodes['preview-0'].src, /reconnect=/);
  console.log('UI checks passed: Record/Stop, errors, camera recovery, server restart.');
})().catch(error => {console.error(error);process.exitCode = 1;});
