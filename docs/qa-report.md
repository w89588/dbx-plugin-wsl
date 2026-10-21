# WSL Manager 插件 —— QA 验证报告（v0.1.0 官方商店发布）

| 项目 | 内容 |
| --- | --- |
| 被测插件 | `com.dbx.wsl` — WSL Manager **v0.1.0**（DBX 插件，Go Sidecar + 单文件 UI） |
| 发布信息 | publisher **`w89588`**；源码仓库 `https://github.com/w89588/dbx-plugin-wsl`；DBX 插件商店（`t8y2/dbx-store` fork：`w89588/dbx-store`） |
| 版本重置 | 由开发期 `v0.1.1` 重置为正式发布版 **`0.1.0`**：manifest / backend `main.go` / UI `APP_VERSION` / probe / mock / contract / README / installation 全部同步；Go module path 改为 `github.com/w89588/dbx-plugin-wsl` |
| 最终 UI | SHA256 `0DD851C9B0ADDEF3CEC39DEF27973C4CB2EDE18608C4AFDC487826CBDCC8C878`，321,026 bytes |
| 最终 Sidecar | SHA256 `3CE4A69F774A675AD6FB2E7AEBF7336692E0387F645DC11D78EC2239669F696A`，5,629,952 bytes（go build 产物） |
| 最终安装包 | `dist/com.dbx.wsl-0.1.0-windows-x64.dbxp`，3,307,021 bytes，SHA256 `CABC5DD6D44F8B9555FE46683B7D2309B8F3FEAAFD85D5EF301548CF44E672F4` |
| 最终运行 | `node tests/run-all.mjs`，开始 2026-10-21 22:09:33（+08:00），总耗时 **117.2s**，结果 **PASS** |
| 结论 | **通过**：sidecar-probe **26/26 PASS**；ui-cdp-check **81/81 PASS**（55 基础 + 20 终版 + 6 注册表），0 FAIL / 0 WARN，20 张截图；`plugin/initialize` 实测 `plugin.id=com.dbx.wsl`、`plugin.version=0.1.0`；`manifest-check` 11/11 PASS；本地包/候选 JSON 哈希一致；GitHub Release 资产的独立校验见第 10 节（`docs/release-verification.md`，待 Release 发布后完成） |

---

## 1. 结论摘要

- **发布回归（v0.1.0）**：在版本重置、publisher/模块路径变更后的最终代码上完整重跑，sidecar 26/26、UI 81/81 全部通过，0 FAIL / 0 WARN，耗时 117.2s（网络较上一轮顺畅，注册表检查更快）。
- **身份冒烟**：`plugin/initialize` 返回 `{"protocolVersion":1,"plugin":{"id":"com.dbx.wsl","version":"0.1.0"},"capabilities":["events","jobs"]}`，与 manifest 完全一致；`system/info`、`wslc/info` 等均为真实 WSL/wslc 数据（WSL 3.0.4.0 / wslc 3.0.4.0）。
- **功能覆盖**：原有 22 项 RPC + v0.1.1 引入的注册表凭据 4 项（错误凭据 → `REGISTRY_AUTH_FAILED` 且无密码泄漏、参数校验、logout 幂等、pull 失败 → `REGISTRY_AUTH_REQUIRED`）；UI 55 基础 + 20 终版 + 6 注册表专项（含空提交「参数不合法」文案、密码不入 DOM/日志/diagnostics、拉取失败 callout +「去登录」）。
- **发布产物（本地预检）**：`.dbxp` 与 `artifact.json`、`release-candidates.json`、`dist/store/candidates/com.dbx.wsl.json` 的 sha256/size 完全一致（`cabc5dd6…` / 3,307,021）；publisher=`w89588`、permissions 与 candidate 一致；`manifest-check` 11/11 PASS。GitHub Release 资产的独立下载校验将在 Lead 发布后进行（第 10 节）。
- **保留的历史记录**：Docker Hub 曾在 v0.1.1 QA 期间出现一次瞬断（EOF/E_FAIL），工具已按「网络瞬断 vs 分类缺陷」区分；本轮 v0.1.0 回归未再出现（见 7.4）。
- **清理**：`qa-` 容器/卷全部删除，网络仅默认 bridge/host/none；Debian 测试前后均 `Stopped`；dev host / Chrome / sidecar 无残留，5192/5193/5194 无监听，Chrome profile 已清空。

---

## 2. 环境

### 2.1 软硬件与运行时

| 项 | 值 | 来源 |
| --- | --- | --- |
| OS | Windows 11 专业版，Build `10.0.26300.9711`（`10.0.26300`） | `system/info.platform` + `Win32_OperatingSystem` |
| 主机 | `PC-HOME` / 用户 `iaz00` / `amd64` | `system/info.platform` |
| WSL | `3.0.4.0`；内核 `6.18.54.1-1`；WSLg `1.0.79`；MSRDC `1.2.7391`；Direct3D `1.611.1-81528511`；DXCore `10.0.26100.1-240331-1435.ge-release` | `wsl --version` / `system/info.wsl.version` |
| wslc | `3.0.4.0`；client `3.0.4.0`；Session Manager `3.0.4`；会话 `wslc-cli-iaz00`（id=1） | `wslc --version` / `wslc/info` |
| 发行版 | Debian，WSL2，测试前后均 `Stopped`；`PRETTY_NAME=Debian GNU/Linux 13 (trixie)`、内核 `6.18.54.1-microsoft-standard-WSL2`、`x86_64`、uid 1000；vhd 约 8.8 GB | `wsl -l -v` / `wsl/distros/list` / `wsl/distros/info` |
| wslc 镜像 | `alpine:3.20`（`bf8527eb54c3`，7.81 MB，测试前已存在） | `wslc/images/list` |
| Node.js | `v24.22.0`（QA 工具运行环境） | `node --version` |
| Go | `go1.27.3 windows/amd64`（sidecar 构建） | `go version` |
| Chrome | `Chrome/155.0.8059.85`（headless，CDP） | CDP `/json/version` |
| dbx-plugin CLI | `@dbx-app/plugin-cli` `0.1.9` | `package.json` |
| 契约版本 | `docs/rpc-contract.md`（版本 0.1.0 / publisher w89588 同步后；含 §2 错误码、§4.3 registry、§7.11-12、§8 验收 1-13、§9 实现注记） | 文件快照 |

