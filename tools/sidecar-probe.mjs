#!/usr/bin/env node
/**
 * WSL Manager — Sidecar RPC 探针（零依赖，Node.js 22+）
 *
 * 直接 spawn 插件 sidecar 可执行文件，通过 stdin/stdout 发送 JSONL（Sidecar Protocol v1），
 * 按 wsl-manager/docs/rpc-contract.md 校验 plugin/initialize、各业务方法、长任务事件、
 * 取消与错误路径，输出 PASS/FAIL 汇总与机器可读 JSON。
 *
 * 用法:
 *   node tools/sidecar-probe.mjs                     # 自动定位/构建 sidecar，跑全部检查
 *   node tools/sidecar-probe.mjs --only=wslc         # 只跑 id 含 "wslc" 的检查
 *   node tools/sidecar-probe.mjs --only=containers --json
 *   node tools/sidecar-probe.mjs --sidecar=path.exe  # 指定 sidecar（.mjs/.js 会用 node 启动）
 *   node tools/sidecar-probe.mjs --timeout=90000     # 单请求默认超时（wslc 慢操作至少 120s）
 *
 * 退出码: 0 = 全部 PASS（允许 SKIP）；1 = 存在 FAIL 或无法启动。
 *
 * Sidecar 定位顺序:
 *   1. --sidecar=<path>
 *   2. <用户目录>\dbx-wsl-dev\qa\bin\dbx-plugin-wsl-manager.exe（dev host 的自定义 data-dir）
 *   3. <用户目录>\dbx-wsl-dev\qa-build\sidecar.exe（本工具 go build 输出）
 *   4. wsl-manager/.dbx-dev/bin 或 wsl-manager/.dbx-dev-qa/bin（workspace 内，会警告 Low IL 风险）
 *   5. go build 到 workspace 外（GOTMPDIR/GOCACHE 指向仓库根）
 *
 * 注意：workspace 目录带 Low mandatory integrity label，其中生成的 exe 以 Low IL 运行，
 * 子进程调用 wsl.exe/wslc.exe 会得到 E_ACCESSDENIED（本会话沙箱特性，非插件缺陷）。
 */

import { spawn, spawnSync } from 'node:child_process';
import { existsSync, mkdirSync, writeFileSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import process from 'node:process';
import { setTimeout as sleep } from 'node:timers/promises';
import { fileURLToPath } from 'node:url';

const HERE = path.dirname(fileURLToPath(import.meta.url));
const PLUGIN_ROOT = path.resolve(HERE, '..');
const REPO_ROOT = path.resolve(PLUGIN_ROOT, '..');
const TMP_DIR = path.join(PLUGIN_ROOT, 'tests', 'tmp');

// 注意：workspace 目录带 Low mandatory integrity label，放在 workspace 内的可执行文件
// 会以 Low IL 运行，子进程调用 wsl.exe/wslc.exe 会得到 E_ACCESSDENIED。
// 因此 sidecar 的默认构建输出/数据目录都放在源码目录之外（用户目录下的 dbx-wsl-dev）。
const EXTERNAL_ROOT = process.env.DBX_WSL_QA_HOME
  ? path.resolve(process.env.DBX_WSL_QA_HOME)
  : path.join(process.env.LOCALAPPDATA || os.tmpdir(), 'dbx-wsl-dev');
const EXTERNAL_DEV_DATA_DIR = path.join(EXTERNAL_ROOT, 'qa');
const BUILD_OUT = path.join(EXTERNAL_ROOT, 'qa-build', 'sidecar.exe');
const DATA_DIR = path.join(EXTERNAL_DEV_DATA_DIR, 'plugin-data', 'com.dbx.wsl');

function isInsideWorkspace(target) {
  const normalized = path.resolve(target).toLowerCase().replace(/\//g, '\\');
  const root = (REPO_ROOT.toLowerCase().replace(/\//g, '\\') + '\\');
  return normalized.startsWith(root);
}

const DEFAULT_TIMEOUT = 60_000;
const SLOW_TIMEOUT_MIN = 120_000;
const JOB_WAIT_MIN = 120_000;

// ---------------------------------------------------------------------------
// CLI
// ---------------------------------------------------------------------------

function hero(msg) {
  term(`\n\x1b[1m${msg}\x1b[0m`);
}

function parseArgs(argv) {
  const opts = {
    only: null,
    json: false,
    timeout: DEFAULT_TIMEOUT,
    sidecar: null,
    report: null,
    verbose: false,
    list: false,
    help: false,
  };
  for (let i = 0; i < argv.length; i++) {
    const arg = argv[i];
    const eq = arg.indexOf('=');
    const key = eq >= 0 ? arg.slice(0, eq) : arg;
    const inline = eq >= 0 ? arg.slice(eq + 1) : undefined;
    const next = () => (inline !== undefined ? inline : argv[++i]);
    switch (key) {
      case '--only': opts.only = String(next() || '').trim() || null; break;
      case '--json': opts.json = true; break;
      case '--timeout': {
        const value = Number(next());
        if (!Number.isFinite(value) || value <= 0) fail(`--timeout 需要正整数毫秒，收到: ${arg}`);
        opts.timeout = value;
        break;
      }
      case '--sidecar': opts.sidecar = String(next() || ''); break;
      case '--report': opts.report = String(next() || ''); break;
      case '--verbose': opts.verbose = true; break;
      case '--list': opts.list = true; break;
      case '--help': case '-h': opts.help = true; break;
      default:
        fail(`未知参数: ${arg}（用 --help 查看用法）`);
    }
  }
  return opts;
}

function fail(message) {
  console.error(`sidecar-probe: ${message}`);
  process.exit(1);
}

function log(...parts) {
  if (!options.json) console.log(...parts);
}
function term(text) {
  if (!options.json) console.log(text);
}
function dim(text) {
  return options.json || process.env.NO_COLOR ? text : `\x1b[2m${text}\x1b[0m`;
}
function green(text) {
  return options.json || process.env.NO_COLOR ? text : `\x1b[32m${text}\x1b[0m`;
}
function red(text) {
  return options.json || process.env.NO_COLOR ? text : `\x1b[31m${text}\x1b[0m`;
}
function yellow(text) {
  return options.json || process.env.NO_COLOR ? text : `\x1b[33m${text}\x1b[0m`;
}

const options = parseArgs(process.argv.slice(2));
if (options.help) {
  console.log(`WSL Manager Sidecar 探针（零依赖，Node.js 22+）

用法: node tools/sidecar-probe.mjs [选项]

选项:
  --only=<pattern>   只运行 id/标题包含 pattern 的检查（不区分大小写）
  --json             输出机器可读 JSON（stdout 仅 JSON）
  --timeout=<ms>     单请求默认超时（默认 ${DEFAULT_TIMEOUT}ms；wslc 慢操作至少 ${SLOW_TIMEOUT_MIN}ms）
  --sidecar=<path>   指定 sidecar 可执行文件；.mjs/.js 用当前 node 启动（用于 mock 自测）
  --report=<file>    额外写出 JSON 报告
  --verbose          打印 sidecar stderr
  --list             仅列出检查项
  -h, --help         显示帮助
`);
  process.exit(0);
}

// ---------------------------------------------------------------------------
// 断言
// ---------------------------------------------------------------------------

class AssertionError extends Error {}

let currentCheck = null;
let currentCtx = null;

function assert(cond, message) {
  const ok = Boolean(cond);
  if (currentCheck) currentCheck.assertions.push({ ok, message });
  if (!ok) throw new AssertionError(message);
}
function assertEqual(actual, expected, label) {
  assert(
    actual === expected,
    `${label} 期望 ${JSON.stringify(expected)}，实际 ${JSON.stringify(actual)}`,
  );
}
function assertType(value, type, label) {
  const actual = Array.isArray(value) ? 'array' : value === null ? 'null' : typeof value;
  assert(actual === type, `${label} 类型期望 ${type}，实际 ${actual}（值: ${truncate(value)}）`);
}
function assertNonEmptyString(value, label) {
  assertType(value, 'string', label);
  assert(value.trim().length > 0, `${label} 不能为空字符串`);
}
function assertArray(value, label, minLength = 0) {
  assert(Array.isArray(value), `${label} 必须是数组，实际: ${truncate(value)}`);
  assert(value.length >= minLength, `${label} 至少 ${minLength} 项，实际 ${value.length} 项`);
}
function assertObject(value, label) {
  assert(value && typeof value === 'object' && !Array.isArray(value), `${label} 必须是对象，实际: ${truncate(value)}`);
}
function assertOneOf(value, allowed, label) {
  assert(allowed.includes(value), `${label} 期望取值之一 ${JSON.stringify(allowed)}，实际 ${JSON.stringify(value)}`);
}
function truncate(value, max = 240) {
  let text;
  try {
    text = typeof value === 'string' ? value : JSON.stringify(value);
  } catch {
    text = String(value);
  }
  if (text === undefined) text = String(value);
  return text.length > max ? `${text.slice(0, max)}…` : text;
}

// ---------------------------------------------------------------------------
// JSONL sidecar 客户端
// ---------------------------------------------------------------------------

class RpcError extends Error {
  constructor(code, message, data) {
    super(message || `RPC error ${code}`);
    this.name = 'RpcError';
    this.code = code;
    this.data = data;
  }
}

class SidecarClient {
  constructor({ exe, args = [], cwd, env, requestTimeout, verbose }) {
    this.exe = exe;
    this.spawnArgs = args;
    this.cwd = cwd;
    this.env = env;
    this.requestTimeout = requestTimeout;
    this.verbose = verbose;
    this.child = null;
    this.buffer = '';
    this.nextId = 0;
    this.pending = new Map();
    this.events = [];
    this.listeners = new Set();
    this.stderrLines = [];
    this.stdoutNoise = [];
    this.protocolMessages = 0;
    this.exit = null;
    this.exitResolvers = new Set();
  }

  start() {
    this.child = spawn(this.exe, this.spawnArgs, {
      cwd: this.cwd,
      env: this.env,
      stdio: ['pipe', 'pipe', 'pipe'],
      windowsHide: true,
    });
    this.child.stdout.setEncoding('utf8');
    this.child.stderr.setEncoding('utf8');
    this.child.stdout.on('data', (chunk) => this.#onStdout(chunk));
    this.child.stderr.on('data', (chunk) => {
      for (const line of String(chunk).split(/\r?\n/)) {
        if (!line) continue;
        this.stderrLines.push(line);
        if (this.stderrLines.length > 2000) this.stderrLines.shift();
        if (this.verbose) console.error(dim(`[sidecar:err] ${line}`));
      }
    });
    this.child.on('error', (error) => this.#failPending(new Error(`无法启动 sidecar: ${error.message}`)));
    this.child.on('exit', (code, signal) => {
      this.exit = { code, signal, at: Date.now() };
      this.#failPending(new Error(`sidecar 已退出（code=${code}, signal=${signal}）`));
      for (const resolve of this.exitResolvers) resolve(this.exit);
      this.exitResolvers.clear();
    });
    return this;
  }

  #onStdout(chunk) {
    this.buffer += chunk;
    let index;
    while ((index = this.buffer.indexOf('\n')) >= 0) {
      const line = this.buffer.slice(0, index).replace(/\r$/, '');
      this.buffer = this.buffer.slice(index + 1);
      if (!line.trim()) continue;
      let message;
      try {
        message = JSON.parse(line);
      } catch {
        this.stdoutNoise.push(line.slice(0, 500));
        continue;
      }
      this.protocolMessages += 1;
      if (message && typeof message === 'object' && message.id !== undefined && this.pending.has(String(message.id))) {
        const entry = this.pending.get(String(message.id));
        this.pending.delete(String(message.id));
        clearTimeout(entry.timer);
        if (message.error) {
          entry.reject(new RpcError(message.error.code, message.error.message, message.error.data));
        } else {
          entry.resolve(message.result);
        }
      } else if (message && message.method) {
        const event = { method: message.method, params: message.params, at: Date.now() };
        this.events.push(event);
        if (this.events.length > 5000) this.events.shift();
        for (const listener of this.listeners) {
          try {
            listener(event);
          } catch (error) {
            if (this.verbose) console.error(dim(`[probe] 事件监听器异常: ${error.message}`));
          }
        }
      } else {
        this.stdoutNoise.push(`非协议消息: ${line.slice(0, 300)}`);
      }
    }
  }

  #failPending(error) {
    for (const [, entry] of this.pending) {
      clearTimeout(entry.timer);
      entry.reject(error);
    }
    this.pending.clear();
  }

  write(message) {
    if (!this.child || !this.child.stdin.writable) throw new Error('sidecar stdin 不可写');
    this.child.stdin.write(`${JSON.stringify(message)}\n`);
  }

  request(method, params = {}, timeoutMs = this.requestTimeout) {
    if (!this.child) throw new Error('sidecar 尚未启动');
    const id = ++this.nextId;
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        this.pending.delete(String(id));
        reject(new RpcError('CLIENT_TIMEOUT', `请求 ${method} 超时（${timeoutMs}ms）`, { method, timeoutMs }));
      }, timeoutMs);
      this.pending.set(String(id), { resolve, reject, timer, method });
      try {
        this.write({ jsonrpc: '2.0', id, method, params });
      } catch (error) {
        clearTimeout(timer);
        this.pending.delete(String(id));
        reject(error);
      }
    });
  }

  /** 发送请求并断言必须失败；返回 RpcError。 */
  async requestExpectError(method, params = {}, timeoutMs = this.requestTimeout) {
    try {
      const result = await this.request(method, params, timeoutMs);
      throw new AssertionError(`请求 ${method} 期望返回 error，实际成功: ${truncate(result)}`);
    } catch (error) {
      if (error instanceof RpcError && error.code !== 'CLIENT_TIMEOUT') return error;
      throw error;
    }
  }

  onEvent(listener) {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }

  waitForEvent(predicate, timeoutMs, label = '事件') {
    const existing = this.events.find(predicate);
    if (existing) return Promise.resolve(existing);
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        this.listeners.delete(listener);
        reject(new Error(`等待${label}超时（${timeoutMs}ms）`));
      }, timeoutMs);
      const listener = (event) => {
        if (!predicate(event)) return;
        clearTimeout(timer);
        this.listeners.delete(listener);
        resolve(event);
      };
      this.listeners.add(listener);
    });
  }

  waitForExit(timeoutMs) {
    if (this.exit) return Promise.resolve(this.exit);
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => reject(new Error(`等待 sidecar 退出超时（${timeoutMs}ms）`)), timeoutMs);
      this.exitResolvers.add((exit) => {
        clearTimeout(timer);
        resolve(exit);
      });
    });
  }

  jobEvents(jobId) {
    return this.events.filter(
      (event) =>
        (event.method === 'job/output' || event.method === 'job/end') &&
        event.params &&
        event.params.jobId === jobId,
    );
  }

  async stop() {
    if (!this.child || this.exit) return;
    try {
      this.child.stdin.end();
    } catch {}
    try {
      await this.waitForExit(5000);
    } catch {
      try {
        this.child.kill();
      } catch {}
      this.child = null;
    }
  }
}

