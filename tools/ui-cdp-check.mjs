#!/usr/bin/env node
/**
 * WSL Manager — dev host UI 检查（零依赖，Node.js 22+）
 *
 * 启动系统 Chrome/Edge headless，直连 Chrome DevTools Protocol：
 *   1. 打开 dbx-plugin dev host（默认 http://127.0.0.1:5193/）
 *   2. 点击「工作台」按钮，等待插件 iframe（sandbox srcdoc / OOPIF）加载
 *   3. 依次切换 9 个页面，等待渲染稳定后截图到 tests/screenshots/
 *   4. 校验：无白屏、无 uncaught exception、无 console error、侧边栏 9 项、
 *      各页面关键结构（表格/空态/表单等）
 *
 * 用法:
 *   node tools/ui-cdp-check.mjs
 *   node tools/ui-cdp-check.mjs --dev-url=http://127.0.0.1:5193/ --port=9333
 *   node tools/ui-cdp-check.mjs --out=tests/screenshots --json --report=tests/tmp/ui-report.json
 *   node tools/ui-cdp-check.mjs --keep-open          # 结束后保留 Chrome 便于人工检查
 *   node tools/ui-cdp-check.mjs --pages=dashboard,containers   # 只切部分页面（调试）
 *
 * 退出码: 0 = 全部 PASS（warning 不影响）；1 = 存在 FAIL / 启动失败。
 */

import { spawn, spawnSync } from 'node:child_process';
import { existsSync, mkdirSync, readFileSync, readdirSync, rmSync, statSync, writeFileSync } from 'node:fs';
import net from 'node:net';
import os from 'node:os';
import path from 'node:path';
import process from 'node:process';
import { setTimeout as sleep } from 'node:timers/promises';
import { fileURLToPath } from 'node:url';

const HERE = path.dirname(fileURLToPath(import.meta.url));
const PLUGIN_ROOT = path.resolve(HERE, '..');
const TMP_DIR = path.join(PLUGIN_ROOT, 'tests', 'tmp');
const DEFAULT_DEV_URL = 'http://127.0.0.1:5193/';
const DEFAULT_OUT = path.join(PLUGIN_ROOT, 'tests', 'screenshots');
const VIEWPORT = { width: 1440, height: 900 };

// Chrome 的临时 profile 放在 workspace 外，避免污染仓库和清理残留。
const CHROME_PROFILE_ROOT = path.join(
  process.env.DBX_WSL_QA_HOME
    ? path.resolve(process.env.DBX_WSL_QA_HOME)
    : path.join(process.env.LOCALAPPDATA || os.tmpdir(), 'dbx-wsl-dev'),
  'chrome-qa',
);

const CHROME_CANDIDATES = [
  'D:\\tools\\Scoop\\shims\\chrome.exe',
  'C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe',
  'C:\\Program Files (x86)\\Google\\Chrome\\Application\\chrome.exe',
  'C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe',
  'C:\\Program Files\\Microsoft\\Edge\\Application\\msedge.exe',
];

const PAGES = [
  { id: 'dashboard', labels: ['概览', '仪表盘', 'Dashboard', 'Overview'] },
  { id: 'distros', labels: ['发行版', 'Distros', 'Distributions'] },
  { id: 'containers', labels: ['容器', 'Containers'] },
  { id: 'images', labels: ['镜像', 'Images'] },
  { id: 'volumes', labels: ['数据卷', '卷', 'Volumes'] },
  { id: 'networks', labels: ['网络', 'Networks'] },
  { id: 'console', labels: ['控制台', 'Console', '命令控制台', '终端'] },
  { id: 'events', labels: ['事件', 'Events'] },
  { id: 'settings', labels: ['设置', 'Settings'] },
];

// 终版 + v0.1.1 新增的 11 张交互/主题/响应式/注册表截图（+ 9 页基础截图 = 20 张）
const FINAL_SCREENSHOTS = {
  drawer: 'distros-drawer.png',
  exec: 'distros-exec-result.png',
  dialogRun: 'dialog-run-container.png',
  dialogPull: 'dialog-pull-image.png',
  registryCredentials: 'registry-credentials.png',
  registryValidation: 'registry-validation.png',
  registryAuthError: 'registry-auth-error.png',
  dark: 'dashboard-dark.png',
  mobileDashboard: 'mobile-dashboard.png',
  mobileContainers: 'mobile-containers.png',
  mobileSidebar: 'mobile-sidebar.png',
};

// 契约 §9.1 前端调用超时建议 → 静态核对 UI 是否按方法传了正确的 timeoutMs
const RPC_TIMEOUT_EXPECTATIONS = [
  ['system/info', 60000],
  ['wsl/distros/list', 60000],
  ['wsl/distros/info', 90000],
  ['wsl/distros/exec', 90000],
  ['wsl/distros/wslconf', 90000],
  ['wslc/sessions/list', 60000],
  ['wslc/containers/list', 60000],
  ['wslc/images/list', 60000],
  ['wslc/networks/list', 60000],
];

// ---------------------------------------------------------------------------
// CLI / 输出
// ---------------------------------------------------------------------------

function parseArgs(argv) {
  const opts = {
    port: null,
    devUrl: DEFAULT_DEV_URL,
    out: DEFAULT_OUT,
    keepOpen: false,
    chrome: null,
    json: false,
    report: null,
    pages: null,
    timeout: 30_000,
    verbose: false,
    help: false,
  };
  for (let i = 0; i < argv.length; i++) {
    const arg = argv[i];
    const eq = arg.indexOf('=');
    const key = eq >= 0 ? arg.slice(0, eq) : arg;
    const inline = eq >= 0 ? arg.slice(eq + 1) : undefined;
    const next = () => (inline !== undefined ? inline : argv[++i]);
    switch (key) {
      case '--port': opts.port = Number(next()); break;
      case '--dev-url': opts.devUrl = String(next()); break;
      case '--out': opts.out = path.resolve(PLUGIN_ROOT, String(next())); break;
      case '--keep-open': opts.keepOpen = true; break;
      case '--chrome': opts.chrome = String(next()); break;
      case '--json': opts.json = true; break;
      case '--report': opts.report = path.resolve(PLUGIN_ROOT, String(next())); break;
      case '--pages': opts.pages = String(next()).split(',').map((item) => item.trim()).filter(Boolean); break;
      case '--timeout': opts.timeout = Number(next()); break;
      case '--verbose': opts.verbose = true; break;
      case '--help': case '-h': opts.help = true; break;
      default: fail(`未知参数: ${arg}（用 --help 查看用法）`);
    }
  }
  if (opts.port !== null && (!Number.isInteger(opts.port) || opts.port <= 0 || opts.port > 65535)) {
    fail(`--port 需要 1-65535 的整数，收到: ${opts.port}`);
  }
  if (!/^https?:\/\//.test(opts.devUrl)) fail(`--dev-url 需要 http(s) URL，收到: ${opts.devUrl}`);
  if (!opts.devUrl.endsWith('/')) opts.devUrl += '/';
  return opts;
}

function fail(message) {
  console.error(`ui-cdp-check: ${message}`);
  process.exit(1);
}

const options = parseArgs(process.argv.slice(2));
if (options.help) {
  console.log(`WSL Manager dev host UI 检查（零依赖，Node.js 22+）

用法: node tools/ui-cdp-check.mjs [选项]

选项:
  --port=<n>          Chrome DevTools 端口（默认随机空闲端口）
  --dev-url=<url>     dev host 地址（默认 ${DEFAULT_DEV_URL}）
  --out=<dir>         截图目录（默认 tests/screenshots）
  --report=<file>     额外写出 JSON 报告
  --pages=a,b,c       仅检查指定页面（调试用）
  --chrome=<path>     指定 Chrome/Edge 可执行文件
  --keep-open         结束后保留 Chrome 进程
  --json              stdout 仅输出 JSON
  --verbose           打印调试日志
  -h, --help          显示帮助
`);
  process.exit(0);
}

const colorEnabled = !options.json && !process.env.NO_COLOR && process.stdout.isTTY !== false;
const paint = (code) => (text) => (colorEnabled ? `\x1b[${code}m${text}\x1b[0m` : text);
const green = paint('32');
const red = paint('31');
const yellow = paint('33');
const dim = paint('2');
const bold = paint('1');

function log(...parts) {
  if (!options.json) console.log(...parts);
}
function debug(...parts) {
  if (options.verbose && !options.json) console.error(dim(`[debug] ${parts.join(' ')}`));
}

// ---------------------------------------------------------------------------
// 结果模型
// ---------------------------------------------------------------------------

const results = { checks: [], pages: [], consoleErrors: [], pageErrors: [], warnings: [] };

function addCheck(id, title, ok, detail, extra = {}) {
  const entry = { id, title, status: ok ? 'pass' : 'fail', detail, ...extra };
  results.checks.push(entry);
  if (!options.json) {
    const tag = ok ? green('PASS') : red('FAIL');
    console.log(`  ${tag} ${id}${detail ? dim(` — ${detail}`) : ''}`);
  }
  if (!ok) debug(`检查失败: ${id}: ${detail}`);
  return entry;
}

function addWarn(id, title, detail) {
  results.checks.push({ id, title, status: 'warn', detail });
  if (!options.json) console.log(`  ${yellow('WARN')} ${id}${detail ? dim(` — ${detail}`) : ''}`);
}

function recordConsoleEvent(kind, session, payload) {
  if (kind === 'error') results.consoleErrors.push({ session, ...payload, at: Date.now() });
  else if (kind === 'warning') results.warnings.push({ session, ...payload, at: Date.now() });
}

function isBenignConsoleError(entry) {
  const text = String(entry.text || '');
  const url = String(entry.url || '');
  if (url.includes('favicon.ico') || text.includes('favicon.ico')) return true;
  if (/^DevTools/.test(text)) return true;
  return false;
}

/** dev host bridge 在 backend RPC 失败时返回 HTTP 400，浏览器会记录 network error；
 *  这类错误来自宿主调试桥本身，不归入插件 UI console error，单独作为 warning 汇总。 */
function isDevHostBridgeError(entry) {
  const text = String(entry.text || '');
  const url = String(entry.url || '');
  if (!/^(log\/network|network)$/.test(String(entry.source || ''))) return false;
  if (!/\/api\/(bridge|workbenches|plugin)/.test(url)) return false;
  return /\b(400|500|502|503)\b/.test(text);
}

function describeConsoleEntry(entry) {
  const url = entry.url ? ` (${entry.url})` : '';
  return `${entry.source}: ${entry.text}${url}`;
}

// ---------------------------------------------------------------------------
// 通用工具
// ---------------------------------------------------------------------------

async function freePort() {
  return new Promise((resolve, reject) => {
    const server = net.createServer();
    server.unref();
    server.on('error', reject);
    server.listen(0, '127.0.0.1', () => {
      const { port } = server.address();
      server.close(() => resolve(port));
    });
  });
}

function detectChrome() {
  if (options.chrome) {
    if (!existsSync(options.chrome)) fail(`--chrome 指定的文件不存在: ${options.chrome}`);
    return options.chrome;
  }
  if (process.env.CHROME_PATH && existsSync(process.env.CHROME_PATH)) return process.env.CHROME_PATH;
  for (const candidate of CHROME_CANDIDATES) {
    if (existsSync(candidate)) return candidate;
  }
  fail(`未找到 Chrome/Edge，尝试过:\n  ${CHROME_CANDIDATES.join('\n  ')}`);
}

function readManifestLabels() {
  const labels = new Set(['WSL Manager', 'WSL 管理器']);
  try {
    const manifest = JSON.parse(readFileSync(path.join(PLUGIN_ROOT, 'manifest.json'), 'utf8'));
    for (const contribution of manifest.contributions || []) {
      if (contribution.type !== 'workbench') continue;
      if (contribution.label) labels.add(contribution.label);
      const localized = manifest.localizations?.['zh-CN']?.contributions?.[contribution.id]?.label;
      if (localized) labels.add(localized);
      const localizedEn = manifest.localizations?.en?.contributions?.[contribution.id]?.label;
      if (localizedEn) labels.add(localizedEn);
    }
  } catch (error) {
    addWarn('manifest-labels', '读取 manifest 工作台标签失败', String(error.message || error));
  }
  return [...labels].filter(Boolean);
}

function detectPageFilters() {
  if (!options.pages) return PAGES;
  const wanted = new Set(options.pages.map((item) => item.toLowerCase()));
  const selected = PAGES.filter((page) => wanted.has(page.id));
  if (selected.length === 0) fail(`--pages 没有匹配任何页面: ${options.pages.join(', ')}（可选: ${PAGES.map((p) => p.id).join(', ')}）`);
  return selected;
}

// ---------------------------------------------------------------------------
// Chrome / CDP
// ---------------------------------------------------------------------------

async function waitForDevTools(port, chromeState) {
  const deadline = Date.now() + 30_000;
  let lastError = '';
  while (Date.now() < deadline) {
    if (chromeState.exited) {
      throw new Error(`Chrome 提前退出（code=${chromeState.exitCode}）: ${chromeState.stderrTail.slice(-5).join(' | ')}`);
    }
    try {
      const response = await fetch(`http://127.0.0.1:${port}/json/version`);
      if (response.ok) return await response.json();
      lastError = `HTTP ${response.status}`;
    } catch (error) {
      lastError = String(error.message || error);
    }
    await sleep(200);
  }
  throw new Error(`无法连接 Chrome DevTools (${lastError})`);
}

async function launchChrome(port) {
  const exe = detectChrome();
  mkdirSync(CHROME_PROFILE_ROOT, { recursive: true });
  const profileDir = path.join(
    CHROME_PROFILE_ROOT,
    `run-${Date.now()}-${Math.floor(Math.random() * 1e4)}`,
  );
  mkdirSync(profileDir, { recursive: true });
  const child = spawn(
    exe,
    [
      '--headless=new',
      `--remote-debugging-port=${port}`,
      `--user-data-dir=${profileDir}`,
      '--no-first-run',
      '--no-default-browser-check',
      '--disable-gpu',
      '--hide-scrollbars',
      '--window-size=1440,900',
      '--disable-background-timer-throttling',
      '--disable-renderer-backgrounding',
      'about:blank',
    ],
    { stdio: ['ignore', 'pipe', 'pipe'], windowsHide: true },
  );
  const state = { child, exe, profileDir, stderrTail: [], exited: false, exitCode: null };
  child.stderr.setEncoding('utf8');
  child.stderr.on('data', (chunk) => {
    for (const line of String(chunk).split(/\r?\n/)) {
      if (!line) continue;
      state.stderrTail.push(line);
      if (state.stderrTail.length > 200) state.stderrTail.shift();
      debug(`chrome: ${line}`);
    }
  });
  child.on('exit', (code) => {
    state.exited = true;
    state.exitCode = code;
  });
  const version = await waitForDevTools(port, state);
  state.version = version;
  return state;
}

function killChrome(state) {
  if (!state || state.keepOpen || state.exited) return;
  try {
    if (process.platform === 'win32') {
      spawnSync('taskkill', ['/PID', String(state.child.pid), '/T', '/F'], { stdio: 'ignore', windowsHide: true });
    } else {
      state.child.kill('SIGKILL');
    }
  } catch {}
  // 给 Chrome 子进程退出/释放 profile 文件的时间，再重试清理
  const wait = (ms) => {
    try {
      Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, ms);
    } catch {
      const until = Date.now() + ms;
      while (Date.now() < until) {
        /* busy wait */
      }
    }
  };
  wait(700);
  for (let attempt = 0; attempt < 4; attempt++) {
    try {
      rmSync(state.profileDir, { recursive: true, force: true, maxRetries: 6, retryDelay: 200 });
      return;
    } catch {
      wait(400);
    }
  }
}

