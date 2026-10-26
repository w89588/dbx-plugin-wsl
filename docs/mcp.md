# AI / MCP 接入

WSL 管理器 0.1.6 通过 DBX 的插件 MCP 桥公开四个工具，覆盖 **85 个管理 RPC 接口**（两个资源订阅接口仅供 UI 使用）。不启动额外端口，不另存连接或凭据，不需要插件 API Key。

## 安装与启用

1. 安装本版本 `.dbxp`，不再用 `engines.dbx` 人为限定产品版本。当前 MCP 声明从 **DBX 0.6.27** 开始可解析，0.6.26 及更早的解析器不识别该类型，详见[兼容性证据](compatibility.md)。GitHub Release 为未签名候选包，本地安装需允许未签名开发包；官方商店版本由维护者审核签名。
2. 使用 DBX 自带的 MCP 配置/安装入口，将 **DBX MCP 服务**连接到 AI 客户端。沿用 DBX 生成的配置和当前 Windows 用户的数据目录。
3. 重连 MCP 客户端并刷新工具列表。插件 Manifest 已声明 `external_tools:true`，兼容的宿主会发现 `dbx_wsl__wsl_connections`、`dbx_wsl__wsl_describe`、`dbx_wsl__wsl_read`、`dbx_wsl__wsl_write`。
4. 查询能力使用只读工具；需要修改时，在 DBX MCP 策略及客户端中允许对应写工具。宿主只读策略优先，插件不能放宽宿主策略。

**内置 AI 的当前限制**：核对 DBX 0.6.55 发布时的插件桥实现，内置 AI 仅发现已打开的连接型插件；WSL 管理器是工作台型插件，尚无连接提供者，所以本版本使用外部 DBX MCP 接入。Manifest 的 `ai_tools:true` 声明了允许接入意图，但不会让内置 AI 绕过上述发现规则。

**连接含义**：`wsl_connections` 返回当前 Windows 用户已安装的 WSL 发行版（含停止的发行版），不是 DBX 数据库连接，也不是远程 SSH 连接。WSLC 容器另用 `wslc/containers/list` 查询。运行身份就是启动 DBX MCP 的 Windows 用户；宿主传入的 lifecycle 不会改变本机执行身份。

## 四个工具

| 插件工具名 | 用途 | 只读 |
| --- | --- | --- |
| `wsl_connections` | 列出 WSL 发行版名称、运行状态、版本、默认发行版、磁盘路径 | 是 |
| `wsl_describe` | 列出全部方法，或查询一个方法的参数类型 | 是 |
| `wsl_read` | 状态、配置、发行版/容器详情、镜像/卷/网络/会话、日志快照、任务查询 | 是 |
| `wsl_write` | 启停/创建/删除、导入导出、命令及文件读写、配置、镜像、网络、注册表、任务取消 | 否 |

外部 MCP 工具名增加 `dbx_wsl__` 前缀。`mcp/tools` 与 `mcp/call` 是 DBX 与插件之间的协议方法，不是另一个独立 MCP 服务地址。

使用四个稳定工具而非逐个暴露 85 个方法，避免 DBX 每插件最多 64 个工具导致后面的管理能力被截断。`paramsJson` 使用 JSON 对象字符串，兼容宿主和不同模型对嵌套 Schema 的简化；具体字段由现有 Go 参数类型生成，通过 `wsl_describe` 查询，语义必填和取值范围仍由原服务校验。省略 `paramsJson` 等价于 `{}`。

## 调用示例

先列连接：

```json
{"name":"dbx_wsl__wsl_connections","arguments":{}}
```

查询接口参数：

```json
{"name":"dbx_wsl__wsl_describe","arguments":{"method":"wslc/containers/run"}}
```

列出所有容器：

```json
{"name":"dbx_wsl__wsl_read","arguments":{"method":"wslc/containers/list","paramsJson":"{\"all\":true}"}}
```

启动指定发行版：

```json
{"name":"dbx_wsl__wsl_write","arguments":{"method":"wsl/distros/start","paramsJson":"{\"name\":\"Debian\"}"}}
```

在发行版里执行命令（WSL 的 `command` 是字符串；这是具备任意读写能力的写工具）：

```json
{"name":"dbx_wsl__wsl_write","arguments":{"method":"wsl/distros/exec","paramsJson":"{\"name\":\"Debian\",\"command\":\"printf hello\"}"}}
```

在容器中执行命令（WSLC 的 `command` 是数组）：

```json
{"name":"dbx_wsl__wsl_write","arguments":{"method":"wslc/containers/exec","paramsJson":"{\"id\":\"容器ID\",\"command\":[\"sh\",\"-lc\",\"printf hello\"]}"}}
```

文件读写通过上述 exec 接口执行 Linux 文件命令，继承当前用户/容器权限；没有另设文件传输工具或绕过权限的提权能力。文件命令的结果同样受原有超时和输出上限约束。

## 完整能力范围

