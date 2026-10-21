#!/usr/bin/env node
/**
 * WSL Manager — 端到端验证一键入口（零依赖，Node.js 22+）
 *
 * 依次执行:
 *   1. go build 构建 sidecar（产物在 workspace 外，GOTMPDIR/GOCACHE 指向仓库根）
 *   2. 复制 dbx-plugin CLI 到 workspace 外并启动 dev host
 *      （端口 5193；data-dir %LOCALAPPDATA%\dbx-wsl-dev\qa）
 *   3. 等待 /api/diagnostics 可用且 backendState=ready
 *   4. 运行 tools/sidecar-probe.mjs（真实 wsl/wslc RPC 检查）
 *   5. 运行 tools/ui-cdp-check.mjs（Chrome CDP 打开工作台、切换 9 页、截图）
 *   6. 关闭 dev host（Windows 进程树 taskkill /T /F），输出汇总
 *
 * 环境注意：本会话 workspace 目录带 Low mandatory integrity label，workspace 内的
 * exe 以 Low IL 运行，spawn 的 wsl.exe/wslc.exe 会 E_ACCESSDENIED。因此本脚本
 * 自动把 CLI 复制到 %LOCALAPPDATA%\dbx-wsl-dev 再运行（可用 DBX_WSL_QA_HOME 覆盖）。
 * 这是本会话沙箱特性，不影响插件安装到真实 DBX 插件目录后的运行。
 *
 * 用法:
 *   node tests/run-all.mjs
 *   node tests/run-all.mjs --port=5193 --skip-build
 *   node tests/run-all.mjs --skip-ui           # 只跑 sidecar
 *   node tests/run-all.mjs --skip-sidecar      # 只跑 UI（复用已构建 sidecar）
 *
 * 退出码: 0 = 全部 PASS；1 = 任一环节 FAIL。
 */

import { spawn, spawnSync } from 'node:child_process';
import { cpSync, existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import process from 'node:process';
import { setTimeout as sleep } from 'node:timers/promises';
import { fileURLToPath } from 'node:url';

const HERE = path.dirname(fileURLToPath(import.meta.url));
const PLUGIN_ROOT = path.resolve(HERE, '..');
const REPO_ROOT = path.resolve(PLUGIN_ROOT, '..');
const TMP_DIR = path.join(PLUGIN_ROOT, 'tests', 'tmp');

// workspace 目录带 Low mandatory integrity label：其中的 exe 会以 Low IL 运行，
// 子进程调用 wsl.exe/wslc.exe 会 E_ACCESSDENIED。因此 dev data-dir、构建产物
// 以及 dbx-plugin CLI 本身都必须放在 workspace 外（默认 %LOCALAPPDATA%\dbx-wsl-dev）。
const EXTERNAL_ROOT = process.env.DBX_WSL_QA_HOME
  ? path.resolve(process.env.DBX_WSL_QA_HOME)
  : path.join(process.env.LOCALAPPDATA || os.tmpdir(), 'dbx-wsl-dev');
const DEV_DATA_DIR = path.join(EXTERNAL_ROOT, 'qa');
const BUILD_OUT = path.join(EXTERNAL_ROOT, 'qa-build', 'sidecar.exe');
const EXTERNAL_CLI_ROOT = path.join(EXTERNAL_ROOT, 'plugin-cli');
const SOURCE_PLUGIN_CLI = path.join(REPO_ROOT, 'node_modules', '@dbx-app', 'plugin-cli');
const DEVHOST_LOG = path.join(TMP_DIR, 'devhost.log');
const SIDECAR_REPORT = path.join(TMP_DIR, 'sidecar-report.json');
const UI_REPORT = path.join(TMP_DIR, 'ui-report.json');
const DEFAULT_REPORT = path.join(TMP_DIR, 'run-all-report.json');
const CLI_JS = path.join(EXTERNAL_CLI_ROOT, 'node_modules', '@dbx-app', 'plugin-cli', 'bin', 'dbx-plugin.js');

const colorEnabled = !process.env.NO_COLOR && process.stdout.isTTY !== false;
const paint = (code) => (text) => (colorEnabled ? `\x1b[${code}m${text}\x1b[0m` : text);
const green = paint('32');
const red = paint('31');
const yellow = paint('33');
const dim = paint('2');
const bold = paint('1');

function log(...parts) {
  console.log(...parts);
}

function fail(message) {
  console.error(red(`run-all: ${message}`));
  process.exit(1);
}

function parseArgs(argv) {
  const opts = {
    port: 5193,
    skipBuild: false,
    skipSidecar: false,
    skipUi: false,
    verbose: false,
    report: DEFAULT_REPORT,
    sidecarTimeout: 900_000,
    uiTimeout: 420_000,
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
      case '--skip-build': opts.skipBuild = true; break;
      case '--skip-sidecar': opts.skipSidecar = true; break;
      case '--skip-ui': opts.skipUi = true; break;
      case '--verbose': opts.verbose = true; break;
      case '--report': opts.report = path.resolve(PLUGIN_ROOT, String(next())); break;
      case '--sidecar-timeout': opts.sidecarTimeout = Number(next()); break;
      case '--ui-timeout': opts.uiTimeout = Number(next()); break;
      case '--help': case '-h': opts.help = true; break;
      default: fail(`未知参数: ${arg}（用 --help 查看用法）`);
    }
  }
  if (!Number.isInteger(opts.port) || opts.port <= 0 || opts.port > 65535) fail(`--port 非法: ${opts.port}`);
  return opts;
}

