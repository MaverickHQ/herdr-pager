// The setup page's script (phase 5 design D5P-3; the steps of requirement
// 2.1, the live output of 2.2, Manage of 2.3, flashing of 2.4). It builds
// every element with textContent and append, never from text as markup, so
// whatever the bridge, the pager or the Mac says is shown only as text
// (requirement 2.5). A Wi-Fi password lives only in this page's memory and
// in the one request that pairs: never in a URL, the page's storage or its
// output (requirement 2.6).
(() => {
'use strict';

/* ---------- helpers ---------- */
const $ = (s, r = document) => r.querySelector(s);
const $$ = (s, r = document) => Array.from(r.querySelectorAll(s));
const p2 = n => String(n).padStart(2, '0');
const hm = () => { const d = new Date(); return `${p2(d.getHours())}:${p2(d.getMinutes())}`; };
const hms = () => { const d = new Date(); return `${hm()}:${p2(d.getSeconds())}`; };
/* The page's own preferences only (the dock's size): never a password. */
const prefs = {
  get(k){ try { return localStorage.getItem('herdr-pager-setup:' + k); } catch (e) { return null; } },
  set(k, v){ try { localStorage.setItem('herdr-pager-setup:' + k, v); } catch (e) { /* private browsing */ } },
};

/* h makes an element: props set its class, text, properties and attributes,
   and on… props add listeners; each child is an element or text. */
function h(tag, props, ...kids){
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(props || {})) {
    if (v == null || v === false) continue;
    if (k === 'class') el.className = v;
    else if (k === 'text') el.textContent = v;
    else if (k.startsWith('on') && typeof v === 'function') el.addEventListener(k.slice(2), v);
    else if (k === 'value' || k === 'checked' || k === 'disabled' || k === 'readOnly') el[k] = v;
    else el.setAttribute(k, v === true ? '' : String(v));
  }
  return add(el, kids);
}
function add(el, kids){
  for (const k of kids.flat(Infinity)) if (k != null && k !== false && k !== '') el.append(typeof k === 'number' ? String(k) : k);
  return el;
}
const code = s => h('code', { text: s });
const b = s => h('b', { class: 'strong', text: s });
const keyName = s => h('span', { class: 'key', text: s });
const spin = () => h('span', { class: 'spin', 'aria-hidden': 'true', text: '|' });
const plural = (n, one, many) => `${n} ${n === 1 ? one : many}`;
const shortFP = fp => (fp || '').length > 18 ? fp.slice(0, 11) + '…' + fp.slice(-4) : (fp || '');

function fpEl(fp){
  const body = fp.startsWith('SHA256:') ? fp.slice(7) : fp;
  return h('code', { class: 'fp' }, h('span', { class: 'fp-pre', text: 'SHA256:' }),
    h('b', { text: body.slice(0, 4) }), body.slice(4, -4), h('b', { text: body.slice(-4) }));
}

async function copyText(text, btn){
  let ok = false;
  try { await navigator.clipboard.writeText(text); ok = true; } catch (e) { ok = false; }
  if (btn) flashLabel(btn, ok ? 'Copied' : 'Couldn’t copy');
}
function flashLabel(btn, text){
  const was = btn.dataset.label || btn.textContent; btn.dataset.label = was;
  btn.textContent = text; clearTimeout(btn.flashTimer);
  btn.flashTimer = setTimeout(() => { btn.textContent = was; }, 1300);
}
function copyBtn(text, label){
  return h('button', { class: 'btn xs', type: 'button', text: label || 'Copy', onclick: e => copyText(text, e.currentTarget) });
}

/* ---------- the bridge's API ---------- */
async function api(method, path, body){
  const init = { method, credentials: 'same-origin', cache: 'no-store', headers: {} };
  if (body !== undefined) { init.headers['Content-Type'] = 'application/json'; init.body = JSON.stringify(body); }
  let resp;
  try { resp = await fetch(path, init); } catch (e) { checkAlive(); throw new Error('The setup page didn’t answer.'); }
  if (resp.status === 403) { stopped(); throw new Error('The setup page refused the request. Run pager-bridge setup again.'); }
  let data = null;
  try { data = await resp.json(); } catch (e) { data = null; }
  if (!resp.ok) {
    const err = new Error((data && data.error) || `${method} ${path}: ${resp.status}`);
    err.status = resp.status;
    err.data = data;
    throw err;
  }
  return data;
}

/* ---------- state ---------- */
const S = {
  view: 'setup', step: 'mac', stopped: false,
  doctor: null, doctorBusy: false, doctorErr: '', fwSeen: false,
  keys: null, keysMade: false,
  fw: null,
  flash: 'idle',       // idle | dialog | closed | done | skipped
  ewtFor: '',          // which view opened ESP Web Tools' dialog
  upd: 'idle',         // Manage's Update: idle | dialog | closed
  wifi: [{ ssid: '', pass: '' }], known: [], near: [], wifiDone: false,
  host: { mode: 'local', ip: '' },
  ports: [], port: '',
  pair: { st: 'idle', name: 'pager-01', again: '', offer: null, result: null, err: '' },
  join: { st: 'idle', says: '', network: '' }, // after pairing: watching | joined | refused | not_found | failed | unknown | error
  pagerFW: '',
  devices: null, running: false, devErr: '',
  login: { busy: false, err: '' },
  m: { confirm: '', repair: '', fpOpen: {} },
};
const nets = () => S.wifi.filter(n => n.ssid.trim());
const check = id => (S.doctor && S.doctor.checks ? S.doctor.checks.find(c => c.id === id) : null) || null;
const loginOn = () => { const c = check('login'); return !!c && (c.state === 'brew' || c.state === 'launchd'); };
const pagerName = () => (S.pair.result && S.pair.result.paired) || S.pair.again || S.pair.name;
function theDevice(){
  const ds = S.devices || [];
  return ds.find(d => d.name === pagerName()) || ds.find(d => d.connected) || ds[0] || null;
}
const connected = () => { const d = theDevice(); return !!(d && d.connected && S.running); };
const hostShown = () => S.pair.offer ? S.pair.offer.host : (S.host.mode === 'ip' && S.host.ip.trim() ? S.host.ip.trim() : 'this Mac’s .local name');

async function runDoctor(){
  if (S.doctorBusy) return;
  S.doctorBusy = true; S.doctorErr = ''; refresh();
  try {
    let r = await api('GET', '/api/doctor');
    const keys = (r.checks || []).find(c => c.id === 'keys');
    if (keys && keys.state === 'missing') {
      S.keys = await api('POST', '/api/keys', {});
      S.keysMade = true;
      r = await api('GET', '/api/doctor');
    }
    S.doctor = r;
    const fw = check('firewall');
    if (fw && (fw.state === 'blocking' || fw.state === 'block-all')) S.fwSeen = true;
  } catch (e) { S.doctorErr = e.message; }
  S.doctorBusy = false; refresh();
}
async function loadFirmware(){
  try { S.fw = await api('GET', '/api/firmware'); } catch (e) { S.fw = { available: false, problem: e.message, fix: '' }; }
  $('#chip-fw').textContent = S.fw.available ? `firmware ${S.fw.version}` : 'no firmware';
}
async function loadPorts(){
  const was = S.ports.join('\n');
  try { S.ports = (await api('GET', '/api/ports')).ports || []; } catch (e) { S.ports = []; }
  if (!S.ports.includes(S.port)) S.port = S.ports[0] || '';
  return was !== S.ports.join('\n');
}
async function loadDevices(){
  const was = JSON.stringify([S.devices, S.running, S.devErr]);
  try { const d = await api('GET', '/api/devices'); S.devices = d.devices || []; S.running = !!d.running; S.serve = d.serve || 'pager-bridge serve'; S.devErr = ''; }
  catch (e) { S.devErr = e.message; }
  return was !== JSON.stringify([S.devices, S.running, S.devErr]);
}

/* ---------- the pager's screens ---------- */
const SCR = {
  off:         { type: 'off', label: 'Not plugged in' },
  rom:         { type: 'off', label: 'Download mode' },
  pairViaUsb:  { type: 'img', img: 'assets/screens/boot_unpaired.png', label: 'PAIR VIA USB' },
  pairConfirm: { type: 'pair', label: 'PAIR WITH THIS MAC?' },
  connecting:  { type: 'center', band: '~', l1: 'CONNECTING', label: 'CONNECTING' },
  noBridge:    { type: 'img', img: 'assets/screens/no_bridge.png', patch: { cls: 'pa', text: n => `${hm()}  ~ ${n}` }, label: 'NO BRIDGE ON THIS NETWORK' },
  list:        { type: 'img', img: 'assets/screens/demo_list.png', label: 'CONNECTED' },
  paged:       { type: 'img', img: 'assets/screens/demo_paged.png', label: 'Beep beep' },
  empty:       { type: 'img', img: 'assets/screens/list_empty.png', patch: { cls: 'pa', text: n => `${hm()}  o ${n}` }, label: 'NO AGENTS' },
  notAuth:     { type: 'img', img: 'assets/screens/not_authorised.png', patch: { cls: 'pb', rule: true, text: n => `!0 *0 >0  ${hm()}  x ${n}` }, label: 'NOT AUTHORISED' },
  hostKey:     { type: 'img', img: 'assets/screens/host_key_changed.png', patch: { cls: 'pb', rule: true, text: n => `!0 *0 >0  ${hm()}  # ${n}` }, label: 'HOST KEY CHANGED' },
  pairAgain:   { type: 'img', img: 'assets/screens/confirm_pair_again.png', patch: { cls: 'pc', text: n => `${hm()}  o ${n}` }, label: 'PAIR AGAIN?' },
};
const netName = () => (nets()[0] ? nets()[0].ssid : 'your Wi-Fi');

function band(left, hints){
  return h('div', { class: 'band' }, h('div', { class: 'band-left', text: left }),
    h('div', { class: 'hint pwr', text: hints[0] }), h('div', { class: 'hint boot', text: hints[1] }));
}
function screenEl(key){
  const s = SCR[key];
  if (!s || s.type === 'off') return h('div', { class: 'lcd-off' });
  if (s.type === 'img') {
    return h('div', { class: 'lcd' }, h('img', { src: s.img, alt: `The pager’s screen: ${s.label}` }),
      s.patch && h('div', { class: 'patch ' + s.patch.cls + (s.patch.rule ? ' rule' : ''), text: s.patch.text(netName()) }));
  }
  if (s.type === 'center') {
    return h('div', { class: 'lcd', role: 'img', 'aria-label': `The pager’s screen: ${s.l1}` },
      band(`!0 *0 >0  ${hm()}  ${s.band} ${netName()}`, ['INFO', 'HOLD:\nMENU']),
      h('div', { class: 'c28', text: s.l1 }), h('div', { class: 'c20', text: `to the bridge at ${hostShown()}` }));
  }
  const o = S.pair.offer;
  const host = o ? o.host_name : 'this Mac';
  const rest = fp => (fp || '').replace(/^SHA256:/, '');
  return h('div', { class: 'lcd', role: 'img', 'aria-label': `The pager’s screen: pair with ${host}, with both fingerprints` },
    band('', ['CANCEL', 'PAIR']),
    h('div', { class: 't20 r1', text: `PAIR WITH ${host}?` }),
    h('div', { class: 't20 r2' }, 'HOST SHA256:', h('br'), rest(o && o.bridge.fingerprint)),
    h('div', { class: 't20 r3' }, 'DEV SHA256:', h('br'), rest(o && o.pager.fingerprint)));
}
function deviceEl(key, armed){
  const dark = !SCR[key] || SCR[key].type === 'off';
  return h('div', { class: 'device' + (dark ? ' is-dark' : '') + (armed ? ' armed' : '') },
    h('div', { class: 'device-glow' }),
    h('div', { class: 'bezel' },
      h('div', { class: 'nubs', 'aria-hidden': 'true' },
        h('span', { class: 'nub pwr' }, h('span', { text: 'PWR' })),
        h('span', { class: 'nub boot' }, h('span', { text: 'BOOT' }))),
      h('div', { class: 'screen' }, screenEl(key))));
}

/* ---------- the live output ---------- */
const TERM = { bridge: [], pager: [], step: [], active: 'step', follow: true, unread: {} };
const MAX_LINES = 2000;
const EMPTY = {
  bridge: 'The bridge has logged nothing since this page opened. Its log shows here while it runs.',
  pager: 'The pager’s own lines show here while the bridge holds its USB port, when pairing. While it’s flashed, ESP Web Tools’ dialog shows them.',
  step: 'Nothing has run yet.',
};
function lineClass(ch, text){
  if (ch === 'bridge') { const m = /\blevel=(INFO|WARN|ERROR)\b/.exec(text); return m ? m[1] : ''; }
  if (ch === 'pager') { const m = /^([IWE]) \(\d+\)/.exec(text); return m ? m[1] : ''; }
  if (/^FAIL\b|failed|refused/i.test(text)) return 'E';
  if (/^(ok\b|Paired\b|Revoked\b)/.test(text)) return 'ok';
  return '';
}
function lineEl(ln){
  return h('div', { class: 'ln ' + ln.cls }, h('span', { class: 'ts', text: ln.t }), h('span', { class: 'm', text: ln.text }));
}
function addLine(ch, text){
  const ln = { t: hms(), text, cls: lineClass(ch, text) };
  const buf = TERM[ch];
  buf.push(ln);
  if (buf.length > MAX_LINES) buf.splice(0, buf.length - MAX_LINES);
  if (TERM.active === ch) {
    const term = $('#term');
    const empty = $('.term-empty', term); if (empty) empty.remove();
    term.append(lineEl(ln));
    while (term.childElementCount > MAX_LINES) term.firstElementChild.remove();
    if (TERM.follow) term.scrollTop = term.scrollHeight;
  } else { TERM.unread[ch] = true; renderTermTabs(); }
  $('#dock-last').textContent = `${ln.t} ${text}`;
}
const say = text => addLine('step', text); // the page's own lines, beside the bridge's
function renderTerm(){
  const term = $('#term'), buf = TERM[TERM.active];
  term.replaceChildren(...(buf.length ? buf.map(lineEl) : [h('div', { class: 'term-empty', text: EMPTY[TERM.active] })]));
  if (TERM.follow) term.scrollTop = term.scrollHeight;
}
function renderTermTabs(){
  $$('.ttab').forEach(btn => {
    const t = btn.dataset.tt, on = TERM.active === t;
    btn.setAttribute('aria-selected', String(on));
    const badge = $('.badge', btn);
    if (TERM.unread[t] && !on) { if (!badge) btn.append(h('span', { class: 'badge', 'aria-label': 'new lines' })); }
    else if (badge) badge.remove();
  });
}
function setFollow(on){
  TERM.follow = on;
  $('#t-follow').setAttribute('aria-pressed', String(on));
  if (on) { const term = $('#term'); term.scrollTop = term.scrollHeight; }
}
function showTerm(ch){ TERM.active = ch; TERM.unread[ch] = false; renderTermTabs(); renderTerm(); setDockMin(false); }
function setDockMin(min){
  document.body.classList.toggle('dock-min', min);
  const btn = $('#t-min');
  btn.textContent = min ? '▴' : '▾';
  btn.setAttribute('aria-expanded', String(!min));
  btn.title = min ? 'Show the output' : 'Hide the output';
  prefs.set('dock-min', min ? '1' : '0');
  if (!min) renderTerm();
}
function initTerm(){
  $$('.ttab').forEach(btn => btn.addEventListener('click', () => showTerm(btn.dataset.tt)));
  $('#t-follow').addEventListener('click', () => setFollow(!TERM.follow));
  $('#term').addEventListener('scroll', () => {
    const term = $('#term'), atEnd = term.scrollHeight - term.scrollTop - term.clientHeight < 24;
    if (!atEnd && TERM.follow) setFollow(false); else if (atEnd && !TERM.follow) setFollow(true);
  });
  $('#t-copy').addEventListener('click', e => copyText(TERM[TERM.active].map(l => `${l.t} ${l.text}`).join('\n'), e.currentTarget));
  $('#t-min').addEventListener('click', () => setDockMin(!document.body.classList.contains('dock-min')));
  const saved = prefs.get('dock-min');
  setDockMin(saved == null ? window.innerWidth < 560 : saved === '1');
  renderTermTabs();
}

/* The event stream: one connection, three channels, each event one line of
   text as JSON (design §The setup page, Live output). While it's open, the
   page keeps running. */
let stream = null;
function startEvents(){
  stream = new EventSource('/events');
  for (const ch of ['step', 'bridge', 'pager']) {
    stream.addEventListener(ch, e => {
      let d = null;
      try { d = JSON.parse(e.data); } catch (err) { return; }
      if (d && typeof d.text === 'string') addLine(ch, d.text);
    });
  }
  stream.addEventListener('open', () => setLive(true));
  stream.addEventListener('error', () => { setLive(false); checkAlive(); });
}
function setLive(on){ $('#live').classList.toggle('off', !on); $('#live b').textContent = on ? 'LIVE' : 'OFFLINE'; }
async function checkAlive(){
  try {
    const r = await fetch('/api/firmware', { credentials: 'same-origin', cache: 'no-store' });
    if (r.status === 403) stopped();
  } catch (e) { stopped(); }
}
function stopped(){
  if (S.stopped) return;
  S.stopped = true;
  $('#stopped').hidden = false;
  $('#chip-dot').classList.add('off');
  setLive(false);
  if (stream) stream.close();
}

/* ---------- flashing, with ESP Web Tools (D5P-4) ---------- */
const hasSerial = 'serial' in navigator;
function flasher(manifest, label, cls){
  if (!hasSerial) {
    return h('p', { class: 'problem', text: 'This browser can’t flash: it has no WebSerial. Open the address pager-bridge setup printed in Chrome or Edge.' });
  }
  const el = document.createElement('esp-web-install-button');
  el.setAttribute('manifest', manifest);
  /* A first install erases the pager's key, so its pairing goes (the owner's
     recheck of 7.2): with a pager paired, ask first. Stopped here, the click
     never reaches ESP Web Tools' slot. */
  const ask = e => {
    if (manifest !== '/firmware/manifest.json' || !pagersPaired()) return;
    const names = S.devices.map(d => d.name).join(', ');
    if (!window.confirm(`Install erases this pager, and it forgets its pairing: it gets a new key, and its entry in devices.toml (${names}) stops working. To update a paired pager and keep its pairing, cancel, and use Update in Manage. Erase it and install?`)) {
      e.preventDefault(); e.stopPropagation();
    }
  };
  el.append(h('button', { class: cls || 'btn primary', type: 'button', slot: 'activate', text: label, onclick: ask }));
  el.append(h('span', { class: 'problem', slot: 'unsupported', text: 'This browser can’t flash. Open this page in Chrome or Edge.' }));
  el.append(h('span', { class: 'problem', slot: 'not-allowed', text: 'This page can’t flash from here: open the address pager-bridge setup printed, on 127.0.0.1.' }));
  return el;
}
/* What Chrome's prompt calls the board (the owner's checkpoint 7.2). */
function pickPort(){
  return h('span', {}, 'The browser asks ', b(`Allow ${location.host} to access your serial ports?`), ' and lists the board as ', b('USB JTAG/serial debug unit'), ': pick it, and press ', b('Connect'), '.');
}
/* ESP Web Tools adds its dialog to the page while it works, and takes it
   away when it's closed, which also closes its port, so the bridge can open
   it to pair. */
function watchFlashing(){
  new MutationObserver(changes => {
    for (const c of changes) {
      for (const n of c.addedNodes) if (n.nodeName === 'EWT-INSTALL-DIALOG') flashOpened();
      for (const n of c.removedNodes) if (n.nodeName === 'EWT-INSTALL-DIALOG') flashClosed();
    }
  }).observe(document.body, { childList: true });
}
function flashOpened(){
  S.ewtFor = S.view;
  if (S.view === 'manage') S.upd = 'dialog'; else S.flash = 'dialog';
  say('ESP Web Tools has the pager’s port. Its dialog shows the flashing and the pager’s log.');
  refresh();
}
function flashClosed(){
  if (S.ewtFor === 'manage') S.upd = 'closed'; else if (S.flash === 'dialog') S.flash = 'closed';
  say('ESP Web Tools closed the pager’s port.');
  loadPorts().then(refresh);
}

/* ---------- steps (requirement 2.1) ---------- */
const STEPS = [
  { id: 'mac', name: 'Check the Mac' },
  { id: 'flash', name: 'Flash the pager' },
  { id: 'wifi', name: 'Wi-Fi' },
  { id: 'pair', name: 'Pair' },
  { id: 'firewall', name: 'Firewall', when: () => S.fwSeen },
  { id: 'login', name: 'Run at login' },
  { id: 'first', name: 'First page' },
];
const visible = () => STEPS.filter(s => !s.when || s.when());
const curIndex = () => visible().findIndex(s => s.id === S.step);

function stepState(id){
  const paired = S.pair.st === 'paired';
  switch (id) {
    case 'mac': {
      const cs = ['herdr', 'keys', 'port'].map(check);
      if (cs.some(c => c && !c.ok)) return 'need';
      return cs.every(c => c && c.ok) ? 'done' : 'todo';
    }
    case 'flash': return S.flash === 'done' || paired ? 'done' : 'todo';
    case 'wifi': return S.wifiDone || paired ? 'done' : 'todo';
    case 'pair': return paired ? 'done' : (S.pair.st === 'failed' || S.pair.st === 'cancelled' ? 'need' : 'todo');
    case 'firewall': { const c = check('firewall'); return c && c.ok ? 'done' : (c ? 'need' : 'todo'); }
    case 'login': return loginOn() ? 'done' : 'opt';
    case 'first': return connected() ? 'done' : 'todo';
  }
  return 'todo';
}

function renderRail(){
  const vs = visible();
  const done = vs.filter(s => stepState(s.id) === 'done').length;
  $('#rail-count').textContent = `${done} of ${vs.length} done`;
  $('#vt-count').textContent = `${done}/${vs.length}`;
  $('#rail-list').replaceChildren(...vs.map((s, i) => {
    const st = stepState(s.id), cur = s.id === S.step;
    let glyph = '.', gcls = 'todo', word = st === 'opt' ? 'OPTIONAL' : 'TO DO';
    if (st === 'done') { glyph = '*'; gcls = 'done'; word = s.id === 'login' ? 'ON' : 'DONE'; }
    if (st === 'need') { glyph = '!'; gcls = 'need'; word = 'NEEDS YOU'; }
    if (cur && st !== 'done' && st !== 'need') { glyph = '>'; gcls = 'now'; word = 'NOW'; }
    return h('li', {}, h('button', { class: `rail-row is-${st}` + (cur ? ' is-current' : ''), type: 'button', 'aria-current': cur ? 'step' : null, onclick: () => go(s.id) },
      h('span', { class: 'g ' + gcls, 'aria-hidden': 'true', text: glyph }),
      h('span', { class: 'nm' }, h('span', { class: 'num', text: String(i + 1) }), s.name),
      h('span', { class: 'st', text: word })));
  }));
}

function head(title, lede){
  const vs = visible();
  return [h('p', { class: 'kicker', text: `Step ${curIndex() + 1} of ${vs.length}` }), h('h2', { text: title }),
    lede && h('p', { class: 'lede' }, lede)];
}
function foot(){
  const vs = visible(), i = curIndex();
  /* From Check the Mac, with a pager paired, the way on is Manage, not a
     first install (pairedLead offers that). */
  const prev = vs[i - 1], next = S.step === 'mac' && !firstInstall() ? null : vs[i + 1];
  const done = stepState(S.step) === 'done';
  return h('div', { class: 'step-foot' },
    prev ? h('button', { class: 'btn', type: 'button', onclick: () => go(prev.id) }, h('span', { class: 'arr', text: '←' }), ' Back') : h('span'),
    h('span', { class: 'spacer' }),
    next
      ? h('button', { class: 'btn' + (done || S.step === 'wifi' ? ' primary' : ''), type: 'button', onclick: () => go(next.id) }, `Next: ${next.name} `, h('span', { class: 'arr', text: '→' }))
      : h('button', { class: 'btn' + (done ? ' primary' : ''), type: 'button', onclick: () => setView('manage') }, 'Open Manage ', h('span', { class: 'arr', text: '→' })));
}
function banner(...kids){ return h('div', { class: 'banner' }, h('span', { class: 'g need', text: '!' }), h('span', {}, kids)); }

const CHECK_NAMES = { herdr: 'herdr’s socket', keys: 'The bridge’s keys', port: 'The bridge’s port', firewall: 'The firewall', login: 'Run at login', devices: 'The paired pagers' };
function cmdList(cmds){
  return h('div', { class: 'cmds' }, cmds.map((c, i) => h('div', { class: 'cmd' },
    h('div', { class: 'cmd-head' }, h('span', { class: 'n', text: String(i + 1) }), h('span', { class: 'cmd-what', text: 'In Terminal' }), copyBtn(c)),
    h('pre', {}, h('span', { class: 'p', text: '$ ' }), c))),
    cmds.length > 1 ? h('div', {}, copyBtn(cmds.join('\n'), 'Copy them all')) : null);
}
function checkRow(c, id){
  if (!c) {
    return h('div', { class: 'check' }, h('span', { class: 'g ' + (S.doctorBusy ? 'run' : 'idle'), text: S.doctorBusy ? '|' : '.' }),
      h('div', {}, h('div', { class: 'check-title', text: CHECK_NAMES[id] || id }), h('div', { class: 'check-detail', text: S.doctorBusy ? 'Checking…' : 'Not checked yet.' })),
      h('span', { class: 'code ' + (S.doctorBusy ? 'run' : 'todo'), text: S.doctorBusy ? 'checking' : '—' }));
  }
  return h('div', { class: 'check' },
    h('span', { class: 'g ' + (c.ok ? 'done' : 'need'), text: c.ok ? '*' : '!' }),
    h('div', {},
      h('div', { class: 'check-title', text: c.name }),
      h('div', { class: 'check-detail', text: c.detail }),
      (c.devices || []).map(d => h('div', { class: 'check-detail' }, `${d.name}: ${d.connected ? 'connected' : 'not connected'} · `, code(d.fingerprint))),
      c.fix ? h('div', { class: 'check-fix', text: c.fix }) : null,
      c.commands && c.commands.length ? cmdList(c.commands) : null),
    h('span', { class: 'code ' + (c.ok ? 'ok' : 'need'), text: c.state }));
}

/* step 1 */
function tplMac(){
  return [
    head('Check the Mac', 'Three things have to be right on this Mac before a pager can talk to herdr. Setup checks them as this page opens, and again whenever you ask.'),
    S.doctorErr ? h('p', { class: 'problem', text: S.doctorErr }) : null,
    h('div', { class: 'checks' }, ['herdr', 'keys', 'port'].map(id => checkRow(check(id), id))),
    S.keys ? h('p', { class: 'fine mt12' }, S.keysMade ? 'Made just now, as pager-bridge init makes them: the bridge’s host key ' : 'The bridge’s host key: ', code(S.keys.fingerprint), '.') : null,
    h('div', { class: 'row-actions' }, h('button', { class: 'btn sm', type: 'button', disabled: S.doctorBusy, text: S.doctorBusy ? 'Checking…' : 'Check again', onclick: runDoctor })),
    firstInstall() ? null : pairedLead(),
    h('div', { class: 'note' }, h('span', { class: 'note-k', text: 'LOCAL ONLY' }),
      h('p', {}, 'This page is served by ', code('pager-bridge setup'), ' on 127.0.0.1, behind a one-time token. Nothing on it leaves this Mac, except what goes down the USB cable to the pager.')),
    foot(),
  ];
}

/* step 2 */
function pagersPaired(){ return !!(S.devices && S.devices.length); }
/* With a pager paired, a first install is a choice for a new or reset pager
   (the owner's second recheck of 7.2): following the steps, the owner erased
   a paired pager twice to move it to new firmware. */
function firstInstall(){ return !pagersPaired() || !!S.newPager; }
function newPager(){ S.newPager = true; setView('setup'); go('flash'); }
/* Where Check the Mac and the flash step lead while pagers are paired. */
function pairedLead(){
  const ds = S.devices || [];
  return h('div', { class: 'result need mt14' }, h('span', { class: 'rk', text: 'PAIRED' }), h('div', {},
    h('p', {}, `This Mac has ${ds.length === 1 ? 'a pager' : `${ds.length} pagers`} paired (${ds.map(d => d.name).join(', ')}). For a paired pager, use Manage: `,
      b('Update'), ' puts this bridge’s firmware on it and keeps its pairing; ', b('Change the Wi-Fi networks'), ' pairs it again under the same name; and you can revoke the ones you no longer use.'),
    h('p', { class: 'mt6' }, 'A first install erases a pager: its key, its pairing and its networks. Choose it only for a new pager, or one that was reset.'),
    h('div', { class: 'row-actions' },
      h('button', { class: 'btn sm primary', type: 'button', onclick: () => setView('manage') }, 'Open Manage ', h('span', { class: 'arr', text: '→' })),
      h('button', { class: 'btn sm', type: 'button', text: 'Set up a new or reset pager', onclick: () => newPager() }))));
}
/* Above the flash step's Install button whenever devices.toml lists a pager
   (the owner's recheck of 7.2): the owner moved a paired pager to a new
   version here, and it was erased. */
function eraseWarning(){
  if (!pagersPaired()) return null;
  return banner(b('This erases the pager and forgets its pairing. To update it and keep the pairing, use Update in Manage.'), ' ',
    h('button', { class: 'btn xs', type: 'button', text: 'Open Manage', onclick: () => setView('manage') }));
}
function tplFlash(){
  const fw = S.fw, ok = !!(fw && fw.available);
  const again = () => flasher('/firmware/manifest.json', 'Flash it again', 'btn sm');
  let action;
  if (fw && !ok) {
    action = h('div', { class: 'problem' }, h('p', { text: `No firmware to flash: ${fw.problem || 'none found'}.` }), fw.fix ? h('p', { class: 'mt6', text: fw.fix }) : null);
  } else if (S.flash === 'dialog' && S.ewtFor === 'setup') {
    action = h('div', { class: 'spinline mt18' }, spin(), h('span', { text: 'ESP Web Tools is open: follow its dialog. When it says the installation is complete, close it, so the bridge can use the port to pair.' }));
  } else if (S.flash === 'closed') {
    action = h('div', { class: 'result need' }, h('span', { class: 'rk', text: 'FLASHED?' }), h('div', {},
      h('p', {}, 'Once it’s flashed, the pager restarts and shows ', b('PAIR VIA USB'), '. Leave it plugged in: pairing uses the same cable.'),
      h('div', { class: 'row-actions' },
        h('button', { class: 'btn sm primary', type: 'button', text: 'It shows PAIR VIA USB', onclick: () => { S.flash = 'done'; say('The pager shows PAIR VIA USB: flashed.'); go('wifi'); } }),
        again())));
  } else if (S.flash === 'done') {
    action = h('div', { class: 'result ok' }, h('span', { class: 'rk', text: 'FLASHED' }), h('div', {},
      h('p', { text: `It runs the firmware this bridge ships${ok ? `, ${fw.version}` : ''}, and waits to be paired, two steps on.` }),
      h('div', { class: 'row-actions' }, again())));
  } else if (!firstInstall()) {
    action = pairedLead();
  } else {
    action = h('div', { class: 'row-actions' },
      flasher('/firmware/manifest.json', ok ? `Connect and flash ${fw.version}` : 'Connect and flash'),
      hasSerial ? h('span', { class: 'fine' }, pickPort()) : null);
  }
  return [
    head('Flash the pager', 'Put the firmware that ships with this bridge onto the board. It takes about a minute, and the board stays plugged in for pairing.'),
    h('div', { class: 'fwcard' }, h('div', { class: 'fw-badge', text: 'FW' }),
      h('div', {}, h('div', { class: 'fw-name', text: ok ? `herdr-pager firmware ${fw.version}` : 'herdr-pager firmware' }),
        h('div', { class: 'fw-meta', text: fw ? `ships with this bridge · ${fw.dir || 'no folder given'}` : 'Looking for it…' })),
      h('span', { class: 'code ' + (ok ? 'ok' : 'need'), text: ok ? 'on this Mac' : (fw ? 'not found' : '…') })),
    h('ol', { class: 'howto' },
      h('li', {}, h('span', { text: 'Plug the board into this Mac with a USB-C cable that carries data. A charge-only cable shows no port.' })),
      h('li', {}, h('span', {}, 'Press ', b('Connect and flash'), '. ', pickPort(), ' Then press ', b('Install'), ' in the dialog.')),
      h('li', {}, h('span', { text: 'A first install erases the board, Waveshare’s demo included, and a pager that was paired forgets it. To update a paired pager and keep its pairing, use Update in Manage.' }))),
    firstInstall() ? eraseWarning() : null,
    action,
    h('p', { class: 'fine mt16' }, 'Flashing runs in this browser over WebSerial, with ESP Web Tools, which shows the pager’s log while it flashes. The firmware comes from the bridge on this Mac: nothing is downloaded. Already flashed? ',
      h('button', { class: 'btn xs', type: 'button', text: 'Skip to Wi-Fi', onclick: () => { if (S.flash !== 'done') S.flash = 'skipped'; go('wifi'); } })),
    foot(),
  ];
}

/* step 3 */
const nearMiss = ssid => S.near.find(m => m.given === ssid) || null;
function noteFor(n){
  const ssid = n.ssid;
  if (!ssid.trim()) return null;
  const miss = nearMiss(ssid);
  if (miss) {
    return { warn: true, kids: [h('span', { class: 'g need', text: '!' }), h('div', {}, 'Not the same name. This Mac knows ', h('span', { class: 'q', text: `“${miss.known}”` }), ', and the pager joins only the exact name: spelling, case and apostrophes all count.',
      h('div', { class: 'fix' }, h('button', { class: 'btn xs', type: 'button', text: 'Use the Mac’s spelling', onclick: () => { n.ssid = miss.known; say(`A network’s name is now “${miss.known}”, as this Mac spells it.`); renderNets(); checkWifi(); } })))] };
  }
  if (S.known.includes(ssid)) return { warn: false, kids: [h('span', { class: 'g done', text: '*' }), h('div', { text: 'This Mac has joined it before.' })] };
  if (/[-_ ]?5\s?g(hz)?$/i.test(ssid)) return { warn: true, kids: [h('span', { class: 'g need', text: '!' }), h('div', { text: 'That sounds like a 5 GHz network. The pager joins only 2.4 GHz.' })] };
  return { warn: false, kids: [h('span', { class: 'g idle', text: '.' }), h('div', { text: 'This Mac hasn’t saved it. That’s fine if it’s somewhere else.' })] };
}
function updateNote(i){
  const n = S.wifi[i], note = noteFor(n), el = $('#note-' + i), input = $('#ssid-' + i);
  if (el) { el.className = 'net-note' + (note && note.warn ? ' warn' : ''); el.replaceChildren(...(note ? note.kids : [])); }
  if (input) input.classList.toggle('warn', !!(note && note.warn));
}
let wifiTimer = null;
function scheduleWifiCheck(){ clearTimeout(wifiTimer); wifiTimer = setTimeout(checkWifi, 400); }
async function checkWifi(){
  try {
    const r = await api('POST', '/api/wifi/check', { ssids: nets().map(n => n.ssid) });
    S.known = r.known || []; S.near = r.near_misses || [];
  } catch (e) { S.near = []; }
  S.wifi.forEach((n, i) => updateNote(i));
}
function renderNets(){
  const ol = $('#nets'); if (!ol) return;
  ol.replaceChildren(...S.wifi.map((n, i) => {
    const pass = h('input', { class: 'input', type: 'password', value: n.pass, autocomplete: 'off', placeholder: S.pair.again ? 'type it again' : 'blank if open', 'aria-label': `Network ${i + 1}’s password`, oninput: e => { n.pass = e.target.value; } });
    return h('li', { class: 'net' },
      h('div', { class: 'net-rank', 'aria-label': `Choice ${i + 1}`, text: String(i + 1) }),
      h('div', { class: 'net-fields' },
        h('label', { class: 'field' }, h('span', { text: 'Network name' }),
          h('input', { class: 'input', id: 'ssid-' + i, value: n.ssid, autocomplete: 'off', spellcheck: 'false', list: 'known-nets', oninput: e => { n.ssid = e.target.value; updateNote(i); scheduleWifiCheck(); } })),
        h('label', { class: 'field' }, h('span', { text: 'Password' }),
          h('div', { class: 'pw' }, pass, h('button', { class: 'eye', type: 'button', 'aria-label': 'Show the password', text: 'Show', onclick: e => {
            const show = pass.type === 'password'; pass.type = show ? 'text' : 'password';
            e.currentTarget.textContent = show ? 'Hide' : 'Show'; e.currentTarget.setAttribute('aria-label', show ? 'Hide the password' : 'Show the password');
          } }))),
        h('div', { class: 'net-note', id: 'note-' + i })),
      h('div', { class: 'net-tools' },
        h('button', { class: 'icon-btn', type: 'button', 'aria-label': 'Move up', text: '↑', disabled: i === 0, onclick: () => { [S.wifi[i - 1], S.wifi[i]] = [S.wifi[i], S.wifi[i - 1]]; renderNets(); } }),
        h('button', { class: 'icon-btn', type: 'button', 'aria-label': 'Move down', text: '↓', disabled: i === S.wifi.length - 1, onclick: () => { [S.wifi[i + 1], S.wifi[i]] = [S.wifi[i], S.wifi[i + 1]]; renderNets(); } }),
        h('button', { class: 'icon-btn', type: 'button', 'aria-label': 'Remove', text: '✕', disabled: S.wifi.length === 1, onclick: () => { S.wifi.splice(i, 1); renderNets(); checkWifi(); } })));
  }));
  S.wifi.forEach((n, i) => updateNote(i));
  const addBtn = $('#addnet'); if (addBtn) addBtn.disabled = S.wifi.length >= 4;
  const count = $('#netcount');
  if (count) count.textContent = S.wifi.length >= 4 ? 'Four is the most a pager keeps.' : `Up to four. ${plural(4 - S.wifi.length, 'more', 'more')} can go here.`;
}
function useKnown(ssid){
  let i = S.wifi.findIndex(n => !n.ssid.trim());
  if (i < 0 && S.wifi.length < 4) { S.wifi.push({ ssid: '', pass: '' }); i = S.wifi.length - 1; }
  if (i < 0) return;
  S.wifi[i].ssid = ssid; renderNets(); checkWifi();
}
function tplWifi(){
  const hostNow = h('code', { text: `${S.host.mode === 'ip' && S.host.ip.trim() ? S.host.ip.trim() : 'this Mac’s .local name'}:2277` });
  const setHost = () => { hostNow.textContent = `${S.host.mode === 'ip' && S.host.ip.trim() ? S.host.ip.trim() : 'this Mac’s .local name'}:2277`; };
  const radio = (value, ...label) => h('label', {}, h('input', { type: 'radio', name: 'hostmode', value, checked: S.host.mode === value, onchange: () => { S.host.mode = value; setHost(); } }), h('span', {}, label));
  return [
    head('Wi-Fi', 'The networks the pager may join, best first. It must share one with this Mac: it has no other way to reach the bridge.'),
    S.pair.again ? banner('Changing ', b(S.pair.again), '’s Wi-Fi networks: pairing it again, under the same name, so its entry is replaced. On the pager, hold ', keyName('BOOT'), ' for its menu, choose SETTINGS, then PAIR AGAIN, and ', keyName('BOOT'), ' for yes. Below, its networks’ names are filled in where this page knows them: type each password again, since nothing kept them.') : null,
    h('div', { class: 'rules' },
      h('div', { class: 'rule' }, h('span', { class: 'rule-k', text: '2.4 GHz only' }), h('span', { text: 'The ESP32-S3 can’t join 5 GHz. A dual-band network with one name works while its 2.4 GHz band is on.' })),
      h('div', { class: 'rule' }, h('span', { class: 'rule-k', text: 'iPhone hotspot' }), h('span', {}, 'Turn on ', b('Maximize Compatibility'), ', and keep Settings → Personal Hotspot open until the pager joins. The Mac must be on the hotspot too.')),
      h('div', { class: 'rule' }, h('span', { class: 'rule-k', text: 'Exact names' }), h('span', {}, 'Spelling, case and apostrophes all count: an iPhone names its hotspot with a curly one (', code('Sam’s iPhone'), ').'))),
    h('h3', { text: 'Networks, best first' }),
    h('ol', { class: 'nets', id: 'nets' }),
    h('div', { class: 'row-actions' },
      h('button', { class: 'btn sm', id: 'addnet', type: 'button', text: '+ Add a network', onclick: () => { if (S.wifi.length < 4) { S.wifi.push({ ssid: '', pass: '' }); renderNets(); $('#ssid-' + (S.wifi.length - 1)).focus(); } } }),
      h('span', { class: 'fine', id: 'netcount' })),
    S.known.length ? h('div', { class: 'seen' }, h('span', { class: 'seen-label', text: 'This Mac has saved' }), S.known.map(n => h('button', { class: 'seen-chip', type: 'button', text: n, onclick: () => useKnown(n) }))) : null,
    h('datalist', { id: 'known-nets' }, S.known.map(n => h('option', { value: n }))),
    h('div', { class: 'lock' }, h('span', { class: 'note-k', text: 'PASSWORDS' }),
      h('p', { text: 'They go to the pager only, down the USB cable, when you pair. They’re never written to a file or a log, and this Mac doesn’t keep them: this page forgets them once the pager has them.' })),
    h('details', { class: 'host', open: S.host.mode === 'ip' },
      h('summary', {}, 'The pager calls this Mac at ', hostNow, h('span', { class: 'chg', text: 'Change' })),
      h('div', { class: 'host-body' },
        radio('local', b('This Mac’s name'), ', its .local name. Works wherever .local names resolve, which most home networks do.'),
        radio('ip', b('An IP address.'), ' Always works, but may change.', h('br'),
          h('input', { class: 'input', placeholder: '192.0.2.10', value: S.host.ip, inputmode: 'decimal', 'aria-label': 'IP address', oninput: e => { S.host.ip = e.target.value; setHost(); } })))),
    foot(),
  ];
}

/* step 4 */
async function pairStart(replace){
  S.pair.st = 'starting'; S.pair.err = ''; S.pair.offer = null; S.pair.result = null; S.pair.taken = null; refresh();
  try {
    await loadPorts();
    if (!S.port) throw new Error('No pager on USB. Plug it into this Mac with a data cable, then start again.');
    const host = S.host.mode === 'ip' ? S.host.ip.trim() : '';
    S.pair.offer = await api('POST', '/api/pair', { port: S.port, name: S.pair.again || S.pair.name, host, replace: replace || '' });
    S.pair.st = 'offered';
    if (S.pair.offer.firmware) S.pagerFW = S.pair.offer.firmware.pager || '';
  } catch (e) {
    S.pair.st = /cancelled/i.test(e.message) ? 'cancelled' : 'failed';
    S.pair.err = S.pair.st === 'cancelled' ? '' : e.message;
    if (e.data && e.data.taken) { S.pair.taken = e.data.taken; S.pair.st = 'idle'; }
  }
  refresh();
}
/* A name paired with another key (the owner's checkpoint 7.2): replace that
   device, after a confirm, or choose another name. */
function takenBlock(){
  const t = S.pair.taken;
  if (!t) return null;
  const replace = () => {
    if (!window.confirm(`Replace ${t.name}? The bridge stops accepting its old key, ${t.fingerprint}, now, even if this pairing is cancelled. A pager that still has that key can’t connect until it’s paired again.`)) return;
    pairStart(t.fingerprint);
  };
  return h('div', { class: 'result need' }, h('span', { class: 'rk', text: 'NAME TAKEN' }), h('div', {},
    h('p', {}, b(t.name), ' is already paired, with another key: ', code(t.fingerprint), '. That’s another pager, or this one before it was erased.'),
    h('div', { class: 'row-actions' },
      h('button', { class: 'btn sm primary', type: 'button', text: `Replace ${t.name}`, onclick: replace }),
      h('button', { class: 'btn sm', type: 'button', text: 'Choose another name', onclick: () => { S.pair.taken = null; S.pair.err = ''; refresh(); const el = $('#pname'); if (el) { el.focus(); el.select(); } } }))));
}
async function pairConfirm(){
  const offer = S.pair.offer;
  S.pair.st = 'sending'; S.pair.err = ''; refresh();
  try {
    const wifi = nets().map(n => ({ ssid: n.ssid, pass: n.pass }));
    S.pair.result = await api('POST', '/api/pair/confirm', { fingerprint: offer.pager.fingerprint, wifi });
    S.pair.st = 'paired'; S.pair.again = '';
    for (const n of S.wifi) n.pass = ''; // the pager has them now
    S.flash = 'done';
    watchJoin();
    await loadDevices();
    runDoctor();
  } catch (e) {
    // Settings the pager wouldn't take (400): nothing was sent, and the
    // pairing still waits for them.
    if (e.status === 400) S.pair.st = 'offered'; else { S.pair.st = 'failed'; S.pair.offer = null; }
    S.pair.err = e.message;
  }
  refresh();
}
async function pairCancel(){
  try { await api('POST', '/api/pair/cancel', {}); } catch (e) { S.pair.err = e.message; }
  if (S.pair.st !== 'sending') { S.pair.st = 'cancelled'; S.pair.offer = null; }
  refresh();
}
/* After pairing, the bridge reads the pager's log over USB for how its join
   went (the owner's recheck of 7.2): a password is tried only then. */
async function watchJoin(){
  S.join = { st: 'watching', says: '', network: '' }; refresh();
  try {
    const r = await api('POST', '/api/pair/join', {});
    S.join = { st: r.result, says: r.says, network: r.network || '' };
  } catch (e) {
    S.join = { st: 'error', says: e.message, network: '' };
  }
  refresh();
}
function joinBlock(){
  const j = S.join, name = pagerName();
  if (connected() || j.st === 'joined') {
    return h('div', { class: 'result ok mt12' }, h('span', { class: 'rk', text: 'JOINED' }), h('div', {}, h('p', { text: j.says || `${name} is on Wi-Fi.` })));
  }
  if (j.st === 'idle') return null;
  if (j.st === 'watching') {
    return h('div', { class: 'spinline mt12' }, spin(), h('span', { text: `Reading ${name}’s log over USB while it restarts and joins. Leave it plugged in.` }));
  }
  return h('div', { class: 'result need mt12' }, h('span', { class: 'rk', text: j.st === 'refused' ? 'WRONG PASSWORD' : 'NOT JOINED' }), h('div', {},
    h('p', { text: S.join.says }),
    h('div', { class: 'row-actions' },
      h('button', { class: 'btn sm primary', type: 'button', text: 'Change the Wi-Fi networks', onclick: () => changeNets(pagerName()) }),
      j.st === 'unknown' || j.st === 'error' ? h('button', { class: 'btn sm', type: 'button', text: 'Read its log again', onclick: watchJoin }) : null)));
}
function fpCompare(o){
  return h('div', { class: 'fp-compare' },
    h('div', { class: 'fp-row' }, h('span', { class: 'fp-tag', text: 'HOST' }), h('div', {}, h('div', { class: 'fp-label', text: `The bridge’s key, on this Mac (${o.bridge.type})` }), fpEl(o.bridge.fingerprint))),
    h('div', { class: 'fp-row' }, h('span', { class: 'fp-tag', text: 'DEV' }), h('div', {}, h('div', { class: 'fp-label', text: `The pager’s key, just read over USB (${o.pager.type})` }), fpEl(o.pager.fingerprint))));
}
function pairForm(){
  const list = nets();
  const name = h('input', { class: 'input', id: 'pname', value: S.pair.again || S.pair.name, maxlength: '24', spellcheck: 'false', readOnly: !!S.pair.again,
    oninput: e => { S.pair.name = e.target.value.replace(/[^A-Za-z0-9._-]/g, '').slice(0, 24); if (e.target.value !== S.pair.name) e.target.value = S.pair.name; } });
  const port = S.ports.length > 1
    ? h('label', { class: 'field w320 mt12' }, h('span', { text: 'Its USB port' }),
      h('select', { class: 'input', onchange: e => { S.port = e.target.value; } }, S.ports.map(p => h('option', { value: p, text: p, selected: p === S.port }))))
    : h('p', { class: 'fine mt12' }, S.port ? ['On USB at ', code(S.port), '.'] : 'No pager on USB yet. Plug it in: this page looks again every few seconds.');
  return [
    h('label', { class: 'field w320' }, h('span', { text: 'The pager’s name' }), name),
    h('p', { class: 'fine mt6' }, '1 to 24 of A–Z a–z 0–9 . _ -, starting with a letter or digit, such as ', code('pager-01'), '.'),
    port,
    h('div', { class: 'sendmap' },
      h('div', { class: 'sendbox' }, h('span', { class: 'note-k', text: 'TO THE PAGER' }), h('ul', {},
        h('li', {}, list.length ? `${plural(list.length, 'network', 'networks')}: ${list.map(n => n.ssid).join(', ')} (passwords hidden)` : 'no networks yet'),
        h('li', {}, code(`${hostShown()}:2277`), ', to call'),
        h('li', { text: 'the bridge’s key, to pin' }))),
      h('div', { class: 'sendbox' }, h('span', { class: 'note-k', text: 'TO THE BRIDGE' }), h('ul', {},
        h('li', { text: 'the pager’s public key (ed25519)' }), h('li', { text: 'nothing else: the pager’s private key never leaves it' })))),
    h('div', { class: 'row-actions' },
      h('button', { class: 'btn primary', type: 'button', text: S.pair.st === 'idle' ? 'Start pairing' : 'Start again', disabled: !list.length || !S.port, onclick: () => pairStart() }),
      list.length ? null : h('button', { class: 'btn sm', type: 'button', text: 'Add a network first', onclick: () => go('wifi') })),
  ];
}
function tplPair(){
  const p = S.pair, o = p.offer;
  let body;
  switch (p.st) {
    case 'starting':
      body = [h('div', { class: 'spinline' }, spin(), h('span', {}, 'Opening ', code(S.port), '. Waiting up to 2 minutes for the pager to say hello: it must show ', b('PAIR VIA USB'), ' (a pager paired before: Pair again, in its menu’s SETTINGS).')),
        h('div', { class: 'row-actions' }, h('button', { class: 'btn sm', type: 'button', text: 'Cancel', onclick: pairCancel }))];
      break;
    case 'offered':
      body = [h('h3', { class: 'mt0', text: 'Both keys, read just now' }), fpCompare(o),
        o.firmware && o.firmware.update ? h('p', { class: 'fine mt12' }, `It runs firmware ${o.firmware.pager || 'of no version'}; this bridge ships ${o.firmware.shipped}. Update it in Manage, after pairing: an update keeps the pairing.`) : null,
        h('p', { class: 'body-text mt16' }, 'The pager shows ', b(`PAIR WITH ${o.host_name}?`), ' with the same two keys, HOST above DEV. It has been sent nothing secret. Check every character, or at least the first and last four. If they match, press ', keyName('BOOT'), ' on it, then confirm here: the networks and their passwords go only then, and the pager waits 30 s for them. ', keyName('PWR'), ' cancels.'),
        h('div', { class: 'row-actions' },
          h('button', { class: 'btn primary', type: 'button', disabled: !nets().length, onclick: pairConfirm }, 'They match: send the Wi-Fi networks ', h('span', { class: 'arr', text: '→' })),
          h('button', { class: 'btn sm', type: 'button', text: 'Cancel', onclick: pairCancel }),
          nets().length ? null : h('button', { class: 'btn sm', type: 'button', text: 'Add a network first', onclick: () => go('wifi') }))];
      break;
    case 'sending':
      body = [h('h3', { class: 'mt0', text: 'Compare with the pager’s screen' }), fpCompare(o),
        h('div', { class: 'press' },
          h('div', { class: 'edge', 'aria-hidden': 'true' }, h('span', { class: 'pk pwr' }), h('span', { class: 'pk boot' }),
            h('span', { class: 'pl pwr' }, 'PWR', h('small', { text: 'CANCEL' })), h('span', { class: 'pl boot' }, 'BOOT', h('small', { text: 'PAIR' }))),
          h('div', {},
            h('div', { class: 'press-text' }, 'Waiting for ', keyName('BOOT'), ' on the pager, if you haven’t pressed it yet. ', keyName('PWR'), ' cancels.'),
            h('div', { class: 'press-sub', text: `Once it shows NOW CONFIRM ON ${o.host_name}, the networks go, and it pairs without another press. Nothing secret goes before both of you have confirmed.` })))];
      break;
    case 'paired': {
      const first = nets()[0];
      body = h('div', { class: 'result ok' }, h('span', { class: 'rk', text: 'PAIRED' }), h('div', {},
        h('p', {}, b(p.result.paired), ' is in ', code('devices.toml'), ', with its full key. It restarts, joins ', first ? first.ssid : 'its network', ' and calls the bridge at ', code(`${o ? o.host : hostShown()}:2277`), '.'),
        h('p', { class: 'fine', text: 'This page has forgotten the passwords: the pager has them now.' })));
      body = [body, joinBlock()];
      break;
    }
    default:
      body = pairForm();
  }
  return [
    head('Pair', 'Pairing gives the pager its networks, this Mac’s address and the bridge’s key, and gives the bridge the pager’s key. You compare both keys here, and confirm on the pager itself.'),
    p.again && p.st !== 'paired' ? banner('Pairing ', b(p.again), ' again: start pairing below, then choose Pair again in its menu’s SETTINGS. The page waits 2 minutes for it, and a pager already showing PAIR VIA USB is found at once. Same key, so its entry is updated, not added.') : null,
    p.st === 'cancelled' ? h('p', { class: 'fine', text: 'Cancelled: nothing was sent to the pager, and devices.toml is as it was.' }) : null,
    p.err && !p.taken ? h('p', { class: 'problem', text: p.err }) : null,
    takenBlock(),
    body,
    foot(),
  ];
}

/* step 5 */
function tplFirewall(){
  const c = check('firewall'), allowed = !!(c && c.ok);
  return [
    head('Firewall', allowed ? 'The Mac’s firewall lets pager-bridge take calls now.' : 'The Mac’s firewall is on, and it doesn’t let pager-bridge take calls, so the pager’s calls never arrive. That’s why it says NO BRIDGE ON THIS NETWORK.'),
    h('div', { class: 'statusline' }, h('span', { class: 'code ' + (allowed ? 'ok' : 'need'), text: c ? `firewall: ${c.state}` : '…' })),
    c ? h('p', { class: 'body-text', text: c.detail }) : null,
    c && c.fix && !allowed ? h('p', { class: 'body-text mt10', text: c.fix }) : null,
    c && c.commands && c.commands.length && !allowed ? [h('h3', { text: 'Run these in Terminal' }), cmdList(c.commands)] : null,
    h('div', { class: 'row-actions' }, h('button', { class: 'btn sm' + (allowed ? '' : ' primary'), type: 'button', disabled: S.doctorBusy, text: S.doctorBusy ? 'Checking…' : 'Check again', onclick: runDoctor })),
    h('p', { class: 'fine mt12' }, 'Only an administrator can change the firewall, so ', code('sudo'), ' asks for your Mac’s password, in Terminal. This page never sees it, and never runs ', code('sudo'), ' itself.'),
    allowed ? h('div', { class: 'result ok' }, h('span', { class: 'rk', text: 'ALLOWED' }), h('div', {},
      h('p', {}, 'The pager’s next call gets through. After a ', code('brew upgrade'), ' the bridge is a new file at a new path: allow it again then. Manage’s checks say when.'))) : null,
    foot(),
  ];
}

/* step 6 */
async function setLogin(on, launchd){
  S.login.busy = true; S.login.err = ''; S.login.offer = null; S.login.cmds = null; refresh();
  try {
    const r = await api('POST', '/api/login', { on, launchd: !!launchd });
    S.login.cmds = r && r.commands && r.commands.length ? r.commands : null;
    S.login.reload = !!(r && r.reload);
  } catch (e) {
    // A bridge Homebrew didn't install (the owner's checkpoint 7.2): the
    // launchd agent instead, once the person agrees.
    if (e.data && e.data.launchd) S.login.offer = e.data.launchd; else S.login.err = e.message;
  }
  S.login.busy = false;
  await runDoctor();
  await loadDevices();
  refresh();
}
function loginSwitch(){
  const c = check('login'), on = loginOn();
  return h('button', { class: 'switch' + (S.login.busy ? ' busy' : ''), type: 'button', role: 'switch', 'aria-checked': String(on), 'aria-label': 'Start pager-bridge at login',
    disabled: S.login.busy || !c, onclick: () => setLogin(!on) }, h('span', { class: 'knob' }));
}
function loginBlock(){
  const c = check('login'), on = loginOn();
  /* The launchd agent runs another bridge (the owner's second recheck of
     7.2: rc.3's, while rc.4's page said on): say so, and offer to switch it
     to this one. The click is the consent; the page rewrites the agent, and
     gives the commands that reload it. */
  const other = !!(c && c.state === 'other') && !S.login.offer && !S.login.cmds;
  const offerOther = !!(S.login.offer && S.login.offer.current);
  const later = () => { S.login.offer = null; refresh(); };
  return [
    h('div', { class: 'toggle-row' },
      h('div', {}, h('div', { class: 'toggle-title', text: 'Start pager-bridge at login' }),
        h('div', { class: 'toggle-sub', text: c && c.state === 'launchd' ? 'the launchd agent dev.herdr-pager.bridge runs it'
          : c && c.state === 'other' ? 'the launchd agent dev.herdr-pager.bridge runs another bridge' : `brew services ${on ? 'stop' : 'start'} herdr-pager` })),
      loginSwitch()),
    c ? h('p', { class: 'body-text mt14', text: c.detail }) : h('p', { class: 'fine mt14', text: S.doctorBusy ? 'Checking…' : 'Not checked yet.' }),
    c && !c.ok && c.fix && !other ? h('p', { class: 'body-text mt10', text: c.fix }) : null,
    c && !c.ok && c.commands && c.commands.length ? h('div', { class: 'mt12' }, cmdList(c.commands)) : null,
    S.login.err ? h('p', { class: 'problem', text: S.login.err }) : null,
    other ? h('div', { class: 'result need' }, h('span', { class: 'rk', text: 'SWITCH?' }), h('div', {},
      h('p', { text: 'At login, launchd starts another copy of pager-bridge, not this one: that copy’s version is the one the pagers call. Switch the agent to this bridge: the page rewrites it to run this copy, where it is, and gives you the two commands that reload it.' }),
      h('div', { class: 'row-actions' },
        h('button', { class: 'btn sm primary', type: 'button', text: 'Switch to this bridge', onclick: () => setLogin(true, true) })))) : null,
    S.login.offer ? h('div', { class: 'result need' }, h('span', { class: 'rk', text: offerOther ? 'SWITCH?' : 'LAUNCHD?' }), h('div', {},
      offerOther
        ? h('p', {}, 'The launchd agent ', code('dev.herdr-pager.bridge'), ' runs another bridge at login: ', code(S.login.offer.current), '. Switch it to this one, ', code(S.login.offer.program), '? The page rewrites ', code(S.login.offer.plist), ', and gives you the commands that reload it.')
        : h('p', {}, 'This pager-bridge wasn’t installed with Homebrew, so brew services can’t run it. A launchd agent can, from ', code(S.login.offer.plist), ', running ', code(S.login.offer.program), ' at login. It runs this copy where it is: move or delete it, and the agent fails.'),
      h('div', { class: 'row-actions' },
        h('button', { class: 'btn sm primary', type: 'button', text: offerOther ? 'Switch to this bridge' : 'Use a launchd agent', onclick: () => setLogin(true, true) }),
        h('button', { class: 'btn sm', type: 'button', text: 'Not now', onclick: later })))) : null,
    /* After a switch, launchd runs the old bridge until these run (the
       owner's last recheck of 7.2, open-items O-96: missed as a note). */
    S.login.cmds ? (S.login.reload ? h('div', { class: 'result need' }, h('span', { class: 'rk', text: 'RELOAD' }), h('div', {},
      h('p', { text: 'Not done yet: launchd still runs the bridge it started before, and the pagers still call that one. Run these in Terminal, in order: they stop it, and start this one.' }),
      cmdList(S.login.cmds)))
      : [h('p', { class: 'body-text mt12', text: 'To do it now rather than at your next login, run this in Terminal:' }), cmdList(S.login.cmds)]) : null,
  ];
}
function tplLogin(){
  return [
    head('Run at login', 'Keep the bridge running whenever you’re logged in, so the pager always has something to call.'),
    loginBlock(),
    loginOn() ? null : h('p', { class: 'body-text mt18' }, 'Off, the bridge runs only while ', code(S.serve || 'pager-bridge serve'), ' is open in a terminal. Close it, and the pager says NO BRIDGE ON THIS NETWORK.'),
    h('dl', { class: 'facts' },
      h('dt', { text: 'Turn off' }), h('dd', {}, code('brew services stop herdr-pager'), ', or this switch'),
      h('dt', { text: 'Its log' }), h('dd', {}, 'the Bridge log tab below, from ', code('/opt/homebrew/var/log/herdr-pager.log'))),
    foot(),
  ];
}

/* step 7 */
function tplFirst(){
  const d = theDevice(), conn = connected(), name = d ? d.name : pagerName();
  const items = [
    [S.running, 'The bridge is running', S.running ? 'pager-bridge' : ''],
    [!!d, d ? `${d.name} is paired` : 'A pager is paired', d ? shortFP(d.fingerprint) : ''],
    [conn, `${name} called the bridge`, conn ? 'connected' : ''],
  ];
  const firstOpen = items.findIndex(x => !x[0]);
  let hint = null;
  if (!S.running) hint = [h('p', { class: 'body-text mt14' }, 'No bridge is running, so the pager has nothing to call: it says NO BRIDGE ON THIS NETWORK. Run this in a Terminal window, and leave it open, or turn on Run at login. ', h('button', { class: 'btn xs', type: 'button', text: 'Go to Run at login', onclick: () => go('login') })),
    cmdList([S.serve || 'pager-bridge serve'])];
  else if (!d) hint = h('p', { class: 'body-text mt14' }, 'No pager is paired yet. ', h('button', { class: 'btn xs', type: 'button', text: 'Go to Pair', onclick: () => go('pair') }));
  else if (!conn) hint = [
    S.join.says && S.join.st !== 'joined' ? h('p', { class: 'problem mt14', text: S.join.says }) : null,
    h('p', { class: 'body-text mt14' }, `Waiting for ${d.name} to call. If it says NO BRIDGE ON THIS NETWORK, the firewall drops its calls, or the Mac is on another network. `, h('button', { class: 'btn xs', type: 'button', text: 'Check the firewall', onclick: () => { S.fwSeen = true; go('firewall'); runDoctor(); } })),
    h('p', { class: 'body-text mt10' }, `If it says NO WIFI or WRONG WIFI PASSWORD, it isn’t on a network it shares with this Mac. `, h('button', { class: 'btn xs', type: 'button', text: 'Change the Wi-Fi networks', onclick: () => changeNets(d.name) }))];
  const screen = !S.running
    ? h('div', { class: 'bigl' }, h('div', { class: 'bigl-band' }, h('span', { text: '!0 *0 >0' }), h('span', { text: `${hm()}  ~ ${netName()}` })),
      h('div', { class: 'bigl-main' }, 'NO BRIDGE RUNNING', h('small', { text: 'start it below: this page looks every few seconds' })))
    : conn
    ? h('div', { class: 'bigl green' }, h('div', { class: 'bigl-band' }, h('span', { text: '!0 *0 >0' }), h('span', { text: `${hm()}  o ${netName()}` })),
      h('div', { class: 'bigl-main' }, 'CONNECTED', h('small', { text: `${name} is on the bridge` })))
    : h('div', { class: 'bigl' }, h('div', { class: 'bigl-band' }, h('span', { text: '!0 *0 >0' }), h('span', { text: `${hm()}  ~ ${netName()}` })),
      h('div', { class: 'bigl-main' }, `WAITING FOR ${name.toUpperCase()}`, h('span', { class: 'cursor' }), h('small', { text: 'this page looks every few seconds' })));
  return [
    head('First page', conn ? 'The pager is on the bridge. From here on it beeps when an agent needs you.' : (S.running ? 'The pager calls the bridge over Wi-Fi. This page waits for it.' : 'The pager calls the bridge over Wi-Fi, and no bridge is running on this Mac.')),
    screen,
    h('ul', { class: 'tick-list' }, items.map(([ok, t, d2], i) => h('li', {},
      h('span', { class: 'g ' + (ok ? 'done' : (i === 0 ? 'need' : 'todo')) }, ok ? '*' : (i === 0 ? '!' : (i === firstOpen ? spin() : '.'))), h('span', { text: t }), h('span', { class: 'mono', text: d2 })))),
    hint,
    conn ? [
      h('h3', { text: 'What the pager shows now' }),
      h('p', { class: 'body-text', text: 'Every herdr agent on one list, the most urgent on top. The status bar along the top reads, for example:' }),
      h('ul', { class: 'legend-list' },
        h('li', {}, code('!1 *1 >1'), ' one blocked, one done, one working'),
        h('li', {}, code('o'), ' the link to the bridge is up (', code('~'), ' while it’s down)'),
        h('li', {}, code('OPEN'), ' and ', code('NEXT'), ' what PWR and BOOT do here, written under each button')),
      h('div', { class: 'codes' }, [['!', 'Blocked: it needs you'], ['*', 'Done: its turn is over'], ['>', 'Working'], ['?', 'Unknown'], ['.', 'Idle']].map(([g, t]) => h('div', {}, h('b', { text: g }), t))),
      h('div', { class: 'say solo' }, h('div', { class: 'screen' }, screenEl('empty')), h('p', {}, h('b', { text: 'NO AGENTS' }), 'What it shows instead while herdr has no agents running.')),
      h('h3', { text: 'Try it' }),
      h('p', { class: 'body-text' }, 'Ask an agent for something that needs permission. Within about 4 s the pager beeps and selects it: ', keyName('PWR'), ' opens its card, a 1 s hold on ', keyName('BOOT'), ' approves.'),
      h('p', { class: 'safety' }, h('b', { text: 'An attention device, not a security gate.' }), ' Any process in a herdr pane can already drive herdr’s socket and answer its own prompts. Read the command on every card.'),
    ] : null,
    foot(),
  ];
}

const TPL = { mac: tplMac, flash: tplFlash, wifi: tplWifi, pair: tplPair, firewall: tplFirewall, login: tplLogin, first: tplFirst };

function renderStep(){
  if (!visible().some(s => s.id === S.step)) S.step = 'mac';
  $('#step').replaceChildren(...[TPL[S.step]()].flat(Infinity).filter(Boolean));
  if (S.step === 'wifi') renderNets();
}

/* ---------- the preview ---------- */
function screenKey(){
  const p = S.pair.st;
  switch (S.step) {
    case 'mac': return 'off';
    case 'flash': return S.flash === 'dialog' ? 'rom' : (S.flash === 'closed' || S.flash === 'done' ? 'pairViaUsb' : 'off');
    case 'wifi': return S.flash === 'idle' && !S.ports.length ? 'off' : 'pairViaUsb';
    case 'pair': return p === 'offered' || p === 'sending' ? 'pairConfirm' : (p === 'paired' ? (connected() ? 'list' : 'connecting') : 'pairViaUsb');
    case 'firewall': { const c = check('firewall'); return c && !c.ok ? 'noBridge' : (connected() ? 'list' : 'connecting'); }
    default: return connected() ? 'list' : (theDevice() ? 'connecting' : 'pairViaUsb');
  }
}
function caption(key){
  const host = S.pair.offer ? S.pair.offer.host_name : 'this Mac';
  switch (key) {
    case 'off': return S.step === 'mac'
      ? ['Not plugged in', 'The pager isn’t needed yet. It comes in at step 2.']
      : ['Not connected', 'Plug it into this Mac by USB-C, then Connect and flash.'];
    case 'rom': return ['Download mode', 'Dark while it’s flashed: the board is in its ROM bootloader, and nothing drives the screen.'];
    case 'pairViaUsb': return ['PAIR VIA USB', S.step === 'pair'
      ? 'Waiting to be paired. Start pairing, and this page and the pager both show both keys.'
      : 'Flashed, and never paired: it waits for pairing over USB. Nothing reaches it until you pair it.'];
    case 'pairConfirm': return [`PAIR WITH ${host}?`, 'HOST is the bridge’s key, DEV the pager’s: the same two as on this page. It has been sent nothing secret. BOOT confirms on the pager, then it shows NOW CONFIRM ON ' + host + ' and waits 30 s for the networks; PWR cancels.'];
    case 'connecting': return ['CONNECTING', `Restarted paired: it joins ${netName()} and calls the bridge at ${hostShown()}.`];
    case 'noBridge': return ['NO BRIDGE ON THIS NETWORK', 'On Wi-Fi, but its calls never arrive: the Mac’s firewall drops them.'];
    case 'list': return ['CONNECTED', 'Your herdr agents, the most urgent on top (this is an example list). The o in the status bar: the link to the bridge is up.'];
  }
  return [SCR[key] ? SCR[key].label : '', ''];
}
let shownScreen = '';
function renderPreview(){
  const key = screenKey(), armed = key === 'pairConfirm';
  const sig = [key, armed, hm(), netName(), S.pair.offer ? S.pair.offer.pager.fingerprint : ''].join('|');
  if (sig !== shownScreen) { $('#pv-device').replaceChildren(deviceEl(key, armed)); shownScreen = sig; }
  const [label, text] = caption(key);
  $('#pv-state').textContent = label; $('#pv-text').textContent = text;
  const usb = S.ports.length > 0, d = theDevice();
  $('#pv-chips').replaceChildren(
    h('span', { class: 'pv-chip' + (usb ? ' on' : ''), text: `USB ${usb ? '●' : '○'}` }),
    h('span', { class: 'pv-chip ' + (connected() ? 'on' : (d ? 'warn' : '')), text: `LINK ${connected() ? 'o' : (d ? '~' : '–')}` }));
}

/* ---------- Manage (requirement 2.3) ---------- */
async function revoke(name){
  try { await api('POST', '/api/devices/revoke', { name }); S.m.confirm = ''; } catch (e) { S.devErr = e.message; }
  await loadDevices(); runDoctor(); refresh();
}
function pairAgain(name){
  S.m.repair = '';
  S.pair = { st: 'idle', name, again: name, offer: null, result: null, err: '' };
  S.join = { st: 'idle', says: '', network: '' };
  S.wifiDone = false;
  setView('setup'); go('wifi');
}
/* Change the Wi-Fi networks (the owner's recheck of 7.2): pairing again under
   the same name, so the bridge replaces that key's entry; the networks' names
   filled in, from the bridge (the ones it was paired with on this page) or
   this page; their passwords asked again, since nothing kept them. */
function changeNets(name){
  const d = (S.devices || []).find(x => x.name === name);
  const names = d && d.networks && d.networks.length ? d.networks : nets().map(n => n.ssid);
  S.wifi = names.length ? names.map(ssid => ({ ssid, pass: '' })) : [{ ssid: '', pass: '' }];
  pairAgain(name);
  checkWifi();
}
function devRow(d){
  const open = !!S.m.fpOpen[d.name];
  const state = !S.running
    ? h('span', { class: 'code todo' }, h('span', { class: 'lk', text: '~' }), ' bridge not running')
    : d.connected ? h('span', { class: 'code ok' }, h('span', { class: 'lk', text: 'o' }), ' connected')
      : h('span', { class: 'code todo' }, h('span', { class: 'lk', text: '~' }), ' not connected');
  let extra = null;
  if (S.m.confirm === d.name) {
    extra = h('div', { class: 'inline-confirm' }, b(`Revoke ${d.name}?`), ' It comes out of ', code('devices.toml'), '; a running bridge closes its session and refuses it from then on, and it shows NOT AUTHORISED. Lost it? Change the passwords of the Wi-Fi networks it knew: its storage isn’t encrypted.',
      h('div', { class: 'row-actions' },
        h('button', { class: 'btn sm danger solid', type: 'button', text: `Revoke ${d.name}`, onclick: () => revoke(d.name) }),
        h('button', { class: 'btn sm', type: 'button', text: 'Keep it', onclick: () => { S.m.confirm = ''; renderManage(); } })));
  } else if (S.m.repair === d.name) {
    extra = h('div', { class: 'inline-confirm info' }, b(`Change ${d.name}’s Wi-Fi networks`), ', or pair it again after a factory reset. Same name and key, so its entry is replaced, not added.',
      h('ol', {}, h('li', { text: 'Plug it into this Mac by USB-C.' }),
        h('li', {}, 'On the pager: hold ', keyName('BOOT'), ' for its menu, choose SETTINGS, then PAIR AGAIN, and ', keyName('BOOT'), ' for yes.'),
        h('li', { text: 'In Setup, check its networks’ names, type each password again, and pair.' })),
      h('div', { class: 'row-actions' },
        h('button', { class: 'btn sm primary', type: 'button', onclick: () => changeNets(d.name) }, 'Continue in Setup ', h('span', { class: 'arr', text: '→' })),
        h('button', { class: 'btn sm', type: 'button', text: 'Not now', onclick: () => { S.m.repair = ''; renderManage(); } })));
  }
  return h('div', { class: 'dev' },
    h('div', { class: 'dev-id' }, h('span', { class: 'g ' + (d.connected && S.running ? 'done' : 'idle'), text: d.connected && S.running ? 'o' : '~' }),
      h('div', {}, h('div', { class: 'dev-name', text: d.name }),
        h('button', { class: 'fpbtn', type: 'button', title: open ? 'Show less' : 'Show the full fingerprint', text: open ? d.fingerprint : shortFP(d.fingerprint), onclick: () => { S.m.fpOpen[d.name] = !open; renderManage(); } }))),
    h('div', { class: 'dev-state' }, state, h('div', { class: 'dim', text: d.added ? `paired ${new Date(d.added).toLocaleDateString()}` : '' })),
    h('div', { class: 'dev-actions' },
      h('button', { class: 'btn sm', type: 'button', text: 'Change the Wi-Fi networks', onclick: () => { S.m.repair = d.name; S.m.confirm = ''; renderManage(); } }),
      h('button', { class: 'btn sm danger', type: 'button', text: 'Revoke', onclick: () => { S.m.confirm = d.name; S.m.repair = ''; renderManage(); } })),
    extra);
}
function updateBlock(){
  const fw = S.fw;
  if (!fw || !fw.available) return h('p', { class: 'problem', text: fw ? `No firmware to update with: ${fw.problem || 'none found'}. ${fw.fix || ''}` : 'Looking for the firmware…' });
  if (S.upd === 'dialog' && S.ewtFor === 'manage') return h('div', { class: 'spinline mt14' }, spin(), h('span', { text: 'ESP Web Tools is open. It calls every flash Install herdr-pager: choose it, leave Erase device unticked, and go on. If its last screen says All data on the device will be erased, go Back: the box is ticked.' }));
  return [
    S.upd === 'closed' ? h('div', { class: 'result ok' }, h('span', { class: 'rk', text: 'DONE?' }), h('div', {}, h('p', { text: `If it finished, the pager restarted with ${fw.version}, still paired, and calls the bridge again.` }))) : null,
    h('div', { class: 'row-actions' }, flasher('/firmware/update.json', `Update to ${fw.version}`, 'btn sm primary')),
    hasSerial ? h('p', { class: 'fine mt10' }, pickPort()) : null,
    h('p', { class: 'fine mt10' }, 'Over USB. ESP Web Tools calls every flash Install herdr-pager, since the pager doesn’t speak its Improv, and then asks whether to erase the device. Leave Erase device unticked: the pager keeps its key, pairing and networks. If its last screen says All data on the device will be erased, go Back and untick it; ticked, the pager forgets them, and needs pairing again.'),
  ];
}
function card(title, side, wide, ...kids){
  return h('section', { class: 'card' + (wide ? ' wide' : '') }, h('div', { class: 'card-head' }, h('h3', { class: 'card-title', text: title }), side), kids);
}
function tplManage(){
  const devs = S.devices || [];
  const conn = S.running ? devs.filter(d => d.connected).length : 0;
  const herdr = check('herdr');
  const update = !!(S.pagerFW && S.fw && S.fw.available && S.pagerFW !== S.fw.version);
  const says = [
    ['noBridge', 'NO BRIDGE ON THIS NETWORK', 'It’s on Wi-Fi but can’t reach the bridge: the firewall, the Mac on another network, or .local names that don’t resolve there.'],
    ['notAuth', 'NOT AUTHORISED', 'The bridge refused its key: it was revoked, or devices.toml lost it. Pair it again.'],
    ['hostKey', 'HOST KEY CHANGED', 'The bridge answering isn’t the one it paired with. If you replaced the bridge’s host key, pair again; if not, find out what answered.'],
    ['pairViaUsb', 'PAIR VIA USB', 'Never paired, or factory reset. Pair it in Setup.'],
  ];
  return [
    h('div', { class: 'mhead' }, h('p', { class: 'kicker', text: 'Manage' }), h('h2', { text: 'Your pagers and the bridge' }),
      h('p', { class: 'lede', text: 'Update a paired pager’s firmware and keep its pairing, change its Wi-Fi networks, revoke the ones you no longer use, run the bridge at login, and see the bridge’s checks.' }),
      h('div', { class: 'summary' },
        h('span', { class: 'code ' + (conn ? 'ok' : 'todo'), text: `${conn} connected` }),
        h('span', { class: 'code ' + (S.running ? 'ok' : 'need'), text: S.running ? 'bridge running' : 'bridge not running' }),
        herdr ? h('span', { class: 'code ' + (herdr.ok ? 'ok' : 'need'), text: herdr.ok ? 'herdr ok' : `herdr ${herdr.state}` }) : null,
        update ? h('span', { class: 'code need', text: 'update available' }) : null)),
    h('div', { class: 'mgrid' },
      card('The pagers', h('span', { class: 'fine', text: `${devs.length} paired` }), true,
        S.devErr ? h('p', { class: 'problem', text: S.devErr }) : null,
        devs.length ? devs.map(devRow) : h('p', { class: 'dim', text: 'No pagers paired. Pair one in Setup.' }),
        h('div', { class: 'row-actions' }, h('button', { class: 'btn sm', type: 'button', onclick: () => newPager() }, 'Set up a new or reset pager ', h('span', { class: 'arr', text: '→' })),
          h('span', { class: 'fine', text: 'A first install erases the board: for a pager that’s paired, use Update.' })),
        h('p', { class: 'cardnote', text: 'Fingerprints are labels; the bridge checks each pager’s full public key. Each pager keeps its own Wi-Fi networks, and this Mac never keeps their passwords: to change them, pair it again.' })),
      card('Versions', h('span', { class: 'code ' + (update ? 'need' : 'ok'), text: update ? 'update' : 'current' }), false,
        h('div', { class: 'ver' }, h('span', { text: 'The firmware this bridge ships' }), h('span', { class: 'mono', text: S.fw && S.fw.available ? S.fw.version : '–' })),
        h('div', { class: 'ver' }, h('span', { text: 'The pager runs' }), h('span', { class: 'mono ' + (S.pagerFW ? (update ? 'need' : 'ok') : ''), text: S.pagerFW || '–' }),
          S.pagerFW ? null : h('span', { class: 'dim', text: 'Read from its hello when it pairs over USB on this page.' })),
        updateBlock()),
      card('Run at login', null, false, loginBlock()),
      card('Checks', h('button', { class: 'btn xs', type: 'button', disabled: S.doctorBusy, text: S.doctorBusy ? 'Checking…' : 'Run them again', onclick: runDoctor }), false,
        S.doctorErr ? h('p', { class: 'problem', text: S.doctorErr }) : null,
        h('div', { class: 'checks flat' }, S.doctor ? S.doctor.checks.map(c => checkRow(c, c.id)) : ['herdr', 'keys', 'port', 'firewall', 'login', 'devices'].map(id => checkRow(null, id)))),
      card('When the pager says', null, true,
        h('div', { class: 'says' }, says.map(([k, t, d]) => h('div', { class: 'say' }, h('div', { class: 'screen' }, screenEl(k)), h('p', {}, h('b', { text: t }), d))))),
    ),
  ];
}
function renderManage(){ $('#manage').replaceChildren(...tplManage().flat(Infinity).filter(Boolean)); }

/* ---------- navigation ---------- */
function refresh(){
  renderRail();
  if (S.view === 'setup') { renderStep(); renderPreview(); } else renderManage();
}
function go(id){
  if (S.step === 'wifi' && id !== 'wifi' && nets().length) S.wifiDone = true;
  S.step = id;
  refresh();
  const top = $('#step').getBoundingClientRect().top;
  if (top < 0) window.scrollBy(0, top - 16);
}
function setView(v){
  S.view = v;
  $$('.view-tab').forEach(t => t.setAttribute('aria-selected', String(t.dataset.view === v)));
  $('#view-setup').hidden = v !== 'setup';
  $('#view-manage').hidden = v !== 'manage';
  refresh();
  if (v === 'manage') Promise.all([loadDevices(), loadFirmware()]).then(() => { refresh(); runDoctor(); });
}

/* Every few seconds: the USB ports while the pager is wanted on USB, and the
   paired pagers while the page waits for one to call. Only what changed is
   drawn again, so typing isn't interrupted. */
async function tick(){
  if (S.stopped || document.hidden) return;
  let changed = false;
  if (S.view === 'setup' && (S.step === 'flash' || S.step === 'pair') && !['starting', 'offered', 'sending'].includes(S.pair.st)) changed = await loadPorts() || changed;
  if ((S.view === 'setup' && ['first', 'login', 'firewall'].includes(S.step)) || S.view === 'manage' || S.pair.st === 'paired') changed = await loadDevices() || changed;
  if (changed && !(S.view === 'setup' && S.step === 'wifi')) refresh();
  else if (S.view === 'setup') renderPreview();
}

/* ---------- start ---------- */
function start(){
  $('#chip-addr').textContent = location.host;
  if (!hasSerial) $('#no-serial').hidden = false;
  $$('.view-tab').forEach(t => t.addEventListener('click', () => setView(t.dataset.view)));
  initTerm();
  startEvents();
  watchFlashing();
  refresh();
  runDoctor();
  loadFirmware().then(refresh);
  loadPorts().then(refresh);
  // With a pager paired, the page opens on Manage (the owner's second
  // recheck of 7.2): Update keeps the pairing, a first install erases it.
  loadDevices().then(() => { if (pagersPaired()) setView('manage'); else refresh(); });
  checkWifi();
  setInterval(tick, 3000);
}
start();
})();