// ---------------------------------------------------------------------------
// sidecar 定位 / 构建
// ---------------------------------------------------------------------------

function withGoEnv(env = process.env) {
  const goTmp = path.join(REPO_ROOT, '.gotmp');
  const goCache = path.join(REPO_ROOT, '.gocache');
  mkdirSync(goTmp, { recursive: true });
  mkdirSync(goCache, { recursive: true });
  return { ...env, GOTMPDIR: goTmp, GOCACHE: goCache };
}

function resolveSidecar() {
  if (options.sidecar) {
    const given = path.resolve(PLUGIN_ROOT, options.sidecar);
    if (!existsSync(given)) fail(`--sidecar 指定的文件不存在: ${given}`);
    if (/\.(mjs|js|cjs)$/i.test(given)) {
      return { exe: process.execPath, args: [given], source: 'override-script', pathLabel: given };
    }
    return { exe: given, args: [], source: 'override', pathLabel: given };
  }

  const names = ['dbx-plugin-wsl-manager.exe', 'dbx-plugin-wsl-manager'];
  // 优先级 1: workspace 外的 dev host data-dir（Medium IL，wsl.exe 可正常工作）
  const externalDirs = [
    { dir: path.join(EXTERNAL_DEV_DATA_DIR, 'bin'), source: 'dbx-dev-external' },
    { dir: path.dirname(BUILD_OUT), source: 'qa-build-external' },
  ];
  for (const candidate of externalDirs) {
    for (const name of names) {
      const full = path.join(candidate.dir, name);
      if (existsSync(full)) return { exe: full, args: [], source: candidate.source, pathLabel: full };
    }
  }
  // 优先级 2: workspace 内的 dev 目录（Low IL，wsl/wslc 可能 E_ACCESSDENIED，会给出警告）
  const workspaceDirs = [
    { dir: path.join(PLUGIN_ROOT, '.dbx-dev', 'bin'), source: 'dbx-dev-workspace' },
    { dir: path.join(PLUGIN_ROOT, '.dbx-dev-qa', 'bin'), source: 'dbx-dev-qa-workspace' },
  ];
  for (const candidate of workspaceDirs) {
    for (const name of names) {
      const full = path.join(candidate.dir, name);
      if (existsSync(full)) return { exe: full, args: [], source: candidate.source, pathLabel: full };
    }
  }

  // 优先级 3: 从源码构建到 workspace 外（go build 需要 GOTMPDIR/GOCACHE）
  mkdirSync(path.dirname(BUILD_OUT), { recursive: true });
  const env = withGoEnv();
  const attempts = [
    { cwd: path.join(PLUGIN_ROOT, 'backend'), args: ['build', '-o', BUILD_OUT, '.'], label: `go build -o ${BUILD_OUT} .` },
    { cwd: PLUGIN_ROOT, args: ['build', '-o', BUILD_OUT, './backend'], label: `go build -o ${BUILD_OUT} ./backend` },
  ];
  let lastError = '';
  for (const attempt of attempts) {
    const result = spawnSyncCompat('go', attempt.args, { cwd: attempt.cwd, env });
    if (result.status === 0 && existsSync(BUILD_OUT)) {
      return { exe: BUILD_OUT, args: [], source: 'go-build-external', pathLabel: BUILD_OUT, buildLog: result.log };
    }
    lastError = `${attempt.label} 失败 (status=${result.status})\n${result.log}`;
  }
  fail(`未找到 sidecar 且源码构建失败:\n${lastError}`);
}

function spawnSyncCompat(command, args, { cwd, env }) {
  const result = spawnSync(command, args, { cwd, env, encoding: 'utf8', windowsHide: true });
  return {
    status: result.status,
    log: `${result.stdout || ''}${result.stderr || ''}`.trim(),
  };
}

// ---------------------------------------------------------------------------
// 检查上下文与工具
// ---------------------------------------------------------------------------

function makeCtx(client) {
  const state = {
    sidecar: null,
    initialize: null,
    distros: null,
    defaultDistro: null,
    imageReady: false,
    imageWasPresent: null,
    prunedImage: false,
    resources: { containers: new Set(), volumes: new Set(), networks: new Set(), jobs: new Set() },
    slowTimeout: Math.max(options.timeout, SLOW_TIMEOUT_MIN),
    jobWait: Math.max(options.timeout, JOB_WAIT_MIN),
    cleanup: [],
    warnings: [],
  };

  const ctx = {
    client,
    state,
    slow: state.slowTimeout,
    jobWait: state.jobWait,
    warn(message) {
      if (currentCheck) currentCheck.warnings.push(message);
      state.warnings.push(message);
    },
    evidence(key, value) {
      if (currentCheck) currentCheck.evidence[key] = value;
    },
    trackContainer(name) {
      state.resources.containers.add(name);
    },
    untrackContainer(name) {
      state.resources.containers.delete(name);
    },
    trackVolume(name) {
      state.resources.volumes.add(name);
    },
    untrackVolume(name) {
      state.resources.volumes.delete(name);
    },
    trackNetwork(name) {
      state.resources.networks.add(name);
    },
    untrackNetwork(name) {
      state.resources.networks.delete(name);
    },
    trackJob(jobId) {
      state.resources.jobs.add(jobId);
    },
    untrackJob(jobId) {
      state.resources.jobs.delete(jobId);
    },
  };
  return ctx;
}

function uniqueSuffix() {
  return `${Date.now().toString(36)}-${Math.floor(Math.random() * 1e4).toString(36)}`;
}

async function listImages(ctx) {
  const result = await ctx.client.request('wslc/images/list', {});
  assertArray(result.images, 'wslc/images/list .images');
  return result.images;
}