> **版本兼容说明**：契约示例文案以 WSL/wslc `3.0.3` 书写；本机实测 `3.0.4.0`，26+81 项检查全部通过，未发现 3.0.3→3.0.4 行为差异导致的回归。工具内没有硬编码 `3.0.3` 的断言。

### 2.2 被测产物（v0.1.0 发布快照）

| 产物 | 值 |
| --- | --- |
| UI `wsl-manager/ui/index.html` | size `321026`，mtime `2026-10-21T22:06:52+08:00`，SHA256 `0DD851C9B0ADDEF3CEC39DEF27973C4CB2EDE18608C4AFDC487826CBDCC8C878` |
| Sidecar（go build 产物） | `C:\Users\iaz00\AppData\Local\dbx-wsl-dev\qa-build\sidecar.exe`，size `5,629,952`，SHA256 `3CE4A69F774A675AD6FB2E7AEBF7336692E0387F645DC11D78EC2239669F696A` |
| 安装包 | `dist/com.dbx.wsl-0.1.0-windows-x64.dbxp`，size `3,307,021`，SHA256 `CABC5DD6D44F8B9555FE46683B7D2309B8F3FEAAFD85D5EF301548CF44E672F4` |
| 包/Manifest 预检 | `node tools/manifest-check.mjs manifest.json --package dist/com.dbx.wsl-0.1.0-windows-x64.dbxp` → **11/11 PASS，0 WARN，0 FAIL** |
| 构建环境 | `GOTMPDIR=D:\work\dbx-plugin-wsl\.gotmp`、`GOCACHE=D:\work\dbx-plugin-wsl\.gocache` |
| dev host 数据目录 | `C:\Users\iaz00\AppData\Local\dbx-wsl-dev\qa`（**workspace 外**，原因见 2.3） |
| dev host 地址 | `http://127.0.0.1:5193/`，`/api/diagnostics` → `backendState=ready`，182 条诊断、RPC 完成 55 次、RPC error 1 条（错误凭据负向测试，预期内） |
| Chrome profile | `C:\Users\iaz00\AppData\Local\dbx-wsl-dev\chrome-qa\<run-id>`（运行结束自动清理，实测无残留） |
| 插件版本 / 发布者 | manifest/backend/UI 均为 `version=0.1.0`、`publisher=w89588`；module path `github.com/w89588/dbx-plugin-wsl` |

### 2.3 环境注意事项：workspace 的 Low integrity label（**不是插件缺陷**）

本会话 workspace（`D:\work\dbx-plugin-wsl\...`）目录带 **Low mandatory integrity label**；从该目录直接启动的 exe 会以 Low IL 运行，其子进程调用 `wsl.exe` / `wslc.exe` 会得到 `Wsl/E_ACCESSDENIED`。

- 从 workspace 启动的 exe：`Mandatory Label\Low Mandatory Level (S-1-16-4096)` → `wsl -l -v` 返回 `Wsl/E_ACCESSDENIED`；
- 从 `C:\Users\iaz00\AppData\Local\dbx-wsl-dev\...` 启动：`Medium (S-1-16-8192)` → 正常；
- 从真实 DBX 插件商店目录 `D:\tools\Scoop\persist\dbx\data\plugins\...` 启动：`Medium` → 正常。

**结论**：本会话 workspace 的沙箱完整性标签特性，**不影响用户在 DBX 中安装插件后的运行**，不记为插件缺陷；契约 §9.3 已记录。QA 的处理方式：dev host 使用 workspace 外 data-dir、把 `@dbx-app/plugin-cli` 复制到 `%LOCALAPPDATA%\dbx-wsl-dev\plugin-cli` 后启动、Chrome `--user-data-dir` 使用 `%LOCALAPPDATA%\dbx-wsl-dev\chrome-qa`；`sidecar-probe` 会检测并告警 workspace 内 sidecar；可用 `DBX_WSL_QA_HOME` 覆盖。

### 2.4 发布相关变更（v0.1.0）

| 变更 | 验证方式 |
| --- | --- |
| 版本重置 0.1.1 → **0.1.0**；publisher **w89588**；module path `github.com/w89588/dbx-plugin-wsl` | manifest/backend/UI 同步；`plugin/initialize` 断言 id/version；`manifest-check` 11/11 |
| 注册表凭据（login/logout、AUTH_FAILED/REQUIRED/RATE_LIMITED 分类）+ 空提交文案修复 | sidecar 26 项 + UI 注册表专项 6 项（含「参数不合法」标题断言） |
| 官方商店发布材料：`.dbxp`、`artifact.json`、`release-candidates.json`、`dist/store/candidates/com.dbx.wsl.json`、`dist/store/publishers/w89588.json` | 本地一致性预检（见 2.5）；GitHub Release 后做独立下载校验（第 10 节） |

### 2.5 本地发布产物一致性预检

