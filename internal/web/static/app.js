'use strict';

// ---------- helpers ----------
const $ = (s, el = document) => el.querySelector(s);
const $$ = (s, el = document) => [...el.querySelectorAll(s)];
const esc = (s) => String(s ?? '').replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));

async function api(path, opts = {}) {
  const isForm = opts.body instanceof FormData;
  const r = await fetch('api/' + path, {
    method: opts.method || (opts.body ? 'POST' : 'GET'),
    headers: opts.body && !isForm ? { 'Content-Type': 'application/json' } : {},
    body: opts.body && !isForm ? JSON.stringify(opts.body) : opts.body,
  });
  const j = await r.json().catch(() => ({}));
  if (!r.ok) throw new Error(j.error || r.statusText);
  return j;
}

const CATS = {
  spam: { name: 'Spam', key: 's', does: 'blocked, reported and deleted' },
  offer: { name: 'Offer', key: 'o', does: 'deleted from the phone, kept here' },
  archive: { name: 'Archive', key: 'a', does: 'moved to Archived on the phone' },
  keep: { name: 'Keep', key: 'k', does: 'left alone' },
  unknown: { name: 'Unsure', does: 'left alone until you decide' },
};
const LABELS = ['spam', 'offer', 'archive', 'keep'];
const catName = (c) => (CATS[c] || CATS.unknown).name;
const tag = (c) => `<span class="tag cat-${esc(c || 'unknown')} ${c === 'offer' ? 'offer' : ''}">${esc(catName(c))}</span>`;

