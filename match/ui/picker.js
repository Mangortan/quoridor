// Виджет «выбор бота»: поле со спецификацией + выпадающая панель (база, параметры оценки, теги).
// Использование: const p = await createPicker(hostElement, {value: "v2", human: true, onchange: spec => ...});
// p.value — текущая спецификация (читается и пишется).
(function () {
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

  window.createPicker = async function (host, opts) {
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