| 校验项 | 结果 |
| --- | --- |
| `.dbxp` sha256 / size | `cabc5dd6d44f8b9555fe46683b7d2309b8f3feaafd85d5ef301548cf44e672f4` / `3307021` |
| `dist/com.dbx.wsl-0.1.0-windows-x64.artifact.json` | target=`windows-x64`，url=`com.dbx.wsl-0.1.0-windows-x64.dbxp`，sha256/size **与包一致** |
| `dist/release-candidates.json` | plugin：`com.dbx.wsl / WSL Manager / w89588 / 0.1.0`，permissions=`host.workbench,host.events,host.storage`；artifacts：target=`windows-x64`、GitHub Release URL、sha256/size **与包一致** |
| `dist/store/candidates/com.dbx.wsl.json` | schemaVersion=1、id/publisher/version/name/description/permissions/source/homepage/license/targets 齐全；targets sha256/size **与包一致**；本地化 zh-CN 齐全 |
| `dist/store/publishers/w89588.json` | `{id:"w89588", name:"w89588", status:"unverified"}` |
| `tools/manifest-check.mjs` | **11/11 PASS**（含打包后 manifest 与源一致） |

> 上述为**本地文件**一致性预检；GitHub Release 上传后的远端资产需重新下载并独立校验（sha256/大小/manifest 身份/candidate schema），结论见第 10 节与 `docs/release-verification.md`。

---

## 3. 交付物与运行命令

### 3.1 交付物

| 路径 | 说明 |
| --- | --- |
| `tools/sidecar-probe.mjs` | 零依赖 Sidecar RPC 探针：**26 项检查**（原 22 + 注册表 4）；含「网络瞬断 vs 分类缺陷」区分逻辑 |
| `tools/ui-cdp-check.mjs` | 零依赖 UI 检查：**81 项检查**（55 基础 + 20 终版 + 6 注册表），**20 张截图** |
| `tests/run-all.mjs` | 一键端到端：构建 sidecar → 启动 dev host（5193）→ 等 diagnostics ready → sidecar-probe → ui-cdp-check → 还原发行版状态 → 关闭 dev host → 汇总 |
| `tests/fixtures/mock-sidecar.mjs` | 契约级 mock sidecar（含 registry login/logout、pull 认证失败），自测 26 项全 PASS |
| `tests/screenshots/*.png` | QA 自动化截图 20 张；另有前端交付参考截图（见第 6 节附录） |
| `tests/tmp/run-all-report.json` | 端到端汇总（步骤、耗时、PASS/FAIL、截图索引、发行版还原记录） |
| `tests/tmp/sidecar-report.json` | 26 项 RPC 检查明细（断言、证据、网络瞬断记录、清理记录；含 initialize 证据） |
| `tests/tmp/ui-report.json` | 81 项 UI 检查明细（页面快照、注册表专项证据、RPC 汇总、截图） |
| `tests/tmp/devhost.log` | 本次 dev host 完整日志 |
| `dist/com.dbx.wsl-0.1.0-windows-x64.dbxp` | 安装包（Lead 打包，本报告记录哈希） |
| `docs/release-verification.md` | GitHub Release 资产独立校验报告（第 10 节，待 Release 发布后生成） |

### 3.2 常用命令

```powershell
cd D:\work\dbx-plugin-wsl\wsl-manager

# 一键端到端（推荐；自动构建 + 启动/关闭 dev host + 还原发行版状态）
node tests\run-all.mjs

# 只跑注册表相关 RPC（自动定位 workspace 外 sidecar，或 --sidecar=... 指定）
node tools\sidecar-probe.mjs --only=registry
node tools\sidecar-probe.mjs --only=pull-auth

# 只跑 UI（需要 dev host 已在跑）
node tools\ui-cdp-check.mjs --dev-url=http://127.0.0.1:5193/
node tools\ui-cdp-check.mjs --pages=images               # 子集模式（跳过终版/注册表专项，仅调试）

# Manifest / 包预检
node tools\manifest-check.mjs manifest.json --package dist\com.dbx.wsl-0.1.0-windows-x64.dbxp

# 手动启动 dev host（data-dir 必须在 workspace 外）
$env:GOTMPDIR='D:\work\dbx-plugin-wsl\.gotmp'; $env:GOCACHE='D:\work\dbx-plugin-wsl\.gocache'
node C:\Users\iaz00\AppData\Local\dbx-wsl-dev\plugin-cli\node_modules\@dbx-app\plugin-cli\bin\dbx-plugin.js `
  dev --path D:\work\dbx-plugin-wsl\wsl-manager --port 5193 --data-dir C:\Users\iaz00\AppData\Local\dbx-wsl-dev\qa