const dtShort = new Intl.DateTimeFormat('en-GB', { day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit' });
const dtTime = new Intl.DateTimeFormat('en-GB', { hour: '2-digit', minute: '2-digit' });
let dtJalali;
try { dtJalali = new Intl.DateTimeFormat('fa-IR-u-ca-persian', { dateStyle: 'full', timeStyle: 'short' }); } catch { dtJalali = null; }
function fmtTime(ts) {
  if (!ts) return '';
  const d = new Date(ts * 1000);
  return d.toDateString() === new Date().toDateString() ? 'today ' + dtTime.format(d) : dtShort.format(d);
}
const jalali = (ts) => (ts && dtJalali ? dtJalali.format(new Date(ts * 1000)) : '');
function fmtIn(ts) {
  const m = Math.round((ts * 1000 - Date.now()) / 60000);
  if (m <= 1) return 'within a minute';
  if (m < 60) return `in ${m} min`;
  return `in ${Math.round(m / 60)} h`;
}

// Discount codes: mixed letters+digits in capitals, e.g. FALL50, DK40.
const CODE_RE = /\b(?=[A-Z0-9]*[A-Z])(?=[A-Z0-9]*\d)[A-Z0-9]{4,14}\b/g;
const bodyHTML = (text, codes) => {
  const e = esc(text);
  return codes ? e.replace(CODE_RE, (m) => `<mark class="code" title="Copy code" data-code="${m}">${m}</mark>`) : e;
};

let toastTimer;
function toast(msg) {
  const t = $('#toast');
  t.textContent = msg;
  t.hidden = false;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => (t.hidden = true), 3200);
}

function confirmBox(title, html, okLabel, danger) {
  return new Promise((resolve) => {
    const m = $('#modal');
    m.innerHTML = `<div class="modal-bg"><div class="modal" role="dialog" aria-modal="true" aria-labelledby="mt">
      <h2 id="mt">${esc(title)}</h2><div>${html}</div>
      <div class="actions"><button class="btn" data-a="no">Cancel</button><button class="btn ${danger ? 'danger' : 'primary'}" data-a="yes">${esc(okLabel)}</button></div></div></div>`;
    const done = (v) => { m.innerHTML = ''; resolve(v); };
    m.querySelector('[data-a=yes]').onclick = () => done(true);
    m.querySelector('[data-a=no]').onclick = () => done(false);
    m.querySelector('.modal-bg').onclick = (e) => { if (e.target.classList.contains('modal-bg')) done(false); };
    m.querySelector('[data-a=yes]').focus();
  });
}

// ---------- phone layout ----------
const MQ = window.matchMedia('(max-width: 720px)');
const isMobile = () => MQ.matches;
// The lists the Messages tab swipes between, in order: [view, tab name, category, status count key].
const MSG_TABS = [['spam', 'Spam', 'spam', 'spam'], ['offers', 'Offers', 'offer', 'offer'], ['archive', 'Archive', 'archive', 'archive'], ['keep', 'Keep', 'keep', 'keep'], ['all', 'All', '', 'messages']];
const SUBPAGES = { phone: 1, cleaning: 1, rules: 1, test: 1, model: 1 };
const tabOf = (v) => (['home', 'review', 'activity'].includes(v) ? v : MSG_TABS.some((t) => t[0] === v) ? 'messages' : 'settings');
const svg = (d, size = 22) => `<svg width="${size}" height="${size}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${d}</svg>`;
const ICON = {
  back: svg('<path d="M15 18l-6-6 6-6"/>'),
  up: svg('<path d="M18 15l-6-6-6 6"/>', 20),
  down: svg('<path d="M6 9l6 6 6-6"/>', 20),
  chev: svg('<path d="M9 6l6 6-6 6"/>', 18),
  copy: svg('<rect x="9" y="9" width="11" height="11" rx="2"/><path d="M5 15V6a2 2 0 0 1 2-2h8"/>', 16),
};
const backHTML = () => (isMobile() && SUBPAGES[S.view] ? `<button class="m-back" onclick="go('settings')">${ICON.back}Settings</button>` : '');
const defaultView = () => (isMobile() ? 'home' : 'review');

// Horizontal swipes on touch screens. Areas that scroll sideways opt out with .no-swipe.
function onSwipe(el, cb) {
  let x0 = null, y0 = 0, t0 = 0;
  el.addEventListener('touchstart', (e) => {
    if (e.touches.length !== 1 || e.target.closest('.no-swipe, input, textarea, select')) { x0 = null; return; }
    x0 = e.touches[0].clientX; y0 = e.touches[0].clientY; t0 = Date.now();
  }, { passive: true });
  el.addEventListener('touchend', (e) => {
    if (x0 === null) return;
    const t = e.changedTouches[0], dx = t.clientX - x0, dy = t.clientY - y0;
    x0 = null;
    if (Math.abs(dx) > 70 && Math.abs(dx) > 2 * Math.abs(dy) && Date.now() - t0 < 700 && !String(window.getSelection?.() || '')) cb(dx < 0 ? 1 : -1);
  }, { passive: true });
}
function animate(el, dir) {
  if (!el || !dir) return;
  el.classList.remove('swipe-next', 'swipe-prev');
  void el.offsetWidth;
  el.classList.add(dir > 0 ? 'swipe-next' : 'swipe-prev');
}
function copyCode(code) {
  const done = () => toast(`Copied ${code}`);
  if (navigator.clipboard?.writeText) navigator.clipboard.writeText(code).then(done, () => fallbackCopy(code, done));
  else fallbackCopy(code, done);
}
function fallbackCopy(text, done) {
  const ta = document.createElement('textarea');
  ta.value = text; ta.setAttribute('readonly', ''); ta.style.position = 'fixed'; ta.style.opacity = '0';
  document.body.appendChild(ta); ta.select();
  try { document.execCommand('copy'); done(); } catch (e) { toast(`Code: ${text}`); }
  ta.remove();
}
const fmtInterval = (min) => (min % 1440 === 0 ? `${min / 1440} day${min === 1440 ? '' : 's'}` : min % 60 === 0 ? `${min / 60} h` : `${min} min`);

// ---------- state ----------
const S = { view: 'review', status: null, list: [], total: 0, filter: {}, selected: null, detail: null, wasRunning: false };

// ---------- status / header ----------
async function refreshStatus() {
  try {
    S.status = await api('status');
  } catch (e) {
    $('#statusLine').innerHTML = `<span class="err">${esc(e.message)}</span>`;
    return;
  }
  const st = S.status;
  const s = st.stats || {};
  const by = s.by_category || {};
  const counts = { review: s.review, spam: by.spam, offer: by.offer, archive: by.archive, keep: by.keep, messages: s.messages, rules: s.rules };
  $$('[data-count]').forEach((el) => {
    const v = counts[el.dataset.count];
    el.textContent = v ? v : '';
    el.classList.toggle('attention', el.dataset.count === 'review' && v > 0);
  });
  $('#modeDry').setAttribute('aria-pressed', st.mode !== 'live');
  $('#modeLive').setAttribute('aria-pressed', st.mode === 'live');

  const parts = [];
  if (st.running) parts.push(`<span class="progress">${esc(st.progress || 'running')}</span>`);
  else if (st.last_run) {
    const lr = st.last_run;
    const ls = lr.stats || {};
    const what = lr.error ? `<span class="err">failed: ${esc(lr.error)}</span>`
      : `${lr.dry ? 'preview' : 'cleaned'}: <b>${(ls.plan_block || 0)}</b> spam, <b>${(ls.plan_delete || 0) + (ls.action_delete_message || 0)}</b> offers, <b>${ls.plan_archive || 0}</b> to archive`;
    parts.push(`<span title="${esc(jalali(lr.started_at))}">Last run ${esc(fmtTime(lr.started_at))}, ${what}</span>`);
  } else parts.push('<span>No runs yet</span>');
  if (st.next_run && !st.running) parts.push(`<span>Next ${st.mode === 'live' ? 'clean' : 'preview'} ${esc(fmtIn(st.next_run))}</span>`);
  else if (st.paired && !st.running && !st.demo) parts.push(`<span>Schedule off <a href="#/phone">turn on</a></span>`);
  $('#statusLine').innerHTML = parts.join('');

  const b = [];
  if (st.demo) b.push(`<div class="banner warn">Demo mode: sample messages, no phone connected.</div>`);
  else if (!st.paired) b.push(`<div class="banner">Your phone isn't paired yet. <button class="btn primary" onclick="go('phone')">Pair phone</button></div>`);
  else if (st.phone?.error) b.push(`<div class="banner warn">Can't reach your phone: ${esc(st.phone.error)} <button class="btn" onclick="go('phone')">Details</button></div>`);
  const up = st.update;
  let skipped = '';
  try { skipped = localStorage.getItem('smsc.skipUpdate') || ''; } catch (e) {}
  if (up?.newer && up.download && skipped !== up.latest) {
    b.push(`<div class="banner">Version ${esc(up.latest)} is out (you have ${esc(up.current)}).
      <a class="btn primary" href="${esc(up.download)}" target="_blank" rel="noopener noreferrer">Download</a>
      <button class="btn ghost" id="skipUpdate">Later</button></div>`);
  }
  $('#banners').innerHTML = b.join('');
  const skip = $('#skipUpdate');
  if (skip) skip.onclick = () => { try { localStorage.setItem('smsc.skipUpdate', up.latest); } catch (e) {} refreshStatus(); };
  $('#runNow').disabled = $('#runPreview').disabled = st.running || !st.paired;

  if (S.wasRunning && !st.running) {
    toast('Run finished');
    if (['review', 'spam', 'offers', 'archive', 'keep', 'all', 'activity'].includes(S.view)) render();
  }
  S.wasRunning = st.running;
  if (S.view === 'home' && isMobile() && !S.selected) renderHome();
  clearTimeout(S.statusTimer);
  S.statusTimer = setTimeout(refreshStatus, st.running ? 2000 : 15000);
}

$('#modeLive').onclick = async () => {
  if (S.status?.mode === 'live') return;
  const ok = await confirmBox('Switch scheduled runs to live?',
    `<p>Every scheduled run will then change your phone:</p><ul>
      <li><b>Spam</b> is blocked, reported to Google and deleted.</li>
      <li><b>Offers</b> are deleted from the phone; a copy stays in Offers here.</li>
      <li><b>Service notices</b> (banks, codes, deliveries) are archived.</li></ul>
      <p>Contacts, group chats, pinned chats and threads you replied to are never touched. Messages it is unsure about stay in your inbox.</p>`, 'Go live', true);
  if (!ok) return;
  await api('settings', { method: 'PUT', body: { mode: 'live' } });
  toast('Scheduled runs are live');
  refreshStatus();
};
$('#modeDry').onclick = async () => {
  await api('settings', { method: 'PUT', body: { mode: 'dry' } });
  toast('Scheduled runs only preview now');
  refreshStatus();
};
$('#runPreview').onclick = async () => {
  try { await api('run', { body: { dry: true } }); toast('Preview started, nothing on the phone changes'); refreshStatus(); } catch (e) { toast(e.message); }
};
$('#runNow').onclick = async () => {
  const ok = await confirmBox('Clean your phone now?',
    `<p>Blocks and deletes spam, deletes offers (copies stay here) and archives service notices, up to the per-run limit in settings. Contacts are never touched.</p>
     <p class="muted small">Tip: run a preview first and check Activity.</p>`, 'Clean now', true);
  if (!ok) return;
  try { await api('run', { body: { dry: false } }); toast('Cleaning started'); refreshStatus(); } catch (e) { toast(e.message); }
};

// ---------- routing ----------
function go(view) { location.hash = '#/' + view; }
window.go = go;
window.addEventListener('hashchange', () => { S.view = location.hash.replace('#/', '') || defaultView(); S.selected = null; render(); });
$$('.tray').forEach((b) => (b.onclick = () => go(b.dataset.view)));
$$('#bottomNav [data-tab]').forEach((b) => (b.onclick = () => go(b.dataset.tab === 'messages' ? (S.lastMsgTab || 'offers') : b.dataset.tab)));
// Android's back button closes an open message before it leaves the list.
window.addEventListener('popstate', () => { if (S.selected && isMobile() && !history.state?.smscMsg) closeTeach(); });
MQ.addEventListener?.('change', () => render());

const LIST_VIEWS = {
  review: { title: 'Needs review', cat: 'unknown', sub: 'The classifier wasn\'t sure about these, so it left them in your inbox. Label a few and it learns from them.', kbd: true, label: 'none', onPhone: 'yes' },
  spam: { title: 'Spam', cat: 'spam', sub: 'Blocked, reported to Google and deleted from the phone. Copies are kept here.' },
  offers: { title: 'Offers', cat: 'offer', sub: 'Discounts and promotions, removed from your phone and kept here to search. Tap a code to copy it.', offers: true },
  archive: { title: 'Archive', cat: 'archive', sub: 'Bank alerts, codes, deliveries and other service notices. They stay on your phone, under Archived.' },
  keep: { title: 'Keep', cat: 'keep', sub: 'Written by a person. Never touched.' },
  all: { title: 'Everything', cat: '', sub: 'Every message the cleaner has seen. Contacts are never read, so they are not here.' },
};

function render() {
  const mobile = isMobile();
  document.body.classList.toggle('mobile', mobile);
  // Home, Settings and Cleaning are phone pages; the desktop has the header and Phone & settings instead.
  if (!mobile && ['home', 'settings', 'cleaning'].includes(S.view)) return go(S.view === 'home' ? 'review' : 'phone');
  $$('.tray').forEach((b) => { if (b.dataset.view === S.view) b.setAttribute('aria-current', 'page'); else b.removeAttribute('aria-current'); });
  const tab = tabOf(S.view);
  $$('#bottomNav [data-tab]').forEach((b) => { if (b.dataset.tab === tab) b.setAttribute('aria-current', 'page'); else b.removeAttribute('aria-current'); });
  if (MSG_TABS.some((t) => t[0] === S.view)) S.lastMsgTab = S.view;
  const listView = LIST_VIEWS[S.view];
  $('#app').classList.toggle('wide', !listView);
  $('#main').onclick = null;
  closeTeach();
  if (mobile) window.scrollTo(0, 0);
  if (listView) return renderList(listView);
  ({ home: renderHome, settings: renderSettings, cleaning: renderCleaning, rules: renderRules, test: renderTest, model: renderModel, activity: renderActivity, phone: renderPhone }[S.view] || (() => go(defaultView())))();
}

// ---------- message lists ----------
async function renderList(v) {
  const f = (S.filter[S.view] ||= { q: '', onPhone: v.onPhone || '', label: v.label || '', sender: '', from: '', to: '' });
  const main = $('#main');
  const mobile = isMobile();
  if (mobile) main.innerHTML = mobileListHTML(v, f);
  else main.innerHTML = `
    <div class="view-head"><h1>${esc(v.title)}</h1><span class="muted num" id="total"></span></div>
    <p class="view-sub">${esc(v.sub)}${v.kbd ? '<span class="kbd-hint"> Keys: ↑ ↓ to move, S O A K to label.</span>' : ''}</p>
    <div class="filters">
      <input class="input search" type="search" id="q" placeholder="Search text or sender" value="${esc(f.q)}" aria-label="Search">
      ${v.offers ? `<select class="input" id="sender" aria-label="Sender"><option value="">All senders</option></select>
        <label class="date-field">From <input class="input" type="date" id="from" value="${esc(f.from)}"></label>
        <label class="date-field">To <input class="input" type="date" id="to" value="${esc(f.to)}"></label>` : ''}
      ${S.view !== 'review' ? `<div class="seg" role="group" aria-label="Where">
        ${[['', 'All'], ['yes', 'On phone'], ['no', 'Removed']].map(([k, n]) => `<button data-onphone="${k}" aria-pressed="${f.onPhone === k}">${n}</button>`).join('')}</div>
        <div class="seg" role="group" aria-label="Labels">
        ${[['', 'Any'], ['any', 'Labeled by you'], ['none', 'Guessed']].map(([k, n]) => `<button data-label="${k}" aria-pressed="${f.label === k}">${n}</button>`).join('')}</div>` : ''}
    </div>
    <div class="slips" id="slips" role="listbox" aria-label="Messages"></div>
    <button class="btn more" id="more" hidden>Show more</button>`;
  let t;
  $('#q').oninput = (e) => { clearTimeout(t); t = setTimeout(() => { f.q = e.target.value; loadList(v, true); }, 250); };
  $$('[data-onphone]').forEach((b) => (b.onclick = () => { f.onPhone = b.dataset.onphone; renderList(v); }));
  $$('[data-label]').forEach((b) => (b.onclick = () => { f.label = b.dataset.label; renderList(v); }));
  if (mobile) {
    const chip = (el, on) => el?.closest('.fchip')?.classList.toggle('on', !!on);
    $('#onPhoneSel')?.addEventListener('change', (e) => { f.onPhone = e.target.value; chip(e.target, f.onPhone); loadList(v, true); });
    $('#labelSel')?.addEventListener('change', (e) => { f.label = e.target.value; chip(e.target, f.label); loadList(v, true); });
    $('#dateChip')?.addEventListener('click', () => { const r = $('#dateRow'); r.hidden = !r.hidden; });
    $$('.mtab').forEach((b) => (b.onclick = () => go(b.dataset.view)));
    $('.mtab[aria-selected="true"]')?.scrollIntoView({ inline: 'center', block: 'nearest' });
    animate(main, S.swipeDir);
    S.swipeDir = 0;
  }
  if (v.offers) {
    api('senders?category=offer').then((list) => {
      const sel = $('#sender');
      if (!sel) return;
      sel.innerHTML += list.map((s) => `<option value="${esc(s.sender)}" ${s.sender === f.sender ? 'selected' : ''}>${esc(s.name && s.name !== s.sender ? `${s.name} (${s.sender})` : s.sender)} (${s.count})</option>`).join('');
    });
    const dates = () => { const b = $('#dateChip'); if (b) { b.textContent = f.from || f.to ? 'Dates set' : 'Any date'; b.closest('.fchip').classList.toggle('on', !!(f.from || f.to)); } };
    $('#sender').onchange = (e) => { f.sender = e.target.value; e.target.closest('.fchip')?.classList.toggle('on', !!f.sender); loadList(v, true); };
    $('#from').onchange = (e) => { f.from = e.target.value; dates(); loadList(v, true); };
    $('#to').onchange = (e) => { f.to = e.target.value; dates(); loadList(v, true); };
  }
  $('#more').onclick = () => loadList(v, false);
  loadList(v, true);
}

function mobileListHTML(v, f) {
  const st = S.status?.stats || {};
  const by = st.by_category || {};
  const counts = { spam: by.spam, offer: by.offer, archive: by.archive, keep: by.keep, messages: st.messages };
  const inTabs = MSG_TABS.some((t) => t[0] === S.view);
  const head = inTabs ? '<div class="view-head"><h1>Messages</h1></div>'
    : `<div class="view-head"><h1>${esc(v.title)}</h1><span class="muted num" id="total"></span></div>
      <p class="view-sub">${S.view === 'review' ? 'The cleaner wasn\'t sure, so these stay on your phone. Each answer teaches it.' : esc(v.sub)}</p>`;
  const tabs = inTabs ? `<div class="mtabs no-swipe" role="tablist" aria-label="Lists">${MSG_TABS.map(([view, name, cat, key]) => `<button type="button" class="mtab ${cat ? 'cat-' + cat : ''}" role="tab" data-view="${view}" aria-selected="${view === S.view}">${name} <span class="num">${counts[key] || ''}</span></button>`).join('')}</div>` : '';
  const sel = (id, label, opts, cur) => `<label class="fchip ${cur ? 'on' : ''}"><select id="${id}" aria-label="${label}">${opts.map(([k, n]) => `<option value="${k}" ${k === cur ? 'selected' : ''}>${n}</option>`).join('')}</select></label>`;
  const chips = [
    v.offers ? `<label class="fchip ${f.sender ? 'on' : ''}"><select id="sender" aria-label="Sender"><option value="">All senders</option></select></label>` : '',
    v.offers ? `<span class="fchip plain ${f.from || f.to ? 'on' : ''}"><button type="button" id="dateChip">${f.from || f.to ? 'Dates set' : 'Any date'}</button></span>` : '',
    S.view !== 'review' ? sel('onPhoneSel', 'Where', [['', 'On phone or removed'], ['yes', 'On phone'], ['no', 'Removed from phone']], f.onPhone) : '',
    S.view !== 'review' ? sel('labelSel', 'Labels', [['', 'Labeled or guessed'], ['any', 'Labeled by you'], ['none', 'Guessed']], f.label) : '',
  ].join('');
  return `${head}${tabs}
    <div class="m-filters">
      <input class="input search" type="search" id="q" placeholder="Search text or sender" value="${esc(f.q)}" aria-label="Search">
      ${chips ? `<div class="fchips no-swipe">${chips}</div>` : ''}
      ${v.offers ? `<div class="date-row" id="dateRow" ${f.from || f.to ? '' : 'hidden'}><label class="date-field">From <input class="input" type="date" id="from" value="${esc(f.from)}"></label><label class="date-field">To <input class="input" type="date" id="to" value="${esc(f.to)}"></label></div>` : ''}
    </div>
    ${inTabs ? '<p class="m-count" id="total"></p>' : ''}
    <div class="slips" id="slips" role="listbox" aria-label="Messages"></div>
    <button class="btn more" id="more" hidden>Show more</button>`;
}

function listQuery(v, offset) {
  const f = S.filter[S.view];
  const p = new URLSearchParams({ limit: 60, offset });
  if (v.cat) p.set('category', v.cat);
  if (f.q) p.set('q', f.q);
  if (f.onPhone) p.set('on_phone', f.onPhone);
  if (f.label) p.set('label', f.label);
  if (f.sender) p.set('sender', f.sender);
  if (f.from) p.set('from', Math.floor(new Date(f.from + 'T00:00').getTime() / 1000));
  if (f.to) p.set('to', Math.floor(new Date(f.to + 'T00:00').getTime() / 1000) + 86400);
  return 'messages?' + p;
}

async function loadList(v, reset) {
  const offset = reset ? 0 : S.list.length;
  let res;
  try { res = await api(listQuery(v, offset)); } catch (e) { $('#slips').innerHTML = `<p class="err">${esc(e.message)}</p>`; return; }
  S.list = reset ? res.items : S.list.concat(res.items);
  S.total = res.total;
  const box = $('#slips');
  if (!box) return;
  const inTabs = isMobile() && MSG_TABS.some((t) => t[0] === S.view);
  $('#total').textContent = res.total ? `${res.total} message${res.total === 1 ? '' : 's'}${inTabs ? '. Swipe left or right for the other lists.' : ''}` : '';
  if (!S.list.length) {
    box.innerHTML = `<div class="empty">${S.view === 'review'
      ? (S.status?.stats?.messages ? 'Nothing to review. Everything has been sorted.' : 'No messages yet. Pair your phone and run a preview to fill this in.')
      : 'No messages match.'}</div>`;
  } else box.innerHTML = S.list.map((m) => slipHTML(m, v.offers)).join('');
  $('#more').hidden = S.list.length >= S.total;
  box.onclick = (e) => {
    const code = e.target.closest('mark.code, .code-btn');
    if (code) { copyCode(code.dataset.code); e.stopPropagation(); return; }
    const quick = e.target.closest('[data-quick]');
    if (quick) {
      const s = quick.closest('.slip');
      s.style.setProperty('--leave', quick.dataset.quick === 'spam' || quick.dataset.quick === 'offer' ? '-40px' : '40px');
      s.classList.add('leaving');
      setTimeout(() => setLabel(s.dataset.id, quick.dataset.quick), 160);
      return;
    }
    const s = e.target.closest('.slip');
    if (s) select(s.dataset.id);
  };
  box.onkeydown = (e) => { if (e.key === 'Enter' && e.target.classList.contains('m-slip')) select(e.target.dataset.id); };
}

function slipHTML(m, offers) {
  if (isMobile()) return mobileSlipHTML(m, offers);
  const cat = m.label || m.category || 'unknown';
  const state = !m.on_phone ? '<span class="state deleted">removed from phone</span>' : m.action === 'archived' ? '<span class="state">archived</span>' : '';
  return `<button class="slip cat-${esc(cat)} ${m.on_phone ? '' : 'gone'}" role="option" data-id="${esc(m.id)}" aria-selected="${S.selected === m.id}">
    <div class="slip-meta"><span class="slip-sender">${esc(m.sender_name && m.sender_name !== m.sender ? m.sender_name : m.sender)}</span>
      <span title="${esc(jalali(m.ts))}">${esc(fmtTime(m.ts))}</span>${tag(cat)}${m.label ? '<span class="state">labeled by you</span>' : m.confidence ? `<span class="state num">${Math.round(m.confidence * 100)}%</span>` : ''}${state}</div>
    <div class="slip-body ${offers ? 'full' : ''}" dir="auto">${bodyHTML(m.body, offers)}</div></button>`;
}

// On the phone a slip is an article (it holds buttons): codes get Copy buttons, Needs review gets the four labels.
function mobileSlipHTML(m, offers) {
  const cat = m.label || m.category || 'unknown';
  const review = S.view === 'review';
  const state = !m.on_phone ? '<span class="state deleted">removed from phone</span>' : m.action === 'archived' ? '<span class="state">archived</span>' : '';
  const codes = offers ? [...new Set(String(m.body || '').match(CODE_RE) || [])] : [];
  return `<article class="slip m-slip cat-${esc(cat)} ${m.on_phone ? '' : 'gone'}" role="option" tabindex="0" data-id="${esc(m.id)}" aria-selected="${S.selected === m.id}">
    <div class="slip-meta"><span class="slip-sender">${esc(m.sender_name && m.sender_name !== m.sender ? m.sender_name : m.sender)}</span>
      <span title="${esc(jalali(m.ts))}">${esc(fmtTime(m.ts))}</span>${review ? '' : tag(cat)}${m.label ? '<span class="state">labeled by you</span>' : ''}${state}</div>
    <div class="slip-body ${offers ? 'full' : ''}" dir="auto">${esc(m.body)}</div>
    ${codes.length ? `<div class="m-codes">${codes.map((c) => `<button type="button" class="code-btn" data-code="${esc(c)}">${ICON.copy}Copy ${esc(c)}</button>`).join('')}</div>` : ''}
    ${review ? `<div class="labels quick">${LABELS.map((c) => `<button type="button" class="label-btn cat-${c}" data-quick="${c}">${catName(c)}</button>`).join('')}</div>` : ''}
  </article>`;
}

function updateSlip(m) {
  const i = S.list.findIndex((x) => x.id === m.id);
  if (i >= 0) S.list[i] = m;
  const el = $(`.slip[data-id="${CSS.escape(m.id)}"]`);
  if (el) el.outerHTML = slipHTML(m, LIST_VIEWS[S.view]?.offers);
}

// ---------- teach panel ----------
function closeTeach() {
  const t = $('#teach');
  t.className = 'teach idle';
  S.selected = null;
  S.detail = null;
  $$('.slip[aria-selected=true]').forEach((s) => s.setAttribute('aria-selected', 'false'));
  t.innerHTML = `<p class="muted">Pick a message to see why it was sorted that way, and to teach the cleaner.</p>
    <h3>What each category does on your phone</h3>
    <ul class="reasons">${['spam', 'offer', 'archive', 'keep', 'unknown'].map((c) => `<li>${tag(c)}<span>${esc(CATS[c].does)}</span></li>`).join('')}</ul>
    <h3>Never touched</h3><p class="small muted">Contacts, group chats, pinned chats and chats you replied to. The cleaner doesn't even read them.</p>`;
}

async function select(id) {
  if (isMobile() && !S.selected && !history.state?.smscMsg) history.pushState({ smscMsg: true }, '');
  S.selected = id;
  $$('.slip').forEach((s) => s.setAttribute('aria-selected', s.dataset.id === id));
  $(`.slip[data-id="${CSS.escape(id)}"]`)?.scrollIntoView({ block: 'nearest' });
  try { S.detail = await api('messages/' + encodeURIComponent(id)); } catch (e) { toast(e.message); return; }
  if (S.selected === id) renderTeach();
}

function verdictHTML(now, noHead) {
  const cat = now.category;
  const scores = now.scores || {};
  const maxS = Math.max(3, ...Object.values(scores));
  const bars = LABELS.map((c) => `<span class="cat-${c}">${catName(c)}</span><span class="bar cat-${c}"><i style="width:${Math.min(100, ((scores[c] || 0) / maxS) * 100)}%"></i></span><span class="num muted">${(scores[c] || 0).toFixed(1)}</span>`).join('');
  const reasons = (now.reasons || []).slice(0, 8).map((r) => `<li class="cat-${esc(r.category)}"><span class="tag cat-${esc(r.category)} ${r.category === 'offer' ? 'offer' : ''}">${esc(catName(r.category))}</span><span dir="auto">${esc(r.detail)}</span><span class="w">+${r.weight}</span></li>`).join('');
  return `${noHead ? '' : `<div class="verdict cat-${esc(cat)}"><span class="big">${esc(catName(cat))}</span>
    <span class="muted small">${cat === 'unknown' ? 'not sure enough to act' : `would be ${esc(CATS[cat].does)}`}</span></div>`}
    <div class="bars">${bars}</div>${reasons ? `<ul class="reasons">${reasons}</ul>` : '<p class="hint">No rule matched and the learned model had no strong opinion.</p>'}`;
}

function chipsHTML(terms) {
  if (!terms?.length) return '<p class="hint">No words to suggest.</p>';
  return `<div class="chips">${terms.slice(0, 40).map((t) => `<button class="chip ${t.category ? 'cat-' + esc(t.category) : ''}" ${t.category ? 'data-lean' : ''} data-term="${esc(t.term)}" aria-pressed="false" title="${t.category ? `points to ${esc(catName(t.category))} (seen ${t.seen}×)` : t.seen ? `seen ${t.seen}×` : 'new word'}">${esc(t.term)}</button>`).join('')}</div>
    <div class="chip-legend">${LABELS.map((c) => `<span class="cat-${c}">${catName(c)}</span>`).join('')}<span style="--c: var(--line)">no lean yet</span></div>`;
}

function renderTeach() {
  const d = S.detail;
  const m = d.message;
  const t = $('#teach');
  t.className = 'teach';
  const defCat = m.label || (d.now.category !== 'unknown' ? d.now.category : '');
  const catOptions = (sel) => LABELS.map((c) => `<option value="${c}" ${c === sel ? 'selected' : ''}>${catName(c)}</option>`).join('');
  const mobile = isMobile();
  const name = m.sender_name && m.sender_name !== m.sender ? m.sender_name : m.sender;
  const pos = S.list.findIndex((x) => x.id === m.id);
  const vcat = m.label || d.now.category;
  const consequence = (vcat === 'unknown' ? 'Not sure enough to act, so it stays on your phone until you decide.'
    : `${CATS[vcat].does[0].toUpperCase()}${CATS[vcat].does.slice(1)}.`) + (!m.on_phone ? ' Removed from the phone; this copy stays here.' : '');
  const labelBtns = LABELS.map((c) => `<button class="label-btn cat-${c}" data-setlabel="${c}" aria-pressed="${m.label === c}">${catName(c)}${mobile ? '' : `<kbd>${CATS[c].key.toUpperCase()}</kbd>`}</button>`).join('');
  if (mobile) {
    t.innerHTML = `<div class="m-bar">
        <button class="icon-btn" id="closeTeach" aria-label="Back">${ICON.back}</button>
        <div class="m-title"><b>${esc(name)}</b><span>${name !== m.sender ? `<span dir="ltr">${esc(m.sender)}</span>, ` : ''}${esc(d.sender_kind)} sender, ${esc(fmtTime(m.ts))}</span></div>
        ${pos >= 0 ? `<button class="icon-btn" id="prevMsg" aria-label="Previous message" ${pos <= 0 ? 'disabled' : ''}>${ICON.up}</button>
        <span class="num small muted" style="white-space:nowrap">${pos + 1} of ${S.total || S.list.length}</span>
        <button class="icon-btn" id="nextMsg" aria-label="Next message" ${pos >= S.list.length - 1 ? 'disabled' : ''}>${ICON.down}</button>` : ''}
      </div>
      <div class="m-verdict cat-${esc(vcat)}"><b>${esc(catName(vcat))}</b><span>${esc(consequence)}</span></div>
      <div class="body" id="teachBody" dir="auto">${bodyHTML(m.body, true)}</div>
      <div class="teach-form" id="selForm" hidden><span class="small">Selected text:</span><select class="input" id="selCat">${catOptions(defCat || 'offer')}</select><button class="btn" id="selAdd">Add as keyword</button></div>
      <p class="hint">Select part of the text to turn it into a keyword.</p>
      <h3>Why</h3>
      ${verdictHTML(d.now, true)}
      <h3>Teach with words</h3>
      ${chipsHTML(d.terms)}
      <div class="teach-form"><span class="small">Chosen words mean</span><select class="input" id="chipCat">${catOptions(defCat || 'offer')}</select><button class="btn primary" id="chipAdd" disabled>Add keywords</button></div>
      <h3>Sender</h3>
      <div class="teach-form"><span class="small">Always treat <b dir="ltr">${esc(m.sender)}</b> as</span><select class="input" id="sndCat">${catOptions(defCat || 'archive')}</select><button class="btn" id="sndAdd">Save</button></div>
      <h3>Same conversation</h3>
      <button class="btn" id="convBtn">Show other messages</button><div id="conv"></div>
      ${pos >= 0 && S.list.length > 1 ? '<p class="m-swipe-hint">Swipe left or right for the next message.</p>' : ''}
      <div class="m-labels"><span>This message is</span><div class="labels">${labelBtns}</div></div>`;
  } else t.innerHTML = `
    <div style="display:flex;justify-content:space-between;align-items:start;gap:8px">
      <div><div class="slip-sender" style="font-size:16px">${esc(m.sender_name && m.sender_name !== m.sender ? m.sender_name : m.sender)}</div>
      <div class="muted small"><span dir="ltr">${esc(m.sender)}</span>, ${esc(d.sender_kind)} sender, <span title="${esc(jalali(m.ts))}">${esc(fmtTime(m.ts))}</span>
      ${!m.on_phone ? ', <span class="err">removed from phone</span>' : m.action === 'archived' ? ', archived' : ''}</div></div>
      <button class="btn ghost" id="closeTeach" aria-label="Close">✕</button>
    </div>
    <h3>Message</h3>
    <div class="body" id="teachBody" dir="auto">${bodyHTML(m.body, true)}</div>
    <div class="teach-form" id="selForm" hidden><span class="small">Selected text:</span><select class="input" id="selCat">${catOptions(defCat || 'offer')}</select><button class="btn" id="selAdd">Add as keyword</button></div>
    <p class="hint">Select part of the text to turn it into a keyword.</p>

    <h3>This message is</h3>
    <div class="labels">${labelBtns}</div>
    <p class="hint">${m.label ? `You labeled this ${esc(catName(m.label))}. Tap it again to remove the label.` : 'Your label overrides the guess and trains the learned model.'}</p>

    <h3>Current verdict</h3>
    ${verdictHTML(d.now)}

    <h3>Teach with words</h3>
    ${chipsHTML(d.terms)}
    <div class="teach-form"><span class="small">Chosen words mean</span><select class="input" id="chipCat">${catOptions(defCat || 'offer')}</select><button class="btn primary" id="chipAdd" disabled>Add keywords</button></div>
    <p class="hint">Tinted words already lean toward a category. Pick the words that give it away, like a discount phrase or "رمز پویا".</p>

    <h3>Sender</h3>
    <div class="teach-form"><span class="small">Always treat <b dir="ltr">${esc(m.sender)}</b> as</span><select class="input" id="sndCat">${catOptions(defCat || 'archive')}</select><button class="btn" id="sndAdd">Save</button></div>
    <p class="hint">Overrides every other rule for this sender. Prefer words for senders that mix codes and promos.</p>

    <h3>Same conversation</h3>
    <button class="btn" id="convBtn">Show other messages</button><div id="conv"></div>`;

  $('#closeTeach').onclick = () => (isMobile() && history.state?.smscMsg ? history.back() : closeTeach());
  if (mobile) {
    $('#prevMsg') && ($('#prevMsg').onclick = () => step(-1));
    $('#nextMsg') && ($('#nextMsg').onclick = () => step(1));
    t.scrollTop = 0;
    animate(t, S.stepDir);
    S.stepDir = 0;
  }
  $$('[data-setlabel]', t).forEach((b) => (b.onclick = () => setLabel(m.id, m.label === b.dataset.setlabel ? '' : b.dataset.setlabel)));
  $$('.chip', t).forEach((c) => (c.onclick = () => {
    c.setAttribute('aria-pressed', c.getAttribute('aria-pressed') !== 'true');
    $('#chipAdd').disabled = !$$('.chip[aria-pressed=true]', t).length;
  }));
  $('#chipAdd').onclick = async () => {
    const cat = $('#chipCat').value;
    const words = $$('.chip[aria-pressed=true]', t).map((c) => c.dataset.term);
    await addRules(words.map((w) => ({ kind: 'keyword', pattern: w, category: cat, weight: 2 })));
  };
  $('#sndAdd').onclick = async () => {
    const cat = $('#sndCat').value;
    await addRules([{ kind: 'sender', pattern: m.sender, category: cat, weight: 100 }]);
  };
  const body = $('#teachBody');
  const onSel = () => {
    const s = window.getSelection();
    const txt = s && body.contains(s.anchorNode) ? s.toString().trim() : '';
    $('#selForm').hidden = txt.length < 2;
    $('#selAdd').dataset.text = txt;
  };
  body.onmouseup = body.onkeyup = body.ontouchend = onSel;
  $('#selAdd').onclick = () => addRules([{ kind: 'keyword', pattern: $('#selAdd').dataset.text, category: $('#selCat').value, weight: 2 }]);
  body.onclick = (e) => { const code = e.target.closest('mark.code'); if (code) { navigator.clipboard?.writeText(code.dataset.code); toast(`Copied ${code.dataset.code}`); } };
  $('#convBtn').onclick = async () => {
    const res = await api('conversations/' + encodeURIComponent(m.conv_id));
    $('#conv').innerHTML = `<div class="slips" style="margin-top:8px">${res.items.filter((x) => x.id !== m.id).map((x) => slipHTML(x)).join('') || '<p class="hint">No other messages stored.</p>'}</div>`;
    $('#convBtn').hidden = true;
    $('#conv').onclick = (e) => { const s = e.target.closest('.slip'); if (s) select(s.dataset.id); };
  };
}

function step(dir) {
  const i = S.list.findIndex((x) => x.id === S.selected);
  const n = i >= 0 && S.list[i + dir];
  if (!n) return;
  S.stepDir = dir;
  select(n.id);
}

async function setLabel(id, label) {
  let res;
  try { res = await api(`messages/${encodeURIComponent(id)}/label`, { body: { label } }); } catch (e) { toast(e.message); return; }
  toast(label ? `Labeled ${catName(label)}. Model retrained.` : 'Label removed');
  const m = res.message;
  if (S.view === 'review' && label) {
    const fromDetail = S.selected === id;
    const i = S.list.findIndex((x) => x.id === id);
    S.list.splice(i, 1);
    $(`.slip[data-id="${CSS.escape(id)}"]`)?.remove();
    S.total--;
    $('#total').textContent = S.total ? `${S.total} messages` : '';
    const empty = () => { if (!S.list.length) $('#slips').innerHTML = '<div class="empty">Nothing to review. Everything has been sorted.</div>'; };
    if (isMobile() && !fromDetail) { empty(); refreshStatus(); return; }
    const next = S.list[Math.min(i, S.list.length - 1)];
    if (next) select(next.id);
    else { if (isMobile() && history.state?.smscMsg) history.back(); else closeTeach(); empty(); }
  } else {
    updateSlip(m);
    if (S.selected === id) { S.detail = { ...S.detail, ...res }; renderTeach(); }
  }
  refreshStatus();
}

async function addRules(rules) {
  const ok = [], bad = [];
  for (const r of rules) {
    try { await api('rules', { body: r }); ok.push(r.pattern); } catch (e) { bad.push(`${r.pattern}: ${e.message}`); }
  }
  if (ok.length) toast(`Added ${ok.length} rule${ok.length > 1 ? 's' : ''}. Re-scoring messages.`);
  if (bad.length) setTimeout(() => toast(bad.join('; ')), ok.length ? 3300 : 0);
  setTimeout(async () => {
    if (S.selected) select(S.selected);
    if (LIST_VIEWS[S.view]) loadList(LIST_VIEWS[S.view], true);
    refreshStatus();
  }, 1200);
}

// keyboard triage
document.addEventListener('keydown', (e) => {
  if (e.target.closest('input, textarea, select') || e.metaKey || e.ctrlKey || e.altKey || !LIST_VIEWS[S.view]) return;
  if (e.key === 'Escape') return closeTeach();
  const i = S.list.findIndex((m) => m.id === S.selected);
  if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
    e.preventDefault();
    const n = S.list[Math.max(0, Math.min(S.list.length - 1, i + (e.key === 'ArrowDown' ? 1 : -1)))];
    if (n) select(n.id);
    return;
  }
  const cat = LABELS.find((c) => CATS[c].key === e.key.toLowerCase());
  if (cat && S.selected) { e.preventDefault(); setLabel(S.selected, cat); }
});

