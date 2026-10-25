const $ = id => document.getElementById(id);
const cards = new Map();
let pendingCommand = null;
let connected = false, pending = false, timer, lastStatus, pollGeneration = 0, platform, activeConfig, retryTimer;
function scheduleReconnect() {
  clearTimeout(retryTimer);
  retryTimer = setTimeout(() => { if (!pending) void connectCamera(activeConfig, true); else scheduleReconnect(); }, 5000);
}
function showError(message = '') { $('error').textContent = message; $('error').hidden = !message; }
function setControls() {
  const slots = lastStatus?.slots || [];
  const ready = slots.filter(slot => slot.reachable !== false && (slot.kind === 'wifi' || slot.state === 'running'));
  const recording = slots.filter(slot => slot.recording);
  const allRecording = ready.length > 0 && ready.every(slot => slot.recording);
  const labels = { start: '● Запись', stop: '■ Стоп' };
  document.querySelectorAll('[data-command]').forEach(button => {
    const command = button.dataset.command;
    button.disabled = pending || !connected || (command !== 'stop' && (!ready.length || lastStatus?.burst.active || (command === 'start' && allRecording) || (command === 'burst' && recording.length > 0)));
    if (command === 'start') {
      button.textContent = pendingCommand === 'start' ? 'Запускаем…' : allRecording ? '● Идёт запись' : labels.start;
      button.setAttribute('aria-pressed', String(recording.length > 0));
      button.classList.toggle('is-recording', recording.length > 0);
    }
    if (command === 'stop') button.textContent = pendingCommand === 'stop' ? 'Останавливаем…' : labels.stop;
  });
  const indicator = $('recording-status');
  indicator.classList.toggle('active', recording.length > 0);
  indicator.classList.toggle('waiting', Boolean(pendingCommand));
  indicator.textContent = pendingCommand
    ? ({ start: 'Запускаем запись · ждём ответа камеры…', stop: 'Останавливаем запись · ждём ответа камеры…', burst: 'Запускаем Burst · ждём ответа камеры…' })[pendingCommand]
    : !connected ? 'Нет подключения' : recording.length ? `● Идёт запись${slots.length > 1 ? ` · камер: ${recording.length}/${slots.length}` : ''}` : '■ Запись остановлена';
  $('connect').disabled = pending;
}
function clearCards() { for (const card of cards.values()) card.node.remove(); cards.clear(); }
function render(status) {
  lastStatus = status;
  $('empty').hidden = status.slots.length > 0;
  for (const slot of status.slots) {
    let card = cards.get(slot.slot);
    if (!card) {
      const node = $('camera-template').content.firstElementChild.cloneNode(true);
      card = { node, image: node.querySelector('img'), failed: true, generation: null, stream: null };
      card.image.addEventListener('error', () => { card.failed = true; card.image.hidden = true; node.querySelector('.placeholder').hidden = false; });
      card.image.addEventListener('load', () => { card.image.hidden = false; node.querySelector('.placeholder').hidden = true; });
      cards.set(slot.slot, card);
      $('grid').append(node);
    }
    const { node, image } = card;
    node.querySelector('.camera-title').textContent = `Камера ${slot.slot + 1} · ${slot.host}`;
    const state = node.querySelector('.state');
    state.textContent = slot.recording ? '● Запись' : ({ starting: 'Подключение', running: 'В эфире', connecting: 'Подключение', reconnecting: 'Переподключение', disconnected: 'Нет связи' })[slot.state] || slot.state;
    state.classList.toggle('running', slot.state === 'running');
    node.querySelector('.meta').textContent = [slot.model, slot.firmware && `fw ${slot.firmware}`, slot.mode_name].filter(Boolean).join(' · ');
    node.querySelector('.fps').textContent = `${slot.fps_actual || 0} fps`;
    node.querySelector('.rec').hidden = !slot.recording;
    node.querySelector('.camera-error').textContent = slot.error || '';
    node.querySelector('.camera-error').hidden = !slot.error;
    const stream = `${status.url}video_feed/${slot.slot}`;
    if (slot.state === 'running' && slot.reachable !== false) {
      if (card.failed || card.generation !== slot.stream_generation || card.stream !== stream) {
        image.src = `${stream}?t=${Date.now()}`;
        image.hidden = false;
        node.querySelector('.placeholder').hidden = true;
        card.failed = false;
        card.generation = slot.stream_generation;
        card.stream = stream;
      }
    } else {
      image.removeAttribute('src'); image.hidden = true; card.failed = true;
      node.querySelector('.placeholder').hidden = false;
    }
  }
  const ids = new Set(status.slots.map(slot => slot.slot));
  for (const [id, card] of cards) if (!ids.has(id)) { card.node.remove(); cards.delete(id); }
  const lost = status.slots.filter(slot => slot.reachable === false);
  $('network-help').hidden = platform !== 'darwin' || !status.slots.some(slot => slot.reachable === false || /no route to host|operation not permitted|permission denied/i.test(slot.error || ''));
  $('connection').textContent = lost.length ? `Нет связи с камерой: ${lost.map(slot => slot.host).join(', ')} · повторное подключение…` : `Движок подключён · камер: ${status.slots.length}`;
  $('burst-progress').hidden = !status.burst.active;
  if (status.burst.active) {
    $('progress').value = Math.min(100, 100 * status.burst.elapsed_s / status.burst.duration_s);
    $('burst-label').textContent = `Burst · осталось ${Math.max(0, status.burst.duration_s - status.burst.elapsed_s).toFixed(1)} с`;
  }
  setControls();
}
async function poll(generation = pollGeneration) {
  if (!connected || generation !== pollGeneration) return;
  try {
    const status = await window.dicam.status();
    if (generation !== pollGeneration) return;
    render(status);
  }
  catch (error) {
    if (generation !== pollGeneration) return;
    connected = false; lastStatus = null;
    clearCards(); $('empty').hidden = false; $('burst-progress').hidden = true;
    $('connection').textContent = 'Движок недоступен · повторная попытка через 5 секунд'; showError(error.message); setControls();
    scheduleReconnect();
  }
  if (connected && generation === pollGeneration) timer = setTimeout(() => poll(generation), 500);
}
async function connectCamera(config, automatic = false) {
  if (pending) return;
  pending = true; pollGeneration++; clearTimeout(timer); clearTimeout(retryTimer); setControls(); showError();
  $('connection').textContent = 'Подключение…';
  try {
    await window.dicam.connect(config);
    activeConfig = config;
    clearCards(); connected = true;
  } catch (error) {
    showError(error.message); $('connection').textContent = 'Не удалось подключиться';
    if (automatic) scheduleReconnect();
  } finally { pending = false; setControls(); if (connected) void poll(); }
}
$('config-form').addEventListener('submit', event => {
  event.preventDefault();
  void connectCamera(Object.fromEntries(['cameras', 'bind', 'usb', 'recordDir'].map(id => [id, $(id).value])));
});
document.querySelectorAll('[data-command]').forEach(button => button.addEventListener('click', async () => {
  if (pending) return;
  pending = true; pendingCommand = button.dataset.command;
  pollGeneration++; clearTimeout(timer); setControls(); showError();
  try {
    const result = await window.dicam.command(button.dataset.command, Number(button.dataset.duration));
    const results = Object.entries(result.results || {});
    if (!results.length) throw new Error('Нет камер для записи');
    const failures = results.filter(([, value]) => !value.ok);
    if (failures.length) throw new Error(failures.map(([id, value]) => `Камера ${Number(id) + 1}: ${value.error || 'команда не выполнена'}`).join('; '));
  } catch (error) { showError(error.message); }
  finally {
    // Refresh before releasing the buttons, without racing an older status poll.
    if (connected) await poll();
    pending = false; pendingCommand = null; setControls();
  }
}));
$('settings-toggle').addEventListener('click', () => { $('settings').hidden = !$('settings').hidden; $('settings-toggle').setAttribute('aria-expanded', String(!$('settings').hidden)); });
$('choose-directory').addEventListener('click', async () => { try { const dir = await window.dicam.chooseDirectory(); if (dir) $('recordDir').value = dir; } catch (error) { showError(error.message); } });
$('open-recordings').addEventListener('click', async () => { try { await window.dicam.openRecordings(); } catch (error) { showError(error.message); } });
$('network-settings').addEventListener('click', () => window.dicam.openNetworkSettings().catch(error => showError(error.message)));
window.dicam.settings().then(({ config, platform: os, error }) => {
  platform = os;
  activeConfig = config;
  for (const [key, value] of Object.entries(config)) $(key).value = value;
  $('usb-settings').hidden = platform !== 'linux';
  void connectCamera(config, true).then(() => { if (error) showError(error); });
}).catch(error => showError(error.message));