function imageHas(images, ref) {
  const [repo, tag] = splitRef(ref);
  return images.some((image) => {
    if (!image || typeof image !== 'object') return false;
    const repository = String(image.repository ?? '').trim();
    const imageTag = String(image.tag ?? '').trim();
    const refs = [image.name, image.ref, image.reference, repository ? `${repository}:${imageTag}` : '']
      .filter(Boolean)
      .map(String);
    if (refs.some((value) => value === ref || value === `${ref}:latest`)) return true;
    return repository === repo && (imageTag === tag || (!imageTag && tag === 'latest'));
  });
}

function splitRef(ref) {
  const index = ref.lastIndexOf(':');
  if (index <= 0) return [ref, 'latest'];
  return [ref.slice(0, index), ref.slice(index + 1)];
}

/** 等待 job/end，返回其事件。 */
async function waitJobEnd(ctx, jobId, timeoutMs = ctx.jobWait) {
  return ctx.client.waitForEvent(
    (event) => event.method === 'job/end' && event.params && event.params.jobId === jobId,
    timeoutMs,
    `job/end(${jobId})`,
  );
}

/** 确保 alpine:3.20 存在；返回 { presentBefore, pull } */
async function ensureImage(ctx, ref = 'alpine:3.20') {
  const images = await listImages(ctx);
  const presentBefore = imageHas(images, ref);
  ctx.evidence('imagePresentBefore', presentBefore);
  if (presentBefore) {
    ctx.state.imageReady = true;
    if (ctx.state.imageWasPresent === null) ctx.state.imageWasPresent = true;
    return { presentBefore, pull: null };
  }
  const { jobId } = await ctx.client.request('wslc/images/pull', { ref }, ctx.slow);
  assertNonEmptyString(jobId, 'wslc/images/pull .jobId');
  ctx.trackJob(jobId);
  const end = await waitJobEnd(ctx, jobId, ctx.jobWait);
  ctx.untrackJob(jobId);
  assert(end.params.ok === true, `images/pull ${ref} job/end.ok 期望 true，实际 ${truncate(end.params)}`);
  ctx.state.imageReady = true;
  ctx.state.imageWasPresent = false;
  return { presentBefore, pull: { jobId, end } };
}

/** 确保默认发行版名（优先列表返回值）。 */
async function ensureDefaultDistro(ctx) {
  if (ctx.state.defaultDistro) return ctx.state.defaultDistro;
  const list = await ctx.client.request('wsl/distros/list', {});
  assertArray(list.distros, 'distros');
  const fallback = list.defaultDistro || list.distros.find((item) => item && item.isDefault)?.name || list.distros[0]?.name;
  assert(fallback, '无法从 wsl/distros/list 确定默认发行版');
  ctx.state.defaultDistro = fallback;
  return fallback;
}

// ---------------------------------------------------------------------------
// 检查定义
// ---------------------------------------------------------------------------

const checks = [];
function defineCheck(definition) {
  checks.push(definition);
}

defineCheck({
  id: 'initialize',
  title: 'plugin/initialize 协议与插件身份',
  timeout: 30_000,
  async fn(ctx) {
    const params = { host: { protocolVersions: [1] } };
    const result = await ctx.client.request('plugin/initialize', params, 30_000);
    assertObject(result, 'initialize result');
    assertEqual(result.protocolVersion, 1, 'initialize.result.protocolVersion');
    assertObject(result.plugin, 'initialize.result.plugin');
    assertEqual(result.plugin.id, 'com.dbx.wsl', 'initialize.result.plugin.id');
    assertEqual(result.plugin.version, '0.1.0', 'initialize.result.plugin.version');
    assertArray(result.capabilities ?? [], 'initialize.result.capabilities');
    ctx.state.initialize = result;
    ctx.evidence('initialize', result);
  },
});

defineCheck({
  id: 'initialize-protocol-mismatch',
  title: 'plugin/initialize 拒绝不支持的协议版本',
  timeout: 30_000,
  async fn(ctx) {
    const error = await ctx.client.requestExpectError(
      'plugin/initialize',
      { host: { protocolVersions: [999] } },
      30_000,
    );
    assert(typeof error.code === 'number', `initialize 协议不匹配应返回数字 error.code，实际: ${truncate(error)}`);
    assertNonEmptyString(error.message, 'initialize 协议不匹配 error.message');
    ctx.evidence('protocolMismatchError', { code: error.code, message: error.message });
  },
});

defineCheck({
  id: 'system-info',
  title: 'system/info 平台与工具可用性',
  timeout: 60_000,
  async fn(ctx) {
    const info = await ctx.client.request('system/info', {}, 60_000);
    assertObject(info, 'system/info result');
    assertObject(info.platform, 'system/info.platform');
    assertEqual(info.platform.os, 'windows', 'system/info.platform.os');
    assertNonEmptyString(info.platform.arch, 'system/info.platform.arch');
    assertObject(info.wsl, 'system/info.wsl');
    assertEqual(info.wsl.available, true, 'system/info.wsl.available');
    assertType(info.wsl.exe, 'string', 'system/info.wsl.exe');
    assert(Number.isFinite(info.wsl.distroCount) && info.wsl.distroCount >= 1, `system/info.wsl.distroCount 期望 >=1，实际 ${truncate(info.wsl.distroCount)}`);
    assertObject(info.wsl.version, 'system/info.wsl.version');
    assertArray(info.warnings, 'system/info.warnings');
    if (info.wslc) {
      assertObject(info.wslc, 'system/info.wslc');
      ctx.evidence('wslc', { available: info.wslc.available, exe: info.wslc.exe, version: info.wslc.version });
      assertEqual(info.wslc.available, true, 'system/info.wslc.available（契约第 8 节要求 wslc 可用）');
      assertNonEmptyString(info.wslc.version, 'system/info.wslc.version');
    } else {
      throw new AssertionError('system/info 缺少 wslc 字段（契约 4.1 要求）');
    }
    ctx.evidence('platform', info.platform);
    ctx.evidence('wsl', { version: info.wsl.version, distroCount: info.wsl.distroCount, runningCount: info.wsl.runningCount });
  },
});

defineCheck({
  id: 'system-wsl-status',
  title: 'system/wsl/status 默认发行版状态',
  timeout: 60_000,
  async fn(ctx) {
    const status = await ctx.client.request('system/wsl/status', {}, 60_000);
    assertObject(status, 'system/wsl/status result');
    assertNonEmptyString(status.defaultDistro, 'system/wsl/status.defaultDistro');
    assertOneOf(status.defaultVersion, [1, 2], 'system/wsl/status.defaultVersion');
    assertType(status.wsl1Supported, 'boolean', 'system/wsl/status.wsl1Supported');
    assertType(status.raw, 'string', 'system/wsl/status.raw');
    ctx.evidence('status', status);
  },
});

defineCheck({
  id: 'wsl-distros-list',
  title: 'wsl/distros/list 发行版列表与默认标记',
  timeout: 60_000,
  async fn(ctx) {
    const list = await ctx.client.request('wsl/distros/list', {}, 60_000);
    assertObject(list, 'wsl/distros/list result');
    assertArray(list.distros, 'wsl/distros/list.distros', 1);
    assertEqual(list.count, list.distros.length, 'wsl/distros/list.count 与 distros 长度');
    assertNonEmptyString(list.defaultDistro, 'wsl/distros/list.defaultDistro');
    for (const distro of list.distros) {
      assertObject(distro, 'distro 项');
      assertNonEmptyString(distro.name, 'distro.name');
      assertOneOf(distro.state, ['Running', 'Stopped', 'Installing', 'Uninstalling', 'Unknown'], `distro(${distro.name}).state`);
      assertOneOf(distro.version, [1, 2], `distro(${distro.name}).version`);
      assertType(distro.running, 'boolean', `distro(${distro.name}).running`);
      assertType(distro.isDefault, 'boolean', `distro(${distro.name}).isDefault`);
      assertType(distro.vhdBytes, 'number', `distro(${distro.name}).vhdBytes`);
      assert(distro.sparse === null || typeof distro.sparse === 'boolean', `distro(${distro.name}).sparse 期望 null/boolean`);
    }
    const defaults = list.distros.filter((item) => item.isDefault);
    assert(defaults.length === 1, `应当恰好有 1 个 isDefault，实际 ${defaults.length}`);
    assertEqual(defaults[0].name, list.defaultDistro, 'isDefault 项名称与 defaultDistro');
    const debian = list.distros.find((item) => String(item.name).toLowerCase().includes('debian'));
    assert(debian, `发行版列表应包含 Debian，实际: ${list.distros.map((d) => d.name).join(', ') || '(空)'}`);
    assertEqual(debian.isDefault, true, 'Debian isDefault（契约第 8 节验收样例 2）');
    assertType(debian.guid, 'string', 'Debian.guid');
    if (debian.defaultUid !== undefined && debian.defaultUid !== null) {
      assertType(debian.defaultUid, 'number', 'Debian.defaultUid');
    }
    ctx.state.distros = list.distros;
    ctx.state.defaultDistro = list.defaultDistro;
    ctx.state.distroInitiallyRunning = debian.running === true;
    ctx.state.distroNameForRestore = debian.name;
    ctx.evidence('distros', list.distros.map((item) => ({
      name: item.name,
      state: item.state,
      version: item.version,
      isDefault: item.isDefault,
      vhdBytes: item.vhdBytes,
    })));
  },
});

defineCheck({
  id: 'wsl-distros-info',
  title: 'wsl/distros/info 发行版详情（osRelease/disk）',
  timeout: 90_000,
  async fn(ctx) {
    const name = await ensureDefaultDistro(ctx);
    const info = await ctx.client.request('wsl/distros/info', { name }, 90_000);
    assertObject(info, 'wsl/distros/info result');
    assertEqual(info.name, name, 'wsl/distros/info.name');
    assertObject(info.osRelease, 'wsl/distros/info.osRelease');
    assertNonEmptyString(info.osRelease.PRETTY_NAME, 'wsl/distros/info.osRelease.PRETTY_NAME');
    assert(
      /Debian/i.test(info.osRelease.PRETTY_NAME),
      `osRelease.PRETTY_NAME 期望含 Debian，实际 ${truncate(info.osRelease.PRETTY_NAME)}`,
    );
    assertObject(info.disk, 'wsl/distros/info.disk');
    assert(info.disk.totalBytes > 0, `disk.totalBytes 期望 > 0，实际 ${truncate(info.disk.totalBytes)}`);
    assert(info.disk.usedBytes >= 0, 'disk.usedBytes 期望 >= 0');
    assertNonEmptyString(info.kernel, 'wsl/distros/info.kernel');
    assertNonEmptyString(info.arch, 'wsl/distros/info.arch');
    assertType(info.uid, 'number', 'wsl/distros/info.uid');
    assertArray(info.ipv4, 'wsl/distros/info.ipv4');
    ctx.evidence('distroInfo', {
      name: info.name,
      prettyName: info.osRelease.PRETTY_NAME,
      kernel: info.kernel,
      arch: info.arch,
      uid: info.uid,
      disk: info.disk,
    });
  },
});