// ---------- rules ----------
async function renderRules() {
  const main = $('#main');
  const f = (S.filter.rules ||= { cat: '', q: '' });
  main.innerHTML = `${backHTML()}<div class="view-head"><h1>Rules</h1><span class="muted" id="rcount"></span></div>
    <p class="view-sub">Rules add points to a category when they match. A message is decided when one category reaches 2 points and leads by 1; spam needs 3. Sender rules override everything.</p>
    <div class="panel"><h2>Add a rule</h2>
      <div class="filters">
        <select class="input" id="rk"><option value="keyword">Text contains</option><option value="regex">Text matches pattern</option><option value="sender">Sender is</option><option value="sender_regex">Sender matches pattern</option></select>
        <input class="input search" id="rp" placeholder="کد تخفیف" dir="auto" aria-label="Pattern">
        <select class="input" id="rc">${LABELS.map((c) => `<option value="${c}">${catName(c)}</option>`).join('')}</select>
        <input class="input" id="rw" type="number" min="0.5" step="0.5" value="2" style="width:80px" aria-label="Points">
        <button class="btn primary" id="radd">Add rule</button>
      </div></div>
    <div class="filters"><input class="input search" type="search" id="rq" placeholder="Find a rule" value="${esc(f.q)}">
      <div class="seg">${[['', 'All'], ...LABELS.map((c) => [c, catName(c)])].map(([k, n]) => `<button data-rc="${k}" aria-pressed="${f.cat === k}">${n}</button>`).join('')}</div></div>
    <div class="panel" style="padding:4px 8px;overflow-x:auto"><table><thead><tr><th>Matches</th><th>Category</th><th>Points</th><th>Kind</th><th>From</th><th>Hits</th><th></th></tr></thead><tbody id="rbody"></tbody></table></div>`;
  $('#radd').onclick = async () => {
    const r = { kind: $('#rk').value, pattern: $('#rp').value, category: $('#rc').value, weight: parseFloat($('#rw').value) || 2 };
    if (r.kind === 'sender') r.weight = 100;
    try { await api('rules', { body: r }); toast('Rule added'); $('#rp').value = ''; loadRules(); } catch (e) { toast(e.message); }
  };
  $('#rq').oninput = (e) => { f.q = e.target.value; drawRules(); };
  $$('[data-rc]').forEach((b) => (b.onclick = () => { f.cat = b.dataset.rc; $$('[data-rc]').forEach((x) => x.setAttribute('aria-pressed', x === b)); drawRules(); }));
  loadRules();
}
async function loadRules() { S.rules = await api('rules'); drawRules(); }
function drawRules() {
  const f = S.filter.rules;
  const q = f.q.trim().toLowerCase();
  const rows = S.rules.filter((r) => (!f.cat || r.category === f.cat) && (!q || r.pattern.toLowerCase().includes(q) || (r.note || '').toLowerCase().includes(q)));
  $('#rcount').textContent = `${rows.length} of ${S.rules.length}`;
  const kinds = { keyword: 'contains', regex: 'pattern', sender: 'sender', sender_regex: 'sender pattern' };
  $('#rbody').innerHTML = rows.map((r) => `<tr class="${r.enabled ? '' : 'off'}" data-id="${r.id}">
    <td class="pattern" dir="auto" title="${esc(r.note || '')}">${esc(r.pattern)}</td>
    <td><select class="input" data-f="category">${LABELS.map((c) => `<option value="${c}" ${c === r.category ? 'selected' : ''}>${catName(c)}</option>`).join('')}</select></td>
    <td><input class="input num" data-f="weight" type="number" step="0.5" min="0.5" value="${r.weight}" style="width:70px"></td>
    <td class="muted">${kinds[r.kind] || esc(r.kind)}</td><td class="muted">${esc(r.source)}</td><td class="num muted">${r.hits || ''}</td>
    <td style="white-space:nowrap"><button class="btn ghost small" data-act="toggle">${r.enabled ? 'Turn off' : 'Turn on'}</button><button class="btn ghost small err" data-act="del">Delete</button></td></tr>`).join('') || '<tr><td colspan="7" class="muted">No rules match.</td></tr>';
  $('#rbody').onchange = async (e) => {
    const tr = e.target.closest('tr');
    const r = S.rules.find((x) => x.id == tr.dataset.id);
    const upd = { ...r, [e.target.dataset.f]: e.target.dataset.f === 'weight' ? parseFloat(e.target.value) : e.target.value };
    try { await api('rules/' + r.id, { method: 'PUT', body: upd }); Object.assign(r, upd); toast('Rule updated'); } catch (err) { toast(err.message); }
  };
  $('#rbody').onclick = async (e) => {
    const b = e.target.closest('[data-act]');
    if (!b) return;
    const r = S.rules.find((x) => x.id == b.closest('tr').dataset.id);
    if (b.dataset.act === 'del') {
      if (!(await confirmBox('Delete this rule?', `<p dir="auto"><b>${esc(r.pattern)}</b> → ${esc(catName(r.category))}</p>`, 'Delete', true))) return;
      await api('rules/' + r.id, { method: 'DELETE' });
      S.rules = S.rules.filter((x) => x !== r);
    } else {
      r.enabled = !r.enabled;
      await api('rules/' + r.id, { method: 'PUT', body: r });
    }
    drawRules();
  };
}

