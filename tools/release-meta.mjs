#!/usr/bin/env node
/**
 * 生成 DBX 官方商店发布所需的元数据（v1）。
 *
 * 输入：
 *   - 已由 `dbx-plugin package .` 生成的未签名候选包与 artifact.json；
 *   - manifest.json（id/version/publisher/name/permissions 必须与包内一致）。
 *
 * 输出：
 *   dist/release-candidates.json      —— GitHub Release 资产（作者发布用）
 *   dist/store/candidates/<id>.json   —— 提交到 t8y2/dbx-store 的 candidate
 *   dist/store/publishers/<id>.json   —— 首次提交的 publisher record
 *
 * 用法（在插件根目录）：
 *   node tools/release-meta.mjs \
 *     --package dist/com.dbx.wsl-0.1.0-windows-x64.dbxp \
 *     --repo w89588/dbx-plugin-wsl \
 *     --tag v0.1.0 \
 *     --publisher w89588
 *
 * 约束（来自 t8y2/dbx-store/CONTRIBUTING.md 与 schemas/plugin-candidate.schema.json）：
 *   - 所有 URL 必须是 HTTPS，指向不可变的版本化 Release 资产；
 *   - sha256/size 必须与未签名候选包字节完全一致；
 *   - manifest 的 id/version/publisher 必须与 candidate 相同；
 *   - 不得包含 signature.json 或 signingKeyId。
 */
import { createHash } from 'node:crypto';
import { mkdirSync, readdirSync, readFileSync, statSync, writeFileSync } from 'node:fs';
import { basename, dirname, join, resolve } from 'node:path';

const root = resolve(dirname(new URL(import.meta.url).pathname.replace(/^\/([A-Za-z]:)/, '$1')), '..');

function parseArgs(argv) {
  const out = {};
  for (let i = 0; i < argv.length; i++) {
    const arg = argv[i];
    if (!arg.startsWith('--')) continue;
    const key = arg.slice(2);
    const value = argv[i + 1] && !argv[i + 1].startsWith('--') ? argv[++i] : 'true';
    out[key] = value;
  }
  return out;
}

const args = parseArgs(process.argv.slice(2));
const manifest = JSON.parse(readFileSync(join(root, 'manifest.json'), 'utf8'));
const pkgPath = resolve(args.package || findDefaultPackage());
const repo = args.repo || `w89588/${manifest.id}`;
const tag = args.tag || `v${manifest.version}`;
const publisher = args.publisher || manifest.publisher;
const license = args.license || 'Apache-2.0';
const target = args.target || 'windows-x64';
const outDir = resolve(args.out || join(root, 'dist'));

function findDefaultPackage() {
  const distDir = join(root, 'dist');
  const match = readdirSync(distDir).find((name) => name.endsWith('.dbxp'));
  if (!match) throw new Error('dist/ 下没有 .dbxp，请先运行 dbx-plugin package .');
  return join(distDir, match);
}

if (!/^[a-z0-9][a-z0-9._-]*$/.test(manifest.id)) throw new Error(`非法插件 id: ${manifest.id}`);
if (!/^[a-z0-9][a-z0-9._-]*$/.test(publisher)) throw new Error(`非法 publisher: ${publisher}`);
if (manifest.publisher !== publisher) throw new Error(`manifest.publisher(${manifest.publisher}) 与 --publisher(${publisher}) 不一致`);

const bytes = readFileSync(pkgPath);
const sha256 = createHash('sha256').update(bytes).digest('hex');
const size = statSync(pkgPath).size;
const pkgName = basename(pkgPath);
const baseUrl = `https://github.com/${repo}/releases/download/${tag}`;
const rawUrl = `https://raw.githubusercontent.com/${repo}/${tag}`;
const description = (args.description || manifest.description || '').trim();
const name = manifest.name;

const releaseCandidates = {
  plugin: {
    id: manifest.id,
    name,
    description,
    publisher: manifest.publisher,
    version: manifest.version,
    permissions: manifest.permissions || [],
  },
  artifacts: [{ target, url: `${baseUrl}/${pkgName}`, sha256, size }],
};

const candidate = {
  schemaVersion: 1,
  id: manifest.id,
  publisher: manifest.publisher,
  version: manifest.version,
  name,
  description,
  icon: `${rawUrl}/${manifest.icon || 'assets/plugin.svg'}`,
  tags: ['wsl', 'containers', 'windows', 'devops'],
  permissions: manifest.permissions || [],
  source: `https://github.com/${repo}/tree/${tag}`,
  homepage: `https://github.com/${repo}`,
  license,
  releaseNotes: readFileSync(join(root, 'RELEASE_NOTES.md'), 'utf8').trim(),
  localizations: {
    'zh-CN': {
      name: 'WSL 管理器',
      description: '在 DBX 中可视化管理 WSL 发行版（wsl）与 WSL 容器（wslc），支持注册表凭据与镜像管理。',
    },
  },
  targets: [{ target, url: `${baseUrl}/${pkgName}`, sha256, size }],
};

const publisherRecord = { id: publisher, name: publisher, status: 'unverified' };

function writeJson(path, value) {
  mkdirSync(dirname(path), { recursive: true });
  writeFileSync(path, JSON.stringify(value, null, 2) + '\n', 'utf8');
  console.log(`written ${path}`);
}

writeJson(join(outDir, 'release-candidates.json'), releaseCandidates);
writeJson(join(outDir, 'store', 'candidates', `${manifest.id}.json`), candidate);
writeJson(join(outDir, 'store', 'publishers', `${publisher}.json`), publisherRecord);

console.log(JSON.stringify({ package: pkgName, target, sha256, size, tag }, null, 2));
