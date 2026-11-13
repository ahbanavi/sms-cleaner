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
  $('#banners').innerHTML = b.join('');
  $('#runNow').disabled = $('#runPreview').disabled = st.running || !st.paired;

  if (S.wasRunning && !st.running) {
    toast('Run finished');
    if (['review', 'spam', 'offers', 'archive', 'keep', 'all', 'activity'].includes(S.view)) render();
  }
  S.wasRunning = st.running;
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
window.addEventListener('hashchange', () => { S.view = location.hash.replace('#/', '') || 'review'; S.selected = null; render(); });
$$('.tray').forEach((b) => (b.onclick = () => go(b.dataset.view)));

const LIST_VIEWS = {
  review: { title: 'Needs review', cat: 'unknown', sub: 'The classifier wasn\'t sure about these, so it left them in your inbox. Label a few and it learns from them.', kbd: true, label: 'none', onPhone: 'yes' },
  spam: { title: 'Spam', cat: 'spam', sub: 'Blocked, reported to Google and deleted from the phone. Copies are kept here.' },
  offers: { title: 'Offers', cat: 'offer', sub: 'Discounts and promotions, removed from your phone and kept here to search. Tap a code to copy it.', offers: true },
  archive: { title: 'Archive', cat: 'archive', sub: 'Bank alerts, codes, deliveries and other service notices. They stay on your phone, under Archived.' },
  keep: { title: 'Keep', cat: 'keep', sub: 'Written by a person. Never touched.' },
  all: { title: 'Everything', cat: '', sub: 'Every message the cleaner has seen. Contacts are never read, so they are not here.' },
};

function render() {
  $$('.tray').forEach((b) => { if (b.dataset.view === S.view) b.setAttribute('aria-current', 'page'); else b.removeAttribute('aria-current'); });
  const listView = LIST_VIEWS[S.view];
  $('#app').classList.toggle('wide', !listView);
  closeTeach();
  if (listView) return renderList(listView);
  ({ rules: renderRules, test: renderTest, model: renderModel, activity: renderActivity, phone: renderPhone }[S.view] || (() => go('review')))();
}