// ---------- try a message ----------
function renderTest() {
  $('#main').innerHTML = `${backHTML()}<div class="view-head"><h1>Try a message</h1></div>
    <p class="view-sub">Paste any SMS to see what the cleaner would do with it and why. Nothing is saved.</p>
    <div class="panel"><div class="field"><label for="ts">Sender</label><input class="input" id="ts" placeholder="30001234 or Mellat or 0912…" dir="ltr"></div>
    <div class="field"><label for="tb">Text</label><textarea class="input" id="tb" rows="6" dir="auto" placeholder="متن پیامک"></textarea></div>
    <button class="btn primary" id="tgo">Check</button></div><div id="tres"></div>`;
  $('#tgo').onclick = async () => {
    const res = await api('test', { body: { sender: $('#ts').value, body: $('#tb').value } });
    $('#tres').innerHTML = `<div class="panel">${verdictHTML(res.now)}<p class="hint">Sender read as <b dir="ltr">${esc(res.sender || '(none)')}</b>, ${esc(res.sender_kind)}.</p></div>
      <div class="panel"><h2>Teach with words</h2>${chipsHTML(res.terms)}
      <div class="teach-form"><span class="small">Chosen words mean</span><select class="input" id="tcat">${LABELS.map((c) => `<option value="${c}">${catName(c)}</option>`).join('')}</select><button class="btn primary" id="tadd" disabled>Add keywords</button></div></div>`;
    $$('#tres .chip').forEach((c) => (c.onclick = () => { c.setAttribute('aria-pressed', c.getAttribute('aria-pressed') !== 'true'); $('#tadd').disabled = !$$('#tres .chip[aria-pressed=true]').length; }));
    $('#tadd').onclick = async () => { await addRules($$('#tres .chip[aria-pressed=true]').map((c) => ({ kind: 'keyword', pattern: c.dataset.term, category: $('#tcat').value, weight: 2 }))); setTimeout(() => $('#tgo').click(), 1300); };
  };
}