# harness 自测（不依赖真实后端/网络）
node tools\sidecar-probe.mjs --sidecar=tests\fixtures\mock-sidecar.mjs
```

> 注册表检查会真实访问 docker.io（本轮网络较顺畅：错误凭据 ~4s、拉取不存在镜像 ~6.7s）；请预留 ≥120s 超时。

---

## 4. 契约第 8 节验收样例覆盖（1-13）

| # | 验收要求 | 对应检查项 | 结果 | 证据摘要 |
| --- | --- | --- | --- | --- |
| 1 | `system/info` 双工具可用、`distroCount >= 1` | `sidecar/system-info` | **PASS** | `wsl.available=true`、`wslc.available=true`（`3.0.4.0`）、`distroCount=1`、`warnings=[]` |
| 2 | `wsl/distros/list` 含 Debian 且 `isDefault:true` | `sidecar/wsl-distros-list` | **PASS** | Debian / WSL2 / Stopped / isDefault=true |
| 3 | `wsl/distros/info` 的 `PRETTY_NAME` 含 Debian、`disk.totalBytes > 0` | `sidecar/wsl-distros-info` | **PASS** | `Debian GNU/Linux 13 (trixie)`、totalBytes≈1.08TB |
| 4 | `wsl/distros/exec` 执行 `echo hello` | `sidecar/wsl-distros-exec` | **PASS** | `exitCode=0`、`stdout="hello\n"`、`durationMs=107` |
| 5 | `wslc/info` 的 `server.sessionManagerVersion` 非空 | `sidecar/wslc-info` | **PASS** | `sessionManagerVersion=3.0.4`、client `3.0.4.0` |
| 6 | `wslc/networks/list` 至少含 bridge/host/none | `sidecar/wslc-networks-list` | **PASS** | bridge / host / none 均存在 |
| 7 | `wslc/images/pull` `alpine:3.20` 成功 `job/end` | `sidecar/wslc-image-pull` | **PASS** | `job/end.ok=true, exitCode=0, result.ref=alpine:3.20, durationMs=21,365` |
| 8 | 容器 run → list → logs → exec → stats → stop → remove | `sidecar/wslc-containers-lifecycle` | **PASS** | 全链路成功，stop/remove 后列表不再出现 |
| 9 | inspect 不存在容器 → `NOT_FOUND` | `sidecar/wslc-error-not-found` | **PASS** | `data.code=NOT_FOUND`；空参数 `INVALID_PARAMS` |
| 10 | `console/run` 执行 `wsl -l -v` → `exitCode:0` | `sidecar/console-run` | **PASS** | `exitCode=0`、`tool=wsl` |
| 11 | 错误凭据 login → `REGISTRY_AUTH_FAILED` 且无密码；logout 幂等 | `sidecar/wslc-registry-login-invalid` / `wslc-registry-logout` | **PASS** | login 3.99s：`data.code=REGISTRY_AUTH_FAILED`，message/hint 正常；RPC error/事件/stderr 无密码；logout 3 次（含省略 server）均 `loggedOut:true` |
| 12 | 拉取不存在私有镜像 → `job/end.error.code=REGISTRY_AUTH_REQUIRED` | `sidecar/wslc-pull-auth-required` | **PASS** | 6.67s：`ok=false, exitCode=1, code=REGISTRY_AUTH_REQUIRED`；detail 保留 `pull access denied ... may require 'docker login'` + `WSLC_E_IMAGE_NOT_FOUND`；3 条 job/output 原文保留；镜像未落盘 |
| 13 | 登录成功后拉取私有镜像 | —（需有效凭据） | **未自动化** | 无有效凭据且不应使用真实账号；登录成功路径由 backend 单测覆盖，端到端需用户凭据，见 8.1 |

> `plugin/initialize` 冒烟（发布身份）：`protocolVersion=1`、`plugin.id=com.dbx.wsl`、`plugin.version=0.1.0`、`capabilities=["events","jobs"]`。

---

## 5. 详细结果

### 5.1 `tools/sidecar-probe.mjs`（26/26 PASS，59.2s）

| # | 检查项 | 结果 | 耗时 | 关键断言 / 证据 |
| --- | --- | --- | --- | --- |
| 1 | `initialize` | PASS | 10 ms | `protocolVersion=1`、`plugin.id=com.dbx.wsl`、`plugin.version=0.1.0`、`capabilities=["events","jobs"]` |
| 2 | `initialize-protocol-mismatch` | PASS | 0 ms | 协议 [999] 返回 JSON-RPC error（-32001） |
| 3 | `system-info` | PASS | 132 ms | WSL 3.0.4.0 / wslc 3.0.4.0 可用、`distroCount=1` |
| 4 | `system-wsl-status` | PASS | 30 ms | `defaultDistro=Debian`、`defaultVersion=2` |
| 5 | `wsl-distros-list` | PASS | 28 ms | 恰好 1 个 `isDefault`、Debian 默认 |
| 6 | `wsl-distros-info` | PASS | 3,089 ms | osRelease/disk/kernel/arch/uid/ipv4 齐全 |
| 7 | `wsl-distros-exec` | PASS | 107 ms | RunResult 全字段 + `echo hello` |
| 8 | `wslc-info` | PASS | 37 ms | 3.0.4.0 / sessionManager 3.0.4 |
| 9 | `wslc-networks-list` | PASS | 42 ms | bridge/host/none + 字段类型 |
| 10 | `wslc-images-list` | PASS | 56 ms | 镜像字段结构 |
| 11 | `wslc-image-pull` | PASS | 21,365 ms | 长任务事件时序、`job/end`、`jobs/get`、`jobs/list`、`images/inspect` |
| 12 | `wslc-containers-lifecycle` | PASS | 11,833 ms | run/list/inspect/exec/logs/stats/stop/remove 全链路 |
| 13 | `wslc-volumes` | PASS | 137 ms | create → list → inspect → remove |
| 14 | `wslc-networks` | PASS | 545 ms | create(bridge) → list → inspect → remove |
| 15 | `wslc-error-not-found` | PASS | 31 ms | `NOT_FOUND`；空参数 `INVALID_PARAMS` |
| 16 | `wslc-logs-follow-jobs-cancel` | PASS | 2,111 ms | follow → `job/output` → `jobs/list` → `jobs/cancel` → `job/end(ok=false)` → `status=cancelled` |
| 17 | `console-run` | PASS | 34 ms | `wsl -l -v` exit 0 |
| 18 | `system-config-read` | PASS | 38 ms | `.wslconfig` / wslc settings 结构 |
| 19 | `console-presets` | PASS | 1 ms | 预设命令结构 |
| 20 | `wslc-events` | PASS | 6,883 ms | `events/start` → 容器 stop → `wslc/event` → `events/stop` → `job/end` |
| 21 | `wslc-registry-login-validation` | PASS | 2 ms | 6 组非法参数 → `INVALID_PARAMS`，错误信息无密码（无网络） |
| 22 | `wslc-registry-login-invalid` | PASS | 3,986 ms | `docker.io` + 随机凭据 → `REGISTRY_AUTH_FAILED`；RPC error/事件/stderr 无密码 |
| 23 | `wslc-registry-logout` | PASS | 101 ms | 未登录 registry（含省略 server）3 次均 `loggedOut:true`，幂等 |
| 24 | `wslc-pull-auth-required` | PASS | 6,673 ms | `AUTH_REQUIRED/ok=false/exitCode=1`；detail 保留 `docker login` 原文；3 条输出；镜像未落盘；**网络瞬断策略**：纯网络 `PROCESS_FAILED` → PASS+warning+重试；真实 registry 响应误分类 → FAIL |
| 25 | `unknown-method` | PASS | 1 ms | 返回 JSON-RPC `-32601` |
| 26 | `stdout-clean` | PASS | 0 ms | stdout 无非协议输出 |

> 清理记录：`restore-distro-stopped Debian OK`；注册表测试未创建持久凭据（错误登录不写入；logout 使用不存在的测试 registry，未触碰用户 docker.io 凭据）。

### 5.2 UI 基础检查（55/55 PASS）

| 检查类别 | 数量 | 结果 |
| --- | --- | --- |
| 全局（dev host / 调试壳 / 工作台打开 / iframe 内容 / 侧边栏 9 项 / 无异常 / 无 console error / 页面渲染 / 20 张截图 / backend ready） | 10 | 全部 PASS |
| 每个页面 5 项（截图 / 无白屏 / 无 uncaught exception / 无 console error / 关键 DOM 结构）× 9 页 | 45 | 全部 PASS |

| 页面 | 可见文本长度 | 页面切换 | 关键 DOM 证据 | 截图（bytes） |
| --- | --- | --- | --- | --- |
| dashboard 概览 | 948 | click | cards=27；真实数据（Debian、WSL 3.0.4.0、发行版 1、镜像 1、网络 3、会话 1） | `dashboard.png`（149,054） |
| distros 发行版 | 280 | click | tables=3、rows=2；Debian / WSL2 / 已停止 / 8.77 GB | `distros.png`（94,133） |
| containers 容器 | 167 | click | tables=2、emptyState=true | `containers.png`（96,787） |
| images 镜像 | 226 | click | tables=3、rows=2；alpine:3.20；工具栏「注册表凭据」 | `images.png`（92,746） |
| volumes 数据卷 | 147 | click | tables=2、emptyState=true | `volumes.png`（95,822） |
| networks 网络 | 292 | click | tables=3、rows=4；bridge/host/none | `networks.png`（106,330） |
| console 命令控制台 | 388 | click | inputs=2、buttons=26 | `console.png`（100,936） |
| events 事件 | 177 | click | tables=2、cards=3、emptyState=true | `events.png`（95,447） |
| settings 设置 | 713 | click | inputs=4；刷新/语言/开关 + `.wslconfig` | `settings.png`（122,386） |

其他 UI 证据：dev host 调试壳标题「插件调试」；工作台标签 `WSL Manager / WSL 管理器` 与 manifest 一致；工作台为 `sandbox="allow-scripts"` 的 `srcdoc` OOPIF iframe；全程 `Runtime.exceptionThrown` 0 条、console error 0 条；`/api/diagnostics`：`backendState=ready`、182 条诊断。

### 5.3 UI 终版专项检查（20/20 PASS）

| # | 检查项 | 结果 | 证据 |
| --- | --- | --- | --- |
| 1 | 概览 hero 卡片 + KPI 网格 | PASS | hero 统计 6 项（内核/WSLC/发行版/容器/会话/镜像）；KPI 6 张 |
| 2 | 概览 wslc 卡片刷新触发 sessions 接线 | PASS | 点击 `dashboard.wslcInfo` 后出现会话信息 |
| 3 | 发行版固定 6 个操作按钮 | PASS | start/setDefault/terminal/explorer/detail/manage，6/6 可见 |
| 4 | 磁盘列单行显示 | PASS | `8.77 GB`；`white-space=nowrap`；Range rects=1；高度 19px |
| 5 | 发行版详情抽屉（`wsl/distros/info` 90s） | PASS | textLen=574；系统/内核/磁盘/IPv4 等 |
| 6 | `wsl/distros/wslconf` 接线 | PASS | 内容渲染 20 bytes（`[boot] systemd=true`） |
| 7 | 抽屉内执行 `echo hello` | PASS | `成功 / 退出码 0 / 101ms / hello` |
| 8 | 运行容器对话框 | PASS | 字段完整（镜像/名称/命令/环境/端口/卷/资源限制） |
| 9 | 拉取镜像对话框 | PASS | 含私有镜像提示与「去登录」按钮（buttons=4） |
| 10 | 暗色主题概览页 | PASS | `data-theme=dark`，bg `rgb(24,24,27)` |
| 11 | 移动端概览（390×844） | PASS | 宿主 390px、iframe 250px；无横向溢出；菜单按钮可见 |
| 12 | 移动端容器页 | PASS | 无横向溢出 |
| 13 | 移动端侧边栏展开 | PASS | `#app.sidebar-open=true`、`#sidebar` 可见 |
| 14 | 无预期外 RPC 失败 | PASS | 55 次 RPC 完成；仅 1 条负向测试 `REGISTRY_AUTH_FAILED` 属预期 |
| 15 | `wslc/sessions/list` 接线 | PASS | calls=1，maxMs=23 |
| 16 | `wsl/distros/wslconf` 接线 | PASS | calls=1，maxMs=113 |
| 17 | `wsl/distros/exec` 接线 | PASS | calls=1，maxMs=102 |
| 18 | `wsl/distros/info` 接线 | PASS | calls=1，maxMs=2,610 |
| 19 | RPC 耗时均在 60s/90s 配置内 | PASS | 最慢 `wsl/distros/info:2610ms`、`system/info:153ms`、`wslconf:113ms`、`exec:102ms` |
| 20 | UI 按契约 §9.1 传 60s/90s 超时（静态） | PASS | 9/9 命中（system/info/list 类 60000；info/exec/wslconf 90000 等） |

