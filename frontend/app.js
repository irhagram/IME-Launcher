const go = () => window.go.main.App;
const $ = (id) => document.getElementById(id);
const on = (ev, fn) => window.runtime.EventsOn(ev, fn);

const fmtBytes = (n) => {
  if (!n || n < 0) return '0 B';
  const u = ['B', 'KB', 'MB', 'GB', 'TB'];
  const i = Math.min(Math.floor(Math.log(n) / Math.log(1024)), u.length - 1);
  return (n / 1024 ** i).toFixed(i >= 3 ? 2 : i ? 1 : 0) + ' ' + u[i];
};
const fmtEta = (s) => {
  if (s == null || s < 0) return '-';
  const h = Math.floor(s / 3600), m = Math.floor((s % 3600) / 60), x = s % 60;
  return h ? `${h}j ${m}m` : m ? `${m}m ${x}d` : `${x}d`;
};

const fmtUpdated = (ms) => {
  if (!ms) return '-';
  const d = new Date(ms);
  const date = d.toLocaleDateString('id-ID', { day: 'numeric', month: 'long', year: 'numeric' });
  const time = d.toLocaleTimeString('id-ID', { hour: '2-digit', minute: '2-digit', hour12: false }).replace('.', ':');
  const days = Math.floor((Date.now() - ms) / 86400000);
  const ago = days <= 0 ? 'hari ini' : `${days} hari lalu`;
  return `${date}, ${time} (${ago})`;
};

let cfg = null;
let cacheInfo = null;
let dlState = 'idle';

// ---- tabs & window ----
document.querySelectorAll('.tab').forEach((t) => t.addEventListener('click', () => {
  if (blocked) return;
  document.querySelectorAll('.tab').forEach((x) => x.classList.toggle('active', x === t));
  $('btnSettings').classList.remove('active');
  document.querySelectorAll('.view').forEach((v) => v.classList.toggle('active', v.id === 'view-' + t.dataset.tab));
  go().SetHeight(t.dataset.tab === 'cache' ? 630 : 705);
  if (t.dataset.tab === 'cache') loadCache();
}));

// ---- settings (gear) ----
const nickMsg = (text, kind) => { $('nickMsg').textContent = text || ''; $('nickMsg').className = 'msg' + (kind ? ' ' + kind : ''); };

async function loadNickname() {
  try {
    const r = await go().GetNickname();
    $('nickInput').value = r.value || '';
    const own = r.source === 'Settings FiveM';
    $('nickPath').textContent = own ? 'Settings FiveM' + (r.cfgValue && r.cfgValue !== r.value ? ` · fivem.cfg: ${r.cfgValue}` : '') : (r.source || '-');
    $('nickPath').title = r.path || '';
    nickMsg(r.error || (r.running ? 'FiveM sedang terbuka. Tutup FiveM dulu sebelum menyimpan.' : ''), r.error ? 'err' : '');
  } catch (e) { nickMsg(String(e), 'err'); }
}

async function saveNickname(value) {
  $('btnNickSave').disabled = $('btnNickReset').disabled = true;
  try {
    const r = await go().SetNickname(value);
    nickMsg(r.msg, r.ok ? 'ok' : 'err');
    if (r.ok) $('nickInput').value = value.trim();
  } catch (e) { nickMsg(String(e), 'err'); }
  $('btnNickSave').disabled = $('btnNickReset').disabled = false;
}

$('btnSettings').onclick = () => {
  if (blocked) return;
  if ($('btnSettings').classList.contains('active')) { document.querySelector('.tab[data-tab="gate"]').click(); return; }
  document.querySelectorAll('.tab').forEach((x) => x.classList.remove('active'));
  document.querySelectorAll('.view').forEach((v) => v.classList.toggle('active', v.id === 'view-settings'));
  $('btnSettings').classList.add('active');
  go().SetHeight(630);
  loadNickname();
};
$('btnNickSave').onclick = () => saveNickname($('nickInput').value);
$('btnNickReset').onclick = async () => { await saveNickname(''); if ($('nickMsg').classList.contains('ok')) $('nickInput').value = ''; };
$('nickInput').addEventListener('keydown', (e) => { if (e.key === 'Enter') saveNickname($('nickInput').value); });
$('btnMin').onclick = () => go().Minimise();
$('btnClose').onclick = () => go().Quit();
$('btnTest').onclick = runAll;