const options = parseArgs(process.argv.slice(2));
if (options.help) {
  console.log(`WSL Manager 端到端验证（Node.js 22+）

用法: node tests/run-all.mjs [选项]

选项:
  --port=<n>              dev host 端口（默认 5193）
  --skip-build            跳过 go build（复用现有 sidecar）
  --skip-sidecar          跳过 sidecar-probe
  --skip-ui               跳过 ui-cdp-check
  --sidecar-timeout=<ms>  sidecar 检查超时（默认 900000）
  --ui-timeout=<ms>       UI 检查超时（默认 300000）
  --report=<file>         汇总报告路径（默认 tests/tmp/run-all-report.json）
  --verbose               透传 dev host 日志
  -h, --help              显示帮助
`);
  process.exit(0);
}

// ---------------------------------------------------------------------------
// 工具
// ---------------------------------------------------------------------------

function goEnv() {
  const goTmp = path.join(REPO_ROOT, '.gotmp');
  const goCache = path.join(REPO_ROOT, '.gocache');
  mkdirSync(goTmp, { recursive: true });
  mkdirSync(goCache, { recursive: true });
  return { ...process.env, GOTMPDIR: goTmp, GOCACHE: goCache };
}

function killTree(pid) {
  if (!pid) return;
  try {
    if (process.platform === 'win32') {
      spawnSync('taskkill', ['/PID', String(pid), '/T', '/F'], { stdio: 'ignore', windowsHide: true });
    } else {
      process.kill(-pid, 'SIGKILL');
    }
  } catch {}
}

/** 读取 WSL 发行版状态（用于测试后还原：UI 打开详情会按需启动发行版）。 */
function readDistroStates() {
  try {
    const wsl = path.join(process.env.SystemRoot || 'C:\\Windows', 'System32', 'wsl.exe');
    const result = spawnSync(wsl, ['--list', '--verbose'], {
      encoding: 'buffer',
      timeout: 20_000,
      windowsHide: true,
      env: { ...process.env, WSL_UTF8: '1' },
    });
    let text = result.stdout ? result.stdout.toString('utf8') : '';
    if (!text || text.includes('\u0000')) text = result.stdout ? result.stdout.toString('utf16le') : '';
    const states = {};
    for (const line of text.split(/\r?\n/)) {
      const match = line.match(/^\s*\*?\s*(.+?)\s{2,}(Running|Stopped|Installing|Uninstalling)\s+[12]\s*$/);
      if (match) states[match[1].trim()] = match[2];
    }
    return states;
  } catch {
    return {};
  }
}

/** 把测试期间被拉起的发行版恢复为测试前的 Stopped 状态。 */
function restoreDistroStates(initialStates) {
  const entries = [];
  const currentStates = readDistroStates();
  for (const [name, initialState] of Object.entries(initialStates || {})) {
    if (initialState === 'Running') continue; // 测试前就在运行的不动
    const current = currentStates[name];
    if (current !== 'Running') continue;
    try {
      const wsl = path.join(process.env.SystemRoot || 'C:\\Windows', 'System32', 'wsl.exe');
      const result = spawnSync(wsl, ['--terminate', name], {
        encoding: 'utf8',
        timeout: 60_000,
        windowsHide: true,
        env: { ...process.env, WSL_UTF8: '1' },
      });
      const ok = result.status === 0;
      entries.push({ action: 'terminate-distro', target: name, ok, message: ok ? '恢复为测试前状态' : `${result.stdout || ''}${result.stderr || ''}`.trim().slice(0, 200) });
    } catch (error) {
      entries.push({ action: 'terminate-distro', target: name, ok: false, message: String(error.message || error) });
    }
  }
  return entries;
}

