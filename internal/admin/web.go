package admin

import (
	"net/http"
)

// serveIndex 返回管理面板单页（前端为纯 HTML/CSS/JS，无构建步骤）。
func (m *Manager) serveIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Write([]byte(adminHTML))
}

const adminHTML = `<!DOCTYPE html>
<html lang="zh">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>wikisql 管理面板</title>
<style>
:root {
  --bg: #0f1115; --panel: #171a21; --border: #2a2f3a; --text: #e6e9ef;
  --muted: #8b93a3; --accent: #4f8cff; --ok: #34c47c; --warn: #e0a63c; --err: #e05c5c;
}
* { box-sizing: border-box; }
body { margin: 0; font-family: system-ui, -apple-system, "PingFang SC", "Microsoft YaHei", sans-serif;
  background: var(--bg); color: var(--text); font-size: 15px; line-height: 1.5; }
.wrap { max-width: 860px; margin: 0 auto; padding: 16px; }
h1 { font-size: 20px; margin: 12px 0; }
h2 { font-size: 16px; margin: 0 0 12px; color: var(--accent); }
.card { background: var(--panel); border: 1px solid var(--border); border-radius: 10px; padding: 16px; margin-bottom: 14px; }
.muted { color: var(--muted); font-size: 13px; }
.row { display: flex; justify-content: space-between; gap: 12px; padding: 7px 0; border-bottom: 1px dashed var(--border); }
.row:last-child { border-bottom: none; }
.row .k { color: var(--muted); flex-shrink: 0; }
.row .v { text-align: right; word-break: break-all; }
label { display: block; margin: 10px 0 4px; font-size: 13px; color: var(--muted); }
input[type=text], input[type=password], input[type=number], select {
  width: 100%; padding: 9px 10px; border-radius: 8px; border: 1px solid var(--border);
  background: #0c0e12; color: var(--text); font-size: 14px; }
input:focus, select:focus { outline: none; border-color: var(--accent); }
.btn { display: inline-block; padding: 9px 16px; border: none; border-radius: 8px; cursor: pointer;
  background: var(--accent); color: #fff; font-size: 14px; }
.btn:hover { filter: brightness(1.1); }
.btn.ghost { background: transparent; border: 1px solid var(--border); color: var(--text); }
.btn.danger { background: var(--err); }
.btn.small { padding: 5px 10px; font-size: 13px; }
.btn:disabled { opacity: 0.5; cursor: not-allowed; }
.tabs { display: flex; gap: 8px; margin: 14px 0; flex-wrap: wrap; }
.tab { padding: 8px 18px; border-radius: 8px; border: 1px solid var(--border); cursor: pointer;
  background: transparent; color: var(--muted); font-size: 14px; }
.tab.active { background: var(--accent); border-color: var(--accent); color: #fff; }
.switch { display: flex; align-items: center; gap: 10px; margin: 10px 0; }
.switch input { width: 42px; height: 24px; appearance: none; background: var(--border); border-radius: 12px;
  position: relative; cursor: pointer; transition: background .15s; flex-shrink: 0; }
.switch input::after { content: ""; position: absolute; width: 18px; height: 18px; border-radius: 50%;
  background: #fff; top: 3px; left: 3px; transition: left .15s; }
.switch input:checked { background: var(--ok); }
.switch input:checked::after { left: 21px; }
.switch span { font-size: 14px; }
.badge { display: inline-block; padding: 2px 9px; border-radius: 20px; font-size: 12px; }
.badge.ok { background: rgba(52,196,124,.15); color: var(--ok); }
.badge.err { background: rgba(224,92,92,.15); color: var(--err); }
.badge.warn { background: rgba(224,166,60,.15); color: var(--warn); }
.badge.info { background: rgba(79,140,255,.15); color: var(--accent); }
.toast { position: fixed; bottom: 20px; left: 50%; transform: translateX(-50%);
  background: #22262f; border: 1px solid var(--border); padding: 10px 20px; border-radius: 8px;
  font-size: 14px; opacity: 0; transition: opacity .2s; pointer-events: none; z-index: 10; max-width: 90%; }
.toast.show { opacity: 1; }
.toast.err { border-color: var(--err); color: var(--err); }
.toast.ok { border-color: var(--ok); }
.help { font-size: 12px; color: var(--muted); margin-top: 3px; }
.footer { text-align: center; color: var(--muted); font-size: 12px; margin: 24px 0 40px; }
.footer a { color: var(--accent); text-decoration: none; }
.grid2 { display: grid; grid-template-columns: 1fr 1fr; gap: 0 14px; }
@media (max-width: 640px) { .grid2 { grid-template-columns: 1fr; } }
.hidden { display: none !important; }
.secret-row { display: flex; gap: 8px; }
.secret-row input { flex: 1; }
.progress { height: 6px; background: var(--border); border-radius: 3px; overflow: hidden; margin-top: 8px; }
.progress > div { height: 100%; background: var(--accent); width: 0; transition: width .3s; }
</style>
</head>
<body>
<div class="wrap">

<!-- ═══ 登录视图 ═══ -->
<div id="view-login" class="hidden">
  <h1 style="text-align:center; margin-top:60px;">wikisql 管理面板</h1>
  <div class="card" style="max-width:380px; margin:20px auto;">
    <label>管理密码</label>
    <input type="password" id="login-pass" autocomplete="current-password">
    <div style="margin-top:14px;">
      <button class="btn" style="width:100%;" onclick="doLogin()">登录</button>
    </div>
    <div class="muted" style="margin-top:10px; text-align:center;">权限极高，请使用强密码并妥善保管</div>
  </div>
</div>

<!-- ═══ 主视图 ═══ -->
<div id="view-main" class="hidden">
  <h1>wikisql 管理面板</h1>
  <div class="tabs">
    <button class="tab active" data-tab="status" onclick="switchTab('status')">服务状态</button>
    <button class="tab" data-tab="config" onclick="switchTab('config')">配置</button>
    <button class="tab" data-tab="security" onclick="switchTab('security')">安全</button>
    <button class="tab" data-tab="about" onclick="switchTab('about')">关于</button>
    <div style="flex:1"></div>
    <button class="tab" onclick="doLogout()">退出登录</button>
  </div>

  <!-- 状态页 -->
  <div id="tab-status">
    <div class="card">
      <h2>运行状态</h2>
      <div id="status-basic"></div>
    </div>
    <div class="card">
      <h2>数据与存储</h2>
      <div id="status-db"></div>
    </div>
    <div class="card">
      <h2>最近巡检记录</h2>
      <div id="status-health"></div>
    </div>
  </div>

  <!-- 配置页 -->
  <div id="tab-config" class="hidden">
    <div class="card" style="border-color: var(--warn);">
      <div style="display:flex; justify-content:space-between; align-items:center; gap:10px; flex-wrap:wrap;">
        <div>修改配置后需<b>重启服务</b>才能生效。</div>
        <button class="btn" onclick="doRestart()">重启服务</button>
      </div>
      <div id="restart-progress" class="progress hidden"><div></div></div>
    </div>
    <div id="config-sections"></div>
    <div class="card">
      <button class="btn" onclick="saveConfig()">保存全部配置</button>
      <span class="muted" style="margin-left:10px;">密钥类字段留空表示保持原值不变</span>
    </div>
  </div>

  <!-- 安全页 -->
  <div id="tab-security" class="hidden">
    <div class="card">
      <h2>修改管理密码</h2>
      <label>当前密码</label>
      <input type="password" id="pw-current" autocomplete="current-password">
      <label>新密码（至少 8 位）</label>
      <input type="password" id="pw-new" autocomplete="new-password">
      <label>重复新密码</label>
      <input type="password" id="pw-new2" autocomplete="new-password">
      <div style="margin-top:14px;">
        <button class="btn" onclick="changePassword()">修改密码</button>
      </div>
      <div class="help">修改后所有登录会话失效，需用新密码重新登录。</div>
    </div>
    <div class="card">
      <h2>重启服务</h2>
      <div class="muted" style="margin-bottom:10px;">重启会使当前会话失效，并触发一次数据库备份（10 分钟内已备份则跳过）。</div>
      <button class="btn danger" onclick="doRestart()">立即重启</button>
      <div id="restart-progress2" class="progress hidden"><div></div></div>
    </div>
  </div>

  <!-- 关于页 -->
  <div id="tab-about" class="hidden">
    <div class="card">
      <h2>wikisql</h2>
      <div class="row"><span class="k">说明</span><span class="v">个人自托管的一体化数据底座：SQLite 数据库服务 + 知识库</span></div>
      <div class="row"><span class="k">API 文档</span><span class="v"><a href="/docs" style="color:var(--accent)">/docs</a></span></div>
      <div class="row"><span class="k">MCP 端点</span><span class="v">/mcp（供 AI agent 调用）</span></div>
      <div class="row"><span class="k">GitHub</span><span class="v"><a href="https://github.com/Black0Bag/wikisql" target="_blank" rel="noopener" style="color:var(--accent)">Black0Bag/wikisql</a></span></div>
    </div>
  </div>

  <div class="footer">wikisql 管理面板 · <a href="/docs">API 文档</a></div>
</div>

<div id="toast" class="toast"></div>
</div>

<script>
"use strict";
// ── 工具 ──
function $(id) { return document.getElementById(id); }
function toast(msg, isErr) {
  var t = $("toast");
  t.textContent = msg;
  t.className = "toast show" + (isErr ? " err" : " ok");
  clearTimeout(t._timer);
  t._timer = setTimeout(function () { t.className = "toast"; }, 3500);
}
async function api(path, opts) {
  var resp = await fetch("/admin/api" + path, Object.assign({
    headers: { "Content-Type": "application/json" },
  }, opts || {}));
  var data = await resp.json().catch(function () { return {}; });
  if (!resp.ok) {
    if (resp.status === 401) { showLogin(); throw new Error(data.error || "未登录"); }
    throw new Error(data.error || ("请求失败 " + resp.status));
  }
  return data;
}
function fmtBytes(n) {
  if (n === undefined || n === null || n === 0) return "0 B";
  var units = ["B", "KB", "MB", "GB", "TB"], i = 0;
  while (n >= 1024 && i < units.length - 1) { n /= 1024; i++; }
  return (Math.round(n * 10) / 10) + " " + units[i];
}
function fmtDuration(sec) {
  var d = Math.floor(sec / 86400), h = Math.floor(sec % 86400 / 3600), m = Math.floor(sec % 3600 / 60);
  if (d > 0) return d + " 天 " + h + " 小时";
  if (h > 0) return h + " 小时 " + m + " 分钟";
  return m + " 分钟";
}

// ── 视图切换 ──
function showLogin() { $("view-login").classList.remove("hidden"); $("view-main").classList.add("hidden"); }
function showMain() { $("view-login").classList.add("hidden"); $("view-main").classList.remove("hidden"); }
function switchTab(name) {
  ["status", "config", "security", "about"].forEach(function (t) {
    $("tab-" + t).classList.toggle("hidden", t !== name);
  });
  document.querySelectorAll(".tab[data-tab]").forEach(function (el) {
    el.classList.toggle("active", el.dataset.tab === name);
  });
  if (name === "status") loadStatus();
}

// ── 登录 ──
async function doLogin() {
  var pw = $("login-pass").value;
  if (!pw) { toast("请输入密码", true); return; }
  try {
    await api("/login", { method: "POST", body: JSON.stringify({ password: pw }) });
    $("login-pass").value = "";
    showMain();
    loadStatus();
    loadConfig();
  } catch (e) { toast(e.message, true); }
}
$("login-pass").addEventListener("keydown", function (e) { if (e.key === "Enter") doLogin(); });

async function doLogout() {
  try { await api("/logout", { method: "POST" }); } catch (e) {}
  showLogin();
}

// ── 状态页 ──
async function loadStatus() {
  try {
    var st = await api("/status");
    renderBasic(st);
    renderDB(st);
    renderHealth(st.health_recent);
  } catch (e) { if (e.message !== "未登录") toast(e.message, true); }
}
function row(k, v, cls) {
  var d = document.createElement("div"); d.className = "row";
  var kk = document.createElement("span"); kk.className = "k"; kk.textContent = k;
  var vv = document.createElement("span"); vv.className = "v" + (cls ? " " + cls : ""); vv.textContent = v;
  d.appendChild(kk); d.appendChild(vv);
  return d;
}
function renderBasic(st) {
  var el = $("status-basic"); el.textContent = "";
  var mod = st.modules || {};
  function badge(ok, txtOk, txtNo, warn) {
    var s = document.createElement("span");
    if (ok) { s.className = "badge ok"; s.textContent = txtOk; }
    else if (warn) { s.className = "badge warn"; s.textContent = txtNo; }
    else { s.className = "badge err"; s.textContent = txtNo; }
    return s;
  }
  var r1 = document.createElement("div"); r1.className = "row";
  var k1 = document.createElement("span"); k1.className = "k"; k1.textContent = "版本 / 运行时长";
  var v1 = document.createElement("span"); v1.className = "v";
  v1.textContent = "v" + st.version + " · " + fmtDuration(st.uptime_seconds);
  r1.appendChild(k1); r1.appendChild(v1); el.appendChild(r1);

  var r2 = document.createElement("div"); r2.className = "row";
  var k2 = document.createElement("span"); k2.className = "k"; k2.textContent = "鉴权模式";
  var v2 = document.createElement("span"); v2.className = "v"; v2.textContent = mod.auth_mode || "-";
  r2.appendChild(k2); r2.appendChild(v2); el.appendChild(r2);

  var r3 = document.createElement("div"); r3.className = "row";
  var k3 = document.createElement("span"); k3.className = "k"; k3.textContent = "模块状态";
  var v3 = document.createElement("span"); v3.className = "v";
  v3.appendChild(badge(mod.kb_enabled, "知识库", "知识库未启用", false)); v3.appendChild(document.createTextNode(" "));
  v3.appendChild(badge(mod.mcp_enabled, "MCP", "MCP 关闭", false)); v3.appendChild(document.createTextNode(" "));
  if (mod.mcp_writes) v3.appendChild(badge(true, "MCP可写", "", false));
  r3.appendChild(k3); r3.appendChild(v3); el.appendChild(r3);

  el.appendChild(row("Go 运行时", st.go_version + " · " + st.goroutines + " 协程"));
  el.appendChild(row("内存占用（RSS）", fmtBytes(st.rss_bytes)));
  el.appendChild(row("服务器时间", st.time));
}
function renderDB(st) {
  var el = $("status-db"); el.textContent = "";
  var db = st.db || {}, bk = st.backup || {}, dk = st.disk || {};
  el.appendChild(row("数据库大小", fmtBytes(db.size_bytes) + "（WAL " + fmtBytes(db.wal_bytes) + "）"));
  el.appendChild(row("数据表 / 集合", (db.tables || 0) + " 张表 · " + (db.collections || 0) + " 个集合"));
  el.appendChild(row("文档 / 分块", (db.documents || 0) + " 篇 · " + (db.chunks || 0) + " 块"));
  el.appendChild(row("嵌入缓存", (db.embed_cache || 0) + " 条"));
  el.appendChild(row("最近备份", (bk.last_time || "-") + (bk.last_ago ? "（" + bk.last_ago + " 前）" : "")));
  el.appendChild(row("备份份数", (bk.count || 0) + " 份（保留最近 7 份）"));
  var diskTxt = fmtBytes(dk.free_bytes) + " 可用 / " + fmtBytes(dk.total_bytes) + "（已用 " + (dk.used_percent || 0) + "%）";
  el.appendChild(row("磁盘（" + (dk.path || "-") + "）", diskTxt));
}
function renderHealth(list) {
  var el = $("status-health"); el.textContent = "";
  if (!list || !list.length) { el.appendChild(row("记录", "暂无")); return; }
  list.forEach(function (h) {
    var cls = h.severity === "critical" ? "err" : (h.severity === "warning" ? "warn" : "ok");
    el.appendChild(row(h.check + " · " + h.time, h.message || h.status, cls));
  });
}

// ── 配置页 ──
var providerPresets = {
  siliconflow: "https://api.siliconflow.cn/v1",
  openai: "https://api.openai.com/v1",
  deepseek: "https://api.deepseek.com/v1",
};
async function loadConfig() {
  try {
    var data = await api("/config");
    renderConfig(data.sections || []);
  } catch (e) { if (e.message !== "未登录") toast(e.message, true); }
}
function renderConfig(sections) {
  var root = $("config-sections"); root.textContent = "";
  sections.forEach(function (sec) {
    var card = document.createElement("div"); card.className = "card";
    var h = document.createElement("h2"); h.textContent = sec.name; card.appendChild(h);
    var grid = document.createElement("div"); grid.className = "grid2";
    sec.fields.forEach(function (f) {
      var block = document.createElement("div");
      var label = document.createElement("label"); label.textContent = f.label; block.appendChild(label);
      var input = makeInput(f);
      block.appendChild(input);
      if (f.help) { var hp = document.createElement("div"); hp.className = "help"; hp.textContent = f.help; block.appendChild(hp); }
      grid.appendChild(block);
    });
    card.appendChild(grid);
    root.appendChild(card);
  });
}
function makeInput(f) {
  var el;
  if (f.type === "bool") {
    var wrap = document.createElement("div"); wrap.className = "switch";
    el = document.createElement("input"); el.type = "checkbox"; el.checked = !!f.value; el.dataset.key = f.key;
    var span = document.createElement("span"); span.textContent = f.value ? "已启用" : "已关闭";
    el.addEventListener("change", function () { span.textContent = el.checked ? "已启用" : "已关闭"; });
    wrap.appendChild(el); wrap.appendChild(span);
    return wrap;
  }
  if (f.type === "select") {
    el = document.createElement("select");
    (f.options || []).forEach(function (o) {
      var op = document.createElement("option"); op.value = o; op.textContent = o;
      if (o === f.value) op.selected = true;
      el.appendChild(op);
    });
  } else if (f.type === "secret") {
    var srow = document.createElement("div"); srow.className = "secret-row";
    el = document.createElement("input"); el.type = "password";
    el.placeholder = f.has_value ? "已设置（留空保持不变）" : "未设置";
    el.autocomplete = "new-password";
    var genBtn = document.createElement("button"); genBtn.type = "button"; genBtn.className = "btn ghost small";
    genBtn.textContent = "生成";
    genBtn.addEventListener("click", function () {
      var arr = new Uint8Array(24); crypto.getRandomValues(arr);
      el.value = Array.from(arr, function (b) { return b.toString(16).padStart(2, "0"); }).join("");
      el.type = "text";
      toast("已生成随机值，保存后生效");
    });
    srow.appendChild(el); srow.appendChild(genBtn);
    el.dataset.key = f.key;
    return srow;
  } else if (f.type === "model") {
    el = document.createElement("input"); el.type = "text"; el.setAttribute("list", "model-list-" + f.key.replace(/\./g, "-"));
    el.value = f.value || "";
    var dl = document.createElement("datalist"); dl.id = "model-list-" + f.key.replace(/\./g, "-");
    (f.options || []).forEach(function (o) {
      var op = document.createElement("option"); op.value = o; dl.appendChild(op);
    });
    el.dataset.key = f.key;
    var holder = document.createElement("div"); holder.appendChild(el); holder.appendChild(dl);
    return holder;
  } else {
    el = document.createElement("input");
    el.type = f.type === "int" ? "number" : "text";
    el.value = f.value !== undefined && f.value !== null ? f.value : "";
  }
  el.dataset.key = f.key;
  return el;
}
function collectConfig() {
  var patch = {};
  document.querySelectorAll("#config-sections [data-key]").forEach(function (el) {
    var key = el.dataset.key;
    if (el.type === "checkbox") { patch[key] = el.checked; }
    else if (el.type === "number") { patch[key] = el.value === "" ? 0 : Number(el.value); }
    else { patch[key] = el.value; }
  });
  return patch;
}
async function saveConfig() {
  try {
    var data = await api("/config", { method: "POST", body: JSON.stringify(collectConfig()) });
    toast(data.message || "已保存");
    loadConfig();
  } catch (e) { toast(e.message, true); }
}

// ── 安全页 ──
async function changePassword() {
  var cur = $("pw-current").value, np = $("pw-new").value, np2 = $("pw-new2").value;
  if (!cur || !np) { toast("请填写完整", true); return; }
  if (np !== np2) { toast("两次输入的新密码不一致", true); return; }
  if (np.length < 8) { toast("新密码至少 8 位", true); return; }
  try {
    var data = await api("/password", { method: "POST", body: JSON.stringify({ current: cur, new: np }) });
    toast(data.message || "密码已更新");
    setTimeout(showLogin, 1200);
  } catch (e) { toast(e.message, true); }
}

// ── 重启 ──
async function doRestart() {
  if (!confirm("确定重启服务？约 5-10 秒后恢复，当前会话将失效。")) return;
  try {
    await api("/restart", { method: "POST" });
    toast("服务重启中…");
    pollHealth();
  } catch (e) { toast(e.message, true); }
}
function pollHealth() {
  var bars = [$("restart-progress"), $("restart-progress2")];
  bars.forEach(function (b) { if (b) { b.classList.remove("hidden"); b.firstElementChild.style.width = "5%"; } });
  var tries = 0;
  var timer = setInterval(async function () {
    tries++;
    var pct = Math.min(95, tries * 6);
    bars.forEach(function (b) { if (b) { b.firstElementChild.style.width = pct + "%"; } });
    try {
      var resp = await fetch("/health", { cache: "no-store" });
      if (resp.ok) {
        clearInterval(timer);
        bars.forEach(function (b) { if (b) b.firstElementChild.style.width = "100%"; });
        toast("服务已恢复，请重新登录");
        setTimeout(showLogin, 1500);
      }
    } catch (e) { /* 重启中，继续轮询 */ }
    if (tries > 40) { clearInterval(timer); toast("服务未在预期时间内恢复，请检查路由器", true); }
  }, 2000);
}

// ── 启动 ──
(async function init() {
  try {
    await api("/session");
    showMain();
    loadStatus();
    loadConfig();
  } catch (e) { showLogin(); }
})();
</script>
</body>
</html>`