// ---- gates ----
function buildGates() {
  const root = $('gates');
  root.innerHTML = '';
  cfg.gates.forEach((g) => {
    const el = document.createElement('div');
    el.className = `card gate ${g.id}`;
    el.id = 'gate-' + g.id;
    el.innerHTML = `
      <div class="card-head">
        <div><h2>${g.name}</h2><div class="sub">${g.host}</div></div>
        <span class="rating" data-k="rating">Belum dites</span>
      </div>
      <div class="metrics">
        <div class="metric"><label>Ping</label><b data-k="avg">-</b></div>
        <div class="metric"><label>Jitter</label><b data-k="jitter">-</b></div>
        <div class="metric"><label>Loss</label><b data-k="loss">-</b></div>
        <div class="metric"><label>Min / Max</label><b data-k="minmax">-</b></div>
      </div>
      <div class="spark" data-k="spark"></div>
      <div class="btns"><button class="btn primary connect" data-a="connect">Connect ${g.name}</button></div>`;
    root.appendChild(el);
    const q = (k) => el.querySelector(`[data-k="${k}"]`);
    const btn = (a) => el.querySelector(`[data-a="${a}"]`);

    btn('connect').onclick = async () => {
      const all = document.querySelectorAll('[data-a="connect"]');
      all.forEach((b) => (b.disabled = true));
      const r = await go().Connect(g.id);
      $('connectStatus').textContent = r.msg;
      all.forEach((b) => (b.disabled = false));
    };
  });
}

async function runTest(g) {
  const el = $('gate-' + g.id);
  const q = (k) => el.querySelector(`[data-k="${k}"]`);
  q('spark').innerHTML = '';
  q('rating').className = 'rating'; q('rating').textContent = 'Mengukur...';
  showResult(g.id, await go().TestGate(g.id));
}

async function runAll() {
  const b = $('btnTest');
  b.disabled = true; b.textContent = 'Mengukur...';
  // queue: one gate at a time, in config order (main first, then inter)
  cfg.gates.forEach((g) => {
    const el = $('gate-' + g.id);
    el.querySelector('[data-k="spark"]').innerHTML = '';
    const r = el.querySelector('[data-k="rating"]');
    r.className = 'rating'; r.textContent = 'Antre...';
  });
  for (const g of cfg.gates) await runTest(g);
  b.disabled = false; b.textContent = 'Cek Jaringan Lagi';
}

function showResult(id, r) {
  const el = $('gate-' + id);
  const q = (k) => el.querySelector(`[data-k="${k}"]`);
  const ms = (v) => `${v.toFixed(0)}<small> ms</small>`;
  q('rating').textContent = r.rating || 'Gagal';
  q('rating').className = 'rating l' + (r.level ?? 0);
  if (r.ok) {
    q('avg').innerHTML = ms(r.avg);
    q('jitter').innerHTML = ms(r.jitter);
    q('loss').innerHTML = `${r.loss.toFixed(0)}<small> %</small>`;
    q('minmax').innerHTML = `${r.min.toFixed(0)}/${r.max.toFixed(0)}`;
  } else {
    ['avg', 'jitter', 'loss', 'minmax'].forEach((k) => (q(k).textContent = '-'));
    q('rating').title = r.error || '';
  }
}

