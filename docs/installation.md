# 安装与排障

## 1. 前置检查

0.1.6 移除 `engines.dbx >=0.6.55`，仅保留 `engines.host_api: "1"`。当前完整清单的官方解析规则边界为 DBX 0.6.27；0.6.26 不识别 MCP 声明，这不是去掉版本字段能解决的问题。参见[兼容性验证](compatibility.md)。

在 PowerShell 中确认：

```powershell
wsl --version        # 需要 WSL 2.x/3.x；本插件目标 WSL 3.0+
wsl -l -v            # 至少一个发行版
wslc version         # 可选：WSL 3.0+ 自带；缺失时容器相关页面显示不可用
```

若 `wsl --version` 报错，先 `wsl --update` 或安装 WSL：

```powershell
wsl --install --no-distribution
wsl --update
```

`wslc` 安装位置通常为 `C:\Program Files\WSL\wslc.exe`；插件会依次在 `PATH`、`%ProgramFiles%\WSL`、`%LOCALAPPDATA%\Microsoft\WindowsApps` 中查找。

## 2. 构建候选包

```powershell
cd wsl-manager
# 若 go build 报 Temp 目录权限错误，可先设置自定义临时目录：
$env:GOTMPDIR = "$env:TEMP\dbx-wsl-build"
$env:GOCACHE  = "$env:TEMP\dbx-wsl-gocache"

npx --yes @dbx-app/plugin-cli@0.1.9 package .
```

产物：

```
dist/com.dbx.wsl-0.1.6-windows-x64.dbxp
dist/com.dbx.wsl-0.1.6-windows-x64.artifact.json
```

包内结构（可用 ZIP 工具查看，`.dbxp` 本质为 ZIP）：

```
manifest.json
assets/plugin.svg
ui/index.html
bin/windows-x64/dbx-plugin-wsl-manager.exe
checksums.json
```

## 3. 在 DBX 中安装未签名开发包

1. 打开 DBX → **插件中心**。
2. 进入 **设置**（插件中心右上角/全局配置），打开 **允许安装未签名开发包**。
   - 该选项仅用于本地开发验证，不会放宽官方商店安装校验；验证完成后建议关闭。
3. 在插件中心选择 **安装本地插件**（或“从文件安装”），选择 `dist\com.dbx.wsl-0.1.6-windows-x64.dbxp`。
4. 安装成功后打开 **WSL 管理器**：
   - 顶部/侧边栏插件快捷入口（appToolbar）；
   - 命令面板（Ctrl/Cmd+K）搜索 “WSL Manager”；
   - 插件中心 → 已安装 → WSL Manager → 打开工作台。

## 4. 首次使用

- **概览页**首次加载会调用 `wsl --version`、`wsl --status`、`wslc info`，冷启动可能需要几秒。
- **发行版页**：默认不启动发行版；点击“详情/信息”或在发行版内执行命令时会启动它。
- **容器页**：首次拉取镜像需要网络（`wslc pull`），进度在底部任务面板实时显示。
- **设置页**：修改 `.wslconfig` 后需执行 `wsl --shutdown` 使配置生效（界面提供按钮）。

### 镜像仓库登录（`docker login` / `wslc login`）

`wslc` 拉取镜像时报 `may require 'docker login'`、`unauthorized: authentication required`、`denied: requested access`，或 Docker Hub 报 `toomanyrequests` / `pull rate limit` 时，说明需要注册表凭据。

**插件内操作**：镜像页 → 工具栏「注册表凭据」→ 填写 Server / Username / Password → 登录；Docker Hub 可把 Server 留空或填 `docker.io`。拉取失败时任务面板会直接出现「去登录」按钮并预填镜像所属 registry。

| 注册表 | Server | Username | Password |
| --- | --- | --- | --- |
| Docker Hub | `docker.io`（或留空） | Docker Hub 用户名 | 账号密码，或 2FA 开启后的 Access Token |
| GitHub Container Registry | `ghcr.io` | GitHub 用户名 | Personal Access Token（需 `read:packages`） |
| 阿里云 ACR | `registry.cn-hangzhou.aliyuncs.com`（按地域） | 阿里云账号全名 | ACR 控制台设置的固定密码 |
| 自建/企业 | `registry.example.com:5000` | 仓库用户名 | 仓库密码 / Token |

安全说明：

