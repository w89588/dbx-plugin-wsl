# WSL Manager —— 插件后端 RPC 契约（v1）

本文件是 **前端 UI** 与 **Go Sidecar** 之间唯一的接口约定。任何一方改动接口，必须先改本文件并在 PR 描述中说明。

- 插件 ID：`com.dbx.wsl`
- 版本：`0.1.3`
- 传输：Sidecar Protocol v1 / `stdio-jsonl`（每行一个 JSON-RPC 2.0 消息）
- Sidecar 元数据必须与 `manifest.json` 完全一致：`ID = "com.dbx.wsl"`，`Version = "0.1.3"`
- 日志只能写 stderr；stdout 只能输出协议消息

---

## 1. 调用方式

前端统一通过：

```js
await window.dbxPlugin.ready;

// 同步调用：有业务返回值，默认超时 30s，长时间操作不要走这里
const info = await window.dbxPlugin.invoke("system/info", {}, { timeoutMs: 30000 });

// 启动长任务：立即返回 { jobId }，进度通过事件推送
const { jobId } = await window.dbxPlugin.invoke("wslc/images/pull", { ref: "alpine:3.20" });

// 取消任务
await window.dbxPlugin.invoke("jobs/cancel", { jobId });
```

事件（需要 manifest 权限 `host.events`）：

```js
const off = window.dbxPlugin.onEvent((event) => {
  // event.method === "job/output" | "job/end" | "wslc/event" | ...
  // event.params 见第 5 节
});
```

> 所有后端事件通过 `emitter.Event(method, params)` 发出，前端 `onEvent` 收到 `{ method, params }`（不同宿主版本可能把字段放在 `event.method/event.params`，实现时两种都兼容）。

## 2. 错误模型

失败时返回 JSON-RPC error：

```json
{
  "jsonrpc": "2.0",
  "id": 12,
  "error": {
    "code": -32000,
    "message": "wslc 未安装或不可用",
    "data": { "code": "TOOL_NOT_FOUND", "hint": "请安装 WSL 3.0+ 或检查 wslc.exe 路径", "detail": "..." }
  }
}
```

错误码约定：

| `data.code` | 含义 |
| --- | --- |
| `TOOL_NOT_FOUND` | `wsl.exe` / `wslc.exe` 解析失败 |
| `INVALID_PARAMS` | 参数缺失或非法（后端已做校验） |
| `NOT_FOUND` | 目标容器/镜像/卷/网络/发行版不存在 |
| `CONFLICT` | 状态冲突（如删除运行中容器），`detail` 带原始 stderr |
| `PROCESS_FAILED` | 子进程非 0 退出且业务上属于失败（同步方法） |
| `TIMEOUT` | 同步方法超时 |
| `UNSUPPORTED` | 当前 WSL/wslc 版本不支持该能力 |
| `REGISTRY_AUTH_REQUIRED` | 拉取/构建需要注册表登录（私有镜像，或 Docker Hub 匿名限流）；`hint` 引导去“镜像 → 注册表凭据”登录 |
| `REGISTRY_RATE_LIMITED` | Docker Hub 匿名拉取触发 rate limit（`toomanyrequests`）；登录可提高限额 |
| `REGISTRY_AUTH_FAILED` | 注册表登录失败（凭据错误 / 无权限 / token 过期） |
| `INTERNAL` | 其他 |

前端展示：`error.message` 作标题，`data.hint` 作副标题，`data.detail` 折叠显示。**同步方法返回业务结果内的 `exitCode` 非 0 时不算 RPC error**（例如在发行版内执行命令失败）。

## 3. 通用数据结构

### 3.1 RunResult（执行命令返回）

```json
{
  "exitCode": 0,
  "stdout": "hello\n",
  "stderr": "",
  "durationMs": 34,
  "truncated": false,
  "command": "wsl -d Debian -e sh -lc 'uname -r'"
}
```

- `stdout` / `stderr` 为 UTF-8 字符串（后端负责 UTF-16LE 自动解码）。
- 单流输出上限 512 KiB；超出时截断并置 `truncated: true`。
- `command` 为用于展示的可读命令行（不是真实 shell 命令，仅日志用途）。

### 3.2 Job（长任务）

启动类方法返回：

```json
{ "jobId": "job-3" }
```

任务期间事件：

```json
{ "method": "job/output", "params": { "jobId": "job-3", "stream": "stdout", "text": "Pulling from library/alpine\n", "ts": 1792507542137 } }
{ "method": "job/output", "params": { "jobId": "job-3", "stream": "stderr", "text": "...", "ts": 1792507542200 } }
{ "method": "job/end", "params": { "jobId": "job-3", "ok": true, "exitCode": 0, "error": null, "result": { "image": "alpine:3.20" }, "durationMs": 5321 } }
```

- `job/output.text` 以行为单位（保留 `\n`），保证顺序；同一 job 的 stdout/stderr 交错顺序不保证，但各自内部有序。
- `job/end` 必定发送一次（成功、失败、取消都会）。`ok = exitCode === 0 && !error`。
- job 输出在内存中保留最近 2000 行/512 KiB，供 `jobs/get` 补偿。

`jobs/get` 请求 `{ jobId }`：