### 5.4 v0.1.0 注册表凭据专项（6/6 PASS）

| # | 检查项 | 结果 | 证据 |
| --- | --- | --- | --- |
| 1 | 镜像页「注册表凭据」对话框字段与 password 类型 | PASS | `server / username / password` 字段齐全；`password.type=password`；按钮含「登录」「登出」「取消」；截图 `registry-credentials.png` |
| 2 | 空 username/password 提交校验 | PASS | 错误标题为 **「参数不合法: 请输入用户名和密码。」**，断言不含「内部错误」；截图 `registry-validation.png` |
| 3 | 错误凭据登录：失败提示 + 密码即时清空 | PASS | 2.0s 内展示 `注册表登录失败 / 请检查用户名/密码或访问令牌… / 技术详情`；提交后 password 输入框立即清空 |
| 4 | 密码不出现在页面/输入框/宿主 UI/storage | PASS | 页面文本、输入框值、dev host shell、`dbxPlugin.storage.get('registry.credentials')` 均无密码；storage 不可用（dev host 能力降级，UI 显示“仅保存在内存中”） |
| 5 | UI 拉取不存在镜像：认证失败 callout +「去登录」 | PASS | 出现「需要登录注册表」callout（对话框内 + toast）；点击「去登录」打开凭据对话框且 server 预填 `docker.io`；原始 docker 错误保留；截图 `registry-auth-error.png`；attempts=1 |
| 6 | 密码不出现在 dev host 诊断日志 | PASS | 扫描 182 条 `/api/diagnostics` 记录，未发现密码明文 |

