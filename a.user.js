// ==UserScript==
// @name         Barricade → наш бот (Авто-ход)
// @namespace    quoridor-bridge
// @version      0.3.0
// @description  Кнопка «Авто-ход» на barricade.gg/computer: спрашивает ход у локального сервера (cmd/bridge), кликает пешкой и подсвечивает стены; бот и теги выбираются списком
// @match        https://barricade.gg/*
// @grant        GM_xmlhttpRequest
// @connect      127.0.0.1
// @connect      localhost
// @run-at       document-idle
// ==/UserScript==

(() => {
  'use strict';

  // ---------- настройки ----------
  // Мобильные менеджеры скриптов не поддерживают GM_getValue/GM_setValue, поэтому храним в localStorage сайта.
  const store = {
    get(k, d) {
      try { const raw = localStorage.getItem('qb_' + k); if (raw !== null) return JSON.parse(raw); } catch (e) { /* ignore */ }
      return d;
    },
    set(k, v) { try { localStorage.setItem('qb_' + k, JSON.stringify(v)); } catch (e) { /* ignore */ } },
  };
  const DEFAULT_SERVER = 'http://127.0.0.1:8090';
  const CFG = {
    server: store.get('server', DEFAULT_SERVER),
    ms: store.get('ms', 1000),            // время на ход, мс (0 = ограничивать по глубине)
    depth: store.get('depth', 4),         // глубина (работает, когда мс = 0)
    bot: store.get('bot', ''),            // пусто = как запущен сервер
    autoClick: store.get('autoClick', true),   // ходить пешкой кликом
    autoDrag: store.get('autoDrag', true),     // перетаскивать стены синтетическими событиями
    wallOpacityMin: 0.6,                  // порог «стена стоит» по прозрачности слота
    pollMs: 350,
  };
  if (typeof CFG.server !== 'string' || !/^https?:\/\//.test(CFG.server)) CFG.server = DEFAULT_SERVER;
  const save = (k, v) => { CFG[k] = v; store.set(k, v); };

  const $ = (s, r = document) => r.querySelector(s);
  const $$ = (s, r = document) => [...r.querySelectorAll(s)];
  const IS_TOUCH = (navigator.maxTouchPoints || 0) > 0 && matchMedia('(pointer: coarse)').matches;
  const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

  // ---------- чтение позиции со страницы ----------
  const getBoard = () => $('[data-tutorial="board"]');

  // Клетки лежат в CSS-grid: клетка (r,c) = grid-area "2+2r / 2+2c"; нечётные дорожки — щели под стены.
  function cellsMap(board) {
    const map = new Map();
    for (const el of board.children) {
      if (typeof el.className !== 'string' || !el.className.includes('rounded-[16%]')) continue;
      const m = /^(\d+)\s*\/\s*(\d+)/.exec(el.style.gridArea || '');
      if (!m) continue;
      const gr = +m[1], gc = +m[2];
      if (gr % 2 || gc % 2) continue;
      map.set(`${gr / 2 - 1},${gc / 2 - 1}`, el);
    }
    return map;
  }

  // Пешка — обёртка с transform: translate(X%, Y%); шаг клетки = 4fr + щель 0.6fr = 115% от ширины клетки.
  const PITCH = 115;
  function parseTransform(str) {
    const m = /translate\(\s*(-?[\d.]+)%\s*,\s*(-?[\d.]+)%/.exec(str || '');
    return m ? { col: Math.round(+m[1] / PITCH), row: Math.round(+m[2] / PITCH) } : null;
  }
  function readPawns(board) {
    const res = {};
    for (const p of $$('[data-pawn]', board)) {
      const cls = typeof p.className === 'string' ? p.className : '';
      const color = cls.includes('red') ? 'red' : cls.includes('blue') ? 'blue' : null;
      const pos = parseTransform(p.parentElement && p.parentElement.style.transform);
      if (!color || !pos) return null;
      res[color] = pos;
    }
    return res.red && res.blue ? res : null;
  }

  // Какого цвета заливка целевой строки: красноватая (r > b) — цель красного.
  function readGoals(cells) {
    const el = cells.get('0,0');
    const m = el && /rgba?\((\d+),\s*(\d+),\s*(\d+)/.exec(getComputedStyle(el).backgroundColor);
    const topIsRed = m ? +m[1] > +m[3] : true;
    return topIsRed ? { red: 0, blue: 8 } : { red: 8, blue: 0 };
  }

  function readCards() {
    const out = {};
    for (const c of $$('[data-tutorial="player-cards"]')) {
      const dot = c.querySelector('span.rounded-full');
      const dcls = dot && typeof dot.className === 'string' ? dot.className : '';
      const color = dcls.includes('red') ? 'red' : dcls.includes('blue') ? 'blue' : null;
      if (!color) continue;
      const t = (c.querySelector('[title]') || {}).title || '';
      const m = /(\d+)\s*\/\s*(\d+)/.exec(t);
      out[color] = { walls: m ? +m[1] : null, active: /amber/.test(c.className), human: /\(You\)/.test(c.textContent) };
    }
    return out.red && out.blue ? out : null;
  }

  // Стена стоит, если внутренний div слота непрозрачен и закрашен (цвет или градиент).
  function readWalls(board) {
    let hw = 0n, vw = 0n, n = 0;
    for (const s of $$('[id^="slot-"]', board)) {
      const m = /^slot-(horizontal|vertical)-(\d)-(\d)$/.exec(s.id);
      const inner = s.firstElementChild;
      if (!m || !inner) continue;
      const cs = getComputedStyle(inner);
      if (parseFloat(cs.opacity) < CFG.wallOpacityMin) continue;
      const bg = /rgba?\(([^)]+)\)/.exec(cs.backgroundColor);
      const parts = bg ? bg[1].split(',') : [];
      const alpha = parts.length > 3 ? parseFloat(parts[3]) : bg ? 1 : 0;
      if (alpha < 0.05 && cs.backgroundImage === 'none') continue;
      const bit = 1n << BigInt(+m[2] * 8 + +m[3]);
      if (m[1] === 'horizontal') hw |= bit; else vw |= bit;
      n++;
    }
    return { hw, vw, n };
  }

  // Формат match.FormatBoard. Движок: игрок 0 идёт к строке 8, игрок 1 к строке 0.
  function boardText(w, p0idx, p1idx, w0, w1, turn) {
    return `HW=0x${w.hw.toString(16)} VW=0x${w.vw.toString(16)} P=${p0idx},${p1idx} W=${w0},${w1} T=${turn}`;
  }

  function readState() {
    const board = getBoard();
    if (!board) return { err: 'доска не найдена (открой barricade.gg/computer)' };
    const cells = cellsMap(board), pawns = readPawns(board), cards = readCards();
    if (cells.size !== 81 || !pawns || !cards) return { err: 'не удалось разобрать страницу' };
    if (cards.red.walls == null || cards.blue.walls == null) return { err: 'не нашёл счётчики стен' };
    const goals = readGoals(cells);
    const walls = readWalls(board);
    const p0 = goals.red === 8 ? 'red' : 'blue', p1 = p0 === 'red' ? 'blue' : 'red';
    const idx = (c) => pawns[c].row * 9 + pawns[c].col;
    const turnColor = cards.red.active ? 'red' : cards.blue.active ? 'blue' : null;
    const humanColor = cards.red.human ? 'red' : cards.blue.human ? 'blue' : 'red';
    const text = boardText(walls, idx(p0), idx(p1), cards[p0].walls, cards[p1].walls, turnColor === p0 ? 0 : 1);
    const start = walls.n === 0 && cards.red.walls === 10 && cards.blue.walls === 10 &&
      pawns.red.col === 4 && pawns.blue.col === 4 && [0, 8].includes(pawns.red.row) && [0, 8].includes(pawns.blue.row);
    return { board, cells, text, turnColor, humanColor, wallsOnBoard: walls.n, start, pawns, cards, goals };
  }

  // ---------- связь с сервером ----------
  // Сначала GM_xmlhttpRequest (обходит CORS); если менеджер его не поддерживает — обычный fetch (на сервере есть CORS).
  function post(path, data) {
    const url = String(CFG.server || '').replace(/\/+$/, '') + path;
    if (!/^https?:\/\//.test(url)) return Promise.reject(new Error('неверный адрес сервера: ' + CFG.server));
    const body = JSON.stringify(data);
    const parse = (status, text) => {
      if (status && status !== 200) throw new Error(`HTTP ${status} от ${url}`);
      try { return JSON.parse(text); } catch (e) { throw new Error('сервер ответил не JSON: ' + url); }
    };
    const viaFetch = () => fetch(url, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body })
      .then(async (r) => parse(r.status, await r.text()))
      .catch((e) => {
        if (/^(HTTP|сервер)/.test(e.message)) throw e;
        throw new Error('сервер недоступен: ' + CFG.server + ' (запущен ли cmd/bridge?)');
      });
    if (typeof GM_xmlhttpRequest !== 'function') return viaFetch();
    return new Promise((resolve, reject) => {
      try {
        GM_xmlhttpRequest({
          method: 'POST', url, timeout: 30000, headers: { 'Content-Type': 'application/json' }, data: body,
          onload: (r) => { try { resolve(parse(r.status, r.responseText)); } catch (e) { reject(e); } },
          onerror: () => viaFetch().then(resolve, reject),
          ontimeout: () => reject(new Error('таймаут ответа сервера')),
        });
      } catch (e) {
        viaFetch().then(resolve, reject);
      }
    });
  }

  // GET JSON (каталог тегов и текущие настройки моста)
  function getJSON(path) {
    const url = String(CFG.server || '').replace(/\/+$/, '') + path;
    if (!/^https?:\/\//.test(url)) return Promise.reject(new Error('неверный адрес сервера: ' + CFG.server));
    const parse = (status, text) => {
      if (status && status !== 200) throw new Error('HTTP ' + status);
      return JSON.parse(text);
    };
    const viaFetch = () => fetch(url).then(async (r) => parse(r.status, await r.text()));
    if (typeof GM_xmlhttpRequest !== 'function') return viaFetch();
    return new Promise((resolve, reject) => {
      try {
        GM_xmlhttpRequest({
          method: 'GET', url, timeout: 8000,
          onload: (r) => { try { resolve(parse(r.status, r.responseText)); } catch (e) { reject(e); } },
          onerror: () => viaFetch().then(resolve, reject),
          ontimeout: () => reject(new Error('таймаут')),
        });
      } catch (e) { viaFetch().then(resolve, reject); }
    });
  }

  // ---------- выпадающий список бота (тот же виджет, что в панели моста) ----------
// Виджет «выбор бота»: поле со спецификацией + выпадающая панель (база, параметры оценки, теги).
// Использование: const p = await createPicker(hostElement, {value: "v2", human: true, onchange: spec => ...});
// p.value — текущая спецификация (читается и пишется).
const createPicker = (function () {
  const css = `
.pk{position:relative;min-width:0}
.pk-btn{display:flex;align-items:center;gap:6px;width:100%;min-width:0;text-align:left;font:inherit;padding:6px 8px;border:1px solid var(--line,#ccc);border-radius:8px;background:var(--bg,#fff);color:var(--ink,#222);cursor:pointer}
.pk-txt{flex:1;min-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.pk-btn:hover{border-color:var(--muted,#888)}
.pk-panel{position:absolute;z-index:100000;left:0;top:calc(100% + 4px);width:min(440px,92vw);max-height:70vh;overflow:auto;padding:10px;border:1px solid var(--line,#ccc);border-radius:10px;background:var(--panel,#fff);box-shadow:0 8px 28px rgba(0,0,0,.25);display:none}
.pk.open .pk-panel{display:grid;gap:10px}
.pk.right .pk-panel{left:auto;right:0}
.pk-panel label{display:flex;flex-direction:column;gap:3px;font-size:12px;color:var(--muted,#777)}
.pk-panel input[type=text],.pk-panel input[type=number],.pk-panel select{font:inherit;padding:5px 7px;border:1px solid var(--line,#ccc);border-radius:7px;background:var(--bg,#fff);color:var(--ink,#222);min-width:0;width:100%}
.pk-tags{display:grid;grid-template-columns:1fr 1fr;gap:6px 12px}
@media (max-width:420px){.pk-tags{grid-template-columns:1fr}}
.pk-tag{display:flex;flex-direction:row;align-items:center;gap:6px;font-size:13px;color:var(--ink,#222);min-width:0}
.pk-panel .pk-tag{flex-direction:row;flex:none}
.pk-tag input[type=checkbox]{flex:none;width:auto}
.pk-tag input[type=number]{width:64px;flex:none}
.pk-tag input[type=text]{flex:1}
.pk-tag span{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.pk-sec{font-size:12px;color:var(--muted,#777);margin:0}
.pk-help{font-size:12px;color:var(--muted,#777);min-height:16px}
`;
  if (!document.getElementById("pk-css")) {
    const st = document.createElement("style");
    st.id = "pk-css"; st.textContent = css; document.head.appendChild(st);
  }

  let catalog = null;
  async function loadCatalog() {
    if (!catalog) catalog = await (await fetch("/api/tags")).json();
    return catalog;
  }

  // "v2:Walls=200@ponder@conn2@book=b.txt" -> {base, params, tags:{name:value}, extra}
  function parse(spec, cat) {
    spec = (spec || "").trim();
    const m = spec.match(/^([^:@]*)(?::([^@]*))?(.*)$/);
    const out = { base: m[1] || "v2", params: m[2] || "", tags: {}, extra: "" };
    const known = new Map(cat.tags.map(t => [t.name, t]));
    const re = /@([a-z]+)(=[^@]*|\d+)?/g;
    let x;
    while ((x = re.exec(m[3])) !== null) {
      const t = known.get(x[1]);
      const v = x[2];
      if (!t) { out.extra += x[0]; continue; }
      if (t.kind === "flag") out.tags[t.name] = true;
      else if (t.kind === "string") out.tags[t.name] = v ? v.slice(1) : t.default || "";
      else out.tags[t.name] = v ? +v : (t.kind === "intopt" ? true : 0);
    }
    return out;
  }

  function compose(st, cat) {
    let s = st.base;
    if (st.params && st.base !== "human" && st.base !== "walker") s += ":" + st.params;
    if (st.base === "human") return "human";
    for (const t of cat.tags) {
      const v = st.tags[t.name];
      if (v === undefined || v === false || v === "") continue;
      if (t.kind === "flag") s += "@" + t.name;
      else if (t.kind === "string") s += "@" + t.name + "=" + v;
      else if (t.kind === "intopt") s += "@" + t.name + (v === true ? "" : v);
      else if (+v > (t.name === "smp" ? 1 : 0)) s += "@" + t.name + v;
    }
    return s + st.extra;
  }

  return async function (host, opts) {
    opts = opts || {};
    const cat = opts.catalog || await loadCatalog();
    let st = parse(opts.value || "v2", cat);
    host.classList.add("pk");
    host.innerHTML = "";
    const btn = document.createElement("button");
    btn.type = "button"; btn.className = "pk-btn";
    btn.innerHTML = '<span class="pk-txt"></span><span>▾</span>';
    const panel = document.createElement("div");
    panel.className = "pk-panel";
    host.append(btn, panel);

    const bases = (opts.human ? ["human"] : []).concat(cat.bases);
    const baseSel = document.createElement("select");
    bases.forEach(b => baseSel.add(new Option(b === "human" ? "human (человек)" : b, b)));
    const params = document.createElement("input");
    params.type = "text"; params.className = "pk-params"; params.placeholder = "веса оценки, напр. Walls=200,Exit=40";
    const raw = document.createElement("input");
    raw.type = "text"; raw.className = "pk-raw"; raw.spellcheck = false;
    const help = document.createElement("div"); help.className = "pk-help";

    const lb = (t, c) => { const l = document.createElement("label"); l.append(t, c); return l; };
    const tagsBox = document.createElement("div"); tagsBox.className = "pk-tags";
    const inputs = {};
    for (const t of cat.tags) {
      const row = document.createElement("div"); row.className = "pk-tag";
      row.title = t.desc;
      let ctl, num;
      if (t.kind === "flag") { ctl = document.createElement("input"); ctl.type = "checkbox"; }
      else if (t.kind === "intopt") { ctl = document.createElement("input"); ctl.type = "checkbox"; num = document.createElement("input"); num.type = "number"; num.min = t.min; num.max = t.max; num.placeholder = "3"; }
      else if (t.kind === "int") { ctl = document.createElement("input"); ctl.type = "number"; ctl.min = t.min; ctl.max = t.max; }
      else { ctl = document.createElement("input"); ctl.type = "checkbox"; num = document.createElement("input"); num.type = "text"; num.placeholder = t.default; }
      const name = document.createElement("span"); name.textContent = "@" + t.name; name.style.cursor = "pointer"; name.addEventListener("click", () => { if (ctl.type === "checkbox") { ctl.checked = !ctl.checked; ctl.dispatchEvent(new Event("change", { bubbles: true })); } else ctl.focus(); });
      row.append(ctl, name); if (num) row.append(num);
      inputs[t.name] = { ctl, num, t };
      row.addEventListener("mouseenter", () => help.textContent = t.desc);
      ctl.addEventListener("focus", () => help.textContent = t.desc);
      tagsBox.appendChild(row);
    }
    const sec = document.createElement("p"); sec.className = "pk-sec"; sec.textContent = "Теги";
    const paramsLabel = lb("Параметры оценки (после «:»)", params);
    const rawLabel = lb("Спецификация (можно править вручную)", raw);
    panel.append(lb("Игрок", baseSel), paramsLabel, sec, tagsBox, help, rawLabel);

    function toControls() {
      baseSel.value = bases.includes(st.base) ? st.base : bases[0];
      params.value = st.params;
      const isBot = st.base !== "human";
      paramsLabel.style.display = st.base === "v2" ? "" : "none";
      sec.style.display = tagsBox.style.display = isBot ? "" : "none";
      for (const name in inputs) {
        const { ctl, num, t } = inputs[name];
        const v = st.tags[name];
        if (t.kind === "flag") ctl.checked = !!v;
        else if (t.kind === "int") ctl.value = v || (t.name === "smp" ? 1 : 0);
        else if (t.kind === "intopt") { ctl.checked = v !== undefined && v !== false; num.value = typeof v === "number" ? v : ""; num.disabled = !ctl.checked; }
        else { ctl.checked = (typeof v === "string" && v !== "") || v === true; num.value = typeof v === "string" ? v : ""; num.disabled = !ctl.checked; }
      }
      const s = compose(st, cat);
      raw.value = s;
      btn.querySelector(".pk-txt").textContent = s;
      btn.title = s;
    }
    function fromControls() {
      st.base = baseSel.value; st.params = params.value.trim();
      for (const name in inputs) {
        const { ctl, num, t } = inputs[name];
        if (t.kind === "flag") st.tags[name] = ctl.checked;
        else if (t.kind === "int") st.tags[name] = +ctl.value || 0;
        else if (t.kind === "intopt") st.tags[name] = ctl.checked ? (num.value ? +num.value : true) : false;
        else st.tags[name] = ctl.checked ? (num.value.trim() || t.default) : "";
      }
      changed();
    }
    function changed() {
      toControls();
      if (opts.onchange) opts.onchange(compose(st, cat));
    }
    panel.addEventListener("change", e => { if (e.target !== raw) fromControls(); });
    panel.addEventListener("input", e => { if (e.target === params) fromControls(); });
    raw.addEventListener("change", () => { st = parse(raw.value, cat); changed(); });
    btn.addEventListener("click", () => {
      host.classList.toggle("open");
      if (host.classList.contains("open")) { // панель не должна вылезать за правый край экрана
        host.classList.remove("right");
        const w = Math.min(440, window.innerWidth * 0.92), r = host.getBoundingClientRect();
        if (r.left + w > window.innerWidth - 8) host.classList.add("right");
      }
    });
    document.addEventListener("click", e => { if (!host.contains(e.target)) host.classList.remove("open"); });
    document.addEventListener("keydown", e => { if (e.key === "Escape") host.classList.remove("open"); });
    toControls();

    return {
      get value() { return compose(st, cat); },
      set value(v) { st = parse(v, cat); toControls(); },
    };
  };
})();


  // ---------- действия на странице ----------
  function realClick(el) {
    const r = el.getBoundingClientRect();
    const x = r.left + r.width / 2, y = r.top + r.height / 2;
    const base = { bubbles: true, cancelable: true, composed: true, clientX: x, clientY: y, button: 0 };
    const ptr = { ...base, pointerId: 1, pointerType: IS_TOUCH ? 'touch' : 'mouse', isPrimary: true };
    el.dispatchEvent(new PointerEvent('pointerover', ptr));
    el.dispatchEvent(new MouseEvent('mouseover', base));
    el.dispatchEvent(new PointerEvent('pointerdown', { ...ptr, buttons: 1 }));
    el.dispatchEvent(new MouseEvent('mousedown', { ...base, buttons: 1 }));
    el.dispatchEvent(new PointerEvent('pointerup', ptr));
    el.dispatchEvent(new MouseEvent('mouseup', base));
    el.dispatchEvent(new MouseEvent('click', base));
  }

  // Перетаскивание стены из стойки на слот (dnd-kit): pointer-события + touch-события для мобильных.
  async function tryDragWall(horizontal, slot) {
    const item = $(`[data-rack-wall="${horizontal ? 'horizontal' : 'vertical'}"]`);
    const handle = item && item.closest('[role="button"]');
    if (!handle || !slot) return false;
    const a = handle.getBoundingClientRect(), b = slot.getBoundingClientRect();
    const sx = a.left + a.width / 2, sy = a.top + a.height / 2;
    const tx = b.left + b.width / 2, ty = b.top + b.height / 2;
    const ptype = IS_TOUCH ? 'touch' : 'mouse';
    const pev = (type, x, y, buttons) => new PointerEvent(type, {
      bubbles: true, cancelable: true, composed: true, clientX: x, clientY: y,
      pointerId: 1, pointerType: ptype, isPrimary: true, button: 0, buttons,
    });
    const canTouch = IS_TOUCH && typeof Touch === 'function' && typeof TouchEvent === 'function';
    const tev = (type, target, x, y) => {
      const t = new Touch({ identifier: 1, target, clientX: x, clientY: y, pageX: x + scrollX, pageY: y + scrollY, screenX: x, screenY: y });
      const end = type === 'touchend' || type === 'touchcancel';
      return new TouchEvent(type, {
        bubbles: true, cancelable: true, composed: true,
        touches: end ? [] : [t], targetTouches: end ? [] : [t], changedTouches: [t],
      });
    };
    const fire = (el, ev) => { try { el.dispatchEvent(ev); } catch (e) { /* ignore */ } };
    fire(handle, pev('pointerdown', sx, sy, 1));
    if (canTouch) fire(handle, tev('touchstart', handle, sx, sy));
    await sleep(IS_TOUCH ? 220 : 60); // у touch-датчиков бывает задержка активации
    const N = 16;
    for (let i = 1; i <= N; i++) {
      const x = sx + (tx - sx) * i / N, y = sy + (ty - sy) * i / N;
      fire(document, pev('pointermove', x, y, 1));
      if (canTouch) fire(handle, tev('touchmove', handle, x, y));
      await sleep(25);
    }
    await sleep(120);
    fire(document, pev('pointerup', tx, ty, 0));
    if (canTouch) fire(handle, tev('touchend', handle, tx, ty));
    return true;
  }

  // ---------- подсветка ----------
  let hint = null;
  function clearHint() {
    if (!hint) return;
    cancelAnimationFrame(hint.raf);
    hint.o.remove();
    hint = null;
  }
  function trackHint() {
    if (!hint) return;
    if (!hint.el.isConnected) { clearHint(); return; }
    const r = hint.el.getBoundingClientRect(), p = hint.pad;
    Object.assign(hint.o.style, {
      left: r.left - p + 'px', top: r.top - p + 'px', width: r.width + 2 * p + 'px', height: r.height + 2 * p + 'px',
    });
    hint.raf = requestAnimationFrame(trackHint);
  }
  function showHint(el, wall) {
    clearHint();
    if (!el) return;
    const o = document.createElement('div');
    o.style.cssText = 'position:fixed;z-index:99999;pointer-events:none;border-radius:6px;border:3px solid #fbbf24;' +
      'box-shadow:0 0 14px #fbbf24;animation:qbpulse .7s infinite alternate;' + (wall ? 'background:rgba(251,191,36,.6);' : '');
    document.body.appendChild(o);
    hint = { el, o, pad: wall ? 4 : 0, raf: 0 };
    trackHint();
  }

  // ---------- панель ----------
  const INPUT_CSS = 'background:#18181b;color:#e4e4e7;border:1px solid #3f3f46;border-radius:6px;padding:4px 6px;font:13px system-ui,sans-serif;';
  let enabled = false, busy = false, lastSig = '', lastWalls = -1;
  const ui = {};

  function setStatus(text, isErr) {
    if (!ui.status) return;
    ui.status.textContent = text;
    ui.status.style.color = isErr ? '#f87171' : '#a1a1aa';
  }
  function renderToggle() {
    ui.toggle.textContent = enabled ? 'Авто-ход: ВКЛ (бот играет)' : 'Авто-ход: выкл (обычная игра)';
    ui.toggle.style.background = enabled ? '#b45309' : '#27272a';
    ui.toggle.style.color = enabled ? '#fff' : '#d4d4d8';
  }
  function ensurePanel() {
    if (document.getElementById('qb-panel')) return;
    const rack = $('[data-tutorial="wall-rack"]');
    if (!rack || !getBoard()) return;
    if (!document.getElementById('qb-style')) {
      const st = document.createElement('style');
      st.id = 'qb-style';
      st.textContent = '@keyframes qbpulse{from{opacity:.45}to{opacity:1}}' +
        '#qb-panel button{cursor:pointer;border:1px solid #3f3f46;border-radius:8px;padding:10px 12px;font:600 13px system-ui,sans-serif}' +
        '#qb-panel label{font:12px system-ui,sans-serif;color:#a1a1aa;display:flex;gap:6px;align-items:center}' +
        '#qb-panel .pk-btn{padding:6px 8px;font:13px system-ui,sans-serif;background:#18181b;color:#e4e4e7;border:1px solid #3f3f46;border-radius:6px}' +
        '#qb-panel .pk-panel label{flex-direction:column;align-items:stretch}' +
        '#qb-panel .pk-panel{font:13px system-ui,sans-serif}';
      document.head.appendChild(st);
    }
    const panel = document.createElement('div');
    panel.id = 'qb-panel';
    panel.style.cssText = 'order:3;width:var(--game-col-w);margin-top:6px;display:flex;flex-direction:column;gap:6px;--panel:#18181b;--bg:#27272a;--ink:#e4e4e7;--muted:#a1a1aa;--line:#3f3f46;';
    panel.innerHTML =
      '<div style="display:flex;gap:6px"><button id="qb-toggle" style="flex:1"></button><button id="qb-hint" style="background:#27272a;color:#d4d4d8">Подсказать ход</button></div>' +
      '<div id="qb-status" style="font:12px system-ui,sans-serif;color:#a1a1aa;min-height:16px"></div>' +
      '<div style="display:flex;gap:14px;align-items:center;flex-wrap:wrap">' +
      '<label>Глубина <input type="number" id="qb-depth" min="1" max="14" step="1" style="' + INPUT_CSS + 'width:56px"></label>' +
      '<label>Мс <input type="number" id="qb-ms" min="0" max="120000" step="100" style="' + INPUT_CSS + 'width:84px"></label>' +
      '<span style="font:11px system-ui,sans-serif;color:#71717a">мс &gt; 0 — по времени, 0 — по глубине</span></div>' +
      '<div style="display:flex;gap:8px;align-items:center">' +
      '<label style="flex:1">Сервер <input type="text" id="qb-server" autocapitalize="off" spellcheck="false" style="' + INPUT_CSS + 'flex:1;min-width:0"></label></div>' +
      '<div style="font:12px system-ui,sans-serif;color:#a1a1aa">Бот (список с тегами)</div>' +
      '<div id="qb-bot" style="min-width:0"></div>' +
      '<div style="display:flex;gap:12px;flex-wrap:wrap">' +
      '<label><input type="checkbox" id="qb-click"> ходить пешкой сам</label>' +
      '<label><input type="checkbox" id="qb-drag"> тащить стены сам</label>' +
      '<a id="qb-diag" href="#" style="font:12px system-ui,sans-serif;color:#71717a">диагностика</a></div>';
    rack.insertAdjacentElement('afterend', panel);
    ui.toggle = panel.querySelector('#qb-toggle');
    ui.status = panel.querySelector('#qb-status');
    const click = panel.querySelector('#qb-click'), drag = panel.querySelector('#qb-drag');
    // поля глубины и времени: сохраняются сразу, значения приводятся к допустимым границам
    const depthIn = panel.querySelector('#qb-depth'), msIn = panel.querySelector('#qb-ms');
    const clampInt = (v, lo, hi, dflt) => { v = parseInt(v, 10); return Number.isFinite(v) ? Math.min(hi, Math.max(lo, v)) : dflt; };
    depthIn.value = CFG.depth;
    msIn.value = CFG.ms;
    depthIn.onchange = () => { save('depth', clampInt(depthIn.value, 1, 14, 4)); depthIn.value = CFG.depth; };
    msIn.onchange = () => { save('ms', clampInt(msIn.value, 0, 120000, 1000)); msIn.value = CFG.ms; };
    const serverIn = panel.querySelector('#qb-server');
    serverIn.value = CFG.server;
    serverIn.onchange = () => { save('server', serverIn.value.trim().replace(/\/+$/, '') || DEFAULT_SERVER); serverIn.value = CFG.server; ui.botReady = false; ui.botTry = 0; };
    click.checked = CFG.autoClick;
    drag.checked = CFG.autoDrag;
    click.onchange = () => save('autoClick', click.checked);
    drag.onchange = () => save('autoDrag', drag.checked);
    ui.toggle.onclick = () => {
      enabled = !enabled;
      renderToggle();
      lastSig = '';
      if (!enabled) { clearHint(); setStatus('Обычная игра.'); } else setStatus('Жду свой ход…');
    };
    panel.querySelector('#qb-hint').onclick = () => manual();
    panel.querySelector('#qb-diag').onclick = (e) => { e.preventDefault(); diagnose(); };
    renderToggle();
    ui.botTry = Date.now();
    initBotPicker();
  }

  // Список бота: каталог тегов и текущего бота берём у моста. Пока мост недоступен, показываем обычное поле
  // и повторяем попытку (см. tick), поэтому скрипт можно открыть до запуска cmd/bridge.
  async function initBotPicker() {
    const host = document.getElementById('qb-bot');
    if (!host || ui.botLoading) return;
    ui.botLoading = true;
    try {
      const [catalog, cfg] = await Promise.all([getJSON('/api/tags'), getJSON('/api/config')]);
      host.innerHTML = '';
      ui.bot = await createPicker(host, { value: CFG.bot || cfg.bot || 'v2', human: false, catalog, onchange: (spec) => save('bot', spec) });
      ui.botReady = true;
    } catch (e) {
      if (!host.firstChild) {
        host.innerHTML = '<input type="text" id="qb-bot-text" placeholder="сервер недоступен — спецификация бота" autocapitalize="off" spellcheck="false" style="' + INPUT_CSS + 'width:100%;box-sizing:border-box">';
        const t = host.firstChild;
        t.value = CFG.bot;
        t.onchange = () => save('bot', t.value.trim());
      }
    } finally {
      ui.botLoading = false;
    }
  }

  function diagnose() {
    const st = readState();
    if (st.err) { setStatus(st.err, true); console.log('[qb]', st.err); return; }
    const slots = $$('[id^="slot-"]', st.board).length;
    console.log('[qb] state', st);
    setStatus(`позиция: ${st.text} | слотов стен: ${slots} | стен на доске: ${st.wallsOnBoard} | ход: ${st.turnColor} (вы: ${st.humanColor})`);
  }

  // ---------- логика хода ----------
  async function applyMove(st, r) {
    const limit = CFG.ms > 0 ? `лимит ${CFG.ms} мс` : `лимит глубины ${CFG.depth}`;
    const info = [limit, r.score != null ? `оценка ${r.score}` : '', r.depth ? `глубина ${r.depth}` : '', `${Math.round(r.ms)} мс`, r.fromBook ? 'книга' : '']
      .filter(Boolean).join(' · ');
    if (r.kind === 'pawn') {
      const cell = st.cells.get(`${r.row},${r.col}`);
      showHint(cell, false);
      setStatus(`Пешка → (${r.row},${r.col}) · ${info}`);
      if (CFG.autoClick && cell) { await sleep(150); realClick(cell); }
    } else {
      const dir = r.horizontal ? 'horizontal' : 'vertical';
      const slot = document.getElementById(`slot-${dir}-${r.row}-${r.col}`);
      showHint(slot, true);
      setStatus(`СТЕНА ${r.horizontal ? 'горизонтальная' : 'вертикальная'} (${dir}-${r.row}-${r.col}) · ${info}`);
      if (CFG.autoDrag) {
        const ok = await tryDragWall(r.horizontal, slot);
        if (!ok) setStatus('Не нашёл стойку со стеной; поставь вручную. ' + ui.status.textContent, true);
      }
    }
  }

  async function suggest(st, force) {
    busy = true;
    lastSig = st.text;
    try {
      setStatus('Думаю…');
      const reset = st.start || (lastWalls >= 0 && st.wallsOnBoard < lastWalls);
      lastWalls = st.wallsOnBoard;
      const r = await post('/move', { board: st.text, bot: CFG.bot, ms: CFG.ms, depth: CFG.depth, reset });
      if (!r.ok) { setStatus('Ошибка: ' + r.error, true); return; }
      await applyMove(st, r);
    } catch (e) {
      setStatus(e.message, true);
    } finally {
      busy = false;
    }
  }

  async function manual() {
    if (busy) return;
    const st = readState();
    if (st.err) { setStatus(st.err, true); return; }
    if (st.turnColor && st.turnColor !== st.humanColor) { setStatus('Сейчас ход соперника.', true); return; }
    await suggest(st, true);
  }

  async function tick() {
    ensurePanel();
    if (ui.status && !ui.botReady && !ui.botLoading && Date.now() - (ui.botTry || 0) > 5000) { ui.botTry = Date.now(); initBotPicker(); }
    if (!enabled || busy || !ui.status) return;
    const st = readState();
    if (st.err) { setStatus(st.err, true); return; }
    if (!st.turnColor) { clearHint(); setStatus('Партия окончена (или нет активного игрока).'); return; }
    if (st.turnColor !== st.humanColor) { clearHint(); setStatus('Ход соперника…'); lastSig = ''; return; }
    if (st.text === lastSig) return;
    await suggest(st, false);
  }

  setInterval(() => { tick().catch((e) => console.error('[qb]', e)); }, CFG.pollMs);

  // для отладки из консоли: __qb.state(), __qb.diagnose()
  window.__qb = { state: readState, diagnose, parseTransform, boardText, CFG };
})();