```json
{
  "jobId": "job-3",
  "method": "wslc/images/pull",
  "status": "running | completed | failed | cancelled",
  "startedAt": 1792507542000,
  "durationMs": 5321,
  "exitCode": 0,
  "error": null,
  "lines": [ { "stream": "stdout", "text": "...", "ts": 1792507542137 } ]
}
```

`jobs/list` 返回当前活动 job：`{ "jobs": [ { "jobId": "job-3", "method": "...", "startedAt": 1792507542000 } ] }`。

## 4. 方法

### 4.1 system

#### `system/wsl/idle/read`（v0.1.2）

无参数；只读取 `%USERPROFILE%/.wslconfig`，不启动 WSL。返回：

```json
{ "path": "C:\\Users\\user\\.wslconfig", "revision": "<sha256>", "mode": "default", "valid": true, "instanceIdleTimeoutMs": 15000, "vmIdleTimeoutMs": 60000 }
```

`mode` 为 `default | keepAlive | custom`。未配置的值使用 WSL 文档默认值；`valid=false` 表示格式异常、重复超时键或无效值，UI 必须提示检查原始配置。这里只报告磁盘上的配置，不能声称运行中的 WSL 已应用。`revision` 包含文件原始字节及存在状态。

#### `system/wsl/idle/write`（v0.1.2）

请求 `{ "mode": "keepAlive | default | custom", "expectedRevision": "<read 返回的 revision>", "instanceSeconds": 15, "vmSeconds": 60 }`。

- `keepAlive`：只设置 `[general] instanceIdleTimeout=-1`，保留虚拟机超时。
- `default`：移除 `[general] instanceIdleTimeout` 和 `[wsl2] vmIdleTimeout` 的覆盖项。
- `custom`：两项秒数必填，发行版允许 `-1` 或 `1..2147483`，虚拟机允许 `1..2147483`；写入时转换成毫秒（`-1` 保持不变）。
- 原文件需保留无关配置、注释与换行风格；复用配置备份和临时文件替换。策略读改写与普通配置写入串行执行。
- 缺少/过期的 revision 返回 `CONFLICT`，非法参数或格式异常返回 `INVALID_PARAMS`，不会写入。无变更时不创建备份。
- 返回 `{ "path": "...", "backupPath": null, "changed": true, "restartRequired": true, "policy": { ...read 同结构... } }`。已有文件的 `backupPath` 为备份路径；`changed=false` 时 `restartRequired=false`，但不保证运行实例已经加载该配置。
- 保存不得自动停止/重启 WSL。UI 明确提示影响范围，关闭 WSL 仍走既有确认流程；编辑器有未保存的 `.wslconfig` 修改时，UI 阻止策略写入。

#### `system/project/open`（v0.1.2）

请求 `{ "target": "repository | issues | bug | feature | wslDocs" }`。仅允许这些固定枚举：仓库 `https://github.com/w89588/dbx-plugin-wsl`、其 `/issues`、两个 Issue 表单（`bug_report.yml` / `feature_request.yml`）及微软 WSL 配置文档。

Windows 后端通过 `ShellExecuteW(open, 固定 HTTPS URL)` 调用系统默认浏览器，不经过命令行 shell。调用前在同一 OS 线程初始化 COM STA；系统返回失败时提供 HTTPS 默认应用检查提示。返回 `{ "opened": true, "url": "..." }` 表示系统已接受打开请求；不表示网页加载已完成。未知目标返回 `INVALID_PARAMS`；不接受任意 URL，不自动提交 Issue 或上传日志。UI 仅展示仓库、Issues 入口及 WSL 配置文档链接，`bug` / `feature` 枚举为旧客户端保留。

#### `system/info`

无参数。用于概览页与全局能力探测。

```json
{
  "platform": { "os": "windows", "arch": "amd64", "hostname": "PC-HOME", "username": "iaz00", "windowsVersion": "10.0.26300.9711" },
  "wsl": {
    "available": true,
    "exe": "C:\\Windows\\System32\\wsl.exe",
    "version": { "wsl": "3.0.3.0", "kernel": "6.18.54.1-1", "wslg": "1.0.79", "msrdc": "1.2.7391", "direct3d": "1.611.1-81528511", "dxcore": "10.0.26100.1-240331-1435.ge-release", "windows": "10.0.26300.9711" },
    "status": { "defaultDistro": "Debian", "defaultVersion": 2, "wsl1Supported": false, "raw": "..." },
    "distroCount": 1,
    "runningCount": 0
  },
  "wslc": {
    "available": true,
    "exe": "C:\\Program Files\\WSL\\wslc.exe",
    "version": "3.0.3.0",
    "client": { "version": "3.0.3.0", "kernelVersion": "6.18.54.1-1", "settingsFile": "C:\\Users\\iaz00\\AppData\\Local\\wslc\\settings.yaml" },
    "server": { "sessionManagerVersion": "3.0.3", "sessions": [ { "id": 1, "name": "wslc-cli-iaz00", "creatorPid": 24656 } ] }
  },
  "warnings": []
}
```