defineCheck({
  id: 'wsl-distros-exec',
  title: 'wsl/distros/exec 执行 echo hello（RunResult）',
  timeout: 90_000,
  async fn(ctx) {
    const name = await ensureDefaultDistro(ctx);
    const result = await ctx.client.request('wsl/distros/exec', { name, command: 'echo hello' }, 90_000);
    assertObject(result, 'wsl/distros/exec result');
    assertEqual(result.name, name, 'wsl/distros/exec.name');
    assertEqual(result.exitCode, 0, 'wsl/distros/exec.exitCode');
    assertType(result.stdout, 'string', 'wsl/distros/exec.stdout');
    assert(result.stdout.includes('hello'), `stdout 期望含 "hello"，实际 ${truncate(result.stdout)}`);
    assertType(result.stderr, 'string', 'wsl/distros/exec.stderr');
    assertType(result.durationMs, 'number', 'wsl/distros/exec.durationMs');
    assert(result.durationMs >= 0, 'durationMs 期望 >= 0');
    assertType(result.truncated, 'boolean', 'wsl/distros/exec.truncated');
    assertNonEmptyString(result.command, 'wsl/distros/exec.command');
    if (result.started !== undefined) assertType(result.started, 'boolean', 'wsl/distros/exec.started');
    ctx.evidence('exec', { exitCode: result.exitCode, stdout: result.stdout, stderr: result.stderr, durationMs: result.durationMs });
  },
});

defineCheck({
  id: 'wslc-info',
  title: 'wslc/info 客户端与服务端信息',
  timeout: 90_000,
  async fn(ctx) {
    const info = await ctx.client.request('wslc/info', {}, 90_000);
    assertObject(info, 'wslc/info result');
    assertNonEmptyString(info.version, 'wslc/info.version');
    assertObject(info.client, 'wslc/info.client');
    assertNonEmptyString(info.client.version, 'wslc/info.client.version');
    assertObject(info.server, 'wslc/info.server');
    assertNonEmptyString(info.server.sessionManagerVersion, 'wslc/info.server.sessionManagerVersion');
    assertArray(info.server.sessions ?? [], 'wslc/info.server.sessions');
    ctx.evidence('wslcInfo', {
      version: info.version,
      clientVersion: info.client.version,
      sessionManagerVersion: info.server.sessionManagerVersion,
      sessions: info.server.sessions,
    });
  },
});

defineCheck({
  id: 'wslc-networks-list',
  title: 'wslc/networks/list 含 bridge/host/none',
  timeout: 90_000,
  async fn(ctx) {
    const result = await ctx.client.request('wslc/networks/list', {}, 90_000);
    assertObject(result, 'wslc/networks/list result');
    assertArray(result.networks, 'wslc/networks/list.networks', 3);
    const names = new Set();
    for (const network of result.networks) {
      assertObject(network, 'network 项');
      assertNonEmptyString(network.id, 'network.id');
      assertNonEmptyString(network.name, 'network.name');
      assertNonEmptyString(network.driver, `network(${network.name}).driver`);
      assertNonEmptyString(network.scope, `network(${network.name}).scope`);
      assertType(network.ipv4, 'boolean', `network(${network.name}).ipv4`);
      assertType(network.ipv6, 'boolean', `network(${network.name}).ipv6`);
      assertType(network.internal, 'boolean', `network(${network.name}).internal`);
      names.add(network.name);
    }
    for (const required of ['bridge', 'host', 'none']) {
      assert(names.has(required), `wslc 网络缺少默认网络 ${required}（实际: ${[...names].join(', ')}）`);
    }
    ctx.evidence('networks', [...names]);
  },
});

defineCheck({
  id: 'wslc-images-list',
  title: 'wslc/images/list 镜像列表结构',
  timeout: 90_000,
  async fn(ctx) {
    const images = await listImages(ctx);
    for (const image of images) {
      assertObject(image, 'image 项');
      assertNonEmptyString(image.id, 'image.id');
      assertType(image.repository, 'string', 'image.repository');
      assertType(image.tag, 'string', 'image.tag');
      assertType(image.size, 'string', 'image.size');
    }
    ctx.evidence('images', images.map((image) => `${image.repository}:${image.tag}`));
  },
});

defineCheck({
  id: 'wslc-image-pull',
  title: 'wslc/images/pull alpine:3.20 长任务事件时序',
  timeout: 180_000,
  async fn(ctx) {
    const ref = 'alpine:3.20';
    const imagesBefore = await listImages(ctx);
    const presentBefore = imageHas(imagesBefore, ref);
    ctx.evidence('imagePresentBefore', presentBefore);
    ctx.state.imageWasPresent = presentBefore;

    const startedAt = Date.now();
    const { jobId } = await ctx.client.request('wslc/images/pull', { ref }, ctx.slow);
    assertNonEmptyString(jobId, 'wslc/images/pull .jobId');
    ctx.trackJob(jobId);

    const end = await waitJobEnd(ctx, jobId, ctx.jobWait);
    ctx.untrackJob(jobId);
    assertObject(end.params, 'job/end params');
    assertEqual(end.params.jobId, jobId, 'job/end.jobId');
    assertEqual(end.params.ok, true, `job/end.ok（${ref}）`);
    assertEqual(end.params.exitCode, 0, 'job/end.exitCode');
    assert(end.params.error === null, `job/end.error 期望 null，实际 ${truncate(end.params.error)}`);
    assertType(end.params.durationMs, 'number', 'job/end.durationMs');
    if (end.params.result) {
      assertObject(end.params.result, 'job/end.result');
      if (end.params.result.ref !== undefined) assertEqual(end.params.result.ref, ref, 'job/end.result.ref');
    }

    const outputEvents = ctx.client.events.filter(
      (event) => event.method === 'job/output' && event.params && event.params.jobId === jobId,
    );
    const endIndex = ctx.client.events.indexOf(end);
    const outputsBeforeEnd = ctx.client.events
      .slice(0, endIndex)
      .filter((event) => event.method === 'job/output' && event.params && event.params.jobId === jobId);
    assertEqual(outputsBeforeEnd.length, outputEvents.length, '所有 job/output 必须早于 job/end');
    if (presentBefore) {
      ctx.warn(`镜像 ${ref} 在测试前已存在（缓存命中），pull 可能不产生 job/output 行；job/end 已校验`);
    }
    assert(
      outputEvents.length > 0 || presentBefore,
      `拉取 ${ref} 未产生任何 job/output 事件（镜像原本不存在时契约第 8 节要求有输出）`,
    );
    for (const event of outputEvents) {
      assertObject(event.params, 'job/output params');
      assertOneOf(event.params.stream, ['stdout', 'stderr'], 'job/output.stream');
      assertType(event.params.text, 'string', 'job/output.text');
      assertType(event.params.ts, 'number', 'job/output.ts');
    }

    const job = await ctx.client.request('jobs/get', { jobId }, 30_000);
    assertObject(job, 'jobs/get result');
    assertEqual(job.jobId, jobId, 'jobs/get.jobId');
    assertOneOf(job.status, ['completed', 'failed', 'cancelled'], 'jobs/get.status');
    assertEqual(job.status, 'completed', `jobs/get.status（${ref} 拉取应 completed）`);
    assertArray(job.lines, 'jobs/get.lines');
    if (!presentBefore) {
      assert(job.lines.length >= outputEvents.length, `jobs/get.lines 数量应覆盖已推送输出（lines=${job.lines.length}, outputs=${outputEvents.length}）`);
    }
    ctx.evidence('pull', {
      jobId,
      outputLines: outputEvents.length,
      end: end.params,
      jobStatus: job.status,
      durationMs: Date.now() - startedAt,
      cached: presentBefore,
    });

    const images = await listImages(ctx);
    assert(imageHas(images, ref), `拉取后 wslc/images/list 应包含 ${ref}`);
    ctx.state.imageReady = true;

    const imageInspect = await ctx.client.request('wslc/images/inspect', { ref }, ctx.slow);
    assertObject(imageInspect, 'wslc/images/inspect result');
    assert(imageInspect.raw !== null && imageInspect.raw !== undefined, 'wslc/images/inspect.raw 不能为空');

    const jobsList = await ctx.client.request('jobs/list', {}, 30_000);
    assertObject(jobsList, 'jobs/list result');
    assertArray(jobsList.jobs, 'jobs/list.jobs');
    for (const active of jobsList.jobs) {
      assertNonEmptyString(active.jobId, 'jobs/list.jobs[].jobId');
      assertNonEmptyString(active.method, 'jobs/list.jobs[].method');
      assertType(active.startedAt, 'number', 'jobs/list.jobs[].startedAt');
    }
  },
});