// ---------- model ----------
async function renderModel() {
  const main = $('#main');
  main.innerHTML = `${backHTML()}<div class="view-head"><h1>What it learned</h1></div><p class="muted">Loading…</p>`;
  const m = await api('model');
  const cls = m.classes || {};
  main.innerHTML = `${backHTML()}<div class="view-head"><h1>What it learned</h1></div>
    <p class="view-sub">Besides your rules, a learned model reads every labeled message: ${m.examples} so far. These are the words it relies on most. Tap one to add it as a rule.</p>
    <div class="panel"><div class="stats">${LABELS.map((c) => `<div class="stat cat-${c}"><b style="color:var(--c)">${cls[c] || 0}</b><span>${catName(c)} examples</span></div>`).join('')}</div></div>
    <div class="grid2">${LABELS.map((c) => `<div class="panel"><h2 class="cat-${c}" style="color:var(--c)">${catName(c)}</h2>
      <div class="chips">${(m.top_terms[c] || []).map((t) => `<button class="chip cat-${c}" data-lean data-term="${esc(t.term)}" data-cat="${c}" title="seen ${t.seen}×, strength ${t.strength}">${esc(t.term)}</button>`).join('') || '<span class="hint">Needs more labeled examples.</span>'}</div></div>`).join('')}</div>
    <div class="panel"><h2>Bring in more examples</h2>
      <p class="hint">Upload a <b>.jsonl</b> file of labeled SMS (one {"sender","body","label"} per line) or a <b>.json</b> array of rules.</p>
      <div class="teach-form"><input type="file" id="imp" accept=".json,.jsonl" class="input"><button class="btn" id="impGo">Import</button></div>
      <h2 style="margin-top:16px">Re-score</h2><p class="hint">Re-check every stored message against the current rules and model. Runs automatically after rule changes.</p>
      <button class="btn" id="rescore">Re-score all messages</button></div>`;
  $('#main').onclick = async (e) => {
    const c = e.target.closest('.chip[data-cat]');
    if (!c) return;
    if (await confirmBox('Add as a rule?', `<p>Text containing <b dir="auto">${esc(c.dataset.term)}</b> counts toward <b>${esc(catName(c.dataset.cat))}</b> (2 points).</p>`, 'Add rule')) addRules([{ kind: 'keyword', pattern: c.dataset.term, category: c.dataset.cat, weight: 2 }]);
  };
  $('#impGo').onclick = async () => {
    const f = $('#imp').files[0];
    if (!f) return toast('Choose a file first');
    const fd = new FormData();
    fd.append('file', f);
    try { const r = await api('import', { body: fd }); toast(r.result); renderModel(); } catch (e) { toast(e.message); }
  };
  $('#rescore').onclick = async () => { const r = await api('reclassify', { body: {} }); toast(`Re-scored ${r.rescored} messages`); refreshStatus(); };
}