- `wsl.version` 字段来自 `wsl --version`，解析失败置空字符串。
- 任一工具不可用时 `available:false`，`warnings` 追加说明；**不要因此抛 RPC error**。
- `system/info` 允许 60s 超时（首调用可能唤醒服务）。

#### `system/wsl/status`

```json
{ "defaultDistro": "Debian", "defaultVersion": 2, "wsl1Supported": false, "raw": "默认分发: Debian\n默认版本: 2\n..." }
```

#### `system/config/read`

```json
{
  "wslconfig": { "path": "C:\\Users\\iaz00\\.wslconfig", "exists": true, "content": "[wsl2]\nmemory=8GB\n" },
  "wslcSettings": { "path": "C:\\Users\\iaz00\\AppData\\Local\\wslc\\settings.yaml", "exists": false, "content": "" }
}
```

#### `system/config/write`

请求 `{ "target": "wslconfig" | "wslc", "content": "..." }`

```json
{ "path": "C:\\Users\\iaz00\\.wslconfig", "backupPath": "C:\\Users\\iaz00\\.wslconfig.bak-20261020-224500", "bytes": 42 }
```

规则：内容上限 256 KiB；写入前创建同级 `.bak-<yyyyMMdd-HHmmss>` 备份（首次写新文件时 backupPath 为 null）；原子替换（临时文件 + rename）。

#### `system/wsl/online`

列出可安装发行版（`wsl --list --online`，网络，10s 超时，失败返回空数组 + `error` 字段）。

```json
{ "distros": [ { "name": "Ubuntu", "friendlyName": "Ubuntu" } ], "error": null }
```

### 4.2 wsl（发行版）

#### `wsl/distros/list`

```json
{
  "distros": [
    {
      "name": "Debian",
      "guid": "{bcf36fb8-1959-4aff-9873-739ed04dd3e4}",
      "version": 2,
      "state": "Stopped",
      "running": false,
      "isDefault": true,
      "basePath": "C:\\Users\\iaz00\\AppData\\Local\\Packages\\...\\LocalState",
      "vhdPath": "C:\\Users\\iaz00\\...\\ext4.vhdx",
      "vhdBytes": 123456789,
      "sparse": null,
      "defaultUid": 1000
    }
  ],
  "defaultDistro": "Debian",
  "defaultVersion": 2,
  "count": 1
}
```

- 名称/状态以 `wsl --list --verbose` 为准，路径/版本/默认用户从注册表 `HKCU\SOFTWARE\Microsoft\Windows\CurrentVersion\Lxss` 合并。
- `state` 取值：`Running` / `Stopped` / `Installing` / `Uninstalling` / `Unknown`。
- `vhdBytes` 通过 `os.Stat` 读取 `ext4.vhdx`；读不到置 0。
- `sparse` 读取注册表 `Flags` 无法可靠判断，保留 `null`（后续版本再实现）。
- **不得为了列表而启动发行版。**

#### `wsl/distros/start` / `wsl/distros/terminate`

请求 `{ "name": "Debian" }`：

```json
{ "name": "Debian", "state": "Running", "changed": true }
```

- start 实现：`wsl.exe -d <name> -e true`（超时 120s），成功后 `state:"Running"`。
- terminate 实现：`wsl.exe --terminate <name>`。

#### `wsl/distros/setDefault`

请求 `{ "name": "Debian" }` → `{ "defaultDistro": "Debian" }`（`wsl -s <name>`）。

#### `wsl/distros/exec`

请求：

```json
{ "name": "Debian", "command": "uname -r && id -u", "cwd": "~", "user": "root" }
```

返回 `RunResult` 附加字段：

```json
{ "name": "Debian", "exitCode": 0, "stdout": "...", "stderr": "", "durationMs": 120, "truncated": false, "command": "wsl -d Debian -e sh -lc '...'", "started": true }
```

- 命令通过 `sh -lc <command>` 执行（登录 shell，加载 profile）。
- `cwd` 存在时追加 `--cd <cwd>`；`user` 存在时追加 `-u <user>`。
- 参数一律作为独立 argv 传递，禁止拼接 shell 字符串到 Windows 命令行。
- 超时默认 60s，上限 300s。

#### `wsl/distros/info`

请求 `{ "name": "Debian" }`。会按需启动发行版（UI 必须提示）。

```json
{
  "name": "Debian",
  "running": true,
  "osRelease": { "PRETTY_NAME": "Debian GNU/Linux 13 (trixie)", "NAME": "Debian GNU/Linux", "VERSION_ID": "13", "ID": "debian" },
  "kernel": "6.18.54.1-microsoft-standard-WSL2",
  "arch": "x86_64",
  "uid": 1000,
  "userName": "iaz00",
  "defaultShell": "/bin/bash",
  "disk": { "totalBytes": 1081101176832, "usedBytes": 8233324544, "availBytes": 1017875496960, "usePercent": 1 },
  "ipv4": ["172.20.10.5"],
  "uptime": "up 2 minutes",
  "wslConf": "[boot]\nsystemd=true\n"
}
```

- 实现：一次 `wsl -d <name> -e sh -lc <script>`，脚本输出分段标记文本（如 `@@SECTION@@`），后端解析；任何段缺失时给空值，不抛错。
- `wslConf` 读 `/etc/wsl.conf`，不存在时为空字符串。