function readJson(filePath) {
  try {
    return JSON.parse(readFileSync(filePath, 'utf8'));
  } catch {
    return null;
  }
}

function runStep(command, args, { timeout, env, label }) {
  return new Promise((resolve) => {
    const startedAt = Date.now();
    const child = spawn(command, args, { cwd: PLUGIN_ROOT, env: env || process.env, stdio: 'inherit', windowsHide: true });
    let timedOut = false;
    const timer = setTimeout(() => {
      timedOut = true;
      log(red(`\n[${label}] 超时（${timeout}ms），终止进程树`));
      killTree(child.pid);
    }, timeout);
    child.on('error', (error) => {
      clearTimeout(timer);
      resolve({ ok: false, code: null, timedOut, durationMs: Date.now() - startedAt, error: String(error.message || error) });
    });
    child.on('exit', (code) => {
      clearTimeout(timer);
      resolve({ ok: code === 0 && !timedOut, code, timedOut, durationMs: Date.now() - startedAt });
    });
  });
}

// ---------------------------------------------------------------------------
// 主流程
// ---------------------------------------------------------------------------

const report = {
  tool: 'run-all',
  ok: false,
  startedAt: new Date().toISOString(),
  durationMs: 0,
  environment: {
    node: process.version,
    platform: `${process.platform}-${process.arch}`,
    pluginRoot: PLUGIN_ROOT,
    devHostPort: options.port,
    devHostDataDir: DEV_DATA_DIR,
  },
  steps: {},
};

let devHost = null;
let devHostLog = '';
let cleanedUp = false;

function cleanup() {
  if (cleanedUp) return;
  cleanedUp = true;
  if (devHost && !devHost.exited) {
    killTree(devHost.child.pid);
    log(dim(`已关闭 dev host（pid=${devHost.child.pid}）`));
  }
}

process.on('SIGINT', () => {
  cleanup();
  process.exit(130);
});
process.on('uncaughtException', (error) => {
  console.error(red(`未捕获异常: ${error.stack || error}`));
  cleanup();
  process.exit(1);
});

/** 把 dbx-plugin CLI 复制到 workspace 外再启动 dev host：
 *  workspace 内的 Rust/Node 可执行文件会继承 Low integrity label，导致其 spawn 的
 *  backend exe 也是 Low IL，调用 wsl.exe/wslc.exe 时 E_ACCESSDENIED。 */
function ensureExternalCli() {
  const targetScope = path.join(EXTERNAL_CLI_ROOT, 'node_modules', '@dbx-app');
  const sourceScope = path.join(REPO_ROOT, 'node_modules', '@dbx-app');
  if (!existsSync(sourceScope)) fail(`找不到 @dbx-app 依赖目录: ${sourceScope}`);
  const sourceVersion = readJson(path.join(SOURCE_PLUGIN_CLI, 'package.json'))?.version || 'unknown';
  const marker = path.join(EXTERNAL_CLI_ROOT, '.version');
  const currentVersion = existsSync(marker) ? readFileSync(marker, 'utf8').trim() : '';
  const platformPkg = path.join(targetScope, 'plugin-cli-win32-x64');
  if (currentVersion === sourceVersion && existsSync(CLI_JS) && existsSync(platformPkg)) {
    return { copied: false, version: sourceVersion, path: EXTERNAL_CLI_ROOT };
  }
  mkdirSync(EXTERNAL_CLI_ROOT, { recursive: true });
  cpSync(sourceScope, targetScope, { recursive: true, force: true, dereference: false });
  writeFileSync(marker, `${sourceVersion}\n`);
  return { copied: true, version: sourceVersion, path: EXTERNAL_CLI_ROOT };
}