on('net:progress', (p) => {
  const el = document.querySelector(`#gate-${p.gate}`);
  if (!el) return;
  const q = (k) => el.querySelector(`[data-k="${k}"]`);
  const ms = (v) => `${v.toFixed(0)}<small> ms</small>`;
  // live numbers while the test runs
  if (p.ok > 0) {
    q('avg').innerHTML = ms(p.avg);
    q('jitter').innerHTML = ms(p.jitter);
    q('minmax').innerHTML = `${p.min.toFixed(0)}/${p.max.toFixed(0)}`;
  }
  q('loss').innerHTML = `${p.loss.toFixed(0)}<small> %</small>`;
  q('rating').textContent = `Mengukur ${p.i}/${p.n}`;

  const i = document.createElement('i');
  if (p.ms < 0 || p.udpLost) { i.className = 'lost'; i.style.height = '100%'; }
  else {
    i.className = p.ms < 50 ? 'ok' : p.ms < 100 ? 'mid' : 'hi';
    i.style.height = Math.max(8, Math.min(100, p.ms)) + '%';
  }
  q('spark').appendChild(i);
});
on('connect:status', (p) => ($('connectStatus').textContent = p.msg));

// ---- FiveM location (header button) ----
async function refreshFivemLoc() {
  const d = await go().LocateFiveM();
  const b = $('btnFivemTop');
  b.classList.toggle('unset', !d);
  const fp = $('fvPath');
  fp.textContent = d ? shortPath(d) : 'FiveM: belum diatur';
  fp.title = d || '';
  fp.classList.toggle('unset', !d);
  b.title = d ? `FiveM Application Data: ${d}` : 'FiveM belum ditemukan, klik untuk memilih folder FiveM Application Data';
}
$('btnFivemTop').onclick = async () => {
  const b = $('btnFivemTop');
  try {
    const d = await go().PickFiveM();
    if (d) {
      await refreshFivemLoc();
      b.textContent = 'Tersimpan';
      setTimeout(() => (b.textContent = 'Lokasi FiveM'), 1500);
      if ((document.querySelector('.tab.active') || {}).dataset?.tab === 'cache') loadCache();
    }
  } catch (e) {
    $('connectStatus').textContent = String(e);
  }
};

// ---- discord ----
function renderDiscords(list) {
  const box = $('dcs');
  box.innerHTML = '';
  (list || []).forEach((d) => {
    const b = document.createElement('button');
    b.className = 'dc';
    const sub = d.ok ? `<small>${nf(d.members)} member</small><small>${nf(d.online)} online</small>` : '';
    const icon = d.icon ? `<img src="${d.icon}" alt="">` : '';
    b.innerHTML = `${icon}<span><b>${d.name}</b>${sub}</span>`;
    b.onclick = () => go().OpenURL(d.url);
    box.appendChild(b);
  });
}
async function refreshDiscords() { renderDiscords(await go().GetDiscords()); }

// ---- players & 5metrics ----
// long paths keep their tail (the useful part); full path stays in the tooltip
const shortPath = (p, n = 30) => (p.length > n ? '…' + p.slice(-(n - 1)) : p);
const nf = (n) => Number(n).toLocaleString('id-ID');

async function refreshPlayers() {
  const p = await go().GetPlayers();
  const el = $('players');
  if (p.ok) { el.classList.add('on'); $('playersText').textContent = `${nf(p.players)} / ${nf(p.max)} online`; }
  else { el.classList.remove('on'); $('playersText').textContent = 'Server offline'; }
}

let series = [];
async function refreshMetrics() {
  const m = await go().GetMetrics();
  if (!m.ok) return;
  $('peakText').textContent = `Peak ${nf(m.peak)}`;
  $('rankText').textContent = `Rank #${nf(m.rank)}`;
  $('chStats').innerHTML = `Peak <b>${nf(m.peak)}</b> · Rata-rata <b>${nf(m.avg)}</b> · Terendah <b>${nf(m.min)}</b> · Rank global <b>#${nf(m.rank)}</b>`;
  series = m.series || [];
  drawChart();
}