#### `wsl/distros/wslconf`

请求 `{ "name": "Debian" }` → `{ "path": "/etc/wsl.conf", "exists": true, "content": "..." }`。

#### `wsl/distros/export`（长任务）

请求：

```json
{ "name": "Debian", "path": "D:\\wsl-exports\\Debian-20261020.tar", "format": "tar" }
```

返回 `{ "jobId": "job-4" }`。`format` ∈ `tar` / `tar.gz` / `tar.xz` / `vhd`。job/end 的 `result`：`{ "path": "...", "bytes": 123 }`。
后端必须：展开 `%USERPROFILE%` / `~` 等环境变量；父目录不存在时自动创建；目标文件已存在时返回 `CONFLICT`。

#### `wsl/distros/import`（长任务）

```json
{ "name": "Ubuntu-24.04", "location": "D:\\WSL\\Ubuntu-24.04", "path": "D:\\wsl-exports\\ubuntu.tar", "version": 2, "vhd": false }
```

返回 `{ jobId }`。job/end `result`: `{ "name": "...", "location": "..." }`。

#### `wsl/distros/unregister`

请求 `{ "name": "Ubuntu-24.04" }` → `{ "name": "...", "removed": true }`。破坏性操作，UI 必须二次确认（要求用户输入发行版名称）。

#### `wsl/distros/setVersion`（长任务）

请求 `{ "name": "Debian", "version": 2 }` → `{ jobId }`。

#### `wsl/distros/manage`（长任务或同步）

统一入口，请求：

```json
{ "name": "Debian", "operation": "compakt | sparse | defaultUser | resize", "value": "..." }
```

为清晰起见拆成独立同步方法实现：

- `wsl/distros/sparse`：`{ "name": "Debian", "sparse": true }` → `{ "name": "...", "sparse": true }`
- `wsl/distros/compact`：`{ "name": "Debian" }` → `{ "name": "...", "compacted": true }`（要求已停止；运行中返回 `CONFLICT`）
- `wsl/distros/defaultUser`：`{ "name": "Debian", "user": "root" }` → `{ "name": "...", "user": "root" }`
- `wsl/distros/resize`：`{ "name": "Debian", "size": "64GB" }` → 长任务 `{ jobId }`

#### `wsl/distros/openTerminal`

请求 `{ "name": "Debian" }`：

```json
{ "opened": true, "terminal": "wt" }
```

实现优先级：`wt.exe -w 0 new-tab wsl.exe -d <name>`；不存在 `wt.exe` 时用 `cmd.exe /c start "" wsl.exe -d <name>`。**不要阻塞等待终端进程退出。**

#### `wsl/distros/openExplorer`