- 密码只经 Sidecar **stdin** 传给 `wslc login --password-stdin`，不会进入命令行参数、插件日志、事件或 RPC 错误；UI 提交后立即清空，不写持久化存储。
- `wslc` 把凭据保存在本机 WSL 容器会话中；插件不另存密码，也无法枚举“当前登录了哪些 registry”（wslc 3.0.3 / 3.0.4 无此命令），界面会提示“是否已登录以拉取结果为准”。

等价的 CLI 写法（用于排查）：

```powershell
$env:WSL_UTF8 = 1
"<PAT 或密码>" | wslc login ghcr.io -u <用户名> --password-stdin
wslc logout ghcr.io
```

说明：

- Docker Hub 匿名拉取有限流；登录（免费账号也可）可提高额度，或稍后重试。
- 公开镜像通常不需要登录；`pull access denied` 也可能是镜像名拼写错误或仓库本就不存在。
- 凭据错误时任务面板会提示 `REGISTRY_AUTH_FAILED`，请优先检查用户名 / PAT 权限（如 GHCR 需要 `read:packages`）。

## 5. 常见问题

| 现象 | 处理 |
| --- | --- |
| 页面显示 “wsl 不可用” | 检查 `wsl --version`；确认 WSL 已安装并更新。若使用 Store 版 WSL，确认 `C:\Program Files\WSL\wsl.exe` 存在 |
| 页面显示 “wslc 不可用” | 运行 `wslc version`；WSL 3.0+ 才提供 wslc。仍可管理发行版 |
| `wsl -l -v` 返回 `Wsl/E_ACCESSDENIED` | 通常是 WSL 服务未就绪：运行 `wsl --status` 或重启 `WSLService`（`Get-Service WSLService`） |
| 拉取镜像失败 | 检查网络/代理；`wslc pull` 与 Docker Hub 通信，国内网络可能需配置镜像或代理 |
| 拉取报 `may require 'docker login'` / `unauthorized` / `denied` | 需要注册表凭据：镜像页 →「注册表凭据」登录（见上一节）；Docker Hub 可用 `docker.io` + 账号密码/Access Token |
| 拉取报 `toomanyrequests` / `pull rate limit` | Docker Hub 匿名限流：登录免费账号可提高限额，或稍后再试 |
| 登录报 `REGISTRY_AUTH_FAILED` | 检查用户名与密码/PAT 权限（GHCR 需 `read:packages`）、Token 是否过期；私有 registry 确认 `host:port` |
| 导出/导入/构建失败 | 确认路径存在且磁盘可写；导出目标已存在时插件会拒绝覆盖，请换文件名 |
| `.wslconfig` 写入失败 | 检查文件是否被占用；插件会生成 `.bak-<时间戳>` 备份 |
| 安装时提示签名无效 | 确认已开启“允许安装未签名开发包”；仅官方商店包才带签名 |
| 导入提示 `Plugin '…' is incompatible: Menus contribution '…' uses group '…' outside the host word list` | 这是 DBX Host 在 JSON Schema 之外的安装期校验：`menus.items[].group` 只允许 `navigation` / `primary` / `secondary` / `destructive`（本插件已修正为 commandPalette=`primary`、appToolbar=`navigation`）。改完 manifest 后重新 `dbx-plugin package`；打包前可运行 `node tools/manifest-check.mjs --package dist\<包>.dbxp` 预检 |
| 导入提示 `commandPalette` 不能设置 `default_visible` | 命令面板没有可见性开关，把 `default_visible` 只放在 `appToolbar`/`appSidebar` 的菜单项上 |
| 安装后看不到入口 | 在插件中心检查具体的 Host API、清单解析、权限或加载错误；重启 DBX；在插件中心查看加载错误 |

## 6. 卸载

DBX → 插件中心 → 已安装 → WSL Manager → 卸载。插件数据目录（`plugins/…/plugin-data/com.dbx.wsl`）与 `plugin-data` 会按 DBX 规则保留，可在插件中心清理。

## 7. 开发者：在真实 DBX 中调试

- 浏览器开发宿主只能模拟部分 Host API；主题 tokens、CSP、桌面生命周期需在真实 DBX 验证。
- 修改 `backend/` 后需重新 `package` 并重装（或用 dev host 调试）。
- 安装未签名包后若更新版本，需递增 `manifest.json` 的 `version` 并重新打包安装。
