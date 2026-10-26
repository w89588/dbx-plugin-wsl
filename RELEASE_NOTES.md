# WSL 管理器 v0.1.3

## MCP 接入与完整管理能力

- 新增 DBX 插件 MCP 桥 `mcp/tools` / `mcp/call`，公开 `wsl_connections`、`wsl_describe`、`wsl_read`、`wsl_write` 四个工具，覆盖现有全部 73 个接口。
- AI 可读取本机 WSL 发行版连接及 WSLC 容器信息，并管理发行版、容器、镜像、数据卷、网络、注册表、系统配置和后台任务；支持通过 exec 执行命令及文件读写。
- 只读查询和写工具独立声明，阻止通过只读入口执行命令、删除资源、修改配置或启动后台订阅。未知方法、桥递归、错误字段和参数类型均被拒绝。
- 提供参数类型发现、结构化业务错误、非零退出失败标记和长任务查询/取消。任务创建成功不代表任务最终成功。
- 保留保活配置的版本冲突保护与原有凭据 stdin 处理，参数解析失败不回显原始凭据。

## 安装与接入

下载 `com.dbx.wsl-0.1.3-windows-x64.dbxp`，在 **DBX 0.6.55+** 的插件中心安装；该 Release 是未签名候选包，本地安装需启用“允许安装未签名开发包”。官方商店签名由维护者完成。

通过 DBX 的外部 MCP 服务接入 AI 客户端，刷新工具列表后可见 `dbx_wsl__wsl_connections` 等工具。写操作需宿主/客户端授权；插件不能放宽 DBX 的只读策略。

当前 DBX 内置 AI 仅发现已打开的连接型插件，工作台型 WSL 插件通过外部 MCP 使用。`wsl_connections` 表示当前 Windows 用户的本机发行版，不表示远程主机或 DBX 数据库连接。

## 权限、数据与验证

权限仍为 `host.workbench`、`host.events`、`host.storage`、`host.binary`，不新增端口或独立 MCP 密钥。Sidecar 以当前 Windows 用户权限调用原有 WSL/WSLC 服务；命令可读写文件，删除/清理会真实删除资源。注册表密码只经 stdin 交给 wslc，但 AI 客户端/宿主可能记录工具入参，建议先在插件界面登录。拉取镜像、注册表认证和用户执行的命令可访问网络。

已通过 Go 全量测试、静态检查、Manifest 与安装包预检，以及包内程序的 MCP 发现、73 接口目录、隔离配置读写、只读拦截、冲突保护、密码脱敏和桥递归拒绝测试。实际包内 MCP 查询已读取本机 1 个 WSL 发行版和 0 个 WSLC 容器，原有界面回归通过。真实安装/删除/更新的全量操作未在个人工作负载上执行。

[接入与调用示例](https://github.com/w89588/dbx-plugin-wsl/blob/v0.1.3/docs/mcp.md) · [源码仓库](https://github.com/w89588/dbx-plugin-wsl) · [问题与建议](https://github.com/w89588/dbx-plugin-wsl/issues)