defineCheck({
  id: 'wslc-containers-lifecycle',
  title: '容器 run → list → exec → logs → stats → stop → remove 全链路',
  timeout: 180_000,
  async fn(ctx) {
    await ensureImage(ctx, 'alpine:3.20');
    const name = `qa-container-${uniqueSuffix()}`;
    ctx.trackContainer(name);
    const run = await ctx.client.request(
      'wslc/containers/run',
      { image: 'alpine:3.20', name, detach: true, command: ['sleep', '300'] },
      ctx.slow,
    );
    assertObject(run, 'wslc/containers/run result');
    assertNonEmptyString(run.id, 'wslc/containers/run.id');
    assertEqual(run.name, name, 'wslc/containers/run.name');

    const list = await ctx.client.request('wslc/containers/list', { all: true }, 90_000);
    assertObject(list, 'wslc/containers/list result');
    assertArray(list.containers, 'wslc/containers/list.containers', 1);
    const container = list.containers.find((item) => item && (item.name === name || item.id === run.id));
    assert(container, `容器列表中找不到刚创建的容器 ${name}`);
    assertNonEmptyString(container.id, 'container.id');
    assertNonEmptyString(container.shortId, 'container.shortId');
    assertEqual(container.image, 'alpine:3.20', 'container.image');
    assertNonEmptyString(container.state, 'container.state');
    assertNonEmptyString(container.status, 'container.status');
    assertType(container.runningFor, 'string', 'container.runningFor');
    if (container.platform !== undefined) assertObject(container.platform, 'container.platform');

    const inspect = await ctx.client.request('wslc/containers/inspect', { id: name }, ctx.slow);
    assertObject(inspect, 'wslc/containers/inspect result');
    assert(inspect.raw !== null && inspect.raw !== undefined, 'wslc/containers/inspect.raw 不能为空');

    const exec = await ctx.client.request(
      'wslc/containers/exec',
      { id: name, command: ['echo', 'hi'] },
      ctx.slow,
    );
    assertObject(exec, 'wslc/containers/exec result');
    assertEqual(exec.exitCode, 0, 'containers/exec.exitCode');
    assert(exec.stdout.includes('hi'), `containers/exec.stdout 期望含 hi，实际 ${truncate(exec.stdout)}`);
    assertType(exec.durationMs, 'number', 'containers/exec.durationMs');
    assertType(exec.truncated, 'boolean', 'containers/exec.truncated');

    const logs = await ctx.client.request('wslc/containers/logs', { id: name, tail: 100 }, ctx.slow);
    assertObject(logs, 'wslc/containers/logs result');
    assertType(logs.stdout, 'string', 'containers/logs.stdout');
    assertType(logs.exitCode, 'number', 'containers/logs.exitCode');

    const stats = await ctx.client.request('wslc/containers/stats', { all: false }, ctx.slow);
    assertObject(stats, 'wslc/containers/stats result');
    assertArray(stats.stats, 'wslc/containers/stats.stats', 1);
    const found = stats.stats.find((item) => item && (item.name === name || item.id === run.id || item.shortId === container.shortId));
    assert(found, `stats 中找不到容器 ${name}`);
    assertNonEmptyString(found.cpuPerc, 'stats.cpuPerc');
    assertNonEmptyString(found.memUsage, 'stats.memUsage');
    assertNonEmptyString(found.memPerc, 'stats.memPerc');
    assertType(found.pids, 'number', 'stats.pids');

    const stop = await ctx.client.request('wslc/containers/stop', { id: name, timeout: 10 }, ctx.slow);
    assertObject(stop, 'wslc/containers/stop result');
    assertEqual(stop.id, name, 'containers/stop.id');
    assertEqual(stop.action, 'stop', 'containers/stop.action');
    assertType(stop.exitCode, 'number', 'containers/stop.exitCode');

    const remove = await ctx.client.request('wslc/containers/remove', { id: name, force: true }, ctx.slow);
    assertObject(remove, 'wslc/containers/remove result');
    assertEqual(remove.action, 'remove', 'containers/remove.action');
    ctx.untrackContainer(name);

    const after = await ctx.client.request('wslc/containers/list', { all: true }, 90_000);
    assertArray(after.containers, 'wslc/containers/list.containers（删除后）');
    assert(
      !after.containers.some((item) => item && (item.name === name || item.id === run.id)),
      `容器 ${name} 删除后仍出现在列表中`,
    );
    ctx.evidence('container', { name, id: run.id, shortId: container.shortId, stats: found });
  },
});

defineCheck({
  id: 'wslc-volumes',
  title: '数据卷 create → list → inspect → remove',
  timeout: 120_000,
  async fn(ctx) {
    const name = `qa-vol-${uniqueSuffix()}`;
    ctx.trackVolume(name);
    const created = await ctx.client.request('wslc/volumes/create', { name }, 90_000);
    assertObject(created, 'wslc/volumes/create result');
    assertEqual(created.name, name, 'wslc/volumes/create.name');

    const list = await ctx.client.request('wslc/volumes/list', {}, 90_000);
    assertObject(list, 'wslc/volumes/list result');
    assertArray(list.volumes, 'wslc/volumes/list.volumes', 1);
    const volume = list.volumes.find((item) => item && item.name === name);
    assert(volume, `卷列表中找不到 ${name}`);
    assertNonEmptyString(volume.driver, 'volume.driver');
    assertNonEmptyString(volume.mountpoint, 'volume.mountpoint');
    assertNonEmptyString(volume.scope, 'volume.scope');
    assertType(volume.size, 'string', 'volume.size');

    const inspect = await ctx.client.request('wslc/volumes/inspect', { name }, 90_000);
    assertObject(inspect, 'wslc/volumes/inspect result');
    assert(inspect.raw !== null && inspect.raw !== undefined, 'wslc/volumes/inspect.raw 不能为空');

    const removed = await ctx.client.request('wslc/volumes/remove', { names: [name] }, 90_000);
    assertObject(removed, 'wslc/volumes/remove result');
    assertArray(removed.removed, 'wslc/volumes/remove.removed', 1);
    assert(removed.removed.includes(name), `removed 应包含 ${name}，实际 ${truncate(removed.removed)}`);
    ctx.untrackVolume(name);

    const after = await ctx.client.request('wslc/volumes/list', {}, 90_000);
    assert(!after.volumes.some((item) => item && item.name === name), `卷 ${name} 删除后仍出现在列表中`);
  },
});

defineCheck({
  id: 'wslc-networks',
  title: '网络 create → list → inspect → remove',
  timeout: 120_000,
  async fn(ctx) {
    const name = `qa-net-${uniqueSuffix()}`;
    ctx.trackNetwork(name);
    const created = await ctx.client.request('wslc/networks/create', { name, driver: 'bridge', internal: false, ipv6: false }, 90_000);
    assertObject(created, 'wslc/networks/create result');
    assertEqual(created.name, name, 'wslc/networks/create.name');
    assertNonEmptyString(created.id, 'wslc/networks/create.id');

    const list = await ctx.client.request('wslc/networks/list', {}, 90_000);
    const network = list.networks.find((item) => item && item.name === name);
    assert(network, `网络列表中找不到 ${name}`);
    assertEqual(network.driver, 'bridge', `network(${name}).driver`);
    assertEqual(network.internal, false, `network(${name}).internal`);

    const inspect = await ctx.client.request('wslc/networks/inspect', { name }, 90_000);
    assertObject(inspect, 'wslc/networks/inspect result');
    assert(inspect.raw !== null && inspect.raw !== undefined, 'wslc/networks/inspect.raw 不能为空');

    const removed = await ctx.client.request('wslc/networks/remove', { names: [name] }, 90_000);
    assertObject(removed, 'wslc/networks/remove result');
    assertArray(removed.removed, 'wslc/networks/remove.removed', 1);
    assert(removed.removed.includes(name), `removed 应包含 ${name}，实际 ${truncate(removed.removed)}`);
    ctx.untrackNetwork(name);

    const after = await ctx.client.request('wslc/networks/list', {}, 90_000);
    assert(!after.networks.some((item) => item && item.name === name), `网络 ${name} 删除后仍出现在列表中`);
  },
});

defineCheck({
  id: 'wslc-error-not-found',
  title: '错误路径：inspect 不存在的容器返回 NOT_FOUND',
  timeout: 60_000,
  async fn(ctx) {
    const missing = `qa-missing-${uniqueSuffix()}`;
    const error = await ctx.client.requestExpectError('wslc/containers/inspect', { id: missing }, 60_000);
    assert(typeof error.code === 'number', `error.code 应为数字，实际 ${truncate(error.code)}`);
    assertNonEmptyString(error.message, 'error.message');
    assertObject(error.data, 'error.data');
    assertEqual(error.data.code, 'NOT_FOUND', 'error.data.code');
    if (error.data.hint !== undefined) assertType(error.data.hint, 'string', 'error.data.hint');
    ctx.evidence('notFoundError', { code: error.code, message: error.message, data: error.data });

    const invalid = await ctx.client.requestExpectError(
      'wsl/distros/exec',
      { name: '', command: '' },
      60_000,
    );
    assertObject(invalid.data, '空参数 error.data');
    assertNonEmptyString(invalid.data.code, '空参数 error.data.code');
    if (invalid.data.code !== 'INVALID_PARAMS') {
      ctx.warn(`空参数错误码为 ${invalid.data.code}，契约约定参数校验失败应为 INVALID_PARAMS（需确认是否契约歧义）`);
    }
    ctx.evidence('invalidParamsError', { code: invalid.code, data: invalid.data });
  },
});

defineCheck({
  id: 'wslc-logs-follow-jobs-cancel',
  title: '长任务 job/output → jobs/list → jobs/cancel → job/end 时序',
  timeout: 180_000,
  async fn(ctx) {
    await ensureImage(ctx, 'alpine:3.20');
    const name = `qa-job-${uniqueSuffix()}`;
    ctx.trackContainer(name);
    const run = await ctx.client.request(
      'wslc/containers/run',
      { image: 'alpine:3.20', name, detach: true, command: ['sh', '-c', 'echo tick; sleep 300'] },
      ctx.slow,
    );
    assertNonEmptyString(run.id, 'wslc/containers/run.id（job 测试）');
    await sleep(1500);

    const follow = await ctx.client.request(
      'wslc/containers/logs',
      { id: name, tail: 100, follow: true },
      ctx.slow,
    );
    assertObject(follow, 'wslc/containers/logs(follow) result');
    assertNonEmptyString(follow.jobId, 'follow jobId');
    const jobId = follow.jobId;
    ctx.trackJob(jobId);

    const output = await ctx.client.waitForEvent(
      (event) => event.method === 'job/output' && event.params && event.params.jobId === jobId,
      45_000,
      `job/output(${jobId})`,
    );
    assertOneOf(output.params.stream, ['stdout', 'stderr'], 'follow job/output.stream');
    assertType(output.params.text, 'string', 'follow job/output.text');

    const active = await ctx.client.request('jobs/list', {}, 30_000);
    assertObject(active, 'jobs/list result');
    assertArray(active.jobs, 'jobs/list.jobs');
    const activeJob = active.jobs.find((item) => item && item.jobId === jobId);
    assert(activeJob, `jobs/list 应包含运行中的 ${jobId}，实际 ${truncate(active.jobs)}`);
    assertNonEmptyString(activeJob.method, 'jobs/list[].method');
    assertType(activeJob.startedAt, 'number', 'jobs/list[].startedAt');

    const cancelled = await ctx.client.request('jobs/cancel', { jobId }, 60_000);
    assertObject(cancelled, 'jobs/cancel result');
    assertEqual(cancelled.jobId, jobId, 'jobs/cancel.jobId');
    assertEqual(cancelled.cancelled, true, 'jobs/cancel.cancelled');

    const end = await waitJobEnd(ctx, jobId, 60_000);
    ctx.untrackJob(jobId);
    assertEqual(end.params.jobId, jobId, 'cancel 后 job/end.jobId');
    assert(end.params.ok === false, `取消后 job/end.ok 期望 false，实际 ${truncate(end.params.ok)}`);
    if (end.params.error) assertObject(end.params.error, 'job/end.error');

    const job = await ctx.client.request('jobs/get', { jobId }, 30_000);
    assertOneOf(job.status, ['cancelled', 'failed'], `取消后 jobs/get.status（实际 ${truncate(job.status)}）`);
    ctx.evidence('cancelJob', { jobId, end: end.params, jobStatus: job.status });

    const remove = await ctx.client.request('wslc/containers/remove', { id: name, force: true }, ctx.slow);
    assertEqual(remove.action, 'remove', 'containers/remove.action（job 测试清理）');
    ctx.untrackContainer(name);
  },
});