async function main() {
  const startedAt = Date.now();
  mkdirSync(TMP_DIR, { recursive: true });
  mkdirSync(DEV_DATA_DIR, { recursive: true });

  log(bold('\nWSL Manager 端到端验证'));
  log(`插件: ${PLUGIN_ROOT}`);
  log(`dev host: http://127.0.0.1:${options.port}/  data-dir: ${DEV_DATA_DIR}`);
  log('');

  // ---------------------------------------------------------------- 1. build
  const build = { ok: false, skipped: false, durationMs: 0, version: null };
  if (options.skipBuild) {
    build.ok = existsSync(BUILD_OUT);
    build.skipped = true;
    log(yellow('1. 构建 sidecar — 已跳过（--skip-build）'));
    if (!build.ok) log(red(`   现有 sidecar 不存在: ${BUILD_OUT}`));
  } else {
    log(bold('1. 构建 sidecar（go build）'));
    const started = Date.now();
    const result = spawnSync('go', ['build', '-o', BUILD_OUT, '.'], {
      cwd: path.join(PLUGIN_ROOT, 'backend'),
      env: goEnv(),
      encoding: 'utf8',
      windowsHide: true,
    });
    build.durationMs = Date.now() - started;
    build.ok = result.status === 0 && existsSync(BUILD_OUT);
    build.exitCode = result.status;
    const output = `${result.stdout || ''}${result.stderr || ''}`.trim();
    if (output) log(dim(output.split('\n').slice(0, 40).join('\n')));
    if (build.ok) {
      const versionResult = spawnSync(BUILD_OUT, [], { input: '{"jsonrpc":"2.0","id":1,"method":"plugin/initialize","params":{"host":{"protocolVersions":[1]}}}\n', encoding: 'utf8', timeout: 10_000, windowsHide: true });
      try {
        const line = (versionResult.stdout || '').trim().split('\n').pop();
        build.version = JSON.parse(line);
      } catch {}
    }
    log(`   ${build.ok ? green('PASS') : red('FAIL')} sidecar 构建 ${path.relative(PLUGIN_ROOT, BUILD_OUT)} (${build.durationMs}ms)`);
  }
  report.steps.build = build;
  if (!build.ok) {
    report.ok = false;
    report.durationMs = Date.now() - startedAt;
    finish();
    return;
  }

  // ------------------------------------------------------------ 2. dev host
  log(bold('\n2. 启动 dev host'));
  const cli = ensureExternalCli();
  log(
    `   ${cli.copied ? green('PASS') : dim('INFO')} dbx-plugin CLI ${cli.copied ? '已复制到 workspace 外' : '已存在'} (v${cli.version}): ${cli.path}`,
  );
  report.steps.cli = { ok: existsSync(CLI_JS), version: cli.version, path: cli.path, copied: cli.copied };
  if (!existsSync(CLI_JS)) fail(`找不到 dbx-plugin CLI: ${CLI_JS}`);
  const devStartedAt = Date.now();
  devHost = { url: null, diagnostics: null };
  devHost.child = spawn(
    process.execPath,
    [CLI_JS, 'dev', '--path', PLUGIN_ROOT, '--port', String(options.port), '--data-dir', DEV_DATA_DIR],
    {
      cwd: PLUGIN_ROOT,
      env: goEnv(),
      stdio: ['ignore', 'pipe', 'pipe'],
      windowsHide: true,
    },
  );
  devHost.exited = false;
  devHost.child.on('exit', (code) => {
    devHost.exited = true;
    devHost.exitCode = code;
  });
  const appendLog = (chunk) => {
    const text = String(chunk);
    devHostLog += text;
    if (devHostLog.length > 200_000) devHostLog = devHostLog.slice(-150_000);
    writeFileSync(DEVHOST_LOG, devHostLog);
    if (options.verbose) {
      for (const line of text.split(/\r?\n/)) if (line.trim()) log(dim(`[devhost] ${line}`));
    }
  };
  devHost.child.stdout.setEncoding('utf8');
  devHost.child.stderr.setEncoding('utf8');
  devHost.child.stdout.on('data', appendLog);
  devHost.child.stderr.on('data', appendLog);

  const waitDevHost = async () => {
    const deadline = Date.now() + 180_000;
    let url = `http://127.0.0.1:${options.port}/`;
    let firstFailedAt = null;
    while (Date.now() < deadline) {
      if (devHost.exited) {
        throw new Error(`dev host 提前退出（code=${devHost.exitCode}）\n${devHostLog.slice(-3000)}`);
      }
      const match = devHostLog.match(/Plugin dev host: (http:\/\/[\d.]+:\d+)/);
      if (match) url = match[1].endsWith('/') ? match[1] : `${match[1]}/`;
      try {
        const response = await fetch(new URL('api/diagnostics?after=0&limit=1', url), { signal: AbortSignal.timeout(2000) });
        if (response.ok) {
          const diagnostics = await response.json();
          if (diagnostics.backendState === 'ready') {
            return { url, diagnostics };
          }
          if (diagnostics.backendState === 'failed') {
            firstFailedAt = firstFailedAt || Date.now();
            if (Date.now() - firstFailedAt > 15_000) {
              throw new Error(`sidecar backendState=failed\n${devHostLog.slice(-4000)}`);
            }
          }
        }
      } catch (error) {
        if (String(error.message).includes('backendState=failed')) throw error;
      }
      await sleep(500);
    }
    throw new Error(`等待 dev host 就绪超时（180s）\n${devHostLog.slice(-3000)}`);
  };

  try {
    const ready = await waitDevHost();
    devHost.url = ready.url;
    devHost.diagnostics = {
      backendState: ready.diagnostics.backendState,
      plugin: ready.diagnostics.plugin,
      port: ready.diagnostics.port,
    };
    log(`   ${green('PASS')} dev host 就绪 ${ready.url} backendState=${ready.diagnostics.backendState} (${Date.now() - devStartedAt}ms)`);
    if (!ready.url.includes(`:${options.port}/`)) {
      log(yellow(`   注意: 端口 ${options.port} 被占用，dev host 回退到 ${ready.url}`));
    }
  } catch (error) {
    report.steps.devHost = { ok: false, error: String(error.message || error), logTail: devHostLog.slice(-4000) };
    log(`   ${red('FAIL')} dev host 启动失败: ${error.message}`);
    cleanup();
    report.durationMs = Date.now() - startedAt;
    finish();
    return;
  }
  report.steps.devHost = { ok: true, url: devHost.url, diagnostics: devHost.diagnostics };

  // --------------------------------------------------------- 3. sidecar probe
  // 记录测试前发行版状态：UI 打开发行版详情会按需启动发行版，结束后还原
  const initialDistros = readDistroStates();
  report.initialDistroStates = initialDistros;
  log(
    dim(
      `\n   测试前发行版状态: ${Object.entries(initialDistros).map(([name, state]) => `${name}=${state}`).join(', ') || '(未读取到)'}`,
    ),
  );
  if (options.skipSidecar) {
    log(yellow('\n3. sidecar-probe — 已跳过（--skip-sidecar）'));
    report.steps.sidecar = { ok: true, skipped: true };
  } else {
    log(bold('\n3. sidecar-probe（真实 wsl/wslc RPC）'));
    const step = await runStep(
      process.execPath,
      [path.join(PLUGIN_ROOT, 'tools', 'sidecar-probe.mjs'), `--sidecar=${BUILD_OUT}`, `--report=${SIDECAR_REPORT}`],
      { timeout: options.sidecarTimeout, label: 'sidecar-probe' },
    );
    const sidecarReport = readJson(SIDECAR_REPORT);
    report.steps.sidecar = {
      ok: step.ok,
      exitCode: step.code,
      timedOut: step.timedOut,
      durationMs: step.durationMs,
      summary: sidecarReport?.summary ?? null,
      failedChecks: sidecarReport
        ? sidecarReport.checks.filter((check) => check.status === 'fail').map((check) => ({ id: check.id, error: check.error }))
        : null,
      report: path.relative(PLUGIN_ROOT, SIDECAR_REPORT),
    };
    log(`   ${step.ok ? green('PASS') : red('FAIL')} sidecar-probe (${(step.durationMs / 1000).toFixed(1)}s)`);
  }

  // ------------------------------------------------------------ 4. UI check
  if (options.skipUi) {
    log(yellow('\n4. ui-cdp-check — 已跳过（--skip-ui）'));
    report.steps.ui = { ok: true, skipped: true };
  } else {
    log(bold('\n4. ui-cdp-check（Chrome CDP + 9 页截图）'));
    const step = await runStep(
      process.execPath,
      [
        path.join(PLUGIN_ROOT, 'tools', 'ui-cdp-check.mjs'),
        `--dev-url=${devHost.url}`,
        `--out=${path.join(PLUGIN_ROOT, 'tests', 'screenshots')}`,
        `--report=${UI_REPORT}`,
      ],
      { timeout: options.uiTimeout, label: 'ui-cdp-check' },
    );
    const uiReport = readJson(UI_REPORT);
    report.steps.ui = {
      ok: step.ok,
      exitCode: step.code,
      timedOut: step.timedOut,
      durationMs: step.durationMs,
      summary: uiReport?.summary ?? null,
      failedChecks: uiReport
        ? uiReport.checks.filter((check) => check.status === 'fail').map((check) => ({ id: check.id, detail: check.detail }))
        : null,
      screenshots: uiReport?.screenshots ?? uiReport?.pages?.map((page) => page.screenshot) ?? null,
      report: path.relative(PLUGIN_ROOT, UI_REPORT),
    };
    log(`   ${step.ok ? green('PASS') : red('FAIL')} ui-cdp-check (${(step.durationMs / 1000).toFixed(1)}s)`);
  }

  // 还原测试期间被拉起的发行版（UI 打开详情抽屉会触发 wsl/distros/info 启动发行版）
  const distroCleanup = restoreDistroStates(initialDistros);
  report.steps.distroCleanup = distroCleanup;
  if (distroCleanup.length) {
    log(bold('\n5. 发行版状态还原'));
    for (const item of distroCleanup) {
      log(`   ${item.ok ? green('OK') : red('FAIL')} ${item.action} ${item.target}${item.message ? dim(` — ${item.message}`) : ''}`);
    }
  } else {
    log(dim('\n发行版状态无需还原'));
  }

  // --------------------------------------------------------------- 5. 汇总
  const steps = report.steps;
  const allOk =
    steps.build?.ok !== false &&
    steps.devHost?.ok === true &&
    (steps.sidecar?.ok ?? true) &&
    (steps.ui?.ok ?? true);
  report.ok = allOk;
  report.durationMs = Date.now() - startedAt;
  finish();
}