function drawChart() {
  const svg = $('chSvg');
  if (series.length < 2) { svg.innerHTML = ''; return; }
  const W = 600, H = 100, top = 4;
  const t0 = series[0].t, t1 = series[series.length - 1].t;
  const max = Math.max(...series.map((s) => s.n), 1) * 1.08;
  const X = (t) => ((t - t0) / (t1 - t0)) * W;
  const Y = (n) => H - (n / max) * (H - top);
  const pts = series.map((s) => [X(s.t), Y(s.n)]);
  // smooth line (midpoint quadratic curve)
  let d = `M${pts[0][0]},${pts[0][1]}`;
  for (let i = 1; i < pts.length - 1; i++) {
    const mx = (pts[i][0] + pts[i + 1][0]) / 2, my = (pts[i][1] + pts[i + 1][1]) / 2;
    d += ` Q${pts[i][0]},${pts[i][1]} ${mx},${my}`;
  }
  d += ` L${pts[pts.length - 1][0]},${pts[pts.length - 1][1]}`;
  svg.innerHTML = `<line class="grid" x1="0" x2="${W}" y1="${H * 0.25}" y2="${H * 0.25}" vector-effect="non-scaling-stroke"/><line class="grid" x1="0" x2="${W}" y1="${H * 0.6}" y2="${H * 0.6}" vector-effect="non-scaling-stroke"/>
    <path d="${d} L${W},${H} L0,${H} Z" fill="rgba(195,50,53,.18)"/>
    <path d="${d}" fill="none" stroke="#c33235" stroke-width="1.8" vector-effect="non-scaling-stroke"/>`;
  drawAxis(t0, t1);
}

// hour labels every 4 hours along the bottom of the chart
function drawAxis(t0, t1) {
  const ax = $('chAxis');
  ax.innerHTML = '';
  const d = new Date(t0);
  d.setMinutes(0, 0, 0);
  while (d.getTime() < t0 || d.getHours() % 4 !== 0) d.setHours(d.getHours() + 1);
  for (let t = d.getTime(); t <= t1; t += 4 * 3600 * 1000) {
    const f = (t - t0) / (t1 - t0);
    const sp = document.createElement('span');
    sp.textContent = new Date(t).toLocaleTimeString('id-ID', { hour: '2-digit', minute: '2-digit', hour12: false }).replace('.', ':');
    sp.style.left = f * 100 + '%';
    if (f < 0.03) sp.style.transform = 'none';
    if (f > 0.97) sp.style.transform = 'translateX(-100%)';
    ax.appendChild(sp);
  }
}

$('chBox').addEventListener('mousemove', (e) => {
  if (series.length < 2) return;
  const r = $('chBox').getBoundingClientRect();
  const f = Math.min(1, Math.max(0, (e.clientX - r.left) / r.width));
  const t = series[0].t + f * (series[series.length - 1].t - series[0].t);
  const s = series.reduce((a, b) => (Math.abs(b.t - t) < Math.abs(a.t - t) ? b : a));
  const tip = $('chTip');
  tip.style.display = 'block';
  tip.style.left = Math.min(r.width - 40, Math.max(40, f * r.width)) + 'px';
  tip.textContent = `${new Date(s.t).toLocaleTimeString('id-ID', { hour: '2-digit', minute: '2-digit', hour12: false }).replace('.', ':')} · ${nf(s.n)} pemain`;
});
$('chBox').addEventListener('mouseleave', () => ($('chTip').style.display = 'none'));

// ---- cache ----
async function loadCache() {
  cacheInfo = await go().GetCacheInfo();
  $('cacheVer').textContent = cacheInfo.binVersion ? 'versi ' + cacheInfo.binVersion : 'versi -';
  $('cSize').textContent = cacheInfo.size > 0 ? fmtBytes(cacheInfo.size) : '-';
  $('cUpdated').textContent = fmtUpdated(cacheInfo.updated);
  $('cDir').textContent = cacheInfo.dir ? shortPath(cacheInfo.dir, 48) : '-';
  $('cDir').title = cacheInfo.dir || '';
  $('cFivem').textContent = cacheInfo.fivem ? shortPath(cacheInfo.fivem, 48) : 'belum ditemukan, klik Cari';
  $('cFivem').title = cacheInfo.fivem || '';
  $('cTarget').textContent = cacheInfo.target ? shortPath(cacheInfo.target, 48) : '-';
  $('cTarget').title = cacheInfo.target || '';
  if (!cacheInfo.ok) setMsg('Gagal cek CDN: ' + cacheInfo.error, 'err');
  render(await go().GetDownloadState());
}
function setMsg(t, cls = '') { $('dlMsg').textContent = t || ''; $('dlMsg').className = 'msg ' + cls; }