> sidecar 侧密码脱敏由 5.1 的 #21/#22 覆盖；UI 侧由本节 #4/#6 覆盖，形成双向闭环。

### 5.5 长任务 / 事件 / 清理 / 副作用

- **长任务时序**：`job/output` 全部早于 `job/end`；`job/end` 恰好一次；`jobs/get` 与事件行数可对账。
- **取消**：`jobs/cancel` → `job/end(ok=false)` → `jobs/get.status=cancelled`。
- **wslc 事件**：`wslc/events/start` → 容器 stop → `wslc/event` → `events/stop` → `job/end`。
- **注册表副作用**：错误凭据登录不产生持久凭据；logout 使用不存在的测试 registry（未触碰用户真实 docker.io 凭据）；失败镜像未落盘；测试结束后登录状态与测试前一致。
- **资源清理**：`qa-` 容器/卷全部删除，网络仅默认 bridge/host/none。
- **发行版状态**：测试前 `Debian=Stopped`；probe 结束自动 restore；UI 阶段 info 按需启动，run-all 结束前再次核对并自动 `wsl --terminate`；终态 `Stopped`。
- **进程清理**：dev host 通过 `taskkill /T /F` 关闭；Chrome 与临时 profile 清理（`chrome-qa` 为空）；5192/5193/5194 无残留监听。

---

## 6. 截图索引（QA 自动化，20 张）

均为 1440×900（移动端为 780×1688 @2x 模拟 390×844），由 `tools/ui-cdp-check.mjs` 通过 `Page.captureScreenshot` 生成；路径相对 `wsl-manager/`。

| # | 截图 | 说明 | bytes |
| --- | --- | --- | --- |
| 1 | `tests/screenshots/dashboard.png` | 概览（hero + 6 KPI + 运行时真实数据） | 149,054 |
| 2 | `tests/screenshots/distros.png` | 发行版（表格、6 个行操作按钮） | 94,133 |
| 3 | `tests/screenshots/containers.png` | 容器（空态、实时统计开关） | 96,787 |
| 4 | `tests/screenshots/images.png` | 镜像（alpine:3.20、「注册表凭据」入口） | 92,746 |
| 5 | `tests/screenshots/volumes.png` | 数据卷（空态） | 95,822 |
| 6 | `tests/screenshots/networks.png` | 网络（bridge/host/none） | 106,330 |
| 7 | `tests/screenshots/console.png` | 命令控制台 | 100,936 |
| 8 | `tests/screenshots/events.png` | 事件（空态） | 95,447 |
| 9 | `tests/screenshots/settings.png` | 设置 | 122,386 |
| 10 | `tests/screenshots/distros-drawer.png` | 发行版详情抽屉 | 132,438 |
| 11 | `tests/screenshots/distros-exec-result.png` | 抽屉 wsl.conf + exec 结果 | 130,997 |
| 12 | `tests/screenshots/dialog-run-container.png` | 运行容器对话框 | 113,788 |
| 13 | `tests/screenshots/dialog-pull-image.png` | 拉取镜像对话框（私有镜像/限流提示 +「去登录」） | 104,493 |
| 14 | `tests/screenshots/registry-credentials.png` | 注册表凭据对话框（server/username/password、常用 registry、内存降级提示） | 135,894 |
| 15 | `tests/screenshots/registry-validation.png` | 空提交校验（「参数不合法: 请输入用户名和密码。」） | 141,217 |
| 16 | `tests/screenshots/registry-auth-error.png` | 拉取认证失败 callout +「去登录」（含原始 docker 错误） | 194,145 |
| 17 | `tests/screenshots/dashboard-dark.png` | 暗色主题概览 | 176,134 |
| 18 | `tests/screenshots/mobile-dashboard.png` | 移动端 390×844 概览 | 257,134 |
| 19 | `tests/screenshots/mobile-containers.png` | 移动端 390×844 容器页 | 255,885 |
| 20 | `tests/screenshots/mobile-sidebar.png` | 移动端侧边栏展开 | 226,946 |

