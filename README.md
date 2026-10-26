# WSL 管理器（DBX 插件）

简体中文 | [English documentation](README.en.md)

一个 [DBX](https://dbxio.com/) 插件，为 Windows Subsystem for Linux 提供可视化管理界面：
WSL 发行版（`wsl`）与 WSL 容器（`wslc`）。

插件是 DBX 包（`.dbxp`），由沙箱工作台 UI 和原生 Go Sidecar 组成。Sidecar 以当前用户权限运行，调用本机的
WSL 工具与 Windows 辅助程序，详见下方权限披露。

## 功能

**WSL 发行版（`wsl`）**

- 发行版列表：实时状态、WSL 版本、VHD 磁盘占用。
- 启动、终止、设为默认、切换 WSL 版本。
- 默认用户、稀疏 VHD、压缩磁盘、调整容量。
- 空闲退出与保活：关闭终端后保持发行版运行、恢复默认或自定义超时，自动备份配置并提示重启生效。详见[保活设置](docs/idle-policy.md)。
- 导出、导入、卸载发行版（二次确认）。
- 查看 OS 版本、内核、磁盘、IP、`/etc/wsl.conf`。
- 打开终端 / 资源管理器，在发行版内执行命令。

**WSL 容器（`wslc`）**

- 容器列表与生命周期：运行、启动、停止、重启、终止、删除、清理。
- 日志（支持跟随）、`exec`、实时资源占用。
- 镜像：列表、拉取（实时进度）、构建、标记、保存/加载、删除、清理。
- 数据卷与网络：创建、检查、删除、连接/断开、清理。
- 实时 `wslc events` 事件流。

**注册表凭据**

- 在插件内登录/登出容器注册表（等价 `docker login`），支持 Docker Hub、GHCR、阿里云 ACR 及私有 `host:port`。
- 私有镜像或 Docker Hub 限流导致拉取/构建失败时，会给出分类原因与一键“去登录”入口，并预填 registry。
- 密码通过 stdin 传给 `wslc`，不持久化、不写日志、不出现在 RPC 错误中。

**其他**

- 命令控制台：以 argv 方式执行任意 `wsl` / `wslc` 命令（不经过 shell），显示 stdout/stderr 与退出码。
- 设置：可视化编辑 `.wslconfig` 与 wslc `settings.yaml`，自动备份。
- 资源实时更新：通过宿主事件流接收变化，原位更新列表、状态和统计，保留滚动位置、搜索焦点与弹窗草稿。支持断线重连，页面隐藏时暂停采集。
- 概览仪表盘、明暗主题、中英文界面、带实时日志与取消功能的任务面板。任务终态可补偿查询；当前 Sidecar 内保留最近 32 个已结束任务，最长 24 小时。
- 配置保存携带修订号，检测外部修改，冲突时保留草稿并拒绝覆盖。
- 侧栏与“设置 → 关于”提供 GitHub 仓库和 Issues 入口；BUG 与建议统一在 GitHub Issues 中提交。

## AI 与 MCP 接入

提供 `wsl_connections`、`wsl_describe`、`wsl_read`、`wsl_write` 四个工具，覆盖插件 85 个管理接口（资源订阅为 UI 专用）。AI 可以查询本机 WSL 连接（发行版）及 WSLC 容器，执行生命周期管理、命令与文件读写，管理镜像、卷、网络、配置、注册表和后台任务。

通过 DBX 的外部 MCP 服务接入，工具名为 `dbx_wsl__wsl_connections` 等；不再设置 DBX 产品版本硬门槛；当前声明的宿主解析边界为 **0.6.27**，详见[兼容性验证](docs/compatibility.md)。只读与写工具分别声明，写操作遵守宿主/客户端授权。当前 DBX 内置 AI 仅发现已打开的连接型插件，本插件为工作台型插件，因此本版本通过**外部 DBX MCP**使用这些能力。详见[接入与调用示例](docs/mcp.md)。

## 备份与恢复（0.2.0）

在「备份与恢复」选择发行版和 Windows 备份目录，可使用目录选择器。运行中的发行版需先停止，或勾选停止后备份。备份文件与 SHA-256 记录持久保存；恢复前重新校验，必须使用新名称和不存在的新目录，导入后验证 `/bin/sh` 能否启动。失败时保留文件和新发行版供检查，不删除原发行版。空间预检至少要求 512 MiB 可用空间，但不能保证足以存放整个发行版；备份前应按实际数据量预留空间。

## 诊断与资源配置（0.3.0）

「诊断中心」检查 WSL/WSLC 可执行性、用户盘空间、指定回环端口和可选注册表 HTTPS 连通性，提供依据、时间与建议，支持复制脱敏 JSON 报告。不上传日志，不自动修复。

「资源配置助手」设置内存上限、CPU 数量和交换空间，留空恢复默认；预览前后内容再保存。保留其他配置、注释和换行，使用修订号检测冲突；保存不会关闭 WSL，需另行重启后生效。

## 开发环境模板（0.4.0）

「开发环境模板」提供 PostgreSQL 17 Alpine、Redis 7 Alpine：填写唯一环境名和本机端口，预览容器、镜像、数据卷后输入密码创建。仅绑定 `127.0.0.1`，使用命名卷持久化数据，并等待容器健康检查。就绪后显示不含密码的连接信息；不会自动建立 DBX 数据库连接。

同名同配置且健康的环境可复用，不会更新密码；同名异配置、已有非本插件资源或未完成创建会被拒绝。失败保留资源供检查，容器启停和卷管理使用现有页面；重建使用新名称。镜像采用固定主版本标签，标签内容可变化，已运行容器不会自动升级。

环境记录保存在当前用户 `%LOCALAPPDATA%/dbx-wsl-manager/environments`，不含密码。密码通过临时环境文件传递并在正常任务退出时清理，进程被强制结束可能留下 `.env-*`；容器 inspect 与宿主工具输入仍可能包含凭据。这是本机开发模板，不是生产部署或完整 Compose 支持。

`wsl_describe` 增加关键流程的必填项、范围、单位、操作影响、完成条件和重试建议。任务返回 `jobId` 不代表成功，必须查询最终状态；执行命令与删除类操作禁止自动重放。

## 截图

| 概览 | 发行版 |
| --- | --- |
| ![概览](docs/screenshots/overview.webp) | ![发行版](docs/screenshots/distributions.webp) |

| 容器 | 镜像 |
| --- | --- |
| ![容器](docs/screenshots/containers.webp) | ![镜像](docs/screenshots/images.webp) |

| 注册表凭据 | 拉取认证失败引导 |
| --- | --- |
| ![注册表凭据](docs/screenshots/registry-credentials.webp) | ![认证失败引导](docs/screenshots/registry-auth-error.webp) |

| 命令控制台 | 设置 |
| --- | --- |
| ![命令控制台](docs/screenshots/command-console.webp) | ![设置](docs/screenshots/settings.webp) |

暗色主题：[overview-dark.png](docs/screenshots/overview-dark.webp)。

## 环境要求

| 项 | 要求 |
| --- | --- |
| 操作系统 | Windows 11 + WSL 2（已验证 WSL 3.0.3 / 3.0.4） |
| DBX | 无 `engines.dbx` 限制；当前完整声明从 0.6.27 可解析，参见[验证边界](docs/compatibility.md) |
| WSL 容器 | `wslc` 可选；本插件已验证 WSL 3.0.3 / 3.0.4，缺失时发行版功能仍可用 |
| 构建工具 | Go 1.22+、Node.js 22+、`@dbx-app/plugin-cli` |

## 安装

### 官方商店

DBX → 插件中心 → 搜索 **WSL Manager** → 安装。

### GitHub Release（未签名候选）

1. 从 [Releases](https://github.com/w89588/dbx-plugin-wsl/releases) 下载 `com.dbx.wsl-<version>-windows-x64.dbxp`。
2. DBX → 插件中心 → 设置，开启 **允许安装未签名开发包**。
3. 安装本地 `.dbxp`。
4. 从插件快捷入口、命令面板或插件中心打开 **WSL Manager**。

### 从源码构建

```powershell
npm install --no-audit --no-fund @dbx-app/plugin-cli
node node_modules\@dbx-app\plugin-cli\bin\dbx-plugin.js package .
```

产物在 `dist/`。安装与排障见 [docs/installation.md](docs/installation.md)。

## 使用

- **概览**：WSL/wslc 版本、默认发行版、运行数量、快捷操作、最近容器事件。
- **发行版**：管理已安装发行版；详情抽屉包含 OS/内核/磁盘/IP/`wsl.conf`。
- **容器**：运行容器、跟随日志、执行命令、实时统计。
- **镜像**：拉取/构建/删除镜像；私有镜像或限流时打开 **注册表凭据** 登录。
- **数据卷 / 网络**：管理容器存储与网络。
- **命令控制台**：逐参数执行 `wsl` / `wslc` 命令。
- **事件**：容器运行时实时事件。
- **设置**：`.wslconfig` / `settings.yaml` 编辑器、资源采集间隔、危险操作开关。

## 架构

```
DBX 宿主（沙箱 iframe）
  │  window.dbxPlugin.invoke / onEvent        Host API 1
  ▼
ui/index.html（单文件 UI，无外部资源）
  │  Sidecar Protocol v1（stdio，JSON-RPC 2.0）
  ▼
Go Sidecar（backend/）
  ├── wsl.exe   发行版
  └── wslc.exe  WSL 容器
```

- 资源通过 `resources/subscribe` 与 `resources/update` 使用现有 stdio/宿主事件桥持续推送，不是 HTTP SSE，不新增监听端口或网络权限。后台定期采集，内容变化才推送；系统版本信息最多每分钟采集一次。
- 长任务使用 job + `job/output` / `job/end` 事件，可取消。
- 命令以 argv 数组传递，不拼接 shell 字符串。
- UI 为单文件内联 CSS/JS，偏好通过 `dbxPlugin.storage` 持久化。

## 开发

```powershell
# 浏览器开发宿主（会构建 Go Sidecar，无需启动 DBX）
node ..\node_modules\@dbx-app\plugin-cli\bin\dbx-plugin.js dev --path . --port 5190

# 后端测试
cd backend
go vet ./...; go test ./...

# Manifest 安装期规则预检
cd ..
node tools/manifest-check.mjs

# 浏览器交互回归（模拟宿主，不修改真实 WSL 设置）
node tests/ui-smoke.mjs
```

UI 与 Sidecar 的 RPC 契约见 [docs/rpc-contract.md](docs/rpc-contract.md)；发布与商店提交流程见
[docs/releasing.md](docs/releasing.md)。

## 权限与安全

Manifest 只申请：

- `host.workbench`：打开插件工作台、设置标题。
- `host.events`：接收 Sidecar 任务/事件通知。
- `host.binary`：通过原生 Sidecar 执行本地 WSL 工具和 Windows 辅助程序。
- `host.storage`：保存少量 UI 偏好（当前页签、刷新间隔、最近 registry server/username）。

不申请网络、文件系统、剪贴板、数据库、连接或 AI 权限。沙箱 UI 无直接网络访问。原生 Sidecar 以当前用户权限运行，
调用 `wsl.exe` / `wslc.exe`、`wt.exe`（终端回退为 `cmd.exe`）、`explorer.exe`，并通过 `taskkill` 取消进程树；注册表密码通过 `wslc login --password-stdin` 传入，不进入 argv、日志、事件或错误信息。

空闲策略写入当前用户的 `.wslconfig`，已有文件先备份。项目与反馈入口通过 Windows `ShellExecuteW` 调用默认浏览器，打开固定的项目或微软文档 HTTPS 地址，不自动上传日志或提交 Issue。

## 已知限制

- 仅支持 Windows（`windows-x64`）。
- WSL 1 需要启用 Windows 可选组件；否则版本切换只提供 WSL 2。
- `wslc` 功能取决于本机 WSL 版本。
- 导出/导入/构建路径需填写 Windows 路径（暂未接入原生文件选择器）。
- `wslc` 3.0.3/3.0.4 无法列出已登录注册表，是否登录以拉取结果为准。

## 贡献

源码：[w89588/dbx-plugin-wsl](https://github.com/w89588/dbx-plugin-wsl)。
功能 BUG 与建议请到[本仓库 Issues](https://github.com/w89588/dbx-plugin-wsl/issues)，先搜索已有记录，再选择 BUG 或建议表单。
请附版本、复现步骤、预期与实际结果和脱敏日志，不要提交密码或令牌。插件中的反馈按钮只打开网页，不自动提交信息。

欢迎反馈问题与建议。构建、测试与贡献要求见 [CONTRIBUTING.md](CONTRIBUTING.md)。

## 许可证

[Apache-2.0](LICENSE)。`backend/sdk/dbx-plugin-sdk` 为 DBX 官方 Apache-2.0 SDK。
