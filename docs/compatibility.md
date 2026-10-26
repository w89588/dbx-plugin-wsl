# DBX 宿主兼容性验证

验证日期：2026-10-26；插件修正版：0.4.1。

## 结论

删除 `manifest.json` 的 `engines.dbx`，不再人为要求 `>=0.6.55`，保留 `engines.host_api: "1"`、协议版本 1 和现有权限。当前包含 MCP 声明的完整包，**官方解析及兼容性规则的最低已验证发行版本为 DBX 0.6.27**；不能据此声称所有旧版都能安装或已完成真实旧版客户端验收。

原来的 0.5.68 也不是可靠基线：该标签树只有早期 JDBC 插件结构，没有当前通用工作台的 `plugins/manifest.schema.json`。0.1.3 新增 MCP 时直接把参考宿主 0.6.55 当作安装下限，缺少最低版本验证；本次予以纠正。

## 验证证据

| 宿主版本 | 实际验证 | 结果 |
| --- | --- | --- |
| 0.6.26 | 原始 Rust 清单解析器读取 0.4.1 包 | 拒绝 `unknown variant mcp`，发生在版本判断之前 |
| 0.6.27 | 原始 Rust 解析器及 `compatibility()` 检查解包目录 | 通过：权限、贡献点、入口文件、Host API、协议声明及 MCP 开关 |
| 0.6.27 对照组 | 在同一份清单上恢复 `engines.dbx >=0.6.55` | 被版本条件拒绝，证明旧限制阻挡了本可通过规则的包 |
| 0.6.55 | 原始 Rust 解析器及 `compatibility()` | 通过，MCP 开关保留 |
| 0.6.27 前端 SDK | 使用官方 `pluginSdkSource` 注入浏览器，模拟宿主响应 | 页面导航、存储、任务事件、资源原位刷新/断线补偿、备份、配置和模板回归通过 |
| 0.4.1 包内 Sidecar | 原生 stdio 握手、MCP 和隔离配置测试 | 85 个方法、读写边界及配置冲突回归通过；配置测试首次曾遇到 Windows 文件替换 Access denied，隔离目录重跑通过，未以本次兼容性修复声称解决该偶发错误 |

上游新增 MCP 清单类型的提交为 [f61f64f](https://github.com/t8y2/dbx/commit/f61f64f7272508981921c0e144062ae6e749502b)，包含在 v0.6.27，不包含在 v0.6.26。该提交也明确说明：旧宿主无法读取新增类型，前端能力检测不能补救安装阶段的解析失败。

固定版本源码：

- [v0.6.26 解析器](https://github.com/t8y2/dbx/blob/53ef9064074a343f8672cf73fc6caf21d56f91be/crates/dbx-plugin-runtime/src/plugins/manifest.rs)
- [v0.6.27 解析器及兼容性判断](https://github.com/t8y2/dbx/blob/52f2e8904d8bf4f5d05afe144a1355949af1450a/crates/dbx-plugin-runtime/src/plugins/manifest.rs)
- [v0.6.27 前端事件桥与 SDK](https://github.com/t8y2/dbx/blob/52f2e8904d8bf4f5d05afe144a1355949af1450a/apps/desktop/src/lib/plugins/pluginHostBridge.ts)
- [v0.6.55 解析器](https://github.com/t8y2/dbx/blob/aa79f4028f39166e176c7b5dc27ed18853ca3a8f/crates/dbx-plugin-runtime/src/plugins/manifest.rs)

`PluginEngines.dbx` 有 serde 默认值；省略时为空，`validate_engine_requirement` 对空值直接返回。`host_api` 仍为必填项。因此删除产品版本条件符合宿主规则，不是绕过协议或权限校验。

## 复现

需要 Node、Python、GitHub CLI 和具备链接器的 Rust 工具链：

```powershell
npx --yes @dbx-app/plugin-cli@0.1.9 package .
node tests/host-compatibility.mjs
$env:DBX_HOST_BRIDGE_SOURCE='tests/tmp/host-compatibility/bridge-0.6.27.ts'
node tests/ui-smoke.mjs
node tests/sidecar-mcp.mjs
node tests/sidecar-idle.mjs
```

历史源码从固定提交下载，SHA-256 固定在 `tests/fixtures/host-compatibility.json`，未经修改放入独立临时 Rust 工程执行。若本机没有 MSVC 链接器而已有 MinGW，可为该命令设置 `$env:RUSTUP_TOOLCHAIN='stable-x86_64-pc-windows-gnu'`，不改变全局默认工具链。

## 尚未验证的边界

没有安装/替换用户的 DBX 客户端，也没有在旧版桌面端执行完整插件安装、签名验证、授权弹窗、WebView 和外部 MCP 端到端验收。浏览器测试使用真实历史 SDK，但宿主响应模拟；Rust 测试执行真实清单规则，但不是完整宿主进程。

当前包保留 MCP 声明及功能，不声称兼容 0.6.26 及更早版本。真正支持那些版本，需要另行提供不带新清单声明的兼容方案，而不是只改版本号。0.4.1 为新候选发布，旧版本资产不覆盖。