> **附录（前端交付参考截图，非 QA 生成）**：`tests/screenshots/ui-*.png` 共 26 张，其中 v0.1.1 注册表相关 9 张：`ui-registry-credentials.png`、`ui-registry-auth-error.png`、`ui-registry-pull-hint.png`、`ui-registry-pull-error.png`、`ui-registry-job-callout.png`、`ui-registry-goto-login.png`、`ui-registry-credentials-dark.png`、`ui-registry-credentials-mobile.png`、`ui-registry-rate-limit.png`。**注意**：`ui-registry-rate-limit.png` 是前端用 mock 模拟 `REGISTRY_RATE_LIMITED` 的 UI 走查图，不是真实限流实测。QA 的 20 张自动化截图见上表，两者互不替代。

---

## 7. 问题与契约歧义

### 7.1 环境级（非插件缺陷，已适配）

| 现象 | 复现 | 结论 |
| --- | --- | --- |
| workspace 内启动的后端所有 wsl/wslc 调用返回 `Wsl/E_ACCESSDENIED`（`system/info.distroCount=0`） | 在 workspace 内构建并运行 sidecar / dev host（data-dir 位于 workspace） | 本会话 workspace 的 Low integrity label 导致（Low IL 进程无权与 WSL 服务交互）。改用 workspace 外 data-dir + 外置 CLI 后全部恢复；真实 DBX 插件目录为 Medium IL，不复现。**不记为插件缺陷**；契约 §9.3 已记录 |

### 7.2 问题闭环情况

| 问题 | 最终状态 |
| --- | --- |
| 契约 §8.7 缓存命中 pull 输出语义 | **已闭环**：契约 §8.7 明确「`job/end.ok` 为权威」；本轮缓存命中仍输出 4 行并成功 |
| exec 冷启动耗时 vs 默认 30s | **已闭环**：契约 §9.1 + UI 9/9 配置 60s/90s；运行时最慢 2.6s |
| wslc.exe 不在 PATH | **已验证**：后端按契约 6.1 回退 `C:\Program Files\WSL\wslc.exe`（3.0.4.0） |
| 空提交校验标题显示「内部错误」 | **已修复并复验**：UI `0DD851C9…` 中本地校验带 `INVALID_PARAMS`，标题为「参数不合法: 请输入用户名和密码。」；截图 `registry-validation.png` |
| dev host 无 storage | **预期行为**：UI 显示“最近使用的服务器与用户名仅保存在内存中”，属能力探测降级（契约 §7.4 允许），非缺陷；真实 DBX 需复测持久化 |
| logs --follow 取消 error.code | **观察项**：取消后 `job/end.ok=false` 且带结构化 error，前端已兼容 |

### 7.3 遗留观察项（不阻塞）

1. **`REGISTRY_RATE_LIMITED` 真实限流**：未主动触发（避免对 Docker Hub 做高频匿名拉取）；UI 限流展示仅由前端 mock 截图覆盖（见第 6 节附录），真实分支建议后续用受控环境补测。
2. **真实凭据路径**：契约 §8 第 13 条（登录成功后拉取私有镜像）需有效 registry 凭据，未自动化；后端登录成功路径由 `backend/internal/wslc/registry_test.go` 单测覆盖。真实“已登录后再登出”也未做破坏性验证（避免删除用户凭据），本轮 logout 幂等使用不存在的测试 registry。

### 7.4 历史环境事件记录：Docker Hub 瞬断（v0.1.1 QA 期间，保留记录）

在 v0.1.1 QA 期间的一次运行中，`wslc/images/pull` 出现一次 Docker Hub 连接被断开：

```text
来自引擎的错误信息: Head "https://registry-1.docker.io/v2/library/definitely-not-a-real-image-dbx-xyz/manifests/latest": EOF
错误代码： E_FAIL
```

- 该 detail 不含 `docker login` / `unauthorized` / `denied` / `toomanyrequests` 等 registry 响应关键词，后端按契约「其他 → PROCESS_FAILED」兜底是正确行为；同一检查在网络正常轮次返回 `REGISTRY_AUTH_REQUIRED`。
- 工具已按「网络瞬断 vs 分类缺陷」区分：纯网络 `PROCESS_FAILED` → PASS+warning+自动重试 1 次；若 detail 含真实 registry 响应却仍分类为 `PROCESS_FAILED` → FAIL。
- **v0.1.0 发布回归本轮网络顺畅，未再出现该瞬断**（错误凭据登录 4.0s、pull 失败 6.7s，均为认证分类正常返回）。

---

## 8. 遗留风险与后续建议

### 8.1 未自动化 / 需真实凭据或破坏性环境

