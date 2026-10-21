#!/usr/bin/env node
/**
 * DBX Manifest Host-Rules 预检
 *
 * `manifest.schema.json` 只是 JSON Schema；真实 DBX Host 在安装时还有额外的语义校验
 * （例如 menus.group 只接受固定词表）。本脚本从本机 DBX 二进制的校验文案归纳出这些规则，
 * 在打包前提前发现，避免“导入时才报 incompatible”。
 *
 * 用法：
 *   node tools/manifest-check.mjs [manifest.json] [--package dist/xxx.dbxp]
 * 退出码：0 = 通过（允许 WARN），1 = FAIL。
 */
import { readFileSync, existsSync } from "node:fs";
import { execFileSync } from "node:child_process";
import { resolve, dirname, join, isAbsolute } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const projectRoot = resolve(here, "..");

const args = process.argv.slice(2);
let manifestPath = join(projectRoot, "manifest.json");
let packagePath = null;
for (let i = 0; i < args.length; i++) {
  if (args[i] === "--package") packagePath = resolve(args[++i]);
  else if (!args[i].startsWith("--")) manifestPath = resolve(args[i]);
}

const results = [];
function check(name, fn) {
  try {
    const detail = fn();
    results.push({ name, status: "pass", detail });
  } catch (error) {
    results.push({ name, status: error && error.warning ? "warn" : "fail", detail: error.message });
  }
}
function assert(condition, message) {
  if (!condition) throw new Error(message);
}
function warn(condition, message) {
  if (!condition) {
    const error = new Error(message);
    error.warning = true;
    throw error;
  }
}

// —— 从真实 DBX 二进制归纳的规则 ——
const MENU_GROUP_WORDS = ["navigation", "primary", "secondary", "destructive"];
const ICON_EXTENSIONS = [".svg", ".png", ".jpg", ".jpeg", ".gif", ".webp", ".ico"];
const KNOWN_PERMISSIONS = new Set([
  "host.workbench",
  "host.events",
  "host.filesystem",
  "host.binary",
  "host.storage",
  "host.ai",
  "host.plans:read",
  "host.schema:read",
  "host.clipboard:read",
  "host.data:read",
  "host.connections:read",
]);
const CONTRIBUTION_TYPES = new Set([
  "connection-provider",
  "workbench",
  "filesystem-provider",
  "context-menu",
  "result-view",
  "command",
  "menus",
  "mcp",
]);

let manifest;
check("manifest.json 可解析", () => {
  manifest = JSON.parse(readFileSync(manifestPath, "utf8"));
  return manifestPath;
});

if (!manifest) {
  report();
  process.exit(1);
}

check("manifest_version / id / version / publisher", () => {
  assert(manifest.manifest_version === 1, "manifest_version 必须为 1");
  assert(/^[a-z0-9][a-z0-9._-]{0,127}$/.test(manifest.id), `非法 id: ${manifest.id}`);
  assert(/^\d+\.\d+\.\d+([-+].*)?$/.test(manifest.version), `非法 version: ${manifest.version}`);
  assert(typeof manifest.publisher === "string" && manifest.publisher.length > 0, "publisher 必填");
  return `${manifest.id}@${manifest.version}`;
});

check("engines.host_api 声明", () => {
  assert(manifest.engines && manifest.engines.host_api, "engines.host_api 必填");
  return String(manifest.engines.host_api);
});

check("权限词表", () => {
  const unknown = (manifest.permissions || []).filter((p) => !KNOWN_PERMISSIONS.has(p) && !p.startsWith("host.network:"));
  assert(unknown.length === 0, `未知权限: ${unknown.join(", ")}`);
  return `${(manifest.permissions || []).length} 项`;
});

check("entrypoints", () => {
  assert(manifest.entrypoints && manifest.entrypoints.ui, "entrypoints.ui 必填");
  const ui = manifest.entrypoints.ui;
  assert(ui.entry && ui.root, "ui.root / ui.entry 必填");
  assert(!ui.entry.startsWith("/") && !ui.entry.includes("..") && !ui.entry.includes("\\"), "ui.entry 必须是包内相对路径");
  assert(ui.entry === `${ui.root}/${ui.entry.split("/").pop()}`, `ui.entry 必须位于 ui.root 内: ${ui.root} / ${ui.entry}`);
  if (manifest.entrypoints.backend) {
    const be = manifest.entrypoints.backend;
    assert(be.executable && !isAbsolute(be.executable) && !be.executable.includes(".."), "backend.executable 必须是包内相对路径");
    if (be.transport) assert(["stdio-jsonl", "stdio-framed"].includes(be.transport), `非法 transport: ${be.transport}`);
  }
  return manifest.entrypoints.backend ? "ui + backend" : "ui only";
});