// ---------- activity ----------
async function renderActivity() {
  const main = $('#main');
  const runs = await api('runs');
  if (isMobile()) {
    main.innerHTML = `<div class="view-head"><h1>History</h1></div>
      <p class="view-sub">Every run and every change it made, or would have made in a preview.</p>
      <div class="m-group">${runs.map((r) => {
        const s = r.stats || {};
        const sum = r.error ? `<span class="err">${esc(r.error)}</span>` : `${s.plan_block || 0} spam, ${(s.plan_delete || 0) + (s.action_delete_message || 0)} offers, ${s.plan_archive || 0} archived`;
        return `<button class="m-row" data-run="${r.id}"><span class="m-row-t"><span><b>${r.dry ? 'Preview' : 'Clean'}</b> ${esc(fmtTime(r.started_at))}</span><small>${sum}</small></span>${ICON.chev}</button>`;
      }).join('') || '<p class="m-about muted">No runs yet.</p>'}</div>
      <div id="acts"></div>`;
    main.querySelector('.m-group').onclick = (e) => { const b = e.target.closest('[data-run]'); if (b) showActions(b.dataset.run, true); };
    return;
  }
  main.innerHTML = `<div class="view-head"><h1>Activity</h1></div>
    <p class="view-sub">Every run and every change it made, or would have made in a preview.</p>
    <div class="panel" style="padding:4px 8px;overflow-x:auto"><table><thead><tr><th>When</th><th>Kind</th><th>Scanned</th><th>Spam</th><th>Offers</th><th>Archive</th><th>Left alone</th><th>Skipped</th><th></th></tr></thead>
    <tbody>${runs.map((r) => { const s = r.stats || {}; return `<tr data-run="${r.id}" style="cursor:pointer">
      <td title="${esc(jalali(r.started_at))}">${esc(fmtTime(r.started_at))}</td><td>${r.dry ? 'Preview' : '<b>Clean</b>'} <span class="muted small">${esc(r.trigger)}</span></td>
      <td class="num">${s.conversations || 0} chats</td><td class="num">${s.plan_block || 0}</td><td class="num">${(s.plan_delete || 0)}${s.action_delete_message ? ` + ${s.action_delete_message} msgs` : ''}</td>
      <td class="num">${s.plan_archive || 0}</td><td class="num">${(s.plan_leave || 0) + (s.plan_partial || 0)}</td>
      <td class="num muted" title="contacts / groups / pinned / you replied">${(s.skipped_contact || 0) + (s.skipped_group || 0) + (s.skipped_pinned || 0) + (s.skipped_replied || 0)}</td>
      <td>${r.error ? `<span class="err">${esc(r.error)}</span>` : s.errors ? `<span class="err">${s.errors} errors</span>` : s.capped ? `<span class="muted">${s.capped} capped</span>` : ''}</td></tr>`; }).join('') || '<tr><td colspan="9" class="muted">No runs yet.</td></tr>'}</tbody></table></div>
    <div id="acts"></div>`;
  main.querySelector('tbody').onclick = (e) => { const tr = e.target.closest('tr[data-run]'); if (tr) showActions(tr.dataset.run); };
  if (runs[0]) showActions(runs[0].id);
}
async function showActions(runId, scroll) {
  const acts = await api('actions?run=' + runId);
  const names = { block_report: 'Block + report', delete_conversation: 'Delete chat', delete_message: 'Delete message', archive: 'Archive' };
  const result = (a) => (a.ok ? (a.dry ? '<span class="muted">would do</span>' : '<span class="ok">done</span>') : `<span class="err">${esc(a.error)}</span>`);
  if (isMobile()) {
    $('#acts').innerHTML = `<h2 class="m-h2">Run ${esc(runId)}</h2><div class="m-acts">${acts.map((a) => `<div class="m-act"><div class="top-line"><span class="who">${esc(a.sender)}</span>${tag(a.category)}<span>${esc(names[a.action] || a.action)}</span><span style="margin-inline-start:auto">${result(a)}</span></div>${a.detail ? `<span class="small muted" dir="auto">${esc(a.detail)}</span>` : ''}</div>`).join('') || '<p class="muted">Nothing to do in this run.</p>'}</div>`;
    if (scroll) $('#acts').scrollIntoView({ behavior: 'smooth', block: 'start' });
    return;
  }
  $('#acts').innerHTML = `<div class="panel" style="padding:4px 8px;overflow-x:auto"><h2 style="padding:10px 8px 0">Run ${esc(runId)}</h2><table><thead><tr><th>Sender</th><th>Category</th><th>Action</th><th>Details</th><th>Result</th></tr></thead><tbody>
    ${acts.map((a) => `<tr><td dir="ltr" style="text-align:start">${esc(a.sender)}</td><td>${tag(a.category)}</td><td>${esc(names[a.action] || a.action)}</td><td dir="auto" class="small">${esc(a.detail)}</td>
      <td>${a.ok ? (a.dry ? '<span class="muted">would do</span>' : '<span class="ok">done</span>') : `<span class="err">${esc(a.error)}</span>`}</td></tr>`).join('') || '<tr><td colspan="5" class="muted">Nothing to do in this run.</td></tr>'}</tbody></table></div>`;
}