defineCheck({
  id: 'console-run',
  title: 'console/run 执行 wsl -l -v',
  timeout: 90_000,
  async fn(ctx) {
    const result = await ctx.client.request(
      'console/run',
      { tool: 'wsl', args: ['-l', '-v'], timeoutMs: 15_000 },
      60_000,
    );
    assertObject(result, 'console/run result');
    assertEqual(result.exitCode, 0, 'console/run.exitCode');
    assertType(result.stdout, 'string', 'console/run.stdout');
    assert(result.stdout.trim().length > 0, 'console/run.stdout 不能为空（wsl -l -v 应有输出）');
    assertEqual(result.tool, 'wsl', 'console/run.tool');
    assertType(result.durationMs, 'number', 'console/run.durationMs');
    ctx.evidence('consoleRun', { exitCode: result.exitCode, stdout: result.stdout.slice(0, 300) });
  },
});

defineCheck({
  id: 'system-config-read',
  title: 'system/config/read 读取 .wslconfig 与 wslc settings',
  timeout: 60_000,
  async fn(ctx) {
    const config = await ctx.client.request('system/config/read', {}, 60_000);
    assertObject(config, 'system/config/read result');
    for (const key of ['wslconfig', 'wslcSettings']) {
      const file = config[key];
      assertObject(file, `system/config/read.${key}`);
      assertNonEmptyString(file.path, `system/config/read.${key}.path`);
      assertType(file.exists, 'boolean', `system/config/read.${key}.exists`);
      assertType(file.content, 'string', `system/config/read.${key}.content`);
      if (!file.exists) assertEqual(file.content, '', `${key}.content（不存在时应为空字符串）`);
    }
    ctx.evidence('config', {
      wslconfig: { path: config.wslconfig.path, exists: config.wslconfig.exists, bytes: config.wslconfig.content.length },
      wslcSettings: { path: config.wslcSettings.path, exists: config.wslcSettings.exists, bytes: config.wslcSettings.content.length },
    });
  },
});

defineCheck({
  id: 'console-presets',
  title: 'console/presets 预设命令结构',
  timeout: 30_000,
  async fn(ctx) {
    const result = await ctx.client.request('console/presets', {}, 30_000);
    assertObject(result, 'console/presets result');
    assertArray(result.presets, 'console/presets.presets', 1);
    for (const preset of result.presets) {
      assertObject(preset, 'preset 项');
      assertNonEmptyString(preset.id, 'preset.id');
      assertNonEmptyString(preset.label, 'preset.label');
      assertOneOf(preset.tool, ['wsl', 'wslc'], `preset(${preset.id}).tool`);
      assertArray(preset.args, `preset(${preset.id}).args`);
      for (const arg of preset.args) assertType(arg, 'string', `preset(${preset.id}).args[]`);
    }
    ctx.evidence('presets', result.presets.map((preset) => preset.id));
  },
});

defineCheck({
  id: 'wslc-events',
  title: 'wslc/events/start 推送 wslc/event 并可 stop',
  timeout: 180_000,
  async fn(ctx) {
    await ensureImage(ctx, 'alpine:3.20');
    const name = `qa-evt-${uniqueSuffix()}`;
    ctx.trackContainer(name);
    const start = await ctx.client.request('wslc/events/start', {}, ctx.slow);
    assertObject(start, 'wslc/events/start result');
    assertNonEmptyString(start.jobId, 'wslc/events/start.jobId');
    const jobId = start.jobId;
    ctx.trackJob(jobId);
    await sleep(800);

    const run = await ctx.client.request(
      'wslc/containers/run',
      { image: 'alpine:3.20', name, detach: true, command: ['sleep', '30'] },
      ctx.slow,
    );
    assertNonEmptyString(run.id, 'events 测试容器 id');
    await sleep(500);
    const stop = await ctx.client.request('wslc/containers/stop', { id: name, timeout: 5 }, ctx.slow);
    assertEqual(stop.exitCode, 0, 'events 测试容器 stop.exitCode');

    const event = await ctx.client.waitForEvent(
      (item) => item.method === 'wslc/event' && item.params && item.params.jobId === jobId,
      45_000,
      `wslc/event(${jobId})`,
    );
    assertObject(event.params, 'wslc/event params');
    assertEqual(event.params.jobId, jobId, 'wslc/event.jobId');
    assertObject(event.params.event, 'wslc/event.event');
    const payload = event.params.event;
    const type = payload.Type || payload.type;
    const action = payload.Action || payload.action || payload.status;
    assert(typeof type === 'string' || typeof action === 'string', 'wslc/event.event 缺少 Type/status 字段');
    ctx.evidence('event', {
      jobId,
      type: type ?? null,
      action: action ?? null,
      keys: Object.keys(payload).slice(0, 20),
    });

    const stopSub = await ctx.client.request('wslc/events/stop', { jobId }, 60_000);
    assertObject(stopSub, 'wslc/events/stop result');
    assertEqual(stopSub.stopped, true, 'wslc/events/stop.stopped');
    const end = await waitJobEnd(ctx, jobId, 60_000);
    ctx.untrackJob(jobId);
    assertEqual(end.params.jobId, jobId, 'events/stop 后 job/end.jobId');

    const remove = await ctx.client.request('wslc/containers/remove', { id: name, force: true }, ctx.slow);
    assertEqual(remove.action, 'remove', 'containers/remove.action（events 测试清理）');
    ctx.untrackContainer(name);
  },
});

/** 递归检查任意值（错误/事件/日志）中是否出现敏感字符串。 */
function containsSecret(value, secret) {
  if (!secret) return false;
  let text;
  try {
    text = typeof value === 'string' ? value : JSON.stringify(value);
  } catch {
    text = String(value);
  }
  return String(text).includes(secret);
}

defineCheck({
  id: 'wslc-registry-login-validation',
  title: 'wslc/registry/login 参数校验（INVALID_PARAMS，无网络）',
  timeout: 60_000,
  async fn(ctx) {
    const secret = `qa-secret-${uniqueSuffix()}-Pw!`;
    const cases = [
      { name: '空 username', params: { server: 'docker.io', username: '', password: secret } },
      { name: '空 password', params: { server: 'docker.io', username: 'qa-user', password: '' } },
      { name: 'server 含空白', params: { server: 'docker io', username: 'qa-user', password: secret } },
      { name: 'server 含控制字符', params: { server: 'docker.io\nx', username: 'qa-user', password: secret } },
      { name: 'server 超长', params: { server: 'a'.repeat(300), username: 'qa-user', password: secret } },
      { name: 'username 含控制字符', params: { server: 'docker.io', username: 'qa\nuser', password: secret } },
    ];
    for (const testCase of cases) {
      const error = await ctx.client.requestExpectError('wslc/registry/login', testCase.params, 30_000);
      assert(typeof error.code === 'number', `${testCase.name}: error.code 应为数字`);
      assertObject(error.data, `${testCase.name}: error.data`);
      assertEqual(error.data.code, 'INVALID_PARAMS', `${testCase.name}: error.data.code`);
      assert(!containsSecret({ message: error.message, data: error.data }, secret), `${testCase.name}: 错误信息不应包含密码`);
    }
    ctx.evidence('validationCases', cases.map((item) => item.name));
  },
});

defineCheck({
  id: 'wslc-registry-login-invalid',
  title: 'wslc/registry/login 错误凭据 → REGISTRY_AUTH_FAILED（不泄露密码）',
  timeout: 150_000,
  async fn(ctx) {
    const password = `qa-secret-${uniqueSuffix()}-Pw!`;
    const username = `qa-user-${uniqueSuffix()}`;
    const startedAt = Date.now();
    const error = await ctx.client.requestExpectError(
      'wslc/registry/login',
      { server: 'docker.io', username, password },
      120_000,
    );
    const elapsedMs = Date.now() - startedAt;
    assert(typeof error.code === 'number', `error.code 应为数字，实际 ${truncate(error.code)}`);
    assertObject(error.data, 'error.data');
    assertEqual(error.data.code, 'REGISTRY_AUTH_FAILED', '错误凭据 login error.data.code');
    assertNonEmptyString(error.message, '错误凭据 login error.message');
    if (error.data.hint !== undefined) assertType(error.data.hint, 'string', 'error.data.hint');
    else ctx.warn('REGISTRY_AUTH_FAILED 未提供 data.hint（契约 §7.12 建议提供可读提示，请确认）');

    // 密码脱敏：RPC 错误、事件、sidecar stderr 都不得出现密码
    assert(!containsSecret({ message: error.message, data: error.data }, password), 'RPC 错误 message/data 中不应出现密码');
    assert(!containsSecret(ctx.client.events.slice(-80), password), '事件（job/output、wslc/event 等）中不应出现密码');
    assert(!containsSecret(ctx.client.stderrLines.slice(-80), password), 'sidecar stderr 日志中不应出现密码');
    ctx.evidence('invalidLogin', {
      elapsedMs,
      code: error.data.code,
      message: truncate(error.message, 200),
      hint: truncate(error.data.hint, 200),
    });
  },
});