/** 清理本目录中残留的旧 Chrome profile（异常退出/文件占用导致），保留最近 10 分钟内的。 */
function cleanupStaleProfiles() {
  try {
    for (const entry of readdirSync(CHROME_PROFILE_ROOT, { withFileTypes: true })) {
      if (!entry.isDirectory()) continue;
      const full = path.join(CHROME_PROFILE_ROOT, entry.name);
      try {
        const ageMs = Date.now() - statSync(full).mtimeMs;
        if (ageMs > 10 * 60 * 1000) {
          rmSync(full, { recursive: true, force: true, maxRetries: 3, retryDelay: 200 });
        }
      } catch {}
    }
  } catch {}
}

class CdpClient {
  constructor(webSocketUrl) {
    this.webSocketUrl = webSocketUrl;
    this.ws = null;
    this.nextId = 0;
    this.pending = new Map();
    this.listeners = new Set();
    this.closed = false;
  }

  async connect() {
    this.ws = new WebSocket(this.webSocketUrl);
    await new Promise((resolve, reject) => {
      this.ws.onopen = resolve;
      this.ws.onerror = () => reject(new Error(`WebSocket 连接失败: ${this.webSocketUrl}`));
    });
    this.ws.onmessage = (event) => {
      let message;
      try {
        message = JSON.parse(String(event.data));
      } catch {
        return;
      }
      if (message.id !== undefined && this.pending.has(message.id)) {
        const { resolve, reject } = this.pending.get(message.id);
        this.pending.delete(message.id);
        if (message.error) reject(new CdpError(message.error, message.method));
        else resolve(message.result);
        return;
      }
      for (const listener of this.listeners) {
        try {
          listener(message);
        } catch (error) {
          debug(`CDP 监听器异常: ${error.message}`);
        }
      }
    };
    this.ws.onclose = () => {
      this.closed = true;
      for (const { reject } of this.pending.values()) reject(new Error('CDP 连接已关闭'));
      this.pending.clear();
    };
    return this;
  }

  onMessage(listener) {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }

  send(method, params = {}, sessionId) {
    const id = ++this.nextId;
    const payload = { id, method, params };
    if (sessionId) payload.sessionId = sessionId;
    return new Promise((resolve, reject) => {
      this.pending.set(id, { resolve, reject, method });
      try {
        this.ws.send(JSON.stringify(payload));
      } catch (error) {
        this.pending.delete(id);
        reject(error);
      }
    });
  }

  close() {
    try {
      this.ws?.close();
    } catch {}
  }
}

class CdpError extends Error {
  constructor(error, method) {
    super(`${error?.message || 'CDP error'}${method ? ` (${method})` : ''}`);
    this.code = error?.code;
    this.data = error?.data;
  }
}

async function evaluate(sessionId, expression) {
  const response = await cdp.send(
    'Runtime.evaluate',
    { expression, returnByValue: true, awaitPromise: true, userGesture: true },
    sessionId,
  );
  if (response.exceptionDetails) {
    const details = response.exceptionDetails;
    throw new Error(
      `页面内执行异常: ${details.exception?.description || details.text || JSON.stringify(details).slice(0, 300)}`,
    );
  }
  return response.result?.value;
}

async function pollUntil(fn, { timeout = 10_000, interval = 200, label = '条件' } = {}) {
  const deadline = Date.now() + timeout;
  let lastError = null;
  let attempts = 0;
  while (Date.now() < deadline) {
    try {
      const value = await fn();
      if (value) return value;
      if (attempts++ < 3) debug(`${label}: 尚未满足 (${JSON.stringify(value)})`);
    } catch (error) {
      lastError = error;
      if (attempts++ < 3) debug(`${label}: ${error.message}`);
    }
    await sleep(interval);
  }
  throw new Error(`等待${label}超时（${timeout}ms）${lastError ? `: ${lastError.message}` : ''}`);
}

// ---------------------------------------------------------------------------
// 页面操作
// ---------------------------------------------------------------------------

let cdp = null;
let chromeState = null;
let pageSession = null;
let frameSession = null;
let devUrl = '';

function normalizeLabels(labels) {
  return labels.map((label) => String(label).toLowerCase());
}

async function pageText() {
  return evaluate(pageSession, 'document.body ? document.body.innerText : ""');
}

async function frameText() {
  if (!frameSession) throw new Error('插件 iframe 尚未加载');
  return evaluate(frameSession, 'document.body ? document.body.innerText : ""');
}

async function frameHash() {
  return evaluate(frameSession, 'location.hash || ""');
}

async function waitFrameReady(timeout = 25_000) {
  return pollUntil(
    async () => {
      if (!frameSession) return false;
      try {
        const state = await evaluate(
          frameSession,
          '({ ready: document.readyState, textLength: (document.body ? document.body.innerText : "").trim().length })',
        );
        return state && state.ready === 'complete' && state.textLength > 0 ? state : false;
      } catch {
        return false;
      }
    },
    { timeout, interval: 250, label: '插件 iframe 加载完成' },
  );
}

async function waitTextStable(readText, { timeout = 10_000, minLength = 100 } = {}) {
  let previous = null;
  let stableCount = 0;
  const deadline = Date.now() + timeout;
  let lastValue = '';
  while (Date.now() < deadline) {
    try {
      lastValue = String(await readText());
    } catch {
      lastValue = '';
    }
    if (lastValue.length >= minLength) {
      if (previous !== null && lastValue === previous) {
        stableCount += 1;
        if (stableCount >= 2) {
          await sleep(250);
          return lastValue;
        }
      } else {
        stableCount = 0;
      }
      previous = lastValue;
    }
    await sleep(300);
  }
  return lastValue;
}

function clickWorkbenchExpression(labels) {
  return `(() => {
    const labels = ${JSON.stringify(normalizeLabels(labels))};
    const normalize = (el) => ((el.getAttribute('title') || '') + ' ' + (el.innerText || el.textContent || '')).trim().toLowerCase();
    const candidates = [...document.querySelectorAll('button,a,[role="button"],[role="menuitem"],[role="tab"]')];
    const matches = candidates.filter((el) => {
      const text = normalize(el);
      return labels.some((label) => text === label || (text.includes(label) && text.length <= label.length + 12));
    });
    // 优先标题完全匹配的按钮
    const exact = matches.find((el) => labels.includes((el.getAttribute('title') || '').trim().toLowerCase()));
    const target = exact || matches[0];
    if (!target) return { clicked: false, candidates: candidates.length };
    target.click();
    return {
      clicked: true,
      tag: target.tagName,
      text: (target.innerText || '').trim().slice(0, 80),
      title: (target.getAttribute('title') || '').slice(0, 80),
    };
  })()`;
}

async function openWorkbench(labels) {
  // dev host 调试壳由前端框架渲染，侧边栏/工作台按钮可能晚于 body 文本出现，先等按钮可见
  const buttonReady = await pollUntil(
    async () => {
      const state = await evaluate(
        pageSession,
        `(() => {
          const labels = ${JSON.stringify(normalizeLabels(labels))};
          const nodes = [...document.querySelectorAll('button,a,[role="button"],[role="menuitem"],[role="tab"]')];
          const normalize = (el) => ((el.getAttribute('title') || '') + ' ' + (el.innerText || el.textContent || '')).trim().toLowerCase();
          return nodes.some((el) => {
            const text = normalize(el);
            return labels.some((label) => text === label || (text.includes(label) && text.length <= label.length + 12));
          });
        })()`,
      );
      return state === true;
    },
    { timeout: 20_000, interval: 250, label: 'dev host 工作台按钮渲染' },
  ).catch(() => false);
  if (!buttonReady) {
    const text = await pageText().catch(() => '');
    throw new Error(`dev host 页面中找不到打开工作台的按钮（已尝试标题/文本匹配 manifest workbench label；当前页面文本: ${text.slice(0, 200).replace(/\s+/g, ' ')}）`);
  }
  const click = await evaluate(pageSession, clickWorkbenchExpression(labels));
  if (!click.clicked) {
    throw new Error('dev host 页面中找不到打开工作台的按钮（已尝试标题/文本匹配 manifest workbench label）');
  }
  debug(`已点击工作台按钮: ${JSON.stringify(click)}`);
  await pollUntil(() => frameSession !== null, { timeout: 20_000, interval: 150, label: '插件 iframe target 附加' });
  const ready = await waitFrameReady(25_000);
  return { click, ready };
}

async function evaluateNavState(page) {
  if (!frameSession) return { hash: '', active: false, navItems: [] };
  const labels = normalizeLabels(page.labels);
  return evaluate(
    frameSession,
    `(() => {
      const labels = ${JSON.stringify(labels)};
      const nodes = [...document.querySelectorAll('[aria-current="page"],[aria-selected="true"],[class*="active"],[class*="selected"],[class*="current"],[class*="active"]')];
      const active = nodes.some((el) => {
        const text = ((el.innerText || el.textContent || '') + ' ' + (el.getAttribute('aria-label') || '')).trim().toLowerCase();
        return labels.some((label) => text.includes(label));
      });
      return { hash: location.hash || '', active, navItems: nodes.length };
    })()`,
  );
}