// ---------- phone pages: home, settings, cleaning ----------
function connState(st) {
  if (st.demo) return ['Demo', 'unknown'];
  if (!st.paired) return ['Not paired', 'spam'];
  if (st.phone?.connected) return ['Connected', 'archive'];
  if (st.phone?.error) return ['Offline', 'spam'];
  return ['Connecting', 'unknown'];
}

async function renderHome() {
  const st = S.status || {};
  const s = st.stats || {};
  const by = s.by_category || {};
  const lr = st.last_run;
  const ls = lr?.stats || {};
  const spam = ls.plan_block || 0, offers = (ls.plan_delete || 0) + (ls.action_delete_message || 0), arch = ls.plan_archive || 0;
  const [connText, connCat] = connState(st);
  let run;
  if (st.running) run = `<span class="muted small">Working on your messages</span><b class="m-big progress">${esc(st.progress || 'Running')}</b>`;
  else if (!lr) run = '<b class="m-big">No runs yet</b><span class="muted small">Preview first to see what would change. Nothing on the phone changes.</span>';
  else if (lr.error) run = `<span class="muted small">${lr.dry ? 'Last preview' : 'Last clean'}, ${esc(fmtTime(lr.started_at))}</span><b class="m-big">It failed</b><p class="err" style="margin:0">${esc(lr.error)}</p>`;
  else run = `<span class="muted small">${lr.dry ? 'Last preview' : 'Last clean'}, ${esc(fmtTime(lr.started_at))}</span>
    <b class="m-big">${spam + offers + arch} ${lr.dry ? `change${spam + offers + arch === 1 ? '' : 's'} planned` : `message${spam + offers + arch === 1 ? '' : 's'} sorted`}</b>
    <div class="m-stats">
      <div class="cat-spam"><b>${spam}</b><span>spam ${lr.dry ? 'to block' : 'blocked'}</span></div>
      <div class="cat-offer"><b>${offers}</b><span>offers ${lr.dry ? 'to remove' : 'removed'}</span></div>
      <div class="cat-archive"><b>${arch}</b><span>${lr.dry ? 'to archive' : 'archived'}</span></div>
    </div>`;
  const off = st.running || !st.paired;
  const tiles = [['spam', 'Spam', by.spam, 'spam'], ['offers', 'Offers', by.offer, 'offer'], ['archive', 'Archive', by.archive, 'archive'], ['keep', 'Keep', by.keep, 'keep']];
  $('#main').innerHTML = `
    <header class="m-home-head"><div class="brand"><span class="brand-mark" aria-hidden="true"></span>SMS Cleaner</div>
      <button class="conn cat-${connCat}" data-go="phone"><i></i>${connText}</button></header>
    <section class="m-card">${run}
      <button class="btn primary m-cta" id="hClean" ${off ? 'disabled' : ''}>Clean now</button>
      <button class="btn ghost m-cta2" id="hPreview" ${off ? 'disabled' : ''}>Preview first, nothing changes</button></section>
    ${s.review ? `<section class="m-card m-need" style="margin-top:14px"><div><b>${s.review} message${s.review === 1 ? ' needs' : 's need'} you</b><span id="needPeek" dir="auto"></span></div>
      <button class="btn primary" data-go="review">Review</button></section>` : ''}
    <h2 class="m-h2">Kept in the app</h2>
    <div class="m-tiles">${tiles.map(([v, n, c, cat]) => `<button class="slip m-tile cat-${cat}" data-go="${v}"><span>${n}</span><b class="num">${c || 0}</b></button>`).join('')}</div>
    <div class="m-group" style="margin-top:14px"><button class="m-row" data-go="cleaning"><span class="m-row-t">Automatic cleaning</span><span class="val">${st.next_run ? 'On' : 'Off'}</span>${ICON.chev}</button></div>`;
  $('#hClean').onclick = () => $('#runNow').click();
  $('#hPreview').onclick = () => $('#runPreview').click();
  $('#main').onclick = (e) => { const b = e.target.closest('[data-go]'); if (b) go(b.dataset.go); };
  if (s.review) {
    try {
      const r = await api('messages?category=unknown&label=none&on_phone=yes&limit=1');
      const peek = $('#needPeek');
      if (peek) peek.textContent = r.items[0]?.body || '';
    } catch (e) { /* the count alone is enough */ }
  }
}

async function renderSettings() {
  const st = S.status || {};
  let settings;
  try { settings = await api('settings'); } catch (e) { toast(e.message); return; }
  const iv = parseInt(settings.interval_min, 10) || 0;
  const ph = st.phone || {};
  const row = (to, title, sub, val) => `<button class="m-row" data-go="${to}"><span class="m-row-t"><span>${title}</span>${sub ? `<small>${sub}</small>` : ''}</span>${val !== undefined ? `<span class="val">${val}</span>` : ''}${ICON.chev}</button>`;
  const phone = st.demo ? row('phone', 'Demo mode', 'Sample messages, no phone connected')
    : !st.paired ? row('phone', 'Not paired', 'Pair to start cleaning')
    : ph.connected ? row('phone', 'Connected', `${ph.google_account ? 'Through your Google account, ' : ''}since ${esc(fmtTime(ph.since))}`)
    : ph.error ? row('phone', 'Can\'t reach your phone', esc(ph.error)) : row('phone', 'Paired', 'Connecting to your phone');
  $('#main').innerHTML = `<div class="view-head"><h1>Settings</h1></div>
    <h2 class="m-h2">Phone</h2><div class="m-group">${phone}</div>
    <h2 class="m-h2">Cleaning</h2><div class="m-group">
      ${row('cleaning', 'Automatic cleaning', '', iv > 0 ? `every ${fmtInterval(iv)}` : 'Off')}
      ${row('cleaning', 'Automatic runs', st.mode === 'live' ? 'Change the phone' : 'Preview only, nothing on the phone changes')}
      ${row('cleaning', 'Most changes per clean', '', esc(settings.max_actions))}
      ${row('cleaning', 'How sure it must be', `${esc(settings.min_score)} points to decide, ${esc(settings.spam_min_score)} to call it spam`)}</div>
    <h2 class="m-h2">Teaching</h2><div class="m-group">
      ${row('rules', 'Keyword rules', '', st.stats?.rules || 0)}
      ${row('test', 'Try a message')}
      ${row('model', 'What it learned', '', `${st.model_examples || 0} labels`)}</div>
    <h2 class="m-h2">App</h2><div class="m-group m-about">${aboutHTML(st.update)}</div>`;
  $('#main').onclick = (e) => { const b = e.target.closest('[data-go]'); if (b) go(b.dataset.go); };
}

async function renderCleaning() {
  const st = S.status || {};
  let settings;
  try { settings = await api('settings'); } catch (e) { toast(e.message); return; }
  const field = (k, label, hint, attrs = '') => `<div class="field"><label for="s_${k}">${label}</label><input class="input" id="s_${k}" data-k="${k}" value="${esc(settings[k])}" ${attrs}><span class="hint">${hint}</span></div>`;
  $('#main').innerHTML = `${backHTML()}<div class="view-head"><h1>Cleaning</h1></div>
    <div class="panel"><h2>Automatic runs</h2>
      <div class="seg" role="group" aria-label="What automatic runs do"><button type="button" data-mode="dry" aria-pressed="${st.mode !== 'live'}">Preview only</button><button type="button" data-mode="live" aria-pressed="${st.mode === 'live'}">Change the phone</button></div>
      <p class="hint">Clean now on Home always changes the phone, after asking first.</p></div>
    <div class="panel"><h2>Schedule and limits</h2>
      ${field('interval_min', 'Run every (minutes)', '0 turns automatic cleaning off. 720 is every 12 hours.', 'type="number" min="0" inputmode="numeric"')}
      ${st.platform === 'android' ? '<p class="hint" style="margin:-4px 0 12px">Runs in the background even when the app is closed. Android checks about once an hour, so a clean can start up to an hour late. The phone needs to be online.</p>' : ''}
      ${field('max_actions', 'Most phone changes per clean', 'A safety cap; anything over it waits for the next run.', 'type="number" min="0" inputmode="numeric"')}
      ${field('min_score', 'Points needed to decide', 'Higher means more messages left for review.', 'type="number" min="0.5" step="0.5" inputmode="decimal"')}
      ${field('spam_min_score', 'Points needed to call it spam', 'Spam gets blocked, so it needs more evidence.', 'type="number" min="0.5" step="0.5" inputmode="decimal"')}
      ${field('max_convs', 'Inbox chats to scan', 'Newest first.', 'type="number" min="1" inputmode="numeric"')}
      ${field('msgs_per_conv', 'Messages read per chat', 'Newest messages of each chat.', 'type="number" min="1" inputmode="numeric"')}
      <button class="btn primary m-cta" id="saveSet">Save settings</button></div>`;
  $$('[data-mode]').forEach((b) => (b.onclick = async () => {
    await (b.dataset.mode === 'live' ? $('#modeLive').onclick() : $('#modeDry').onclick());
    await refreshStatus();
    renderCleaning();
  }));
  $('#saveSet').onclick = async () => {
    const body = {};
    $$('[data-k]').forEach((i) => (body[i.dataset.k] = i.value));
    try { await api('settings', { method: 'PUT', body }); toast('Settings saved'); refreshStatus(); } catch (e) { toast(e.message); }
  };
}

