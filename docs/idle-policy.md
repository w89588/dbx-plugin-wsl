# 空闲退出与保活 / Idle shutdown and keep-alive

从 v0.1.2 开始，在 **发行版 → 空闲退出与保活** 或 **设置** 页管理 WSL 的空闲策略。

## 三种策略

- **保持发行版运行**：在当前 Windows 用户的 `.wslconfig` 中设置 `[general] instanceIdleTimeout=-1`。发行版启动后，即使关闭终端也不会因空闲自动退出。保留原有 `[wsl2] vmIdleTimeout`，无需让所有发行版都停止后的空虚拟机一直运行。
- **恢复 WSL 默认策略**：移除以上两个空闲超时覆盖项，其他配置保留。当前默认值是发行版 15 秒、WSL 2 虚拟机 60 秒；两个计时器的适用条件不同，不能把“关闭终端后 1 分钟”当作所有环境下的保证。
- **自定义空闲超时**：分别填写整数秒数。发行版允许 `-1` 或 `1–2147483` 秒；虚拟机允许 `1–2147483` 秒。插件转换为毫秒写入 WSL 配置。

## 保存与生效

保存不会停止发行版。请在合适时机点击“关闭 WSL 以应用配置”，确认停止影响，再从发行版页启动需要的发行版。保存的策略不等同于运行中的实例已经加载了新配置。

已有文件会先生成 `.wslconfig.bak-时间戳` 备份。插件保留无关配置与注释，检测其他程序对配置文件的修改；存在冲突时请重新读取，格式异常时请先修复编辑器中的配置。若要恢复以前的自定义设置，可将备份内容重新保存到配置编辑器，再重启 WSL。

本功能使用 WSL 原生配置，不依赖插件保持打开，不添加后台保活进程、定时任务或登录启动项。配置全局作用于当前 Windows 用户的发行版；虚拟机设置针对 WSL 2，不适用于 WSL 1 或 `wslc` 容器。它不阻止手动停止、Windows 睡眠、休眠或关机，也不在 Windows 登录时自动启动发行版。保活会持续占用内存和电量。

插件的目标环境为 Windows 11；原生设置需要支持它们的 WSL 版本。开发主机使用 WSL 3.0.4；旧版本若提示未知配置键，请先更新 WSL。没有声称对所有历史 WSL 版本都适用。

依据：[微软 WSL 配置文档](https://learn.microsoft.com/en-us/windows/wsl/wsl-config)，包括 `[general] instanceIdleTimeout`、`[wsl2] vmIdleTimeout` 和配置重启规则。

## English

Open **Distributions → Idle shutdown & keep-alive**, or **Settings**. Choose keep-alive, WSL defaults, or custom timeouts. Keep-alive writes only `[general] instanceIdleTimeout=-1`; it preserves the existing VM idle timeout. Default mode removes both overrides. Custom values are entered in seconds and written in milliseconds.

Save first, then shut down WSL when convenient and start the desired distribution again. Shutdown stops WSL workloads and requires confirmation. Existing configuration files are backed up; stale writes and malformed files are rejected. An already-saved policy does not prove a running instance has reloaded it.

This is a persistent WSL setting, not a Windows sleep-prevention feature or an autostart service. It remains saved after DBX closes. VM settings apply to WSL 2, not WSL 1 or wslc containers. Use an up-to-date WSL version; keeping distributions running consumes memory and power.

For plugin problems and suggestions, use [this repository’s Issues](https://github.com/w89588/dbx-plugin-wsl/issues). Include versions, reproduction steps, expected/actual behavior and redacted logs.