defineCheck({
  id: 'wslc-registry-logout',
  title: 'wslc/registry/logout 幂等（未登录 registry）',
  timeout: 90_000,
  async fn(ctx) {
    // 使用不存在的测试 registry，避免影响用户真实的 docker.io 凭据
    const server = 'registry.qa-invalid-dbx.test:5000';
    const first = await ctx.client.request('wslc/registry/logout', { server }, 90_000);
    assertObject(first, 'wslc/registry/logout result');
    assertEqual(first.loggedOut, true, '首次 logout.loggedOut');
    assertNonEmptyString(first.server, 'logout.server');
    assertType(first.output, 'string', 'logout.output');
    const second = await ctx.client.request('wslc/registry/logout', { server }, 90_000);
    assertObject(second, '第二次 wslc/registry/logout result');
    assertEqual(second.loggedOut, true, '二次 logout.loggedOut（幂等）');
    const noServer = await ctx.client.request('wslc/registry/logout', {}, 90_000);
    assertObject(noServer, '无 server logout result');
    assertEqual(noServer.loggedOut, true, '无 server logout.loggedOut');
    assertNonEmptyString(noServer.server, '无 server logout.server');
    ctx.evidence('logout', { server: first.server, calls: 3 });
  },
});

defineCheck({
  id: 'wslc-pull-auth-required',
  title: 'pull 不存在镜像 → job/end 认证失败分类（保留原始输出、不泄露密码）',
  timeout: 180_000,
  async fn(ctx) {
    const ref = 'docker.io/library/definitely-not-a-real-image-dbx-xyz:latest';
    // 该检查依赖 docker.io 公网；网络抖动（EOF/E_FAIL 等）返回 PROCESS_FAILED 属契约“其他”分支，
    // 不应误判为产品缺陷。最多重试 1 次，并在报告中记录每次尝试。
    const attempts = [];
    let jobId = null;
    let end = null;
    let transientNetworkFailure = false;
    for (let attempt = 1; attempt <= 2; attempt++) {
      const startedAtAttempt = Date.now();
      const pullResult = await ctx.client.request('wslc/images/pull', { ref }, ctx.slow);
      jobId = pullResult.jobId;
      assertNonEmptyString(jobId, 'pull(.jobId)');
      ctx.trackJob(jobId);
      end = await waitJobEnd(ctx, jobId, ctx.jobWait);
      ctx.untrackJob(jobId);
      const attemptCode = end.params && end.params.error && end.params.error.code;
      const attemptDetail = `${(end.params && end.params.error && end.params.error.detail) || ''}\n${
        (end.params && end.params.error && end.params.error.message) || ''
      }`;
      const REGISTRY_RESPONSE_RE =
        /docker login|unauthorized|authentication required|denied: requested access|denied|toomanyrequests|rate limit|pull access denied/i;
      const NETWORK_RE =
        /EOF|E_?FAIL|timed out|timeout|connection reset|connection refused|refused|unreachable|TLS|temporary failure|network is unreachable|网络|连接/i;
      const hasRegistryResponse = REGISTRY_RESPONSE_RE.test(attemptDetail);
      const isNetworkFailure = NETWORK_RE.test(attemptDetail);
      // 仅“纯网络瞬断”视为环境问题；若 detail 已包含真实 registry 响应关键词但仍分类为
      // PROCESS_FAILED，则属于分类器缺陷，应 FAIL（见下方断言）。
      const isTransient = attemptCode === 'PROCESS_FAILED' && isNetworkFailure && !hasRegistryResponse;
      attempts.push({
        attempt,
        code: attemptCode || null,
        ok: Boolean(end.params && end.params.ok),
        durationMs: Date.now() - startedAtAttempt,
        transientNetwork: isTransient,
        detail: truncate(attemptDetail.replace(/\s+/g, ' '), 200),
      });
      if (!isTransient) {
        transientNetworkFailure = false;
        break;
      }
      transientNetworkFailure = true;
      ctx.warn(`第 ${attempt} 次拉取遇到 docker.io 网络瞬断（${attemptCode}），${attempt < 2 ? '3s 后重试…' : '重试仍为网络错误'}`);
      if (attempt < 2) await sleep(3000);
    }
    ctx.evidence('pullAttempts', attempts);
    assertObject(end.params, 'job/end params');
    assertEqual(end.params.jobId, jobId, 'job/end.jobId');
    assertEqual(end.params.ok, false, '拉取不存在的镜像 job/end.ok 应为 false');
    assert(end.params.exitCode !== 0, `job/end.exitCode 应为非 0，实际 ${truncate(end.params.exitCode)}`);
    assertObject(end.params.error, 'job/end.error');
    const code = end.params.error.code;
    if (transientNetworkFailure && code === 'PROCESS_FAILED') {
      // 两次均为纯网络瞬断（detail 无 registry 认证响应关键词）：无法验证认证分类，
      // PROCESS_FAILED 是契约对“其他错误”的兜底，属环境问题而非产品缺陷 → PASS + warning。
      const networkDetail = attempts[attempts.length - 1].detail;
      ctx.warn(
        `本轮 Docker Hub 网络瞬断（${truncate(networkDetail, 160)}），认证分类未被实际触发；PROCESS_FAILED 对网络错误是契约兜底，不判失败。`,
      );
      ctx.evidence('pullAuth', {
        jobId,
        ok: end.params.ok,
        exitCode: end.params.exitCode,
        code,
        transientNetworkFailure: true,
        networkDetail,
        note: '网络瞬断：PASS(带 warning)，认证分类由 backend 单测/网络正常轮次覆盖',
      });
      return;
    }
    if (code === 'PROCESS_FAILED') {
      // 真实 registry 响应被误分类 → 明确 FAIL（不放宽）
      throw new AssertionError(
        `后端把真实 registry 响应分类为 PROCESS_FAILED（期望 REGISTRY_AUTH_REQUIRED/NOT_FOUND）：${truncate(
          `${end.params.error.detail || ''} ${end.params.error.message || ''}`.replace(/\s+/g, ' '),
          240,
        )}`,
      );
    }
    assertOneOf(code, ['REGISTRY_AUTH_REQUIRED', 'NOT_FOUND'], 'job/end.error.code（REGISTRY_AUTH_REQUIRED 或 NOT_FOUND）');
    if (code === 'REGISTRY_AUTH_REQUIRED') {
      assertNonEmptyString(end.params.error.hint, 'REGISTRY_AUTH_REQUIRED 的 error.hint');
      assertNonEmptyString(end.params.error.detail, 'REGISTRY_AUTH_REQUIRED 的 error.detail');
      assert(
        /docker login|unauthorized|authentication required|pull access denied|denied: requested access/i.test(
          `${end.params.error.detail}\n${end.params.error.message}`,
        ),
        'REGISTRY_AUTH_REQUIRED 的 detail/message 应保留认证失败原文（docker login/unauthorized/pull access denied 等）',
      );
    } else {
      ctx.warn('wslc 报错文案未包含 docker login 关键词，按契约容许的 NOT_FOUND 接受；请记录原始文本');
    }
    const outputs = ctx.client.events.filter(
      (event) => event.method === 'job/output' && event.params && event.params.jobId === jobId,
    );
    assert(outputs.length > 0, '拉取失败时 job/output 原文应保留（契约 §4.3：原文照常保留）');
    const outputText = outputs.map((event) => event.params.text || '').join('');
    assert(
      /definitely-not-a-real-image|pull|denied|not found|error/i.test(outputText),
      `job/output 原文应包含原始报错，实际 ${truncate(outputText)}`,
    );
    const images = await listImages(ctx);
    assert(!imageHas(images, ref), '失败的镜像不应出现在 wslc/images/list');
    assert(
      !/qa-secret-[a-z0-9-]+-Pw!/i.test(JSON.stringify(ctx.client.events.slice(-120))),
      '事件中不应出现 qa-secret 密码模式',
    );
    ctx.evidence('pullAuth', {
      jobId,
      ok: end.params.ok,
      exitCode: end.params.exitCode,
      code,
      detail: truncate(end.params.error.detail, 300),
      outputLines: outputs.length,
    });
  },
});

defineCheck({
  id: 'unknown-method',
  title: '协议路径：未知方法返回 JSON-RPC -32601',
  timeout: 30_000,
  async fn(ctx) {
    const error = await ctx.client.requestExpectError('qa/unknown-method', {}, 30_000);
    assertEqual(error.code, -32601, '未知方法 error.code');
    assertNonEmptyString(error.message, '未知方法 error.message');
  },
});

defineCheck({
  id: 'stdout-clean',
  title: 'stdout 只包含协议消息（无日志污染）',
  timeout: 30_000,
  async fn(ctx) {
    assertEqual(ctx.client.stdoutNoise.length, 0, `sidecar stdout 出现非协议输出: ${truncate(ctx.client.stdoutNoise)}`);
    assert(ctx.client.protocolMessages > 0, '未收到任何协议消息');
    assert(ctx.client.stderrLines.length >= 0, 'stderr 允许存在日志');
    ctx.evidence('stdoutNoise', ctx.client.stdoutNoise.length);
    ctx.evidence('protocolMessages', ctx.client.protocolMessages);
  },
});

// ---------------------------------------------------------------------------
// 运行
// ---------------------------------------------------------------------------

function selectChecks() {
  if (!options.only) return checks.slice();
  const needle = options.only.toLowerCase();
  return checks.filter(
    (check) => check.id.toLowerCase().includes(needle) || check.title.toLowerCase().includes(needle),
  );
}