请求 `{ "name": "Debian" }` → `{ "opened": true, "uri": "\\\\wsl.localhost\\Debian" }`（优先 `\\wsl.localhost\`，回退 `\\wsl$\`，用 `explorer.exe` 打开）。

#### `wsl/shutdown`

请求 `{ "force": false }` → `{ "shutdown": true }`（`wsl --shutdown [--force]`）。

#### `wsl/update`（长任务）

请求 `{ "preRelease": false }` → `{ jobId }`（`wsl --update [--pre-release]`）。

#### `wsl/distros/install`（长任务）

请求 `{ "name": "Ubuntu", "location": "D:\\WSL\\Ubuntu", "version": 2 }` → `{ jobId }`。若需要 UAC 且失败，`job/end.error` 说明“需要管理员权限”。

### 4.3 wslc

#### `wslc/info`

```json
{
  "version": "3.0.3.0",
  "client": { "version": "3.0.3.0", "kernelVersion": "6.18.54.1-1", "direct3dVersion": "...", "dxCoreVersion": "...", "windowsVersion": "...", "settingsFile": "C:\\...\\settings.yaml" },
  "server": { "sessionManagerVersion": "3.0.3", "sessions": [ { "id": 1, "name": "wslc-cli-iaz00", "creatorPid": 24656 } ] }
}
```

字段名后端统一转 camelCase（原始 JSON 为 PascalCase）。会话原始格式可能是 `Sessions: [{CreatorPid, ID, Name}]`，归一化为 `{creatorPid, id, name}`。

#### `wslc/containers/list`

请求 `{ "all": true }`（默认 true）。

```json
{
  "containers": [
    {
      "id": "70759157800b83586540cae6189530a296d94e27ff6a9289d4f85e92077f0391",
      "shortId": "70759157800b",
      "name": "dbx-test",
      "image": "alpine:3.20",
      "command": "\"sleep 300\"",
      "createdAt": "2026-10-20 22:45:31 +0800 GMT+8",
      "runningFor": "2 seconds ago",
      "state": "running",
      "status": "Up 2 seconds",
      "ports": "",
      "networks": "bridge",
      "size": "0B",
      "health": "",
      "platform": { "architecture": "amd64", "os": "linux" }
    }
  ],
  "count": 1
}
```

解析自 `wslc list --all --format json` 的 JSONL（逐行 JSON）。某行解析失败则跳过并计入 `warnings`。

#### `wslc/containers/inspect`

请求 `{ "id": "dbx-test" }` → `{ "raw": { ... } }`（`wslc inspect <id>` 返回数组，取第一个元素；找不到时 `NOT_FOUND`）。

#### `wslc/containers/start|stop|restart|kill|remove`

请求：

- start/stop/restart：`{ "id": "dbx-test", "timeout": 10 }`（timeout 可选，秒）
- kill：`{ "id": "dbx-test", "signal": "SIGTERM" }`
- remove：`{ "id": "dbx-test", "force": true }`

返回：

```json
{ "id": "dbx-test", "action": "start", "output": "dbx-test\n", "exitCode": 0 }
```

#### `wslc/containers/run`

请求：

```json
{
  "image": "alpine:3.20",
  "name": "demo",
  "detach": true,
  "command": ["sleep", "300"],
  "env": { "FOO": "bar" },
  "ports": ["8080:80"],
  "volumes": ["dbxvol:/data"],
  "workdir": "/app",
  "user": "root",
  "cpus": "1.0",
  "memory": "512M",
  "rm": false
}
```

返回 `{ "id": "<container id>", "name": "demo" }`。`detach=false` 时此方法会挂起直到容器退出，**只允许长任务**：始终以 `-d` 启动并立即返回；UI 不提供前台模式。

#### `wslc/containers/exec`

请求：

```json
{ "id": "demo", "command": ["echo", "hi"], "user": "root", "workdir": "/", "env": { "A": "b" } }
```

返回 `RunResult`。

#### `wslc/containers/logs`

请求：

- 快照：`{ "id": "demo", "tail": 200, "timestamps": false }` → `RunResult`（`stdout` 为日志文本）。
- 跟随：`{ "id": "demo", "tail": 200, "follow": true }` → `{ "jobId": "job-5" }`，日志行通过 `job/output` 推送；`job/end` 在用户取消/容器停止时发出。

#### `wslc/containers/stats`

请求 `{ "all": false }` → 

```json
{ "stats": [ { "id": "7075...", "shortId": "70759157800b", "name": "dbx-test", "cpuPerc": "0.00%", "memUsage": "1.461MiB / 31.24GiB", "memPerc": "0.00%", "netIO": "1.04kB / 0B", "blockIO": "0B / 0B", "pids": 1 } ] }
```

解析自 `wslc stats --format json` JSONL。空输出返回 `{ "stats": [] }`。

#### `wslc/containers/prune`

请求 `{}` → `{ "output": "..." }`。

#### `wslc/images/list`

请求 `{}` →

```json
{ "images": [ { "id": "bf8527eb54c3", "repository": "alpine", "tag": "3.20", "digest": "<none>", "createdAt": "2026-04-17 07:53:26 +0800 GMT+8", "createdSince": "6 months ago", "size": "7.81MB", "containers": "1", "uniqueSize": "N/A" } ] }
```

#### `wslc/images/inspect`

请求 `{ "ref": "alpine:3.20" }` → `{ "raw": { ... } }`（数组取第一个）。

#### `wslc/images/pull`（长任务）

请求 `{ "ref": "alpine:3.20", "allTags": false }` → `{ jobId }`。job/end `result`: `{ "ref": "alpine:3.20" }`。

失败时 `job/end.error` 必须做认证原因分类（`job/output` 原文照常保留，便于用户看到原始报错）：

| stderr 命中 | `error.code` | `hint`（示例） |
| --- | --- | --- |
| `docker login` / `unauthorized` / `authentication required` / `pull access denied` / `denied: requested access` | `REGISTRY_AUTH_REQUIRED` | 该镜像需要登录注册表；请到「镜像 → 注册表凭据」登录后重试 |
| `toomanyrequests` / `rate limit` / `pull rate limit` | `REGISTRY_RATE_LIMITED` | Docker Hub 匿名拉取触发速率限制；登录（免费账号）可提高限额，或稍后重试 |
| `WSLC_E_IMAGE_NOT_FOUND` 且不含认证关键词 | `NOT_FOUND` | 镜像不存在或名称拼写错误 |
| 其他 | `PROCESS_FAILED` | 查看任务日志中的原始错误 |

`wslc/containers/run`（镜像缺失时隐式拉取）与 `wslc/images/build`（`FROM` 拉取）失败时使用同一套分类。

#### `wslc/registry/login`

请求：

```json
{ "server": "docker.io", "username": "your-account", "password": "***" }
```

- `server` 可省略（默认由 wslc 会话决定，通常为 Docker Hub）；支持 `docker.io`、`ghcr.io`、`registry.cn-hangzhou.aliyuncs.com`、`registry.example.com:5000` 等。
- 密码字段名固定为 `password`（登录成功后 UI 必须立即清空，不持久化、不写日志、不放进 job 输出）。

返回：

```json
{ "server": "docker.io", "username": "your-account", "loggedIn": true, "output": "Login Succeeded" }
```

实现约束：

- 只能通过 **stdin** 传密码：`wslc login [server] -u <username> --password-stdin`（写入密码 + `\n`）；**禁止**用 `-p` 把密码放进 argv/命令行。
- 超时 60s；`REGISTRY_AUTH_FAILED`（401/unauthorized/denied）、`TIMEOUT`、`INVALID_PARAMS`（空用户名/密码、server 含空白或非法字符）。
- 不把密码或 wslc 的完整命令写入 RPC 错误 `detail`。

#### `wslc/registry/logout`

请求 `{ "server": "docker.io" }`（可省略）→ `{ "server": "docker.io", "loggedOut": true, "output": "Removing login credentials for https://index.docker.io/v1/" }`。

- 幂等：本来未登录也返回 `loggedOut: true`。
- `wslc` 3.0.3 / 3.0.4 没有“列出已登录注册表”的命令，因此**不提供**凭据查询方法；UI 只把最近使用的 server/username 存入 `dbxPlugin.storage`（不含密码），并在界面上标注“是否已登录请以拉取结果为准”。

#### `wslc/images/remove`

请求 `{ "refs": ["alpine:3.20"], "force": false }` → `{ "removed": ["alpine:3.20"], "output": "..." }`。

#### `wslc/images/prune`（长任务）

请求 `{ "all": false }` → `{ jobId }`。

#### `wslc/images/tag`

请求 `{ "source": "alpine:3.20", "target": "local/alpine:test" }` → `{ "source": "..." , "target": "..." }`。

#### `wslc/images/build`（长任务）

请求：

```json
{ "path": "D:\\projects\\demo", "tag": "demo:dev", "dockerfile": "Dockerfile", "buildArgs": { "A": "b" }, "noCache": false, "target": "" }
```

→ `{ jobId }`。

#### `wslc/images/save` / `wslc/images/load`（长任务）

`save`: `{ "refs": ["alpine:3.20"], "path": "D:\\out\\alpine.tar" }`
`load`: `{ "path": "D:\\out\\alpine.tar" }`
→ `{ jobId }`。

#### `wslc/volumes/list`

```json
{ "volumes": [ { "name": "dbxvol", "driver": "guest", "mountpoint": "/var/lib/docker/volumes/dbxvol/_data", "scope": "local", "size": "N/A", "labels": "" } ] }
```

#### `wslc/volumes/inspect` → `{ "raw": { ... } }`

#### `wslc/volumes/create`：请求 `{ "name": "dbxvol" }`（name 可空自动生成）→ `{ "name": "dbxvol" }`

#### `wslc/volumes/remove`：请求 `{ "names": ["dbxvol"] }` → `{ "removed": ["dbxvol"], "output": "..." }`

#### `wslc/volumes/prune`：请求 `{}` → `{ "output": "..." }`

#### `wslc/networks/list`

```json
{ "networks": [ { "id": "0abca66cf2e0", "name": "bridge", "driver": "bridge", "scope": "local", "ipv4": true, "ipv6": false, "internal": false, "createdAt": "2026-10-20 14:44:43 +0000 UTC", "labels": "" } ] }
```

#### `wslc/networks/inspect` → `{ "raw": { ... } }`

#### `wslc/networks/create`：请求 `{ "name": "dbxnet", "driver": "bridge", "internal": false, "ipv6": false }` → `{ "id": "...", "name": "dbxnet" }`

#### `wslc/networks/remove`：请求 `{ "names": ["dbxnet"] }` → `{ "removed": [...], "output": "..." }`

#### `wslc/networks/connect` / `disconnect`：请求 `{ "network": "dbxnet", "container": "demo" }` → `{ "network": "...", "container": "...", "output": "..." }`

#### `wslc/networks/prune`：请求 `{}` → `{ "output": "..." }`

#### `wslc/sessions/list`

```json
{ "sessions": [ { "id": 1, "creatorPid": 24656, "displayName": "wslc-cli-iaz00" } ] }
```

#### `wslc/events/start`（长任务订阅）

请求 `{ "since": "", "until": "", "filters": [] }` → `{ jobId }`。
事件通过 `wslc/event` 推送：

```json
{ "method": "wslc/event", "params": { "jobId": "job-6", "event": { "Action": "die", "Actor": { "ID": "...", "Attributes": { "name": "demo", "exitCode": "137" } }, "Type": "container", "from": "alpine:3.20", "id": "...", "scope": "local", "status": "die", "time": 1792507552, "timeNano": 1792507552289873161 } } }
```

`wslc/events/stop`：请求 `{ "jobId": "job-6" }` → `{ "stopped": true }`（等价于 `jobs/cancel`）。

### 4.4 console（命令控制台）

#### `console/run`

请求：

```json
{ "tool": "wsl", "args": ["-l", "-v"], "timeoutMs": 15000 }
```

返回 `RunResult`（附 `tool`）。约束：

- `tool` 仅允许 `"wsl"` / `"wslc"`。
- `args` 为字符串数组，逐项作为 argv，**绝不拼接成 shell 字符串**。
- 禁止的顶层参数：无（先直接执行；UI 侧对危险命令提示确认）。
- `timeoutMs` 默认 20000，上限 120000。

#### `console/presets`

```json
{ "presets": [ { "id": "wsl-list", "label": "wsl -l -v", "tool": "wsl", "args": ["-l","-v"] } ] }
```

### 4.5 jobs

- `jobs/list` → 见 3.2
- `jobs/get` `{ jobId }` → 见 3.2
- `jobs/cancel` `{ jobId }` → `{ "jobId": "job-3", "cancelled": true }`

取消实现：`exec.CommandContext` + Windows 进程树终止（`taskkill /T /F /PID <pid>`），避免 `wslc pull` 等子进程残留。

## 5. 事件汇总

| method | params |
| --- | --- |
| `job/output` | `{ jobId, stream: "stdout"\|"stderr", text, ts }` |
| `job/end` | `{ jobId, ok, exitCode, error, result, durationMs }` |
| `wslc/event` | `{ jobId, event }` |

`error` 结构：`{ "code": "PROCESS_FAILED", "hint": "...", "detail": "..." }` 或 `null`。

## 6. 进程与编码约定（后端）

1. 工具解析顺序：
   - `wsl.exe`：`%SystemRoot%\System32\wsl.exe` → `PATH` → `%LOCALAPPDATA%\Microsoft\WindowsApps\wsl.exe`
   - `wslc.exe`：`PATH` → `%ProgramFiles%\WSL\wslc.exe` → `%LOCALAPPDATA%\Microsoft\WindowsApps\wslc.exe` → `C:\Program Files\WSL\wslc.exe`
   - 解析结果缓存 5 分钟；可用 `system/info` 查看。
2. `wsl.exe` 调用统一追加环境变量 `WSL_UTF8=1`；若输出仍以 `FF FE` 开头或含大量 `\x00`，按 UTF-16LE 解码。
3. `wslc` 一律使用 `--format json`（列表类）与 `inspect` 的 JSON 输出；解析失败保留原文到 `detail`。
4. stdout/stderr 用并发读取，避免管道阻塞；同步方法有超时（默认 30s，列表/信息 60s，exec 60s，run 120s），超时返回 `TIMEOUT` 并尽力杀进程树。
5. `wslc` 命令一律显式传参，不使用 `cmd.exe /c`。仅打开终端/资源管理器用 `Start-Process` 等价方式（`cmd /c start` 或 os/exec 直接启动）。
6. 所有写操作（unregister/remove/prune/shutdown/import/export/config write）在后端做参数校验（非空、长度、路径展开），错误返回 `INVALID_PARAMS`。
7. Sidecar 不记录任何凭据；`wslc login` 的密码只能经 stdin 传入（`--password-stdin`），不得进入 argv、日志、事件或错误 `detail`。

## 7. 前端约定

1. **单文件 `ui/index.html`**：内联 CSS + 内联 JS（开发宿主不支持相对 module URL；生产宿主也只内联入口脚本）。不引用任何 CDN。
2. 主题：`window.dbxPlugin.theme.appearance` + `tokens`，缺失时用内置明/暗变量；同时支持 `prefers-color-scheme`。
3. 多语言：`zh-CN` / `en`，由 `window.dbxPlugin.locale` 决定；所有文案走 `t()` 字典。
4. 持久化：`window.dbxPlugin.capabilities.storage` 为 true 且权限包含 `host.storage` 时，用 `window.dbxPlugin.storage.get/set` 保存当前页签、刷新间隔、列表筛选；禁止 `localStorage`。
5. 事件：`window.dbxPlugin.onEvent` 处理 `job/output`、`job/end`、`wslc/event`；组件卸载/页签切换时取消订阅。
6. 长任务统一走“任务面板”：展示 job 标题、实时日志、取消按钮；`job/end` 后刷新相关列表并 toast。
7. 破坏性操作（卸载发行版、删除容器/镜像/卷/网络、prune、shutdown）：二次确认，卸载发行版要求输入名称。
8. 加载态/空态/错误态必须齐全；`system/info` 的 `available:false` 要在对应页显示安装提示而不是白屏。
9. 轮询：容器列表/stats 在页面可见时默认 5s 刷新，可在设置中关闭；不在后台标签页轮询。
10. 单页路由：`dashboard | distros | containers | images | volumes | networks | console | events | settings`，使用 hash 路由并持久化。
11. **注册表凭据**：镜像页提供“注册表凭据”对话框（server / username / password + 登录 / 登出）。密码只通过 `invoke("wslc/registry/login")` 交给 Sidecar，由 Sidecar 用 stdin 传给 `wslc`；UI 不持久化密码、不把密码放进日志/标题/toast，提交后立即清空密码框。仅把 `server` / `username` 存入 `dbxPlugin.storage`。
12. 镜像相关失败：`job/end.error.code` 为 `REGISTRY_AUTH_REQUIRED` / `REGISTRY_RATE_LIMITED` / `REGISTRY_AUTH_FAILED` 时，任务面板与 toast 必须显示可读原因，并提供“去登录”入口（打开注册表凭据对话框，`server` 预填镜像所属 registry，例如 `ghcr.io`、`registry.cn-hangzhou.aliyuncs.com`）。

## 8. 验收样例（后端必须覆盖）

1. `system/info` 在 WSL 与 wslc 都可用时返回 `available:true`，`wsl.distroCount >= 1`。
2. `wsl/distros/list` 返回 Debian（本机）且 `isDefault:true`。
3. `wsl/distros/info` 的 `osRelease.PRETTY_NAME` 含 `Debian`，`disk.totalBytes > 0`。
4. `wsl/distros/exec` 执行 `echo hello` 返回 `exitCode:0, stdout` 含 `hello`。
5. `wslc/info` 的 `server.sessionManagerVersion` 非空。
6. `wslc/networks/list` 至少包含 `bridge`、`host`、`none`。
7. `wslc/images/pull` 拉取 `alpine:3.20` 产生成功 `job/end`（镜像已缓存时 `job/output` 可能为 0 行或多行，`job/end.ok` 才是权威结果）。
8. `wslc/containers/run` → list → logs → exec → stats → stop → remove 全链路成功。
9. 错误路径：`wslc/containers/inspect` 传不存在的 id 返回 `NOT_FOUND`。
10. `console/run` 执行 `wsl -l -v` 返回 `exitCode:0`。
11. `wslc/registry/login` 用错误凭据登录返回 `REGISTRY_AUTH_FAILED`，且 RPC 错误/事件中不出现密码；`wslc/registry/logout` 幂等返回 `loggedOut:true`。
12. 拉取不存在的私有镜像（如 `docker.io/library/definitely-not-a-real-image-xyz:latest`）时 `job/end.error.code = REGISTRY_AUTH_REQUIRED`（本机 wslc 3.0.3 / 3.0.4 原始报错含 `may require 'docker login'`）。
13. 登录成功后 `wslc/images/pull` 私有镜像可正常拉取（需要有效凭据；无凭据的自动化测试只覆盖失败分类）。

## 9. 实现注记（v1 实测）

### 9.1 前端调用超时建议

`dbxPlugin.invoke` 默认 30s。会触发发行版冷启动或需要调度 wslc 会话的调用建议显式传更长时间：

| 方法 | 建议 `timeoutMs` |
| --- | --- |
| `wsl/distros/exec` | 90000（冷启动实测可达 ~15s） |
| `wsl/distros/info` / `wslconf` / `start` | 90000 |
| `system/info`（首次唤醒 WSL/wslc 服务） | 60000 |
| 列表/查看类（`wsl/distros/list`、`wslc/*/list`、`*inspect`、`wslc/info`、`wslc/containers/stats`） | 60000 |
| 长任务（`pull`/`build`/`export`/`import`/`logs --follow`/`events`） | 保持默认，走事件与任务面板 |

### 9.2 缓存命中与空输出

- `wslc/images/pull` 在镜像已存在时仍会查询 registry 并可能只输出少量行；以 `job/end.ok` 为准。
- 空列表（无容器/镜像/卷）返回空数组而不是错误；UI 显示空态。
- `wslc` 3.0.3 / 3.0.4 的 `networks/create` 没有 `--ipv6` 选项：`ipv6:true` 返回 `UNSUPPORTED`（不静默降级）。

### 9.3 开发环境注意（Windows 沙箱/完整性级别）

若插件源码位于带 Low mandatory integrity label 的目录（部分受限/沙箱工作区），从该目录**直接启动**的 Sidecar 会以 Low 完整性运行，`wsl.exe` / `wslc.exe` 返回 `Wsl/E_ACCESSDENIED`。这是运行目录的完整性标签问题，不是插件缺陷。验证/开发时请把 dev host 的 `--data-dir` 放到常规用户目录（如 `%LOCALAPPDATA%`），使后端可执行文件在 Medium 完整性下运行；DBX 正常安装路径（插件商店）不受影响。


## 10. MCP bridge（0.1.3）

- `mcp/tools`：返回四个 MCP 工具定义，纯静态发现，不执行 WSL 命令。
- `mcp/call`：参数 `{tool, arguments, lifecycle?}`，兼容 DBX 插件桥。工作台仅操作当前 Windows 用户的本机资源，忽略连接 lifecycle，不接受远程目标。
- `wsl_connections`：无参数，复用 `wsl/distros/list`；不会把 WSLC 容器误认为 WSL 发行版。
- `wsl_describe`：可选 `method`，列举或获取方法类型 Schema；语义必填与取值范围由原有服务校验。
- `wsl_read` / `wsl_write`：`{method, paramsJson?}`，`paramsJson` 为 JSON 对象字符串，默认 `{}`。白名单与现有 73 个接口对应，禁止桥递归、未知方法和未知参数。字符串承载参数以保留跨模型 Schema 兼容性，避免宿主每插件 64 个工具上限截断能力。
- 查询工具只允许明确声明的查询方法；exec、配置、删除、后台事件、取消均需写工具。日志 `follow:true` 也需写工具。
- 返回标准 `{content:[{type:"text",text:"JSON"}],isError,resultType:"complete"}`，业务失败保留 code/message/hint/detail；非零命令退出码标记 `isError:true`。
- 长任务返回 jobId 后，通过同一 sidecar 的 `jobs/get` 查询最终状态和输出；返回 jobId 不代表执行成功。Sidecar 重启后原任务不可查询。
- 凭据仅经原有 registry 服务写入 stdin；参数解析失败不回显请求或解码器详情。MCP 客户端/宿主可能记录原始工具入参，优先在插件界面登录。

接入、宿主兼容限制与示例见 [MCP 指南](mcp.md)。