// ---------- message lists ----------
async function renderList(v) {
  const f = (S.filter[S.view] ||= { q: '', onPhone: v.onPhone || '', label: v.label || '', sender: '', from: '', to: '' });
  const main = $('#main');
  main.innerHTML = `
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
  if (v.offers) {
    api('senders?category=offer').then((list) => {
      const sel = $('#sender');
      if (!sel) return;
      sel.innerHTML += list.map((s) => `<option value="${esc(s.sender)}" ${s.sender === f.sender ? 'selected' : ''}>${esc(s.name && s.name !== s.sender ? `${s.name} (${s.sender})` : s.sender)} (${s.count})</option>`).join('');
    });
    $('#sender').onchange = (e) => { f.sender = e.target.value; loadList(v, true); };
    $('#from').onchange = (e) => { f.from = e.target.value; loadList(v, true); };
    $('#to').onchange = (e) => { f.to = e.target.value; loadList(v, true); };
  }
  $('#more').onclick = () => loadList(v, false);
  loadList(v, true);
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
  $('#total').textContent = res.total ? `${res.total} message${res.total === 1 ? '' : 's'}` : '';
  if (!S.list.length) {
    box.innerHTML = `<div class="empty">${S.view === 'review'
      ? (S.status?.stats?.messages ? 'Nothing to review. Everything has been sorted.' : 'No messages yet. Pair your phone and run a preview to fill this in.')
      : 'No messages match.'}</div>`;
  } else box.innerHTML = S.list.map((m) => slipHTML(m, v.offers)).join('');
  $('#more').hidden = S.list.length >= S.total;
  box.onclick = (e) => {
    const code = e.target.closest('mark.code');
    if (code) { navigator.clipboard?.writeText(code.dataset.code); toast(`Copied ${code.dataset.code}`); e.stopPropagation(); return; }
    const s = e.target.closest('.slip');
    if (s) select(s.dataset.id);
  };
}

function slipHTML(m, offers) {
  const cat = m.label || m.category || 'unknown';
  const state = !m.on_phone ? '<span class="state deleted">removed from phone</span>' : m.action === 'archived' ? '<span class="state">archived</span>' : '';
  return `<button class="slip cat-${esc(cat)} ${m.on_phone ? '' : 'gone'}" role="option" data-id="${esc(m.id)}" aria-selected="${S.selected === m.id}">
    <div class="slip-meta"><span class="slip-sender">${esc(m.sender_name && m.sender_name !== m.sender ? m.sender_name : m.sender)}</span>
      <span title="${esc(jalali(m.ts))}">${esc(fmtTime(m.ts))}</span>${tag(cat)}${m.label ? '<span class="state">labeled by you</span>' : m.confidence ? `<span class="state num">${Math.round(m.confidence * 100)}%</span>` : ''}${state}</div>
    <div class="slip-body ${offers ? 'full' : ''}" dir="auto">${bodyHTML(m.body, offers)}</div></button>`;
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
  S.selected = id;
  $$('.slip').forEach((s) => s.setAttribute('aria-selected', s.dataset.id === id));
  $(`.slip[data-id="${CSS.escape(id)}"]`)?.scrollIntoView({ block: 'nearest' });
  try { S.detail = await api('messages/' + encodeURIComponent(id)); } catch (e) { toast(e.message); return; }
  if (S.selected === id) renderTeach();
}

function verdictHTML(now) {
  const cat = now.category;
  const scores = now.scores || {};
  const maxS = Math.max(3, ...Object.values(scores));
  const bars = LABELS.map((c) => `<span class="cat-${c}">${catName(c)}</span><span class="bar cat-${c}"><i style="width:${Math.min(100, ((scores[c] || 0) / maxS) * 100)}%"></i></span><span class="num muted">${(scores[c] || 0).toFixed(1)}</span>`).join('');
  const reasons = (now.reasons || []).slice(0, 8).map((r) => `<li class="cat-${esc(r.category)}"><span class="tag cat-${esc(r.category)} ${r.category === 'offer' ? 'offer' : ''}">${esc(catName(r.category))}</span><span dir="auto">${esc(r.detail)}</span><span class="w">+${r.weight}</span></li>`).join('');
  return `<div class="verdict cat-${esc(cat)}"><span class="big">${esc(catName(cat))}</span>
    <span class="muted small">${cat === 'unknown' ? 'not sure enough to act' : `would be ${esc(CATS[cat].does)}`}</span></div>
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
  t.innerHTML = `
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
    <div class="labels">${LABELS.map((c) => `<button class="label-btn cat-${c}" data-setlabel="${c}" aria-pressed="${m.label === c}">${catName(c)}<kbd>${CATS[c].key.toUpperCase()}</kbd></button>`).join('')}</div>
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

  $('#closeTeach').onclick = closeTeach;
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

async function setLabel(id, label) {
  let res;
  try { res = await api(`messages/${encodeURIComponent(id)}/label`, { body: { label } }); } catch (e) { toast(e.message); return; }
  toast(label ? `Labeled ${catName(label)}. Model retrained.` : 'Label removed');
  const m = res.message;
  if (S.view === 'review' && label) {
    const i = S.list.findIndex((x) => x.id === id);
    S.list.splice(i, 1);
    $(`.slip[data-id="${CSS.escape(id)}"]`)?.remove();
    S.total--;
    $('#total').textContent = S.total ? `${S.total} messages` : '';
    const next = S.list[Math.min(i, S.list.length - 1)];
    if (next) select(next.id); else { closeTeach(); if (!S.list.length) $('#slips').innerHTML = '<div class="empty">Nothing to review. Everything has been sorted.</div>'; }
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
  main.innerHTML = `<div class="view-head"><h1>Rules</h1><span class="muted" id="rcount"></span></div>
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
  $('#main').innerHTML = `<div class="view-head"><h1>Try a message</h1></div>
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
  main.innerHTML = '<div class="view-head"><h1>What it learned</h1></div><p class="muted">Loading…</p>';
  const m = await api('model');
  const cls = m.classes || {};
  main.innerHTML = `<div class="view-head"><h1>What it learned</h1></div>
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
async function showActions(runId) {
  const acts = await api('actions?run=' + runId);
  const names = { block_report: 'Block + report', delete_conversation: 'Delete chat', delete_message: 'Delete message', archive: 'Archive' };
  $('#acts').innerHTML = `<div class="panel" style="padding:4px 8px;overflow-x:auto"><h2 style="padding:10px 8px 0">Run ${esc(runId)}</h2><table><thead><tr><th>Sender</th><th>Category</th><th>Action</th><th>Details</th><th>Result</th></tr></thead><tbody>
    ${acts.map((a) => `<tr><td dir="ltr" style="text-align:start">${esc(a.sender)}</td><td>${tag(a.category)}</td><td>${esc(names[a.action] || a.action)}</td><td dir="auto" class="small">${esc(a.detail)}</td>
      <td>${a.ok ? (a.dry ? '<span class="muted">would do</span>' : '<span class="ok">done</span>') : `<span class="err">${esc(a.error)}</span>`}</td></tr>`).join('') || '<tr><td colspan="5" class="muted">Nothing to do in this run.</td></tr>'}</tbody></table></div>`;
}

// ---------- phone & settings ----------
async function renderPhone() {
  const main = $('#main');
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
    </div>`;
  drawPair(pair, st);
  $('#saveSet').onclick = async () => {
    const body = {};
    $$('[data-k]').forEach((i) => (body[i.dataset.k] = i.value));
    try { await api('settings', { method: 'PUT', body }); toast('Settings saved'); refreshStatus(); } catch (e) { toast(e.message); }
  };
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
  box.innerHTML = `${p.status === 'error' ? `<p class="err">${esc(p.error)}</p>` : ''}
    <div class="seg" role="group" aria-label="Pairing method" style="margin-bottom:12px">
      <button data-pm="google" aria-pressed="${method === 'google'}">Google account</button><button data-pm="qr" aria-pressed="${method === 'qr'}">QR code</button></div>
    ${method === 'google' ? `
      <p class="small">Use this when Device pairing on your phone offers your Google account instead of a QR scanner.</p>
      <ol class="small">
        <li>In Edge press <b>Ctrl+Shift+N</b> for an InPrivate window and open <a href="https://messages.google.com/web" target="_blank" rel="noopener noreferrer">messages.google.com/web</a>. Sign in with the Google account your phone uses. Don't pair there.</li>
        <li>Press <b>F12</b>, open <b>Network</b>, clear the filter box, click the <b>Doc</b> filter, then press <b>Ctrl+Shift+R</b> to reload.</li>
        <li>Right-click the request to messages.google.com (named <b>conversations</b> or <b>web</b>), then <b>Copy</b>, then <b>Copy as cURL (bash)</b>. Paste it below.</li>
        <li>Close the InPrivate window without signing out. Signing out would end these cookies.</li>
      </ol>
      <textarea class="input" id="cookies" rows="5" style="width:100%;font-size:12px" dir="ltr" spellcheck="false" autocomplete="off" placeholder="curl 'https://messages.google.com/web/conversations' -b 'SID=…; HSID=…; …'"></textarea>
      <p class="hint">These cookies are a sign-in to your Google account. They are stored only in data/session.json on this server, because Google Messages needs them to stay connected. A Cookie header or a cookie-editor JSON export works too.</p>
      <button class="btn primary" id="pairGoogle">Pair with Google account</button>`
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
  } else {
    $('#pairGo').onclick = async () => {
      $('#pairGo').disabled = true;
      try { drawPair(await api('pair/start', { body: {} }), S.status || {}); } catch (e) { toast(e.message); $('#pairGo').disabled = false; }
    };
  }
}

// ---------- boot ----------
S.view = location.hash.replace('#/', '') || 'review';
refreshStatus().then(render);