function clickNavExpression(page) {
  return `(() => {
    const labels = ${JSON.stringify(normalizeLabels(page.labels))};
    const normalize = (el) => ((el.innerText || el.textContent || '') + ' ' + (el.getAttribute('aria-label') || '')).trim().toLowerCase();
    const candidates = [...document.querySelectorAll('a,button,[role="tab"],[role="button"],[role="menuitem"],li,nav *')];
    const matches = candidates.filter((el) => {
      const text = normalize(el);
      return labels.some((label) => (text === label || (text.includes(label) && text.length <= label.length + 14)));
    });
    // 过滤掉包含大量其他文字的容器，优先叶子节点（点击目标应为最小可点击元素）
    matches.sort((a, b) => normalize(a).length - normalize(b).length);
    const target = matches[0];
    if (!target) return { clicked: false, candidates: candidates.length };
    target.click();
    return { clicked: true, tag: target.tagName, text: normalize(target).slice(0, 80) };
  })()`;
}

async function trySwitchByHash(page) {
  if (!frameSession) return false;
  const before = await frameHash();
  const attempts = [`#/${page.id}`, `#${page.id}`, `#/${page.id}/`];
  for (const attempt of attempts) {
    if (before === attempt) continue;
    await evaluate(frameSession, `location.hash = ${JSON.stringify(attempt)}`);
    await sleep(600);
    const state = await evaluateNavState(page);
    if (state.hash === attempt || state.hash.toLowerCase().includes(page.id)) return { attempt, state };
  }
  return false;
}

async function switchPage(page) {
  const before = await evaluate(frameSession, '({ hash: location.hash || "", text: (document.body ? document.body.innerText : "").slice(0, 400) })');
  const click = await evaluate(frameSession, clickNavExpression(page));
  let mode = 'click';
  if (click.clicked) {
    const switched = await pollUntil(
      async () => {
        const state = await evaluateNavState(page);
        return state.hash.toLowerCase().includes(page.id) || state.active ? state : false;
      },
      { timeout: 4000, interval: 200, label: `切换到 ${page.id}` },
    ).catch(() => null);
    if (switched) {
      return { mode, click, state: switched };
    }
  }
  const hashSwitch = await trySwitchByHash(page);
  if (hashSwitch) {
    mode = 'hash';
    return { mode, click, state: hashSwitch.state, attempt: hashSwitch.attempt };
  }
  // 最后兜底：等待内容变化（无 hash/active 标记的 UI）
  const changed = await pollUntil(
    async () => {
      const after = await evaluate(frameSession, '({ hash: location.hash || "", text: (document.body ? document.body.innerText : "").slice(0, 400) })');
      if (after.hash !== before.hash || after.text !== before.text) return after;
      return false;
    },
    { timeout: 4000, interval: 250, label: `${page.id} 页面内容变化` },
  ).catch(() => null);
  if (changed) {
    results.warnings.push({ type: 'page-switch', message: `${page.id} 通过点击后仅检测到内容变化（无 hash/active 标记），可能 UI 未使用 aria-current/active 约定` });
    return { mode: 'content-changed', click, state: changed };
  }
  return { mode: 'unknown', click };
}

async function collectDomSnapshot() {
  return evaluate(
    frameSession,
    `(() => {
      const count = (selector) => document.querySelectorAll(selector).length;
      const text = (document.body ? document.body.innerText : '').trim();
      const emptyState = /(暂无|没有|为空|尚未|未发现|无数据|No data|No .{0,20}(yet|found)|Nothing here|empty)/i.test(text);
      const tables = count('table,[role="table"],[class*="table"]');
      const rows = count('tr,[role="row"]');
      const lists = count('ul,ol,[role="list"]');
      const cards = count('[class*="card"],[class*="Card"],[class*="tile"],[class*="stat"],[class*="Stat"]');
      const forms = count('input,select,textarea');
      const buttons = count('button,[role="button"]');
      const textarea = count('textarea');
      return {
        textLength: text.length,
        textStart: text.slice(0, 240),
        tables,
        rows,
        lists,
        cards,
        forms,
        buttons,
        textarea,
        emptyState,
        bodyHeight: document.body ? document.body.scrollHeight : 0,
        bodyVisible: !!document.body && document.body.getBoundingClientRect().height > 0,
      };
    })()`,
  );
}

function pageStructureCheck(page, snapshot) {
  if (!snapshot) return { ok: false, detail: '无 DOM 快照', renderOk: false };
  const renderOk = snapshot.textLength > 100 && snapshot.bodyVisible;
  if (page.id === 'dashboard') {
    const ok = snapshot.cards >= 2 || snapshot.textLength >= 300;
    return { ok, renderOk, detail: `dashboard: cards=${snapshot.cards}, text=${snapshot.textLength}` };
  }
  if (page.id === 'console') {
    const ok = snapshot.textarea >= 1 || snapshot.forms >= 2;
    return { ok, renderOk, detail: `console: textarea=${snapshot.textarea}, inputs=${snapshot.forms}, buttons=${snapshot.buttons}` };
  }
  if (page.id === 'settings') {
    const ok = snapshot.forms >= 3;
    return { ok, renderOk, detail: `settings: inputs=${snapshot.forms}, buttons=${snapshot.buttons}` };
  }
  // 列表类页面: 表格/行/卡片/空态 任一存在
  const ok = snapshot.tables >= 1 || snapshot.rows >= 1 || snapshot.cards >= 2 || snapshot.emptyState;
  return {
    ok,
    renderOk,
    detail: `${page.id}: tables=${snapshot.tables}, rows=${snapshot.rows}, cards=${snapshot.cards}, emptyState=${snapshot.emptyState}, text=${snapshot.textLength}`,
  };
}

async function checkSidebar() {
  const labels = PAGES.flatMap((page) => page.labels);
  const matched = await evaluate(
    frameSession,
    `(() => {
      const labels = ${JSON.stringify(normalizeLabels(labels))};
      const nodes = [...document.querySelectorAll('a,button,[role="tab"],[role="button"],[role="menuitem"],li')];
      const normalize = (el) => ((el.innerText || el.textContent || '') + ' ' + (el.getAttribute('aria-label') || '')).trim().toLowerCase();
      const texts = [];
      for (const node of nodes) {
        const text = normalize(node);
        if (!text || text.length > 60) continue;
        texts.push(text);
      }
      const pageIds = ${JSON.stringify(PAGES.map((page) => page.id))};
      const matchedPages = [];
      for (const page of ${JSON.stringify(PAGES.map((page) => page.labels))}) {
        const pageLabels = page.map((label) => label.toLowerCase());
        if (texts.some((text) => pageLabels.some((label) => text === label || (text.includes(label) && text.length <= label.length + 10)))) {
          matchedPages.push(page[0]);
        }
      }
      return { matchedPages, sample: texts.slice(0, 40) };
    })()`,
  );
  const count = matched.matchedPages.length;
  addCheck(
    'sidebar-9-items',
    '侧边栏 9 个页面入口',
    count === PAGES.length,
    `匹配 ${count}/${PAGES.length}: ${matched.matchedPages.join(', ')}`,
    { evidence: matched },
  );
  return matched;
}

async function captureScreenshot(filePath) {
  const response = await cdp.send(
    'Page.captureScreenshot',
    { format: 'png', captureBeyondViewport: false, fromSurface: true },
    pageSession,
  );
  const buffer = Buffer.from(response.data, 'base64');
  writeFileSync(filePath, buffer);
  return buffer;
}

async function analyzePage(page) {
  const pageErrorsBefore = results.pageErrors.length;
  const consoleErrorsBefore = results.consoleErrors.length;

  const switchResult = await switchPage(page);
  const text = await waitTextStable(() => frameText(), { timeout: 12_000, minLength: 0 });
  const snapshot = await collectDomSnapshot();
  const structure = pageStructureCheck(page, snapshot);

  const screenshotPath = path.join(options.out, `${page.id}.png`);
  let screenshotBytes = 0;
  try {
    const screenshot = await captureScreenshot(screenshotPath);
    screenshotBytes = screenshot.length;
  } catch (error) {
    addCheck(`${page.id}/screenshot`, `${page.id} 截图`, false, String(error.message || error));
    throw error;
  }
  const screenshotOk = screenshotBytes > 5000;
  if (!screenshotOk) {
    results.warnings.push({ type: 'screenshot-size', message: `${page.id}.png 仅 ${screenshotBytes} 字节，可能为空白页` });
  }
  addCheck(
    `${page.id}/screenshot`,
    `${page.id} 截图`,
    screenshotOk,
    `${path.relative(PLUGIN_ROOT, screenshotPath)} (${screenshotBytes} bytes)`,
    { screenshot: path.relative(PLUGIN_ROOT, screenshotPath) },
  );

  const pageErrors = results.pageErrors.slice(pageErrorsBefore);
  const allConsoleErrors = results.consoleErrors.slice(consoleErrorsBefore).filter((entry) => !isBenignConsoleError(entry));
  const bridgeErrors = allConsoleErrors.filter((entry) => isDevHostBridgeError(entry));
  const consoleErrors = allConsoleErrors.filter((entry) => !isDevHostBridgeError(entry));
  if (bridgeErrors.length) {
    results.warnings.push({
      type: 'devhost-bridge-network',
      message: `${page.id}: dev host bridge 网络错误 ${bridgeErrors.length} 条（backend RPC 失败导致，已从 console error 中排除）`,
    });
  }

  addCheck(
    `${page.id}/render`,
    `${page.id} 页面渲染（无白屏）`,
    structure.renderOk,
    `可见文本 ${snapshot?.textLength ?? 0} 字符, bodyVisible=${snapshot?.bodyVisible}, switch=${switchResult.mode}`,
    { snapshot, switchMode: switchResult.mode, hash: switchResult.state?.hash },
  );
  addCheck(
    `${page.id}/no-exception`,
    `${page.id} 无 uncaught exception`,
    pageErrors.length === 0,
    pageErrors.length === 0 ? '0 条' : pageErrors.map((entry) => entry.text).join(' | ').slice(0, 500),
  );
  addCheck(
    `${page.id}/no-console-error`,
    `${page.id} 无 console error`,
    consoleErrors.length === 0,
    consoleErrors.length === 0 ? '0 条' : consoleErrors.map(describeConsoleEntry).join(' | ').slice(0, 500),
  );
  addCheck(
    `${page.id}/structure`,
    `${page.id} 关键 DOM 结构`,
    structure.ok,
    structure.detail,
  );

  results.pages.push({
    id: page.id,
    ok: structure.ok && pageErrors.length === 0 && consoleErrors.length === 0 && screenshotOk,
    textLength: text.length,
    hash: switchResult.state?.hash || (await frameHash().catch(() => '')),
    switchMode: switchResult.mode,
    snapshot,
    screenshot: path.relative(PLUGIN_ROOT, screenshotPath),
    screenshotBytes,
    pageErrors,
    consoleErrors,
  });
}

// ---------------------------------------------------------------------------
// 终版专项检查（hero/KPI、发行版操作、磁盘列、wslconf/exec 接线、对话框、暗色、移动端）
// ---------------------------------------------------------------------------

async function captureNamedShot(fileName) {
  const filePath = path.join(options.out, fileName);
  const response = await cdp.send(
    'Page.captureScreenshot',
    { format: 'png', captureBeyondViewport: false, fromSurface: true },
    pageSession,
  );
  const buffer = Buffer.from(response.data, 'base64');
  writeFileSync(filePath, buffer);
  return { file: path.relative(PLUGIN_ROOT, filePath), bytes: buffer.length };
}

function frameEval(expression) {
  return evaluate(frameSession, expression);
}

function pageEval(expression) {
  return evaluate(pageSession, expression);
}

async function waitFrame(expression, { timeout = 15_000, interval = 300, label = 'iframe 条件' } = {}) {
  return pollUntil(async () => {
    try {
      const value = await frameEval(expression);
      return value === undefined ? false : value;
    } catch {
      return false;
    }
  }, { timeout, interval, label });
}

function rgbLuminance(color) {
  const match = String(color || '').match(/\d+(?:\.\d+)?/g);
  if (!match || match.length < 3) return 255;
  return (Number(match[0]) + Number(match[1]) + Number(match[2])) / 3;
}