function finish() {
  cleanup();
  const steps = report.steps;
  log(bold('\n=== 汇总 ==='));
  const row = (label, step, extra) => {
    if (!step) {
      log(`  ${dim('SKIP')} ${label}`);
      return;
    }
    const status = step.skipped ? yellow('SKIP') : step.ok ? green('PASS') : red('FAIL');
    log(`  ${status} ${label}${extra ? dim(` — ${extra}`) : ''}`);
  };
  row('构建 sidecar', steps.build, steps.build?.durationMs ? `${(steps.build.durationMs / 1000).toFixed(1)}s` : '');
  row('dev host', steps.devHost, steps.devHost?.url || steps.devHost?.error);
  row(
    'sidecar-probe',
    steps.sidecar,
    steps.sidecar?.summary
      ? `${steps.sidecar.summary.passed} PASS / ${steps.sidecar.summary.failed} FAIL / ${steps.sidecar.summary.skipped} SKIP`
      : '',
  );
  const fails = new Set();
  if (steps.sidecar?.failedChecks) for (const check of steps.sidecar.failedChecks) fails.add(`sidecar/${check.id}`);
  if (steps.ui?.failedChecks) for (const check of steps.ui.failedChecks) fails.add(`ui/${check.id}`);
  row(
    'ui-cdp-check',
    steps.ui,
    steps.ui?.summary ? `${steps.ui.summary.passed} PASS / ${steps.ui.summary.failed} FAIL / ${steps.ui.summary.warned} WARN` : '',
  );
  if (fails.size) {
    log(red('\n  失败项:'));
    for (const item of fails) log(red(`    ✗ ${item}`));
  }
  log('');
  log(report.ok ? green(`端到端验证通过（${(report.durationMs / 1000).toFixed(1)}s）`) : red(`端到端验证失败（${(report.durationMs / 1000).toFixed(1)}s）`));
  log(dim(`报告: ${path.relative(PLUGIN_ROOT, options.report)}`));

  try {
    mkdirSync(path.dirname(options.report), { recursive: true });
    writeFileSync(options.report, `${JSON.stringify(report, null, 2)}\n`);
  } catch (error) {
    console.error(red(`写入汇总报告失败: ${error.message}`));
  }
  process.exit(report.ok ? 0 : 1);
}

main().catch((error) => {
  console.error(red(`run-all 失败: ${error.stack || error.message || error}`));
  report.steps.fatal = String(error.stack || error.message || error);
  report.durationMs = Date.now() - Date.parse(report.startedAt);
  finish();
});