function render(p) {
  dlState = p.state;
  const total = p.total || (cacheInfo && cacheInfo.size) || 0;
  $('dlFill').style.width = (p.percent || 0) + '%';
  $('dlPct').textContent = (p.percent || 0).toFixed(1) + '%';
  $('dlDone').textContent = `${fmtBytes(p.downloaded)} / ${fmtBytes(total)}`;
  $('dlSpeed').textContent = p.speed > 0 ? fmtBytes(p.speed) + '/s' : '-';
  $('dlEta').textContent = p.state === 'downloading' ? fmtEta(p.eta) : '-';
  const running = p.state === 'downloading';
  $('btnDl').disabled = running || p.state === 'done';
  $('btnDl').textContent = p.state === 'paused' || p.state === 'error' ? 'Lanjutkan' : 'Mulai Download';
  $('btnPause').disabled = !running;
  $('btnExtract').disabled = p.state !== 'done';
  if (p.state === 'error') setMsg(p.message, 'err');
  else if (p.state === 'done') setMsg('Download selesai. Klik "Extract ke FiveM".', 'ok');
  else setMsg(p.message);
}
on('cache:progress', render);

$('btnDl').onclick = async () => { setMsg(''); try { await go().StartDownload(); } catch (e) { setMsg(String(e), 'err'); } };
$('btnPause').onclick = () => go().PauseDownload();
$('btnDir').onclick = async () => {
  try { const d = await go().PickDownloadDir(); if (d) loadCache(); } catch (e) { setMsg(String(e), 'err'); }
};
$('btnOpenDir').onclick = () => go().OpenFolder(cacheInfo ? cacheInfo.dir : '');
$('btnFivem').onclick = async () => {
  try { const d = await go().PickFiveM(); if (d) loadCache(); } catch (e) { setMsg(String(e), 'err'); }
};
$('btnExtract').onclick = async () => {
  $('btnExtract').disabled = true;
  setMsg('Menyiapkan installer...');
  const r = await go().ExtractCache();
  setMsg(r.msg, r.ok ? 'ok' : 'err');
  $('btnExtract').disabled = dlState !== 'done';
};

// ---- launcher status (version gate) ----
const ICONS = {
  update: '<path d="M12 19V5M5 12l7-7 7 7"/>',
  force: '<path d="M12 8v5M12 17h.01"/><circle cx="12" cy="12" r="9"/>',
  maintenance: '<path d="M9 5v14M15 5v14"/>',
  disabled: '<circle cx="12" cy="12" r="9"/><path d="M5.6 5.6l12.8 12.8"/>',
  offline: '<path d="M12 8v5M12 17h.01"/><circle cx="12" cy="12" r="9"/>',
  checking: '<path d="M12 3a9 9 0 1 0 9 9"/>',
};
const STATE_LABEL = {
  update: 'Update tersedia', force: 'Update wajib', maintenance: 'Maintenance',
  disabled: 'Dinonaktifkan', offline: 'Tidak terhubung', checking: 'Memeriksa',
};

let started = false;
let blocked = true;
let updating = false;
let popupOpen = false; // an optional update's popup is only shown after clicking NEW
let curStatus = null;
const notesCache = {};

// An optional update ("update") never forces anything: it only lights up the NEW tag
// next to the version, and clicking it opens the details popup. Locks (force update,
// maintenance, ...) open the same popup on their own and cannot be closed.
function renderNewTag(st) {
  const tag = $('newTag');
  tag.hidden = st.state !== 'update';
  if (tag.hidden) return;
  tag.title = `Versi v${st.latest} tersedia. Klik untuk melihat detail.`;
}