check("图标扩展名", () => {
  const icons = [];
  if (manifest.icon) icons.push(manifest.icon);
  for (const c of manifest.contributions || []) if (c.icon) icons.push(c.icon);
  for (const icon of icons) {
    assert(ICON_EXTENSIONS.some((ext) => icon.toLowerCase().endsWith(ext)), `不支持的图标格式: ${icon}`);
    if (!packagePath) assert(existsSync(join(projectRoot, icon)), `图标文件不存在: ${icon}`);
  }
  return `${icons.length} 个`;
});

check("贡献点类型与 id 唯一性", () => {
  const ids = new Set();
  for (const c of manifest.contributions || []) {
    assert(CONTRIBUTION_TYPES.has(c.type), `未知贡献点类型: ${c.type}`);
    assert(/^[a-z0-9][a-z0-9._-]{0,127}$/.test(c.id), `非法贡献点 id: ${c.id}`);
    assert(!ids.has(c.id), `重复贡献点 id: ${c.id}`);
    ids.add(c.id);
  }
  return `${(manifest.contributions || []).length} 个`;
});

check("menus：group 必须在 Host 词表内", () => {
  const items = (manifest.contributions || []).filter((c) => c.type === "menus").flatMap((c) => c.items || []);
  for (const item of items) {
    assert(MENU_GROUP_WORDS.includes(item.group), `menus 使用非法 group '${item.group}'（允许: ${MENU_GROUP_WORDS.join(", ")}）`);
  }
  return `${items.length} 个菜单项`;
});

check("menus：placement 规则", () => {
  const seen = new Set();
  for (const c of (manifest.contributions || []).filter((x) => x.type === "menus")) {
    for (const item of c.items || []) {
      assert(["commandPalette", "appToolbar", "appSidebar"].includes(item.location), `非法 location: ${item.location}`);
      assert(typeof item.command === "string" && item.command.length > 0, "menuItem.command 必填");
      assert(Number.isInteger(item.order), `menuItem.order 必须为整数: ${item.command}`);
      if (item.location === "commandPalette") {
        assert(item.default_visible === undefined, `commandPalette 不能设置 default_visible（${item.command}）`);
      }
      const key = `${item.location}:${item.command}`;
      assert(!seen.has(key), `同一 placement 重复声明: ${key}`);
      seen.add(key);
    }
  }
  return "ok";
});

check("menus/命令引用完整性", () => {
  const commands = new Map((manifest.contributions || []).filter((c) => c.type === "command").map((c) => [c.id, c]));
  const workbenches = new Set((manifest.contributions || []).filter((c) => c.type === "workbench").map((c) => c.id));
  for (const c of (manifest.contributions || []).filter((x) => x.type === "menus")) {
    for (const item of c.items || []) {
      const command = commands.get(item.command);
      assert(command, `menus 引用了不存在的命令: ${item.command}`);
      assert(command.action, `command 缺少 action: ${item.command}`);
      if (command.action.type === "open-workbench") {
        assert(workbenches.has(command.action.workbench), `open-workbench 引用了不存在的工作台: ${command.action.workbench}`);
      }
      assert(manifest.entrypoints && manifest.entrypoints.ui, `command 需要 UI 入口: ${item.command}`);
    }
  }
  return `${commands.size} 个命令`;
});

if (packagePath) {
  check("打包后 manifest 与源一致（menus.group）", () => {
    const output = execFileSync(
      process.platform === "win32" ? "powershell.exe" : "sh",
      process.platform === "win32"
        ? [
            "-NoProfile",
            "-Command",
            `Add-Type -AssemblyName System.IO.Compression.FileSystem; $z=[System.IO.Compression.ZipFile]::OpenRead('${packagePath}'); $e=$z.GetEntry('manifest.json'); $r=New-Object System.IO.StreamReader($e.Open()); $t=$r.ReadToEnd(); $r.Close(); $z.Dispose(); Write-Output $t`,
          ]
        : ["-c", `unzip -p '${packagePath}' manifest.json`],
      { encoding: "utf8" },
    );
    const packaged = JSON.parse(output);
    const packagedMenus = (packaged.contributions || []).find((c) => c.type === "menus");
    assert(packagedMenus, "打包后缺少 menus 贡献点");
    for (const item of packagedMenus.items || []) {
      assert(MENU_GROUP_WORDS.includes(item.group), `打包后 group 非法: ${item.group}`);
    }
    return "ok";
  });
}

report();

function report() {
  const failed = results.filter((r) => r.status === "fail").length;
  const warned = results.filter((r) => r.status === "warn").length;
  for (const r of results) {
    console.log(`${r.status === "pass" ? "PASS" : r.status === "warn" ? "WARN" : "FAIL"}  ${r.name}${r.detail ? " — " + r.detail : ""}`);
  }
  console.log(`\nMANIFEST CHECK: ${results.length - failed - warned}/${results.length} PASS, ${warned} WARN, ${failed} FAIL`);
  process.exit(failed ? 1 : 0);
}