async function gotoHashPage(pageId) {
  await frameEval(`location.hash = ${JSON.stringify(`#/${pageId}`)}`);
  await sleep(900);
  await waitTextStable(() => frameText(), { timeout: 10_000, minLength: 0 });
}

async function runFinalChecks() {
  const evidence = { shots: [], checks: {} };
  const shot = async (key) => {
    const result = await captureNamedShot(FINAL_SCREENSHOTS[key]);
    evidence.shots.push(result);
    return result;
  };
  const section = async (id, title, fn) => {
    try {
      const detail = await fn();
      addCheck(`final/${id}`, title, true, typeof detail === 'string' ? detail : JSON.stringify(detail));
      return true;
    } catch (error) {
      addCheck(`final/${id}`, title, false, String(error.message || error));
      return false;
    }
  };

  // 回到概览页
  await gotoHashPage('dashboard');

  // 1) 概览 hero + KPI 网格
  await section('dashboard-hero-kpi', '概览 hero 卡片 + KPI 网格', async () => {
    const hero = await frameEval(`(() => {
      const heroCard = document.querySelector('.hero-card');
      const stats = heroCard ? [...heroCard.querySelectorAll('.hero-stat')] : [];
      const kpis = [...document.querySelectorAll('.kpi-grid .kpi')];
      const label = (el, cls) => { const node = el && el.querySelector('.' + cls); return node ? (node.innerText || '').trim().slice(0, 40) : ''; };
      return {
        hero: !!heroCard,
        heroEyebrow: label(heroCard, 'hero-eyebrow'),
        heroValue: (heroCard ? (heroCard.querySelector('.hero-value')?.innerText || '') : '').trim().slice(0, 120),
        heroStats: stats.length,
        heroStatLabels: stats.map((stat) => label(stat, 'hs-label')),
        heroCards: document.querySelectorAll('.hero-card').length,
        kpis: kpis.length,
        kpiLabels: kpis.map((kpi) => label(kpi, 'kpi-label')),
        kpiValues: kpis.map((kpi) => label(kpi, 'kpi-value')),
      };
    })()`);
    evidence.checks.hero = hero;
    if (!hero.hero) throw new Error('未找到 .hero-card');
    if (hero.heroStats < 3) throw new Error(`hero 统计项期望 >=3，实际 ${hero.heroStats}`);
    if (hero.kpis !== 6) throw new Error(`KPI 卡片期望 6，实际 ${hero.kpis}（${hero.kpiLabels.join(',')}）`);
    return `heroValue=${hero.heroValue.replace(/\s+/g, ' ')}; heroStats=${hero.heroStats} [${hero.heroStatLabels.join(', ')}]; kpis=${hero.kpis} [${hero.kpiLabels.join(', ')}]`;
  });

  // 2) 概览 wslc 卡片刷新 → 触发 wslc/sessions/list（诊断日志核对见后续检查）
  await section('dashboard-sessions-refresh', '概览 wslc 卡片刷新触发 sessions 接线', async () => {
    const clicked = await frameEval(`(() => {
      const button = document.querySelector('[data-act="dashboard.wslcInfo"]');
      if (!button) return false;
      button.click();
      return true;
    })()`);
    if (!clicked) throw new Error('未找到 [data-act="dashboard.wslcInfo"] 刷新按钮');
    await sleep(2500);
    const text = await frameText();
    if (!/会话|session/i.test(text)) throw new Error('刷新后页面未出现会话信息');
    return '已点击 wslc 卡片刷新并等待 2.5s';
  });

  // 3) 发行版页：6 个固定操作按钮 + 磁盘列单行
  await gotoHashPage('distros');
  const distrosInfo = await (async () => {
    try {
      return await frameEval(`(() => {
        const rows = [...document.querySelectorAll('tbody tr')];
        const row = rows.find((item) => /Debian/.test(item.innerText || '')) || rows[0];
        if (!row) return { found: false };
        const actions = row.querySelector('.row-actions');
        const buttons = actions ? [...actions.querySelectorAll('button')] : [];
        const headers = [...document.querySelectorAll('thead th')].map((th) => (th.innerText || '').trim());
        const cells = [...row.querySelectorAll('td')];
        const diskIndex = headers.findIndex((header) => /磁盘|Disk/i.test(header));
        const diskCell = diskIndex >= 0 ? cells[diskIndex] : null;
        const nowrap = diskCell ? diskCell.querySelector('.cell-main.nowrap') : row.querySelector('td .cell-main.nowrap');
        let lineRects = -1;
        let style = null;
        let diskText = '';
        if (nowrap) {
          style = getComputedStyle(nowrap);
          const range = document.createRange();
          range.selectNodeContents(nowrap);
          lineRects = range.getClientRects().length;
          diskText = (nowrap.innerText || '').trim();
        }
        return {
          found: true,
          name: (row.querySelector('.cell-main')?.innerText || '').trim(),
          headers,
          cells: cells.map((cell) => (cell.innerText || '').trim().replace(/\\s+/g, ' ').slice(0, 60)),
          actionCount: buttons.length,
          actions: buttons.map((button) => ({
            act: button.getAttribute('data-act'),
            title: button.getAttribute('title'),
            aria: button.getAttribute('aria-label'),
            visible: button.getBoundingClientRect().width > 0,
          })),
          disk: {
            headerIndex: diskIndex,
            text: diskText,
            whiteSpace: style ? style.whiteSpace : '',
            lineRects,
            height: nowrap ? Math.round(nowrap.getBoundingClientRect().height) : 0,
            lineHeight: style ? style.lineHeight : '',
          },
        };
      })()`);
    } catch (error) {
      return { found: false, error: String(error.message || error) };
    }
  })();
  evidence.checks.distros = distrosInfo;

  await section('distros-row-actions-6', '发行版固定 6 个操作按钮', async () => {
    if (!distrosInfo.found) throw new Error(`发行版表格行未找到: ${distrosInfo.error || 'empty'}`);
    const acts = distrosInfo.actions.map((item) => item.act);
    if (distrosInfo.actionCount !== 6) throw new Error(`操作按钮期望 6 个，实际 ${distrosInfo.actionCount}: ${acts.join(',')}`);
    const visible = distrosInfo.actions.filter((item) => item.visible).length;
    if (visible !== 6) throw new Error(`操作按钮可见数期望 6，实际 ${visible}: ${acts.join(',')}`);
    const expected = ['distros.setDefault', 'distros.terminal', 'distros.explorer', 'distros.detail', 'distros.manage'];
    for (const act of expected) {
      if (!acts.includes(act)) throw new Error(`操作按钮缺少 ${act}（实际: ${acts.join(',')}）`);
    }
    if (!acts.includes('distros.start') && !acts.includes('distros.stop')) {
      throw new Error(`操作按钮缺少 start/stop（实际: ${acts.join(',')}）`);
    }
    return `buttons=${acts.join(', ')}`;
  });

  await section('distros-disk-single-line', '发行版磁盘列单行显示', async () => {
    if (!distrosInfo.found) throw new Error('发行版表格行未找到');
    const disk = distrosInfo.disk || {};
    if (!disk.text) throw new Error('磁盘列未找到数值文本');
    if (disk.whiteSpace !== 'nowrap') throw new Error(`磁盘列 white-space 期望 nowrap，实际 ${disk.whiteSpace}`);
    if (disk.lineRects !== 1) throw new Error(`磁盘列文本应单行（Range rects=${disk.lineRects}）`);
    const numeric = parseFloat(String(disk.lineHeight));
    if (Number.isFinite(numeric) && numeric > 0 && disk.height > numeric * 1.6) {
      throw new Error(`磁盘列高度 ${disk.height}px 超过单行 line-height ${disk.lineHeight}`);
    }
    evidence.checks.disk = disk;
    return `${disk.text}; whiteSpace=${disk.whiteSpace}; lineRects=${disk.lineRects}; height=${disk.height}px`;
  });

  // 4) 打开发行版详情抽屉（触发 wsl/distros/info，90s 超时）
  const drawerState = await (async () => {
    try {
      const clicked = await frameEval(`(() => {
        const rows = [...document.querySelectorAll('tbody tr')];
        const row = rows.find((item) => /Debian/.test(item.innerText || '')) || rows[0];
        const button = row ? row.querySelector('[data-act="distros.detail"]') : null;
        if (!button) return false;
        button.click();
        return true;
      })()`);
      if (!clicked) return { clicked: false, error: '未找到 distros.detail 按钮' };
      const loaded = await pollUntil(
        async () => {
          const state = await frameEval(`(() => {
            const drawer = document.querySelector('#drawer');
            if (!drawer || drawer.hidden) return false;
            const text = drawer.innerText || '';
            const hasWslconf = !!drawer.querySelector('#distro-wslconf-wrap');
            if (text.trim().length < 80 || !hasWslconf) return false;
            return { textLen: text.trim().length, text: text.slice(0, 300), hasWslconf };
          })()`);
          return state || false;
        },
        { timeout: 60_000, interval: 500, label: '发行版详情抽屉（含 wsl.conf 区块）' },
      );
      return { clicked: true, loaded };
    } catch (error) {
      return { clicked: true, error: String(error.message || error) };
    }
  })();
  evidence.checks.drawer = drawerState;
  const drawerShot = await shot('drawer').catch(() => null);
  await section('distros-drawer', '发行版详情抽屉（wsl/distros/info 90s）', async () => {
    if (!drawerState.loaded) throw new Error(drawerState.error || '抽屉未在 60s 内加载完成');
    return `textLen=${drawerState.loaded.textLen}; screenshot=${drawerShot ? drawerShot.file : 'n/a'}`;
  });

  // 5) 抽屉内 wsl.conf 刷新（触发 wsl/distros/wslconf）
  const wslconfState = await (async () => {
    try {
      const clicked = await frameEval(`(() => {
        const button = document.querySelector('#drawer [data-act="distros.wslconf"]');
        if (!button) return false;
        button.click();
        return true;
      })()`);
      if (!clicked) return { clicked: false };
      const state = await pollUntil(
        async () => {
          const value = await frameEval(`(() => {
            const wrap = document.querySelector('#drawer #distro-wslconf-wrap');
            if (!wrap) return false;
            const pre = wrap.querySelector('#distro-wslconf');
            if (pre) return { hasContent: true, bytes: (pre.innerText || '').length };
            const text = (wrap.innerText || '').trim();
            if (!text || /加载中|loading/i.test(text)) return false;
            return { hasContent: false, emptyText: text.slice(0, 120) };
          })()`);
          return value || false;
        },
        { timeout: 20_000, interval: 300, label: 'wsl.conf 内容加载' },
      );
      return { clicked: true, state };
    } catch (error) {
      return { clicked: true, error: String(error.message || error) };
    }
  })();
  evidence.checks.wslconf = wslconfState;
  await section('distros-wslconf', '发行版 wsl.conf（wsl/distros/wslconf 90s）', async () => {
    if (!wslconfState.state) throw new Error(wslconfState.error || 'wsl.conf 区块未刷新出内容/空态');
    return JSON.stringify(wslconfState.state);
  });

  // 6) 抽屉内执行 echo hello（触发 wsl/distros/exec，90s）
  const execState = await (async () => {
    try {
      const clicked = await frameEval(`(() => {
        const input = document.querySelector('#drawer [data-field="distro-exec"]');
        const button = document.querySelector('#drawer [data-act="distros.exec.run"]');
        if (!input || !button) return false;
        input.value = 'echo hello';
        input.dispatchEvent(new Event('input', { bubbles: true }));
        button.click();
        return true;
      })()`);
      if (!clicked) return { clicked: false };
      const state = await pollUntil(
        async () => {
          const value = await frameEval(`(() => {
            const box = document.querySelector('#drawer #distro-exec-result');
            if (!box) return false;
            const text = (box.innerText || '').trim();
            if (text.length < 5 || /加载中|loading/i.test(text)) return false;
            return { text: text.slice(0, 500), hello: /hello/i.test(text) };
          })()`);
          return value || false;
        },
        { timeout: 60_000, interval: 400, label: '发行版 exec 执行结果' },
      );
      return { clicked: true, state };
    } catch (error) {
      return { clicked: true, error: String(error.message || error) };
    }
  })();
  evidence.checks.exec = execState;
  await frameEval(`(() => {
    const box = document.querySelector('#drawer #distro-exec-result');
    if (box) box.scrollIntoView({ block: 'center' });
    return true;
  })()`).catch(() => {});
  await sleep(500);
  const execShot = await shot('exec').catch(() => null);
  await section('distros-exec', '抽屉内执行 echo hello（wsl/distros/exec 90s）', async () => {
    if (!execState.state || !execState.state.hello) throw new Error(execState.error || `未得到 hello 输出: ${JSON.stringify(execState.state || null)}`);
    return execState.state.text.replace(/\s+/g, ' ').slice(0, 160);
  });
  await frameEval(`(() => { const button = document.querySelector('[data-act="ui.closeDrawer"]'); if (button) { button.click(); return true; } return false; })()`).catch(() => {});
  await sleep(500);

  // 7) 运行容器 / 拉取镜像对话框
  const openDialogAndShot = async (pageId, act, key, titlePattern) => {
    await gotoHashPage(pageId);
    const clicked = await frameEval(`(() => {
      const button = document.querySelector('[data-act="${act}"]');
      if (!button) return false;
      button.click();
      return true;
    })()`);
    if (!clicked) return { clicked: false };
    const state = await pollUntil(
      async () => {
        const value = await frameEval(`(() => {
          const root = document.querySelector('#dialog-root');
          if (!root || root.hidden) return false;
          const text = (root.innerText || '').trim();
          if (text.length < 5) return false;
          return { text: text.slice(0, 300), buttons: root.querySelectorAll('button').length };
        })()`);
        return value || false;
      },
      { timeout: 10_000, interval: 250, label: titlePattern },
    ).catch(() => null);
    if (state) await sleep(700); // 等对话框打开动画结束再截图
    const image = state ? await shot(key).catch(() => null) : null;
    if (state) {
      await frameEval(`(() => { const button = document.querySelector('#dialog-root [data-act="dialog.cancel"]'); if (button) { button.click(); return true; } return false; })()`).catch(() => {});
      await sleep(400);
    }
    return { clicked: true, state, image };
  };
  const runDialog = await openDialogAndShot('containers', 'containers.run', 'dialogRun', '运行容器对话框');
  evidence.checks.dialogRun = runDialog;
  await section('dialog-run-container', '运行容器对话框', async () => {
    if (!runDialog.state) throw new Error('运行容器对话框未打开/无内容');
    return `buttons=${runDialog.state.buttons}; screenshot=${runDialog.image ? runDialog.image.file : 'n/a'}`;
  });
  const pullDialog = await openDialogAndShot('images', 'images.pull', 'dialogPull', '拉取镜像对话框');
  evidence.checks.dialogPull = pullDialog;
  await section('dialog-pull-image', '拉取镜像对话框', async () => {
    if (!pullDialog.state) throw new Error('拉取镜像对话框未打开/无内容');
    return `buttons=${pullDialog.state.buttons}; screenshot=${pullDialog.image ? pullDialog.image.file : 'n/a'}`;
  });

  // 7.5) v0.1.1 注册表凭据（wslc/registry/login|logout）+ 拉取认证失败引导
  await gotoHashPage('images');
  const registrySecret = `qa-ui-secret-${Date.now().toString(36)}-Pw!`;
  results.registrySecret = registrySecret;

  // 打开「注册表凭据」对话框，断言字段与 password 类型
  const registryOpen = await (async () => {
    try {
      const clicked = await frameEval(`(() => {
        const button = document.querySelector('[data-act="images.registry"]');
        if (!button) return false;
        button.click();
        return true;
      })()`);
      if (!clicked) return { opened: false, error: '未找到 [data-act="images.registry"] 按钮' };
      const state = await pollUntil(
        async () => {
          const value = await frameEval(`(() => {
            const root = document.querySelector('#dialog-root');
            if (!root || root.hidden) return false;
            const server = root.querySelector('input[name="server"]');
            const username = root.querySelector('input[name="username"]');
            const password = root.querySelector('input[name="password"]');
            if (!server || !username || !password) return false;
            return {
              server: server.value,
              passwordType: password.type,
              fields: [...root.querySelectorAll('input')].map((input) => ({ name: input.name, type: input.type })),
              buttons: [...root.querySelectorAll('button')].map((button) => ({ act: button.getAttribute('data-act'), text: (button.innerText || '').trim().slice(0, 24) })),
              text: (root.innerText || '').trim().slice(0, 400),
            };
          })()`);
          return value || false;
        },
        { timeout: 10_000, interval: 250, label: '注册表凭据对话框' },
      );
      return { opened: true, state };
    } catch (error) {
      return { opened: false, error: String(error.message || error) };
    }
  })();
  evidence.checks.registryDialog = registryOpen;
  if (registryOpen.opened) await sleep(700); // 等对话框打开动画结束再截图，避免截到淡入中的空屏
  const credentialsShot = registryOpen.opened ? await shot('registryCredentials').catch(() => null) : null;
  await section('registry-credentials-dialog', '镜像页「注册表凭据」对话框（server/username/password + 登录/登出）', async () => {
    if (!registryOpen.opened) throw new Error(registryOpen.error || '对话框未打开');
    const state = registryOpen.state;
    const names = state.fields.map((field) => field.name);
    for (const name of ['server', 'username', 'password']) {
      if (!names.includes(name)) throw new Error(`缺少字段 ${name}（实际: ${names.join(',')}）`);
    }
    if (state.passwordType !== 'password') throw new Error(`password 输入框 type 期望 password，实际 ${state.passwordType}`);
    if (!state.buttons.some((button) => /登录|Login/i.test(button.text))) throw new Error(`缺少「登录」按钮: ${JSON.stringify(state.buttons)}`);
    if (!state.buttons.some((button) => /登出|Logout/i.test(button.text))) throw new Error(`缺少「登出」按钮: ${JSON.stringify(state.buttons)}`);
    return `server=${state.server || '(空)'}; fields=${names.join(',')}; passwordType=password; buttons=${state.buttons.map((button) => button.text).filter(Boolean).join('/')}; screenshot=${credentialsShot ? credentialsShot.file : 'n/a'}`;
  });

  // 空 username/password 提交 → 校验提示（不发网络）
  const emptyValidation = await (async () => {
    try {
      const clicked = await frameEval(`(() => {
        const button = document.querySelector('#dialog-root [data-role=submit]');
        if (!button) return false;
        button.click();
        return true;
      })()`);
      if (!clicked) return { clicked: false };
      const state = await pollUntil(
        async () => {
          const value = await frameEval(`(() => {
            const error = document.querySelector('#dialog-root #dialog-error');
            if (!error || error.hidden) return false;
            const text = (error.innerText || '').trim();
            return text ? { text: text.slice(0, 200) } : false;
          })()`);
          return value || false;
        },
        { timeout: 8000, interval: 200, label: '空表单校验提示' },
      );
      return { clicked: true, state };
    } catch (error) {
      return { clicked: true, error: String(error.message || error) };
    }
  })();
  evidence.checks.registryEmptyValidation = emptyValidation;
  if (emptyValidation.state) await sleep(500);
  const validationShot = emptyValidation.state ? await shot('registryValidation').catch(() => null) : null;
  await section('registry-empty-validation', '空 username/password 提交显示校验提示', async () => {
    if (!emptyValidation.state) throw new Error(emptyValidation.error || '空提交未出现校验提示');
    const text = emptyValidation.state.text;
    // v0.1.1 修复：本地校验带 INVALID_PARAMS，标题应为「参数不合法」而不是「内部错误」
    if (/内部错误|Internal error/i.test(text)) {
      throw new Error(`空提交校验仍显示通用错误标题: ${text.replace(/\s+/g, ' ').slice(0, 140)}`);
    }
    if (!/参数不合法|Invalid parameters/i.test(text)) {
      throw new Error(`空提交校验标题未显示「参数不合法」: ${text.replace(/\s+/g, ' ').slice(0, 140)}`);
    }
    if (!/请输入用户名和密码|required|username|password/i.test(text)) {
      throw new Error(`空提交校验缺少提示内容: ${text.replace(/\s+/g, ' ').slice(0, 140)}`);
    }
    return `${text.replace(/\s+/g, ' ').slice(0, 160)}; screenshot=${validationShot ? validationShot.file : 'n/a'}`;
  });

  // 错误凭据登录（真实网络，最长 125s）→ REGISTRY_AUTH_FAILED 展示 + 密码即时清空
  const invalidLogin = { attempted: true, passwordCleared: false, state: null, elapsedMs: 0 };
  const loginStartedAt = Date.now();
  const filled = await frameEval(`(() => {
    const root = document.querySelector('#dialog-root');
    if (!root) return false;
    const setValue = (name, value) => {
      const input = root.querySelector('input[name="' + name + '"]');
      if (!input) return false;
      input.value = value;
      input.dispatchEvent(new Event('input', { bubbles: true }));
      return true;
    };
    return setValue('server', 'docker.io') && setValue('username', 'qa-ui-user-' + Date.now().toString(36)) && setValue('password', ${JSON.stringify(registrySecret)});
  })()`);
  if (filled) {
    const submitted = await frameEval(`(() => {
      const button = document.querySelector('#dialog-root [data-role=submit]');
      if (!button) return false;
      button.click();
      return true;
    })()`);
    if (submitted) {
      invalidLogin.passwordCleared = await pollUntil(
        async () => {
          const cleared = await frameEval(`(() => {
            const input = document.querySelector('#dialog-root input[name="password"]');
            return !!input && input.value === '';
          })()`);
          return cleared === true;
        },
        { timeout: 3000, interval: 100, label: '提交后 password 输入框清空' },
      ).catch(() => false);
      try {
        invalidLogin.state = await pollUntil(
          async () => {
            const value = await frameEval(`(() => {
              const root = document.querySelector('#dialog-root');
              if (!root) return false;
              const error = root.querySelector('#dialog-error');
              const text = error && !error.hidden ? (error.innerText || '').trim() : '';
              if (!text) return false;
              const stateEl = root.querySelector('#registry-state');
              return { text: text.slice(0, 300), stateText: stateEl ? (stateEl.innerText || '').trim().slice(0, 200) : '' };
            })()`);
            return value || false;
          },
          { timeout: 125_000, interval: 500, label: '错误凭据登录失败提示' },
        );
      } catch (error) {
        invalidLogin.error = String(error.message || error);
      }
    }
  }
  invalidLogin.elapsedMs = Date.now() - loginStartedAt;
  const leakSnapshot = await (async () => {
    try {
      const frameTextValue = await frameText();
      const inputs = await frameEval(`[...document.querySelectorAll('#dialog-root input')].map((input) => ({ name: input.name, value: input.value }))`);
      const shellText = await pageText();
      const storage = await frameEval(`(async () => {
        try {
          const api = window.dbxPlugin && window.dbxPlugin.storage;
          if (!api) return { available: false };
          const value = await api.get('registry.credentials');
          return { available: true, value };
        } catch (error) {
          return { available: true, error: String((error && error.message) || error) };
        }
      })()`);
      return { frameText: frameTextValue, inputs, shellText, storage };
    } catch (error) {
      return { error: String(error.message || error) };
    }
  })();
  const storageValue = leakSnapshot.storage && leakSnapshot.storage.value;
  const storageHasPassword = Boolean(storageValue && typeof storageValue === 'object' && 'password' in storageValue);
  evidence.checks.registryLeak = {
    passwordCleared: invalidLogin.passwordCleared,
    elapsedMs: invalidLogin.elapsedMs,
    storageAvailable: leakSnapshot.storage ? leakSnapshot.storage.available : false,
    inputCount: (leakSnapshot.inputs || []).length,
  };
  await section('registry-credentials-error', '错误凭据登录：失败提示 + 密码即时清空', async () => {
    if (!invalidLogin.state) throw new Error(invalidLogin.error || '125s 内未出现登录失败提示（网络过慢，报告中标注）');
    if (!invalidLogin.passwordCleared) throw new Error('提交后 password 输入框未立即清空（契约 §7.11）');
    return `error=${invalidLogin.state.text.replace(/\s+/g, ' ').slice(0, 140)}; elapsed=${invalidLogin.elapsedMs}ms`;
  });
  await section('registry-password-no-leak', '密码不出现在页面/输入框/宿主 UI/宿主 storage', async () => {
    const dump = JSON.stringify(leakSnapshot);
    if (dump.includes(registrySecret)) throw new Error('页面 DOM/输入框/宿主 UI 中出现密码明文');
    if (storageHasPassword) throw new Error('registry.credentials storage 中出现 password 字段');
    if (storageValue && typeof storageValue === 'object') {
      const keys = Object.keys(storageValue);
      const unexpected = keys.filter((key) => key !== 'server' && key !== 'username');
      if (unexpected.length) throw new Error(`storage registry.credentials 出现非预期字段: ${unexpected.join(',')}`);
    }
    return `storage=${leakSnapshot.storage && leakSnapshot.storage.available ? JSON.stringify(storageValue) : 'unavailable'}; inputs=${(leakSnapshot.inputs || []).length}`;
  });
  await frameEval(`(() => {
    const button = document.querySelector('#dialog-root [data-act="dialog.cancel"]');
    if (button) { button.click(); return true; }
    return false;
  })()`).catch(() => {});
  await sleep(500);

  // 通过 UI 拉取不存在的镜像 → 认证失败 callout + 「去登录」预填 server
  // 该检查依赖 docker.io；网络抖动（EOF/E_FAIL）时 UI 会展示 PROCESS_FAILED 而非认证 callout，重试 1 次并记录
  const pullAuth = await (async () => {
    const ref = 'docker.io/library/definitely-not-a-real-image-dbx-xyz:latest';
    const attempts = [];
    const closeDialog = async () => {
      await frameEval(`(() => {
        const button = document.querySelector('#dialog-root [data-act="dialog.cancel"]');
        if (button) { button.click(); return true; }
        return false;
      })()`).catch(() => {});
      await sleep(500);
    };
    try {
      for (let attempt = 1; attempt <= 2; attempt++) {
        const opened = await frameEval(`(() => {
          const button = document.querySelector('[data-act="images.pull"]');
          if (!button) return false;
          button.click();
          return true;
        })()`);
        if (!opened) return { opened: false, error: '未找到 images.pull 按钮' };
        let state = null;
        let errorText = '';
        try {
          await pollUntil(
            async () =>
              await frameEval(`(() => {
                const root = document.querySelector('#dialog-root');
                return !!root && !root.hidden && !!root.querySelector('input[name="ref"]');
              })()`),
            { timeout: 10_000, interval: 250, label: '拉取镜像对话框（认证测试）' },
          );
          const filledRef = await frameEval(`(() => {
            const input = document.querySelector('#dialog-root input[name="ref"]');
            if (!input) return false;
            input.value = ${JSON.stringify(ref)};
            input.dispatchEvent(new Event('input', { bubbles: true }));
            return true;
          })()`);
          if (!filledRef) return { opened: true, error: 'ref 输入框不可用' };
          const submitted = await frameEval(`(() => {
            const button = document.querySelector('#dialog-root [data-role=submit]');
            if (!button) return false;
            button.click();
            return true;
          })()`);
          if (!submitted) return { opened: true, error: '未找到拉取提交按钮' };
          state = await pollUntil(
            async () => {
              const value = await frameEval(`(() => {
                const auth = [...document.querySelectorAll('.job-auth')].find((el) => !el.hidden && el.getBoundingClientRect().height > 0);
                const goto = [...document.querySelectorAll('[data-act="registry.goto"]')].find((el) => el.getBoundingClientRect().height > 0);
                const dialog = document.querySelector('#dialog-root');
                const dialogText = dialog ? (dialog.innerText || '').trim() : '';
                if (auth || goto) {
                  return {
                    kind: 'auth',
                    authText: auth ? (auth.innerText || '').trim().slice(0, 300) : '',
                    gotoVisible: !!goto,
                    dialogText: dialogText.slice(0, 600),
                  };
                }
                if (/EOF|E_FAIL|timed out|timeout|connection refused|network|TLS/i.test(dialogText)) {
                  return { kind: 'network', authText: '', gotoVisible: false, dialogText: dialogText.slice(0, 600) };
                }
                return false;
              })()`);
              return value || false;
            },
            { timeout: 120_000, interval: 500, label: '认证失败 callout + 去登录按钮' },
          );
        } catch (error) {
          errorText = String(error.message || error);
        }
        attempts.push({
          attempt,
          kind: state ? state.kind : 'timeout',
          error: errorText || undefined,
          dialogText: state && state.kind === 'network' ? state.dialogText.replace(/\s+/g, ' ').slice(0, 200) : undefined,
        });
        if (state && state.kind === 'auth') {
          const authShot = await shot('registryAuthError').catch(() => null);
          let prefilled = null;
          const gotoClicked = await frameEval(`(() => {
            const button = [...document.querySelectorAll('[data-act="registry.goto"]')].find((el) => el.getBoundingClientRect().height > 0);
            if (!button) return false;
            button.click();
            return true;
          })()`);
          if (gotoClicked) {
            prefilled = await pollUntil(
              async () => {
                const value = await frameEval(`(() => {
                  const root = document.querySelector('#dialog-root');
                  if (!root || root.hidden) return false;
                  const server = root.querySelector('input[name="server"]');
                  if (!server) return false;
                  return { server: server.value, passwordType: (root.querySelector('input[name="password"]') || {}).type || '' };
                })()`);
                return value || false;
              },
              { timeout: 8000, interval: 250, label: '「去登录」打开凭据对话框' },
            ).catch(() => null);
          }
          return { opened: true, state, authShot, prefilled, attempts };
        }
        if (state && state.kind === 'network') {
          await closeDialog();
          if (attempt < 2) {
            await sleep(3000);
            continue;
          }
          return { opened: true, networkError: true, dialogText: state.dialogText, attempts };
        }
        return { opened: true, error: errorText || '未出现认证失败 callout/去登录按钮', attempts };
      }
      return { opened: true, error: '拉取认证测试未产生结果', attempts };
    } catch (error) {
      return { opened: true, error: String(error.message || error), attempts };
    }
  })();
  evidence.checks.pullAuth = pullAuth;
  await section('registry-pull-auth-callout', '拉取不存在镜像：认证失败 callout + 「去登录」', async () => {
    if (pullAuth.networkError) {
      // 环境网络抖动：UI 正确展示通用失败而非认证 callout；契约分类由 sidecar-probe/backend 单测覆盖
      results.warnings.push({
        type: 'registry-network',
        message: 'UI 拉取认证测试遇到 docker.io 网络抖动（重试后仍失败），未验证 callout；属环境问题，非产品缺陷',
      });
      return `SKIP(网络抖动): ${(pullAuth.dialogText || 'EOF/E_FAIL').replace(/\s+/g, ' ').slice(0, 160)}`;
    }
    if (!pullAuth.state) throw new Error(pullAuth.error || '未出现认证失败 callout/去登录按钮');
    if (!pullAuth.state.gotoVisible) throw new Error('认证失败 callout 缺少「去登录」按钮');
    if (!/登录|login|认证|requires|not exist|pull access denied/i.test(`${pullAuth.state.authText}\n${pullAuth.state.dialogText}`)) {
      throw new Error(`callout 文案不含认证提示: ${pullAuth.state.authText}`);
    }
    if (!pullAuth.prefilled) throw new Error('点击「去登录」后未打开凭据对话框');
    if (pullAuth.prefilled.server !== 'docker.io') throw new Error(`server 预填期望 docker.io，实际 ${pullAuth.prefilled.server || '(空)'}`);
    return `authText=${pullAuth.state.authText.replace(/\s+/g, ' ').slice(0, 160)}; prefilledServer=${pullAuth.prefilled.server}; attempts=${pullAuth.attempts.length}; screenshot=${pullAuth.authShot ? pullAuth.authShot.file : 'n/a'}`;
  });
  await frameEval(`(() => {
    const button = document.querySelector('#dialog-root [data-act="dialog.cancel"]');
    if (button) { button.click(); return true; }
    return false;
  })()`).catch(() => {});
  await sleep(500);

  // 8) 暗色主题（点击 dev host 主题按钮；失败时回退 prefers-color-scheme 模拟）
  const themeToggle = await pageEval(`(() => {
    const button = [...document.querySelectorAll('button')].find((node) =>
      /深色主题|浅色主题|Dark theme|Light theme/.test((node.getAttribute('aria-label') || '') + (node.getAttribute('title') || '')),
    );
    if (!button) return { found: false };
    const before = button.getAttribute('aria-label') || button.getAttribute('title');
    button.click();
    return { found: true, before };
  })()`);
  let darkState = null;
  if (themeToggle.found) {
    darkState = await pollUntil(
      async () => {
        const raw = await frameEval(`(() => ({
          theme: document.documentElement.dataset.theme || '',
          bg: getComputedStyle(document.body).backgroundColor,
        }))()`);
        const state = { ...raw, luminance: rgbLuminance(raw.bg), mode: 'host-toggle' };
        return state.theme === 'dark' && state.luminance < 110 ? state : false;
      },
      { timeout: 8000, interval: 300, label: '暗色主题生效' },
    ).catch(() => null);
    evidence.checks.darkToggle = themeToggle;
  }
  if (!darkState) {
    await cdp.send('Emulation.setEmulatedMedia', { features: [{ name: 'prefers-color-scheme', value: 'dark' }] }, pageSession);
    await sleep(1200);
    const raw = await frameEval(`(() => ({
      theme: document.documentElement.dataset.theme || '',
      bg: getComputedStyle(document.body).backgroundColor,
    }))()`).catch(() => null);
    darkState = raw ? { ...raw, luminance: rgbLuminance(raw.bg), mode: 'emulated-media' } : null;
  }
  await gotoHashPage('dashboard');
  const darkImage = await shot('dark').catch(() => null);
  await section('dashboard-dark', '暗色主题概览页', async () => {
    if (!darkState) throw new Error('无法切换到暗色主题');
    if (darkState.luminance >= 110) throw new Error(`暗色背景亮度 ${darkState.luminance.toFixed(1)} 过高 (${darkState.bg})`);
    return `mode=${darkState.mode}; theme=${darkState.theme}; bg=${darkState.bg}; screenshot=${darkImage ? darkImage.file : 'n/a'}`;
  });
  // 恢复浅色 / 清除媒体模拟
  if (themeToggle.found) {
    await pageEval(`(() => {
      const button = [...document.querySelectorAll('button')].find((node) =>
        /深色主题|浅色主题|Dark theme|Light theme/.test((node.getAttribute('aria-label') || '') + (node.getAttribute('title') || '')),
      );
      if (button) button.click();
      return true;
    })()`).catch(() => {});
  }
  await cdp.send('Emulation.setEmulatedMedia', { features: [] }, pageSession).catch(() => {});
  await sleep(600);

  // 9) 移动端（390×844）：概览 / 容器 / 侧边栏
  await cdp.send(
    'Emulation.setDeviceMetricsOverride',
    { width: 390, height: 844, deviceScaleFactor: 2, mobile: true },
    pageSession,
  );
  await sleep(1000);
  await gotoHashPage('dashboard');
  const outerViewport = await pageEval('({ width: innerWidth, height: innerHeight })');
  const mobileDashboard = await frameEval(`(() => {
    const root = document.documentElement;
    const menu = document.querySelector('[data-act="ui.openSidebar"]');
    return {
      width: innerWidth,
      height: innerHeight,
      scrollWidth: root.scrollWidth,
      textLength: (document.body.innerText || '').trim().length,
      hasMenuButton: !!menu && menu.getBoundingClientRect().width > 0 && getComputedStyle(menu).display !== 'none',
    };
  })()`);
  evidence.checks.mobileDashboard = { outerViewport, ...mobileDashboard };
  const mobileDashboardImage = await shot('mobileDashboard').catch(() => null);
  await section('mobile-dashboard', '移动端概览页（390×844 无横向溢出）', async () => {
    if (outerViewport.width > 400) throw new Error(`移动端 dev host 视口宽度未生效: ${outerViewport.width}`);
    if (mobileDashboard.width > outerViewport.width) throw new Error(`iframe 宽度 ${mobileDashboard.width} 超过宿主视口 ${outerViewport.width}`);
    if (mobileDashboard.scrollWidth > mobileDashboard.width + 2) throw new Error(`横向溢出: scrollWidth=${mobileDashboard.scrollWidth} > ${mobileDashboard.width}`);
    if (mobileDashboard.textLength < 100) throw new Error(`文本过少疑似白屏: ${mobileDashboard.textLength}`);
    if (!mobileDashboard.hasMenuButton) throw new Error('移动端未显示侧边栏菜单按钮');
    return `outer=${outerViewport.width}px, iframe=${mobileDashboard.width}px, scrollW=${mobileDashboard.scrollWidth}, text=${mobileDashboard.textLength}, screenshot=${mobileDashboardImage ? mobileDashboardImage.file : 'n/a'}`;
  });

  await gotoHashPage('containers');
  const mobileContainers = await frameEval(`(() => ({
    scrollWidth: document.documentElement.scrollWidth,
    width: innerWidth,
    textLength: (document.body.innerText || '').trim().length,
    text: (document.body.innerText || '').trim().slice(0, 200),
  }))()`);
  evidence.checks.mobileContainers = mobileContainers;
  const mobileContainersImage = await shot('mobileContainers').catch(() => null);
  await section('mobile-containers', '移动端容器页（无横向溢出）', async () => {
    if (mobileContainers.scrollWidth > mobileContainers.width + 2) throw new Error(`横向溢出: ${mobileContainers.scrollWidth} > ${mobileContainers.width}`);
    if (mobileContainers.textLength < 80) throw new Error(`文本过少: ${mobileContainers.textLength}`);
    return `scrollW=${mobileContainers.scrollWidth}, text=${mobileContainers.textLength}, screenshot=${mobileContainersImage ? mobileContainersImage.file : 'n/a'}`;
  });

  const menuClicked = await frameEval(`(() => {
    const button = document.querySelector('[data-act="ui.openSidebar"]');
    if (!button) return false;
    button.click();
    return true;
  })()`);
  await sleep(700);
  const mobileSidebar = await frameEval(`(() => {
    const app = document.querySelector('#app');
    const sidebar = document.querySelector('#sidebar');
    return {
      open: !!app && app.classList.contains('sidebar-open'),
      sidebarVisible: !!sidebar && sidebar.getBoundingClientRect().width > 100,
      text: (document.body.innerText || '').trim().slice(0, 120),
    };
  })()`);
  evidence.checks.mobileSidebar = { menuClicked, ...mobileSidebar };
  const mobileSidebarImage = await shot('mobileSidebar').catch(() => null);
  await section('mobile-sidebar', '移动端侧边栏展开', async () => {
    if (!menuClicked) throw new Error('未找到 ui.openSidebar 菜单按钮');
    if (!mobileSidebar.open || !mobileSidebar.sidebarVisible) throw new Error(`侧边栏未展开: open=${mobileSidebar.open}, visible=${mobileSidebar.sidebarVisible}`);
    return `sidebar-open=true, screenshot=${mobileSidebarImage ? mobileSidebarImage.file : 'n/a'}`;
  });
  await frameEval(`(() => { const scrim = document.querySelector('[data-act="ui.closeSidebar"]'); if (scrim) { scrim.click(); return true; } return false; })()`).catch(() => {});

  // 恢复桌面视口并回到概览页
  await cdp.send('Emulation.setDeviceMetricsOverride', { ...VIEWPORT, deviceScaleFactor: 1, mobile: false }, pageSession);
  await sleep(800);
  await gotoHashPage('dashboard');
  results.finalEvidence = evidence;
  return evidence;
}

// 静态核对 UI 是否按契约 §9.1 给关键方法设置了 60s/90s 超时
function checkTimeoutConfig() {
  const source = readFileSync(path.join(PLUGIN_ROOT, 'ui', 'index.html'), 'utf8');
  const missing = [];
  const hits = [];
  for (const [method, timeoutMs] of RPC_TIMEOUT_EXPECTATIONS) {
    const escaped = method.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
    const pattern = new RegExp(`invoke\\(['"]${escaped}['"][^\\n]*?timeoutMs:\\s*${timeoutMs}\\b`);
    if (pattern.test(source)) hits.push(`${method}@${timeoutMs}`);
    else missing.push(`${method}@${timeoutMs}`);
  }
  return { missing, hits, expectations: RPC_TIMEOUT_EXPECTATIONS };
}

async function fetchDiagnostics() {
  try {
    const url = new URL('api/diagnostics?after=0&limit=500', devUrl);
    const response = await fetch(url, { signal: AbortSignal.timeout(8000) });
    if (!response.ok) return { error: `HTTP ${response.status}` };
    const payload = await response.json();
    const entries = Array.isArray(payload.entries) ? payload.entries : [];
    const errorEntries = entries.filter((entry) => entry.level === 'error');
    // favicon/未知路由 404 是 dev host 自身噪声，不算插件/后端问题
    const isBenignHttp = (entry) =>
      entry.category === 'http' &&
      entry.details &&
      (entry.details.status === 404 || entry.details.path === '[unknown route]');
    // 负向测试（错误凭据登录）会产生 /api/bridge 400 + RPC error，属于预期内噪声
    const isExpectedBridgeHttp = (entry) =>
      entry.category === 'http' &&
      entry.details &&
      entry.details.path === '/api/bridge' &&
      entry.details.reason === 'backend-error';
    const bridgeHttpErrors = errorEntries.filter((entry) => isExpectedBridgeHttp(entry));
    // RPC 完成日志 → 每个方法的调用次数与最大耗时（用于核对 §9.1 超时配置是否生效）
    const rpcDone = entries.filter((entry) => entry.category === 'rpc' && entry.message === 'RPC 完成');
    const rpcMethods = {};
    for (const entry of rpcDone) {
      const method = entry.details && entry.details.method;
      if (!method) continue;
      const durationMs = Number(entry.details.durationMs) || 0;
      const current = rpcMethods[method] || { calls: 0, maxMs: 0, totalMs: 0 };
      current.calls += 1;
      current.maxMs = Math.max(current.maxMs, durationMs);
      current.totalMs += durationMs;
      rpcMethods[method] = current;
    }
    return {
      backendState: payload.backendState,
      loopbackPort: payload.port,
      plugin: payload.plugin,
      entryCount: entries.length,
      entries,
      errorEntries,
      rpcErrors: errorEntries.filter((entry) => entry.category === 'rpc'),
      httpErrors: errorEntries.filter((entry) => entry.category === 'http' && !isBenignHttp(entry) && !isExpectedBridgeHttp(entry)),
      bridgeHttpErrors,
      rpcDoneCount: rpcDone.length,
      rpcMethods,
    };
  } catch (error) {
    return { error: String(error.message || error) };
  }
}

// ---------------------------------------------------------------------------
// 主流程
// ---------------------------------------------------------------------------

let cleanedUp = false;
function cleanup() {
  if (cleanedUp) return;
  cleanedUp = true;
  try {
    cdp?.close();
  } catch {}
  if (chromeState) {
    if (options.keepOpen) {
      log(dim(`\nChrome 保持运行（--keep-open），DevTools 端口: ${chromeState.port}，profile: ${chromeState.profileDir}`));
    } else {
      killChrome(chromeState);
    }
  }
}

async function main() {
  devUrl = options.devUrl;
  const startedAt = Date.now();
  const selectedPages = detectPageFilters();
  const labels = readManifestLabels();

  mkdirSync(options.out, { recursive: true });
  mkdirSync(TMP_DIR, { recursive: true });

  const port = options.port || (await freePort());
  log(bold('\nWSL Manager dev host UI 检查'));
  log(`dev host: ${devUrl}`);
  log(`Chrome: ${detectChrome()} (CDP port ${port})`);
  log(`截图目录: ${path.relative(PLUGIN_ROOT, options.out) || options.out}`);
  log(`Chrome profile: ${CHROME_PROFILE_ROOT}`);
  log(`工作台标签: ${labels.join(' / ')}`);
  log('');

  // 等待 dev host
  await pollUntil(
    async () => {
      try {
        const response = await fetch(devUrl, { signal: AbortSignal.timeout(3000) });
        return response.ok;
      } catch {
        return false;
      }
    },
    { timeout: 60_000, interval: 500, label: `dev host 可访问（${devUrl}）` },
  ).catch((error) => {
    addCheck('dev-host', 'dev host 可访问', false, error.message);
    throw error;
  });
  addCheck('dev-host', 'dev host 可访问', true, devUrl);

  cleanupStaleProfiles();
  chromeState = await launchChrome(port);
  chromeState.port = port;
  const version = chromeState.version;
  log(dim(`浏览器: ${version.Browser}`));
  cdp = await new CdpClient(version.webSocketDebuggerUrl).connect();

  cdp.onMessage((message) => {
    if (message.method === 'Target.attachedToTarget') {
      const { sessionId, targetInfo } = message.params;
      debug(`target attached: ${targetInfo.type} ${targetInfo.url}`);
      if (targetInfo.type === 'iframe') {
        frameSession = sessionId;
        // 立即开启 Runtime/Log，避免错过 iframe 早期错误
        void (async () => {
          try {
            await cdp.send('Runtime.enable', {}, sessionId);
            await cdp.send('Log.enable', {}, sessionId);
            await cdp.send('Runtime.runIfWaitingForDebugger', {}, sessionId);
            debug(`iframe session ready: ${sessionId}`);
          } catch (error) {
            debug(`iframe session 初始化失败: ${error.message}`);
          }
        })();
      } else if (message.params.waitingForDebugger) {
        // waitForDebuggerOnStart 也可能暂停页面/worker 等其它 target，必须放行，否则页面无法加载
        void cdp
          .send('Runtime.runIfWaitingForDebugger', {}, sessionId)
          .catch((error) => debug(`resume ${targetInfo.type} failed: ${error.message}`));
      }
    } else if (message.method === 'Target.detachedFromTarget') {
      if (message.params.sessionId === frameSession) {
        debug(`iframe detached: ${message.params.sessionId}`);
        frameSession = null;
      }
    } else if (message.method === 'Runtime.exceptionThrown') {
      const details = message.params.exceptionDetails || {};
      results.pageErrors.push({
        session: message.sessionId || 'page',
        source: 'exception',
        text: details.exception?.description || details.text || 'unknown exception',
        url: details.url,
        line: details.lineNumber,
        at: Date.now(),
      });
    } else if (message.method === 'Runtime.consoleAPICalled') {
      const params = message.params;
      if (params.type !== 'error' && params.type !== 'warning') return;
      const text = (params.args || [])
        .map((arg) => arg.value ?? arg.description ?? arg.unserializableValue ?? '')
        .join(' ')
        .slice(0, 800);
      recordConsoleEvent(params.type === 'error' ? 'error' : 'warning', message.sessionId || 'page', {
        source: 'console',
        text,
      });
    } else if (message.method === 'Log.entryAdded') {
      const entry = message.params.entry || {};
      if (entry.level !== 'error') return;
      recordConsoleEvent('error', message.sessionId || 'page', {
        source: `log/${entry.source || 'unknown'}`,
        text: entry.text || '',
        url: entry.url || '',
      });
    }
  });

  const { targetId } = await cdp.send('Target.createTarget', { url: 'about:blank' });
  const attached = await cdp.send('Target.attachToTarget', { targetId, flatten: true });
  pageSession = attached.sessionId;
  await cdp.send('Page.enable', {}, pageSession);
  await cdp.send('Runtime.enable', {}, pageSession);
  await cdp.send('Log.enable', {}, pageSession);
  await cdp.send('Emulation.setDeviceMetricsOverride', { ...VIEWPORT, deviceScaleFactor: 1, mobile: false }, pageSession);
  // OOPIF iframe 需要通过 auto-attach 才能拿到 session；暂停新 target 直到启用 Runtime
  await cdp.send(
    'Target.setAutoAttach',
    { autoAttach: true, waitForDebuggerOnStart: false, flatten: true },
    pageSession,
  );

  await cdp.send('Page.navigate', { url: devUrl }, pageSession);
  await pollUntil(
    async () => {
      const state = await evaluate(pageSession, '({ ready: document.readyState, textLength: (document.body ? document.body.innerText : "").trim().length })');
      debug(`navigate poll: ${JSON.stringify(state)}`);
      return state.ready === 'complete' && state.textLength > 20 ? state : false;
    },
    { timeout: 30_000, interval: 250, label: 'dev host 页面加载' },
  );
  const shellText = await pageText();
  const shellOk = /插件调试|Plugin Debug|Workbench/i.test(shellText);
  addCheck('dev-shell', 'dev host 调试壳加载', shellOk, `${shellText.length} 字符, 标题「${await evaluate(pageSession, 'document.title')}」`);

  // 打开工作台
  try {
    const opened = await openWorkbench(labels);
    addCheck(
      'workbench-open',
      '打开插件工作台（iframe 加载完成）',
      true,
      `点击「${opened.click.text || opened.click.title}」, iframe 已就绪`,
    );
  } catch (error) {
    addCheck('workbench-open', '打开插件工作台（iframe 加载完成）', false, String(error.message || error));
    throw error; // 交由 main().catch 统一清理并退出，避免进程挂起
  }

  const iframeText = await frameText();
  const iframeOk = iframeText.trim().length > 100;
  addCheck(
    'workbench-content',
    '工作台内容渲染（无白屏）',
    iframeOk,
    `可见文本 ${iframeText.trim().length} 字符`,
  );
  results.workbench = { textLength: iframeText.trim().length, textStart: iframeText.slice(0, 300) };

  await checkSidebar();

  // 依次切换页面
  for (const page of selectedPages) {
    try {
      await analyzePage(page);
    } catch (error) {
      addCheck(`${page.id}/fatal`, `${page.id} 检查中断`, false, String(error.message || error));
      results.pages.push({ id: page.id, ok: false, error: String(error.message || error) });
    }
  }

  // 终版专项检查（仅完整 9 页模式执行）
  if (selectedPages.length === PAGES.length) {
    try {
      await runFinalChecks();
    } catch (error) {
      addCheck('final/fatal', '终版专项检查执行中断', false, String(error.message || error));
    }
  } else {
    results.warnings.push({ type: 'skipped-final', message: '--pages 子集模式，跳过终版新增项检查（hero/操作按钮/暗色/移动端等）' });
  }

  // 汇总检查
  const processErrors = results.pageErrors.filter((entry) => !isBenignConsoleError(entry));
  const allConsoleErrors = results.consoleErrors.filter((entry) => !isBenignConsoleError(entry));
  const bridgeConsoleErrors = allConsoleErrors.filter((entry) => isDevHostBridgeError(entry));
  const consoleErrors = allConsoleErrors.filter((entry) => !isDevHostBridgeError(entry));
  // 错误凭据登录的负向测试必然产生 1 次 bridge 400 + console network error，属预期
  const expectedBridgeErrors = results.registrySecret ? 1 : 0;
  results.expectedBridgeConsoleErrors = Math.min(bridgeConsoleErrors.length, expectedBridgeErrors);
  const unexpectedBridgeErrors = bridgeConsoleErrors.length - results.expectedBridgeConsoleErrors;
  if (unexpectedBridgeErrors > 0) {
    results.warnings.push({
      type: 'devhost-bridge-network',
      message: `dev host bridge 网络错误共 ${bridgeConsoleErrors.length} 条（其中预期内负向测试 ${results.expectedBridgeConsoleErrors} 条，预期外 ${unexpectedBridgeErrors} 条）`,
    });
  }
  addCheck('no-uncaught-exception', '全程无 uncaught exception', processErrors.length === 0, `${processErrors.length} 条`);
  addCheck('no-console-error', '全程无 console error', consoleErrors.length === 0, `${consoleErrors.length} 条`);

  const pagesRendered = results.pages.filter((page) => page.ok).length;
  addCheck(
    'pages-rendered',
    `${selectedPages.length} 个页面全部渲染成功`,
    pagesRendered === selectedPages.length,
    `${pagesRendered}/${selectedPages.length}`,
  );

  const baseScreenshots = results.pages
    .map((page) => page.screenshot)
    .filter((screenshot) => screenshot && existsSync(path.join(PLUGIN_ROOT, screenshot)));
  const finalScreenshots = (results.finalEvidence?.shots || [])
    .map((item) => item.file)
    .filter((screenshot) => screenshot && existsSync(path.join(PLUGIN_ROOT, screenshot)));
  const expectedScreenshots =
    selectedPages.length + (selectedPages.length === PAGES.length ? Object.keys(FINAL_SCREENSHOTS).length : 0);
  const allScreenshots = [...baseScreenshots, ...finalScreenshots];
  addCheck(
    'screenshots',
    `截图产出完整（${expectedScreenshots} 张）`,
    allScreenshots.length === expectedScreenshots,
    `${allScreenshots.length}/${expectedScreenshots} 张（9 页基线 + 终版交互/暗色/移动端）`,
    { files: allScreenshots },
  );

  // dev host 后端状态 + RPC 诊断（额外信息，RPC error 记为 warn）
  const diagnostics = await fetchDiagnostics();
  results.diagnostics = diagnostics;
  if (diagnostics.error) {
    addWarn('devhost-diagnostics', '读取 /api/diagnostics 失败', diagnostics.error);
  } else {
    addCheck(
      'backend-ready',
      'dev host sidecar 后端状态 ready',
      diagnostics.backendState === 'ready',
      `state=${diagnostics.backendState}, entries=${diagnostics.entryCount}`,
    );
    // 负向测试（错误凭据登录 → REGISTRY_AUTH_FAILED）是预期内的 RPC error，不计为缺陷
    const expectedNegativeRpcErrors = diagnostics.rpcErrors.filter((entry) => {
      const method = entry.details && entry.details.method;
      const code = entry.details && entry.details.error && entry.details.error.data && entry.details.error.data.code;
      return method === 'wslc/registry/login' && code === 'REGISTRY_AUTH_FAILED';
    });
    const unexpectedRpcErrors = diagnostics.rpcErrors.filter((entry) => !expectedNegativeRpcErrors.includes(entry));
    addCheck(
      'final/no-rpc-errors',
      '无预期外的 RPC 调用失败',
      unexpectedRpcErrors.length === 0,
      unexpectedRpcErrors.length === 0
        ? `0 条预期外错误（RPC 完成 ${diagnostics.rpcDoneCount} 次；负向测试 ${expectedNegativeRpcErrors.length} 条 REGISTRY_AUTH_FAILED 属预期）`
        : `${unexpectedRpcErrors.length} 条: ` +
          unexpectedRpcErrors
            .slice(0, 5)
            .map((entry) => `${entry.details?.method || ''}/${JSON.stringify(entry.details?.error || {}).slice(0, 160)}`)
            .join(' | '),
    );
    results.negativeRpcErrors = expectedNegativeRpcErrors.length;
    if (diagnostics.httpErrors.length > 0) {
      addWarn(
        'http-errors',
        'dev host 诊断中出现 HTTP error 日志',
        `${diagnostics.httpErrors.length} 条: ` +
          diagnostics.httpErrors
            .slice(0, 5)
            .map((entry) => `status=${entry.details?.status} path=${entry.details?.path || '?'}`)
            .join(' | '),
      );
    }

    // ---- 终版专项：新增 RPC 接线 + 超时配置（依赖本次运行产生的诊断日志） ----
    const rpcMethods = diagnostics.rpcMethods || {};
    const describeRpc = (method) => {
      const stat = rpcMethods[method];
      return stat ? `calls=${stat.calls}, maxMs=${stat.maxMs}` : '未调用';
    };
    addCheck(
      'final/rpc-sessions-list',
      'UI 成功调用 wslc/sessions/list',
      Boolean(rpcMethods['wslc/sessions/list'] && rpcMethods['wslc/sessions/list'].calls >= 1),
      describeRpc('wslc/sessions/list'),
    );
    addCheck(
      'final/rpc-wslconf',
      'UI 成功调用 wsl/distros/wslconf',
      Boolean(rpcMethods['wsl/distros/wslconf'] && rpcMethods['wsl/distros/wslconf'].calls >= 1),
      describeRpc('wsl/distros/wslconf'),
    );
    addCheck(
      'final/rpc-distros-exec',
      'UI 成功调用 wsl/distros/exec',
      Boolean(rpcMethods['wsl/distros/exec'] && rpcMethods['wsl/distros/exec'].calls >= 1),
      describeRpc('wsl/distros/exec'),
    );
    addCheck(
      'final/rpc-distros-info',
      'UI 成功调用 wsl/distros/info（90s）',
      Boolean(rpcMethods['wsl/distros/info'] && rpcMethods['wsl/distros/info'].calls >= 1),
      describeRpc('wsl/distros/info'),
    );
    const timeoutFor = (method) => {
      const match = RPC_TIMEOUT_EXPECTATIONS.find(([name]) => name === method);
      return match ? match[1] : 120_000;
    };
    const overruns = Object.entries(rpcMethods)
      .filter(([method, stat]) => stat.maxMs > timeoutFor(method))
      .map(([method, stat]) => `${method}=${stat.maxMs}ms>${timeoutFor(method)}ms`);
    const slowest = Object.entries(rpcMethods)
      .sort((a, b) => b[1].maxMs - a[1].maxMs)
      .slice(0, 6)
      .map(([method, stat]) => `${method}:${stat.maxMs}ms`);
    addCheck(
      'final/rpc-duration-timeouts',
      'RPC 实际耗时均在 60s/90s 超时配置内',
      diagnostics.rpcDoneCount > 0 && overruns.length === 0,
      `完成 ${diagnostics.rpcDoneCount} 次；最慢: ${slowest.join(', ')}${overruns.length ? `；超限: ${overruns.join(', ')}` : ''}`,
    );
    results.rpcSummary = {
      doneCount: diagnostics.rpcDoneCount,
      errorCount: diagnostics.rpcErrors.length,
      methods: rpcMethods,
      slowest,
      overruns,
    };

    try {
      const timeoutCheck = checkTimeoutConfig();
      results.timeoutConfig = timeoutCheck;
      addCheck(
        'final/timeout-config',
        'UI 按契约 §9.1 传 60s/90s 超时（静态）',
        timeoutCheck.missing.length === 0,
        timeoutCheck.missing.length
          ? `缺少: ${timeoutCheck.missing.join(', ')}`
          : `命中 ${timeoutCheck.hits.length}/${RPC_TIMEOUT_EXPECTATIONS.length}: ${timeoutCheck.hits.join(', ')}`,
      );
    } catch (error) {
      addCheck('final/timeout-config', 'UI 按契约 §9.1 传 60s/90s 超时（静态）', false, String(error.message || error));
    }

    // v0.1.1 凭据脱敏：密码不得出现在 dev host 诊断日志（宿主自身会脱敏 password 字段）
    if (results.registrySecret) {
      const diagnosticsDump = JSON.stringify(diagnostics.entries || []);
      const leaked = diagnosticsDump.includes(results.registrySecret);
      addCheck(
        'final/registry-password-not-in-diagnostics',
        '密码不出现在 dev host 诊断日志',
        !leaked,
        leaked ? '诊断日志中发现密码明文' : `已扫描 ${diagnostics.entryCount} 条诊断`,
      );
    }
  }

  const durationMs = Date.now() - startedAt;
  const failed = results.checks.filter((check) => check.status === 'fail').length;
  const passed = results.checks.filter((check) => check.status === 'pass').length;
  const warned = results.checks.filter((check) => check.status === 'warn').length;
  const report = {
    tool: 'ui-cdp-check',
    ok: failed === 0,
    startedAt: new Date(startedAt).toISOString(),
    durationMs,
    devUrl,
    browser: version.Browser,
    workbenchLabels: labels,
    screenshotsDir: path.relative(PLUGIN_ROOT, options.out),
    summary: { passed, failed, warned },
    checks: results.checks,
    pages: results.pages,
    finalEvidence: results.finalEvidence || null,
    rpcSummary: results.rpcSummary || null,
    timeoutConfig: results.timeoutConfig || null,
    screenshots: [
      ...results.pages.map((page) => page.screenshot).filter(Boolean),
      ...((results.finalEvidence?.shots || []).map((item) => item.file).filter(Boolean)),
    ],
    consoleErrors,
    pageErrors: processErrors,
    warnings: results.warnings,
    diagnostics,
  };

  if (!options.json) {
    log('');
    log(
      failed === 0
        ? green(`全部通过: ${passed} PASS, ${warned} WARN (${(durationMs / 1000).toFixed(1)}s)`)
        : red(`存在失败: ${passed} PASS, ${failed} FAIL, ${warned} WARN (${(durationMs / 1000).toFixed(1)}s)`),
    );
    if (failed > 0) {
      log(red('\n失败项:'));
      for (const check of results.checks.filter((entry) => entry.status === 'fail')) {
        log(red(`  ✗ ${check.id}: ${check.detail}`));
      }
    }
    if (results.warnings.length > 0) {
      log(yellow(`\n警告 ${results.warnings.length} 条:`));
      for (const warning of results.warnings.slice(0, 10)) log(yellow(`  ! ${warning.message}`));
    }
  } else {
    process.stdout.write(`${JSON.stringify(report, null, 2)}\n`);
  }
  if (options.report) {
    mkdirSync(path.dirname(options.report), { recursive: true });
    writeFileSync(options.report, `${JSON.stringify(report, null, 2)}\n`);
    log(dim(`JSON 报告已写入: ${path.relative(PLUGIN_ROOT, options.report)}`));
  }

  cleanup();
  process.exit(failed === 0 ? 0 : 1);
}

process.on('SIGINT', () => {
  cleanup();
  process.exit(130);
});
process.on('uncaughtException', (error) => {
  if (!options.json) console.error(red(`未捕获异常: ${error.stack || error}`));
  cleanup();
  process.exit(1);
});
process.on('unhandledRejection', (error) => {
  if (!options.json) console.error(red(`未处理的 Promise 拒绝: ${error?.stack || error}`));
  cleanup();
  process.exit(1);
});

main().catch(async (error) => {
  if (options.json) {
    process.stdout.write(
      `${JSON.stringify(
        {
          tool: 'ui-cdp-check',
          ok: false,
          fatal: String(error.stack || error.message || error),
          checks: results.checks,
          pages: results.pages,
          consoleErrors: results.consoleErrors,
          pageErrors: results.pageErrors,
          warnings: results.warnings,
        },
        null,
        2,
      )}\n`,
    );
  } else {
    console.error(red(`\nUI 检查失败: ${error.stack || error.message || error}`));
    if (results.checks.length) {
      for (const check of results.checks.filter((entry) => entry.status === 'fail')) {
        console.error(red(`  ✗ ${check.id}: ${check.detail}`));
      }
    }
    if (chromeState?.stderrTail?.length) {
      console.error(dim(`Chrome stderr 尾部:\n${chromeState.stderrTail.slice(-10).join('\n')}`));
    }
  }
  cleanup();
  process.exit(1);
});