- **契约 §8 第 13 条（登录成功后拉取私有镜像）**：无可用真实 registry 凭据，未端到端验证；后端登录成功路径由单测覆盖。建议真实 DBX 中用测试账号/PAT 人工验证。
- **`REGISTRY_RATE_LIMITED` 真实触发**：需高频匿名拉取，未主动制造；UI 该分支仅 mock 截图覆盖。
- **其他未自动化方法**：`distros start/terminate/setDefault/export/import/unregister/setVersion/sparse/compact/defaultUser/resize`、`shutdown/update/install`、`system/config/write`、`containers restart/kill/prune`、`images remove/prune/tag/build/save/load`、`volumes prune`、`networks connect/disconnect/prune` 等写操作。
- **多 registry 登录**（ghcr.io / registry.cn-hangzhou.aliyuncs.com / 自建 host:port）：UI 提供建议与输入支持，本轮网络验证仅覆盖 docker.io 错误凭据路径。

### 8.2 UI 交互流覆盖边界

已覆盖：9 页渲染/截图、hero/KPI、发行版 6 操作按钮、详情抽屉（info/wslconf/exec）、运行容器/拉取镜像对话框、注册表凭据对话框（字段/类型/空校验/错误凭据/密码脱敏/拉取失败引导）、暗色主题、移动端概览/容器/侧边栏。未自动执行：对话框成功提交（运行容器、拉取成功、登录成功）、破坏性操作二次确认（登出确认、卸载发行版、删除对象）、实时轮询定时行为、`en` 语言完整组合、真实宿主 `host.storage` 持久化。

### 8.3 其他风险

- UI/backend 若在本报告快照后继续改动，需重跑 `node tests/run-all.mjs`；建议纳入 CI（Windows runner 需 WSL + wslc，且注册表检查会访问 docker.io，需允许网络）。
- **注册表检查依赖公网 docker.io**：纯网络失败按环境问题 PASS+warning 并重试（见 7.4）；CI 网络受限时建议提供可控 registry 测试端点或允许 skip 标记。
- **并发调试干扰**：QA 期间其他开发者 dev host 可能同时调用 WSL/wslc；最终回归前已确认 5192/5193 无监听后再运行。复跑建议先停止其他 dev host。
- dev host 与真实 DBX 宿主存在差异（storage、Secret Store、打包/签名、权限模型、全宽布局——移动端 iframe 在 dev host 中因连接侧栏仅 250px 宽）；`.dbxp` 已打包并通过 manifest-check 11/11，真实安装运行与 GitHub Release 资产校验见第 10 节。
- 测试使用真实 wslc 资源（`qa-` 前缀）；清理逻辑在成功/异常路径均执行，若进程被强杀，下次运行会先清理 `qa-` 残留。

---

## 9. 复现与证据

```powershell
# 最终 v0.1.0 发布回归（本次结果：117.2s，26/26 + 81/81 PASS，0 WARN，20 张截图）
cd D:\work\dbx-plugin-wsl\wsl-manager
node tests\run-all.mjs

# 身份冒烟（initialize: id=com.dbx.wsl, version=0.1.0）
node tools\sidecar-probe.mjs --only=initialize

# 注册表专项
node tools\sidecar-probe.mjs --only=registry
node tools\sidecar-probe.mjs --only=pull-auth

# Manifest / 安装包预检（11/11 PASS）
node tools\manifest-check.mjs manifest.json --package dist\com.dbx.wsl-0.1.0-windows-x64.dbxp

# 查看证据
Get-Content tests\tmp\run-all-report.json      # 汇总（步骤/耗时/截图/发行版还原）
Get-Content tests\tmp\sidecar-report.json     # 26 项 RPC 断言/证据（含 initialize 结果）
Get-Content tests\tmp\ui-report.json          # 81 项 UI 断言（含注册表专项/密码脱敏/RPC 汇总）
Get-ChildItem tests\screenshots               # 20 张 QA 截图（+ 前端交付参考截图）
```

- 最终产物 SHA256：UI `0DD851C9B0ADDEF3CEC39DEF27973C4CB2EDE18608C4AFDC487826CBDCC8C878`；Sidecar `3CE4A69F774A675AD6FB2E7AEBF7336692E0387F645DC11D78EC2239669F696A`；`.dbxp` `CABC5DD6D44F8B9555FE46683B7D2309B8F3FEAAFD85D5EF301548CF44E672F4`。
- 工具自测（不依赖真实后端/网络）：`node tools\sidecar-probe.mjs --sidecar=tests\fixtures\mock-sidecar.mjs`（26 项契约级 mock 检查，全部 PASS）。
- 环境：Windows 11 10.0.26300.9711 / WSL 3.0.4.0 / wslc 3.0.4.0 / Debian 13 / Node 24.22.0 / Go 1.27.3 / Chrome 155.0.8059.85。

---

## 10. 发布资产校验（GitHub Release v0.1.0）

> 状态：**待 Lead 完成 GitHub Release v0.1.0 并提供资产 URL 后执行**。届时将下载 Release 资产，独立校验：
>
> 1. 下载的 `.dbxp` 的 sha256/大小与 `artifact.json`、`release-candidates.json`、`dist/store/candidates/com.dbx.wsl.json` 完全一致；
> 2. 解包后的 `manifest.json`：`id=com.dbx.wsl`、`version=0.1.0`、`publisher=w89588`，permissions 与 candidate 一致；
> 3. `release-candidates.json` 结构（plugin 字段 + artifacts 的 target/url/sha256/size）；
> 4. store candidate 符合 `t8y2/dbx-store/schemas/plugin-candidate.schema.json`（必填/枚举/正则）。
>
> 结果将写入 `docs/release-verification.md` 并在此同步（含 Release/PR 链接与 PASS/FAIL）。