// ---------- phone & settings ----------
async function renderPhone() {
  const main = $('#main');
  if (isMobile()) {
    main.innerHTML = `${backHTML()}<div class="view-head"><h1>Phone</h1></div><div class="panel"><div id="pairBox"></div></div>`;
    drawPair(await api('pair'), S.status || {});
    return;
  }
  const [settings, pair] = await Promise.all([api('settings'), api('pair')]);
  const st = S.status || {};
  const field = (k, label, hint, attrs = '') => `<div class="field"><label for="s_${k}">${label}</label><input class="input" id="s_${k}" data-k="${k}" value="${esc(settings[k])}" ${attrs}><span class="hint">${hint}</span></div>`;
  main.innerHTML = `<div class="view-head"><h1>Phone &amp; settings</h1></div>
    <div class="grid2">
      <div class="panel"><h2>Phone</h2><div id="pairBox"></div></div>
      <div class="panel"><h2>Schedule and limits</h2>
        ${field('interval_min', 'Run every (minutes)', '0 turns the schedule off. Runs follow the Dry run / Live switch at the top.', 'type="number" min="0"')}
        ${field('max_actions', 'Most phone changes per run', 'A safety cap; anything over it waits for the next run.', 'type="number" min="0"')}
        ${field('max_convs', 'Inbox chats to scan', 'Newest first.', 'type="number" min="1"')}
        ${field('msgs_per_conv', 'Messages read per chat', 'Newest messages of each chat.', 'type="number" min="1"')}
        ${field('min_score', 'Points needed to decide', 'Higher means more messages left for review.', 'type="number" min="0.5" step="0.5"')}
        ${field('spam_min_score', 'Points needed to call it spam', 'Spam gets blocked, so it needs more evidence.', 'type="number" min="0.5" step="0.5"')}
        <button class="btn primary" id="saveSet">Save settings</button></div>
      <div class="panel"><h2>About</h2>${aboutHTML(st.update)}</div>
    </div>`;
  drawPair(pair, st);
  $('#saveSet').onclick = async () => {
    const body = {};
    $$('[data-k]').forEach((i) => (body[i.dataset.k] = i.value));
    try { await api('settings', { method: 'PUT', body }); toast('Settings saved'); refreshStatus(); } catch (e) { toast(e.message); }
  };
}
function aboutHTML(up) {
  if (!up) return '<p class="small">Development build: no update check.</p>';
  const when = up.checked ? ` Checked ${esc(fmtTime(up.checked))}.` : '';
  let line = `<p>Version <b>${esc(up.current)}</b>.</p>`;
  if (up.newer) line += `<p>Version <b>${esc(up.latest)}</b> is out. <a class="btn primary" href="${esc(up.download)}" target="_blank" rel="noopener noreferrer">Download</a></p>
    <p class="hint">On the phone, open the downloaded file to update. The app keeps its data.</p>`;
  else if (up.error) line += `<p class="hint">Couldn't check for updates: ${esc(up.error)}</p>`;
  else if (up.latest) line += `<p class="hint">Up to date.${when}</p>`;
  else line += '<p class="hint">Checking for updates…</p>';
  return line + '<p class="hint"><a href="https://github.com/ahbanavi/sms-cleaner/releases" target="_blank" rel="noopener noreferrer">All releases</a></p>';
}
function drawPair(p, st) {
  const box = $('#pairBox');
  if (!box) return;
  clearTimeout(S.pairTimer);
  const poll = () => { S.pairTimer = setTimeout(async () => drawPair(await api('pair'), S.status || {}), 2500); };
  const cancelBtn = () => { $('#pairCancel').onclick = async () => { await api('pair/cancel', { body: {} }); renderPhone(); }; };
  if (st.demo) { box.innerHTML = '<p>Demo mode uses sample messages. Unset SMSC_DEMO to use your phone.</p>'; return; }

  if (p.status === 'waiting') {
    box.innerHTML = `<ol class="small"><li>On your phone open Google Messages.</li><li>Tap your profile picture, then <b>Device pairing</b>, then <b>QR code scanner</b>.</li><li>Scan this code. It refreshes every 30 seconds.</li></ol>
      <div class="qr">${p.qr_png ? `<img src="${p.qr_png}" alt="Pairing QR code">` : 'Loading…'}</div><p><button class="btn" id="pairCancel">Cancel</button></p>`;
    cancelBtn(); poll(); return;
  }
  if (p.status === 'confirm') {
    box.innerHTML = `<p>Google Messages on your phone now asks you to confirm a new device. Tap this emoji there:</p>
      <div class="emoji">${p.emoji_svg ? `<img src="${esc(p.emoji_svg)}" alt="" width="88" height="88">` : ''}<span>${esc(p.emoji)}</span></div>
      <p class="hint">No prompt? Open Google Messages on the phone; it appears there. This waits up to 5 minutes.</p>
      <button class="btn" id="pairCancel">Cancel</button>`;
    cancelBtn(); poll(); return;
  }
  if (p.paired) {
    const ph = p.phone || {};
    const conn = ph.connected
      ? `<p class="ok"><b>Connected to your phone</b>${ph.since ? ` since ${esc(fmtTime(ph.since))}` : ''}.</p>`
      : ph.error ? `<p class="err"><b>Can't reach your phone:</b> ${esc(ph.error)}</p><p class="hint">It retries on its own. If it keeps failing, forget the phone and pair again.</p>`
      : '<p><b>Paired.</b> Connecting to your phone…</p>';
    box.innerHTML = `${conn}
      <p class="hint">${ph.google_account ? 'Paired through your Google account. ' : ''}The cleaner stays connected like an open Messages for web tab, so the phone must be online. If you open Messages for web elsewhere, the cleaner takes the session back when it runs.</p>
      <button class="btn" id="unpair">Forget this phone</button>`;
    $('#unpair').onclick = async () => {
      if (!(await confirmBox('Forget this phone?', '<p>The saved session is deleted. Also remove the device under Device pairing in Google Messages on the phone.</p>', 'Forget phone', true))) return;
      await api('unpair', { body: {} }); refreshStatus(); renderPhone();
    };
    if (p.status === 'success') toast('Phone paired');
    if (!ph.connected && !ph.error) S.pairTimer = setTimeout(async () => drawPair(await api('pair'), S.status || {}), 4000);
    return;
  }
  const method = S.pairMethod || 'google';
  const android = st.platform === 'android';
  box.innerHTML = `${p.status === 'error' ? `<p class="err">${esc(p.error)}</p>` : ''}
    <div class="seg" role="group" aria-label="Pairing method" style="margin-bottom:12px">
      <button data-pm="google" aria-pressed="${method === 'google'}">Google account</button><button data-pm="qr" aria-pressed="${method === 'qr'}">QR code</button></div>
    ${method === 'google' ? `
      <p class="small">Use this when Device pairing on your phone offers your Google account instead of a QR scanner.</p>
      ${android ? '<p class="small"><b>Do steps 1 to 3 on a computer.</b> Then send the copied text to this phone, for example to yourself in a messenger, and paste it below.</p>' : ''}
      <ol class="small">
        <li>In Edge press <b>Ctrl+Shift+N</b> for an InPrivate window and open <a href="https://messages.google.com/web" target="_blank" rel="noopener noreferrer">messages.google.com/web</a>. Sign in with the Google account your phone uses. Don't pair there.</li>
        <li>Press <b>F12</b>, open <b>Network</b>, clear the filter box, click the <b>Doc</b> filter, then press <b>Ctrl+Shift+R</b> to reload.</li>
        <li>Right-click the request to messages.google.com (named <b>conversations</b> or <b>web</b>), then <b>Copy</b>, then <b>Copy as cURL (bash)</b>. Paste it below.</li>
        <li>Close the InPrivate window without signing out. Signing out would end these cookies.</li>
      </ol>
      <textarea class="input" id="cookies" rows="5" style="width:100%;font-size:12px" dir="ltr" spellcheck="false" autocomplete="off" placeholder="curl 'https://messages.google.com/web/conversations' -b 'SID=…; HSID=…; …'"></textarea>
      <p class="hint">These cookies are a sign-in to your Google account. They are stored only in data/session.json on this server, because Google Messages needs them to stay connected. A Cookie header or a cookie-editor JSON export works too.</p>
      <button class="btn primary" id="pairGoogle">Pair with Google account</button>`
    : android ? '<p class="small">The QR code has to be scanned by this phone, so it can\'t be shown on this phone. Use the Google account method, or pair from the computer version and move the pairing over.</p>'
    : `<p class="small">Pair once, the same way you would with Google Messages for web. The session is stored on this server only.</p>
      <button class="btn primary" id="pairGo">Show pairing code</button>`}`;
  $$('[data-pm]', box).forEach((b) => (b.onclick = () => { S.pairMethod = b.dataset.pm; drawPair({ ...p, status: 'idle' }, st); }));
  if (method === 'google') {
    $('#pairGoogle').onclick = async () => {
      const raw = $('#cookies').value;
      if (!raw.trim()) return toast('Paste the copied cURL command first');
      $('#pairGoogle').disabled = true;
      $('#pairGoogle').textContent = 'Asking your phone…';
      try { drawPair(await api('pair/google', { body: { cookies: raw } }), S.status || {}); }
      catch (e) { toast(e.message); drawPair({ ...p, status: 'error', error: e.message }, st); }
    };
  } else if ($('#pairGo')) {
    $('#pairGo').onclick = async () => {
      $('#pairGo').disabled = true;
      try { drawPair(await api('pair/start', { body: {} }), S.status || {}); } catch (e) { toast(e.message); $('#pairGo').disabled = false; }
    };
  }
}

// ---------- boot ----------
S.view = location.hash.replace('#/', '') || defaultView();
onSwipe($('#main'), (dir) => {
  if (!isMobile() || S.selected) return;
  const i = MSG_TABS.findIndex((t) => t[0] === S.view);
  const n = i >= 0 && MSG_TABS[i + dir];
  if (n) { S.swipeDir = dir; go(n[0]); }
});
onSwipe($('#teach'), (dir) => { if (isMobile() && S.selected) step(dir); });
refreshStatus().then(render);