- `system/*`：工具状态、WSL 状态、可安装发行版、读取/写入配置、空闲退出/保活策略、固定项目链接。
- `wsl/distros/*`：列表、启动、终止、默认发行版、执行命令、信息、wsl.conf、导出、导入、卸载、版本、稀疏磁盘、压缩、默认用户、扩容、统一 manage、终端与资源管理器、安装。
- `wsl/shutdown`、`wsl/update`：关闭与更新。
- `wslc/containers/*`：列表、检查、启动、停止、重启、kill、删除、run、exec、日志、统计、清理。
- `wslc/images/*`：列表、检查、拉取、删除、清理、标记、构建、保存、加载。
- `wslc/volumes/*`、`wslc/networks/*`：列表、检查、创建、删除、清理，网络连接/断开。
- `wslc/info`、`wslc/sessions/list`、`wslc/events/start|stop`、`wslc/registry/login|logout`。
- `console/run|presets`、`jobs/list|get|cancel`。

查询完整方法和字段类型：调用 `wsl_describe` 且不传 `method`。详细语义参见 [RPC 合约](rpc-contract.md)。

## 长任务、失败与授权

拉取/构建镜像、发行版安装/导入导出/版本转换等返回 `jobId`，只表示任务已经创建。用 `wsl_read` 调用 `jobs/get`（`paramsJson` 为 `{"jobId":"返回的ID"}`），检查 `status`、`error`、`result` 和输出，直到 `completed`、`failed` 或 `cancelled`。需要停止时用 `wsl_write` 调用 `jobs/cancel`。`jobs/list` 默认列出运行中任务；传 `includeCompleted:true` 可查询当前 Sidecar 最近 32 个已结束任务（最长 24 小时），`jobs/get` 返回带序号的输出及权威终态。任务记录属于当前 sidecar，进程重启后不保留；长任务进行中不要关闭宿主或卸载插件。

日志快照允许 `wsl_read`，`follow:true` 会创建后台任务，必须使用 `wsl_write`。事件订阅同样属于写工具，结束后取消任务。命令执行即便只运行 `cat` 也归入写工具，避免任意命令绕过宿主审批。

业务错误返回 `isError:true`，JSON 内容带 `error.code/message/hint/detail`；命令非零退出也标记失败。参数未知字段、错误类型、嵌套桥方法会被拒绝。MCP 不弹出插件 UI 的二次确认框，写操作的确认由宿主/客户端承担；`unregister`、`remove`、`prune` 等会真实删除资源。

保活写入使用 `expectedRevision` 做并发保护。通用 `system/config/write` 必须携带 `system/config/read` 中对应文件的 `revision`，字段名为 `expectedRevision`；缺失返回 INVALID_PARAMS，版本冲突返回 CONFLICT，且不覆盖文件。配置写入不会自动关闭 WSL。

注册表密码继续仅从 stdin 交给 `wslc`；插件不会把参数解析失败的原始请求或密码写入错误/日志。**AI 客户端与宿主可能保存工具调用入参**，优先使用插件 UI 登录后再让 AI 拉取镜像。插件不会自动提交日志到外部服务；拉取/构建/注册表登录以及用户执行的命令可以访问网络。

## 验证

```powershell
# 后端：模拟 WSL/WSLC，覆盖参数、只读边界、凭据、任务和隔离配置写入
cd backend
go test ./...
go vet ./...
cd ..

# 打包后的二进制：隔离 USERPROFILE，不修改个人配置
node tests/sidecar-mcp.mjs
node tests/sidecar-idle.mjs
```

可选真实只读查询：设置环境变量 `WSL_MCP_LIVE=1` 后运行 `node tests/sidecar-mcp.mjs`，额外查询当前用户的 WSL 发行版与容器列表。配置写入部分仍使用隔离配置目录。

包内程序验证不等于所有真实删除/安装/更新操作均已在个人机器执行。当前不通过自动化测试卸载发行版、删除已有容器或修改个人 WSL 配置。


## 0.1.6 工作流语义

先用 `wsl_describe` 查询方法的约束、操作影响、完成条件和重试建议。模板流程为 `templates/catalog` → `templates/plan` → 用户确认 → `templates/create` → `jobs/get` → `templates/status`。创建参数包含敏感密码，宿主和客户端可能保留工具输入；不要在聊天中复述密码。复用健康同配置环境会直接返回 `reused:true`，不产生 jobId，不旋转密码。失败后先检查已有资源，不能自动重放；重建使用新名称。

资源配置先 `system/resources/read` 获取 revision，再 `preview`、确认并 `apply`。遇到冲突应重新读取并重新确认差异，不能只替换 revision 后覆盖。模板和诊断不会自动创建 DBX 连接或自动修复系统。删除、exec 与 console 标为 `never-automatic`，必须保持明确操作授权边界。

0.1.6 已撤下页面模板功能，以上模板方法仅保留在后台 RPC/MCP。