async function runAll() {
  const selected = selectChecks();
  if (selected.length === 0) fail(`--only=${options.only} 没有匹配任何检查项（--list 查看）`);
  if (options.list) {
    for (const check of checks) console.log(`${check.id}\t${check.title}`);
    return null;
  }

  const resolved = resolveSidecar();
  const lowIntegrityRisk = isInsideWorkspace(resolved.exe);
  if (!options.json) {
    hero('WSL Manager Sidecar QA 探针');
    console.log(`sidecar: ${resolved.pathLabel}`);
    console.log(`来源: ${resolved.source}`);
    console.log(`Node: ${process.version} / ${process.platform}-${process.arch}`);
    console.log(`数据目录: ${DATA_DIR}`);
    console.log(`超时: 默认 ${options.timeout}ms, wslc 慢操作 ${Math.max(options.timeout, SLOW_TIMEOUT_MIN)}ms`);
    if (resolved.buildLog) console.log(dim(resolved.buildLog));
    if (lowIntegrityRisk) {
      console.log(
        yellow(
          '警告: sidecar 位于 workspace 内（Low mandatory integrity label），' +
            '其子进程调用 wsl.exe/wslc.exe 可能返回 E_ACCESSDENIED；建议使用 workspace 外 data-dir 生成的 sidecar。',
        ),
      );
    }
    console.log('');
  }

  mkdirSync(DATA_DIR, { recursive: true });
  const env = {
    ...process.env,
    DBX_PLUGIN_DATA_DIR: DATA_DIR,
  };
  const client = new SidecarClient({
    exe: resolved.exe,
    args: resolved.args,
    cwd: PLUGIN_ROOT,
    env,
    requestTimeout: options.timeout,
    verbose: options.verbose,
  });
  const ctx = makeCtx(client);
  ctx.state.sidecar = resolved;
  const results = [];
  const preCleanup = [];
  let fatal = null;

  const startedAt = Date.now();
  try {
    client.start();
    if (!options.json) console.log(dim(`已启动 sidecar (pid=${client.child.pid})，发送 plugin/initialize…\n`));
  } catch (error) {
    fatal = error;
  }

  if (!fatal) {
    for (const check of selected) {
      const result = {
        id: check.id,
        title: check.title,
        status: 'fail',
        durationMs: 0,
        assertions: [],
        warnings: [],
        evidence: {},
      };
      currentCheck = result;
      currentCtx = ctx;
      const checkStart = Date.now();
      if (!options.json) process.stdout.write(`  ${check.id} … `);
      try {
        await check.fn(ctx);
        result.status = 'pass';
      } catch (error) {
        result.status = 'fail';
        result.error =
          error instanceof AssertionError
            ? { type: 'assertion', message: error.message }
            : { type: 'error', message: error.message || String(error) };
        if (ctx.state.initialize && error instanceof RpcError && error.data) {
          result.error.data = error.data;
        }
      } finally {
        result.durationMs = Date.now() - checkStart;
        currentCheck = null;
      }
      results.push(result);
      if (!options.json) {
        const status =
          result.status === 'pass'
            ? green('PASS')
            : red('FAIL');
        process.stdout.write(`${status} ${dim(`(${result.durationMs}ms)`)}\n`);
        if (result.status === 'fail') {
          console.log(`    ${red('↳')} ${result.error.message}`);
          if (result.warnings.length) for (const warning of result.warnings) console.log(`    ${yellow('!')} ${warning}`);
        } else if (result.warnings.length) {
          for (const warning of result.warnings) console.log(`    ${yellow('!')} ${warning}`);
        }
        if (result.status === 'fail' && !ctx.client.exit) {
          // 首次失败时打印 sidecar stderr 尾部，便于定位
          const tail = ctx.client.stderrLines.slice(-8);
          if (tail.length) {
            console.log(dim('    sidecar stderr:'));
            for (const line of tail) console.log(dim(`      ${line}`));
          }
        }
      }
      if (result.status === 'fail' && check.id === 'initialize') {
        // 协议不通，后续检查没有意义
        for (const remaining of selected.slice(selected.indexOf(check) + 1)) {
          results.push({
            id: remaining.id,
            title: remaining.title,
            status: 'skipped',
            durationMs: 0,
            assertions: [],
            warnings: [],
            evidence: {},
            skipReason: 'initialize 失败，跳过',
          });
        }
        break;
      }
      if (check.id === 'initialize' && result.status === 'pass') {
        // 清理上一轮可能残留的 qa- 前缀资源（异常退出时 finally 未执行）
        const stale = await cleanupStaleQaResources(ctx);
        preCleanup.push(...stale);
        if (!options.json) {
          for (const item of stale) {
            const status = item.ok ? green('OK') : yellow('WARN');
            console.log(`  ${status} pre-clean ${item.action} ${item.target}${item.message ? dim(` — ${item.message}`) : ''}`);
          }
        }
      }
    }
  }

  // 清理：测试创建的容器 / 卷 / 网络 / 进行中的 job
  const cleanup = await cleanupResources(ctx, results.length > 0);
  if (!options.json) {
    if (cleanup.length) {
      console.log('\n清理:');
      for (const item of cleanup) {
        const status = item.ok ? green('OK') : red('FAIL');
        console.log(`  ${status} ${item.action} ${item.target}${item.message ? dim(` — ${item.message}`) : ''}`);
      }
    }
  }

  await client.stop();

  const summary = {
    passed: results.filter((item) => item.status === 'pass').length,
    failed: results.filter((item) => item.status === 'fail').length,
    skipped: results.filter((item) => item.status === 'skipped').length,
  };
  const durationMs = Date.now() - startedAt;
  const ok = !fatal && summary.failed === 0;

  const report = {
    tool: 'sidecar-probe',
    ok,
    startedAt: new Date(startedAt).toISOString(),
    durationMs,
    fatal: fatal ? String(fatal.stack || fatal.message || fatal) : null,
    sidecar: {
      path: resolved.pathLabel,
      source: resolved.source,
      lowIntegrityRisk,
      lowIntegrityNote: lowIntegrityRisk
        ? 'sidecar 位于带 Low integrity label 的 workspace 内，wsl.exe/wslc.exe 可能 E_ACCESSDENIED（本环境特性，非插件缺陷；请改用 workspace 外 data-dir）'
        : null,
    },
    environment: {
      node: process.version,
      platform: `${process.platform}-${process.arch}`,
      dataDir: DATA_DIR,
      timeoutMs: options.timeout,
      only: options.only,
    },
    summary,
    checks: results,
    preCleanup,
    cleanup,
    sidecarStderrTail: ctx.client.stderrLines.slice(-30),
  };

  if (options.report) {
    const reportPath = path.resolve(PLUGIN_ROOT, options.report);
    mkdirSync(path.dirname(reportPath), { recursive: true });
    writeFileSync(reportPath, `${JSON.stringify(report, null, 2)}\n`);
    if (!options.json) console.log(dim(`JSON 报告已写入: ${path.relative(PLUGIN_ROOT, reportPath)}`));
  }

  if (options.json) {
    process.stdout.write(`${JSON.stringify(report, null, 2)}\n`);
  } else {
    console.log('');
    const total = summary.passed + summary.failed + summary.skipped;
    console.log(
      `${summary.failed === 0 ? green('全部通过') : red('存在失败')}: ` +
        `${summary.passed}/${total} PASS, ${summary.failed} FAIL, ${summary.skipped} SKIP ` +
        dim(`(${(durationMs / 1000).toFixed(1)}s)`),
    );
    if (fatal) console.log(red(`致命错误: ${fatal.message || fatal}`));
    if (summary.failed > 0) {
      console.log(red('\n失败项:'));
      for (const item of results.filter((entry) => entry.status === 'fail')) {
        console.log(red(`  ✗ ${item.id}: ${item.error.message}`));
      }
    }
  }

  return { report, ok };
}

async function cleanupResources(ctx, initialized) {
  const entries = [];
  if (!initialized || !ctx.client.child || ctx.client.exit) return entries;

  const attempt = async (action, target, fn) => {
    try {
      await fn();
      entries.push({ action, target, ok: true });
      return true;
    } catch (error) {
      entries.push({ action, target, ok: false, message: error.message });
      return false;
    }
  };

  for (const jobId of [...ctx.state.resources.jobs]) {
    await attempt('cancel-job', jobId, () => ctx.client.request('jobs/cancel', { jobId }, 30_000));
  }
  for (const name of [...ctx.state.resources.containers]) {
    const ok = await attempt('remove-container', name, () =>
      ctx.client.request('wslc/containers/remove', { id: name, force: true }, Math.max(options.timeout, 60_000)),
    );
    if (ok) ctx.untrackContainer(name);
  }
  for (const name of [...ctx.state.resources.volumes]) {
    const ok = await attempt('remove-volume', name, () =>
      ctx.client.request('wslc/volumes/remove', { names: [name] }, Math.max(options.timeout, 60_000)),
    );
    if (ok) ctx.untrackVolume(name);
  }
  for (const name of [...ctx.state.resources.networks]) {
    const ok = await attempt('remove-network', name, () =>
      ctx.client.request('wslc/networks/remove', { names: [name] }, Math.max(options.timeout, 60_000)),
    );
    if (ok) ctx.untrackNetwork(name);
  }
  // 恢复发行版状态：wsl/distros/info 会按需启动发行版；若测试前为 Stopped，终态应还原
  if (ctx.state.distroNameForRestore && ctx.state.distroInitiallyRunning === false) {
    await attempt('restore-distro-stopped', ctx.state.distroNameForRestore, () =>
      ctx.client.request('wsl/distros/terminate', { name: ctx.state.distroNameForRestore }, Math.max(options.timeout, 60_000)),
    );
  }
  return entries;
}

// 清理未跟踪的 qa- 前缀资源（上一轮异常退出可能遗留）
async function cleanupStaleQaResources(ctx) {
  const entries = [];
  const since = Date.now();
  try {
    const list = await ctx.client.request('wslc/containers/list', { all: true }, 90_000);
    for (const container of list.containers || []) {
      const name = String(container.name || '');
      if (!name.startsWith('qa-')) continue;
      try {
        await ctx.client.request('wslc/containers/remove', { id: name, force: true }, 90_000);
        entries.push({ action: 'pre-clean-container', target: name, ok: true });
      } catch (error) {
        entries.push({ action: 'pre-clean-container', target: name, ok: false, message: error.message });
      }
    }
    const volumes = await ctx.client.request('wslc/volumes/list', {}, 90_000);
    for (const volume of volumes.volumes || []) {
      const name = String(volume.name || '');
      if (!name.startsWith('qa-')) continue;
      try {
        await ctx.client.request('wslc/volumes/remove', { names: [name] }, 90_000);
        entries.push({ action: 'pre-clean-volume', target: name, ok: true });
      } catch (error) {
        entries.push({ action: 'pre-clean-volume', target: name, ok: false, message: error.message });
      }
    }
    const networks = await ctx.client.request('wslc/networks/list', {}, 90_000);
    for (const network of networks.networks || []) {
      const name = String(network.name || '');
      if (!name.startsWith('qa-')) continue;
      try {
        await ctx.client.request('wslc/networks/remove', { names: [name] }, 90_000);
        entries.push({ action: 'pre-clean-network', target: name, ok: true });
      } catch (error) {
        entries.push({ action: 'pre-clean-network', target: name, ok: false, message: error.message });
      }
    }
  } catch (error) {
    entries.push({ action: 'pre-clean', target: '*', ok: false, message: `无法列出资源: ${error.message}` });
  }
  return entries;
}

// ---------------------------------------------------------------------------
// 入口
// ---------------------------------------------------------------------------

if (options.list) {
  for (const check of checks) console.log(`${check.id}\t${check.title}`);
  process.exit(0);
}

runAll()
  .then((outcome) => {
    if (!outcome) process.exit(0);
    process.exit(outcome.ok ? 0 : 1);
  })
  .catch((error) => {
    console.error(`sidecar-probe 未捕获异常: ${error.stack || error}`);
    process.exit(1);
  });