// Release notes (GitHub release body), rendered with DOM nodes only, never as HTML.
function inline(parent, text) {
  text.split(/(\*\*[^*]+\*\*|`[^`]+`)/).forEach((part) => {
    if (!part) return;
    let el;
    if (part.startsWith('**') && part.endsWith('**') && part.length > 4) { el = document.createElement('b'); el.textContent = part.slice(2, -2); }
    else if (part.startsWith('`') && part.endsWith('`') && part.length > 2) { el = document.createElement('code'); el.textContent = part.slice(1, -1); }
    else el = document.createTextNode(part);
    parent.appendChild(el);
  });
}

const NOTES_STOP = /^(?:#{1,6}\s*)?(?:\*\*)?\s*(?:fitur|file|sha-?256)\b/i;

function renderNotes(box, n) {
  box.replaceChildren();
  const head = document.createElement('div');
  head.className = 'nt-head';
  head.textContent = (n.title || 'Catatan rilis') + (n.published ? ' · ' + new Date(n.published).toLocaleDateString('id-ID', { day: 'numeric', month: 'long', year: 'numeric' }) : '');
  box.appendChild(head);
  let list = null;
  let seenFirst = false;
  let stop = false;
  n.body.split('\n').forEach((raw) => {
    const line = raw.trim();
    // The popup only needs what is new; the feature list, file list and checksum stay on the release page.
    if (stop || NOTES_STOP.test(line)) { stop = true; return; }
    if (!line) { list = null; return; }
    const li = line.match(/^[-*]\s+(.*)$/);
    if (li) {
      if (!list) { list = document.createElement('ul'); box.appendChild(list); }
      const item = document.createElement('li');
      inline(item, li[1]);
      list.appendChild(item);
      return;
    }
    list = null;
    const h = line.match(/^#{1,6}\s+(.*)$/);
    if (h && !seenFirst && h[1].trim().toLowerCase() === (n.title || '').trim().toLowerCase()) { seenFirst = true; return; } // the popup header already shows the title
    seenFirst = true;
    const el = document.createElement(h ? 'h4' : 'p');
    inline(el, h ? h[1] : line);
    box.appendChild(el);
  });
}

async function loadNotes(st) {
  const box = $('ovNotes');
  if (!st.latest || (st.state !== 'update' && st.state !== 'force')) { box.hidden = true; return; }
  if (notesCache[st.latest]) { renderNotes(box, notesCache[st.latest]); box.hidden = false; return; }
  box.hidden = true;
  try {
    const n = await go().GetReleaseNotes(st.latest);
    if (!n || !n.ok || !(n.body || '').trim()) return;
    notesCache[st.latest] = n;
    if (curStatus && curStatus.latest === st.latest && !$('ov').hidden) { renderNotes(box, n); box.hidden = false; }
  } catch (e) { /* notes are optional */ }
}

function renderStatus(st) {
  curStatus = st;
  blocked = !!st.blocked;
  $('ver').textContent = 'v' + st.current;
  if (updating) return;
  const ov = $('ov');
  renderNewTag(st);
  if (st.state === 'ok' || (st.state === 'update' && !popupOpen)) {
    ov.hidden = true;
    if (!started) startApp();
    return;
  }
  ov.hidden = false;
  ov.querySelector('.ov-card').className = 'ov-card tone-' + st.state;
  $('ovIcon').className = 'ov-icon' + (st.state === 'checking' ? ' spin' : '');
  $('ovIcon').innerHTML = `<svg viewBox="0 0 24 24">${ICONS[st.state] || ICONS.force}</svg>`;
  $('ovState').textContent = STATE_LABEL[st.state] || '';
  $('ovTitle').textContent = st.title;
  $('ovMsg').textContent = st.state === 'checking' ? 'Mohon tunggu sebentar...' : st.message;
  const ver = [`Versi kamu <b>v${st.current}</b>`];
  if (st.latest) ver.push(`Terbaru <b>v${st.latest}</b>`);
  if (st.state === 'force' && st.minimum) ver.push(`Minimum <b>v${st.minimum}</b>`);
  $('ovVer').innerHTML = ver.join(' · ');
  $('ovVer').hidden = st.state === 'checking';

  // A forced update can only be answered by updating. An optional one can be closed.
  const show = (id, on) => ($(id).hidden = !on);
  show('ovDl', !!st.downloadUrl && (st.state === 'force' || st.state === 'update'));
  show('ovClose', st.state === 'update');
  show('ovRetry', ['offline', 'maintenance', 'disabled'].includes(st.state));
  show('ovQuit', st.blocked && st.state !== 'checking' && st.state !== 'force');
  show('ovManual', false);
  $('ovProg').hidden = true;
  $('ovDl').textContent = st.selfUpdate ? 'Update Sekarang' : 'Download Update';
  $('ovDl').onclick = st.selfUpdate ? startUpdate : () => go().OpenURL(st.downloadUrl);
  $('ovManual').onclick = () => go().OpenURL(st.downloadUrl);
  loadNotes(st);
}

async function startUpdate() {
  try {
    await go().StartUpdate();
  } catch (e) {
    return updateFailed(String(e));
  }
  updating = true;
  ['ovDl', 'ovManual', 'ovClose', 'ovRetry', 'ovQuit'].forEach((id) => ($(id).hidden = true));
  $('ovProg').hidden = false;
  $('ovFill').style.width = '0';
  $('ovProgTxt').textContent = 'Menghubungkan...';
  $('ovMsg').textContent = 'Mengunduh update. Launcher akan terbuka ulang sendiri setelah selesai.';
}

function updateFailed(msg) {
  updating = false;
  renderStatus(curStatus);
  $('ovMsg').textContent = msg;
  $('ovManual').hidden = false;
}

on('update:progress', (p) => {
  if (p.state === 'error') return updateFailed(p.error);
  if (p.state === 'downloading') {
    const pct = p.total > 0 ? Math.min(100, (p.done / p.total) * 100) : 0;
    $('ovFill').style.width = pct + '%';
    $('ovProgTxt').textContent = `${fmtBytes(p.done)} / ${fmtBytes(p.total)} · ${fmtBytes(p.speed)}/s`;
  } else {
    $('ovFill').style.width = '100%';
    $('ovProgTxt').textContent = { verifying: 'Memverifikasi file...', applying: 'Memasang update...', restarting: 'Membuka ulang launcher...' }[p.state] || '';
  }
});

$('newTag').onclick = () => {
  if (!curStatus || curStatus.state !== 'update') return;
  popupOpen = true;
  renderStatus(curStatus);
};
$('ovClose').onclick = () => {
  popupOpen = false;
  renderStatus(curStatus);
};

$('ovRetry').onclick = async () => {
  const b = $('ovRetry');
  b.disabled = true; b.textContent = 'Memeriksa...';
  renderStatus(await go().CheckStatus());
  b.disabled = false; b.textContent = 'Cek Lagi';
};
$('ovQuit').onclick = () => go().Quit();

const REPO_URL = 'https://github.com/irhagram/IME-Launcher';
$('ver').addEventListener('click', () => go().OpenURL(REPO_URL));
$('ver').addEventListener('keydown', (e) => { if (e.key === 'Enter') go().OpenURL(REPO_URL); });
on('launcher:status', renderStatus);

// ---- boot ----
function startApp() {
  started = true;
  runAll();
  refreshPlayers();
  refreshMetrics();
  refreshFivemLoc();
  refreshDiscords();
  setInterval(refreshDiscords, 300000);
  setInterval(refreshPlayers, 15000);
  setInterval(refreshMetrics, 120000);
}

window.addEventListener('DOMContentLoaded', async () => {
  cfg = await go().GetConfig();
  buildGates();
  renderStatus(await go().GetStatus());
});
