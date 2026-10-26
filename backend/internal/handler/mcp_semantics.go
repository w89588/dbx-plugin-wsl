package handler

import (
	"reflect"
	"runtime"
	"strings"
)

// Descriptions augment the existing wire contract; service validation remains authoritative.
func describeMethod(m mcpMethod) map[string]any {
	schema := mcpSchema(reflect.TypeOf(m.Params))
	props := schema["properties"].(map[string]any)
	field := func(key string, attributes map[string]any) {
		if p, ok := props[key].(map[string]any); ok {
			for k, v := range attributes {
				p[k] = v
			}
		}
	}
	required := func(keys ...string) { schema["required"] = keys }
	d := map[string]any{"method": m.Method, "readOnly": m.ReadOnly, "inputSchema": schema, "description": "当前 Windows 用户的 " + m.Method + " 操作；字段类型来自服务参数。", "preconditions": []string{"使用当前用户可见的资源名称或 ID；服务仍会验证参数及运行时状态"}, "sideEffects": []string{}, "retrySafety": "read-only", "completion": "返回值代表本次调用结果；若包含 jobId，必须继续查询 jobs/get，completed 才表示任务成功。"}
	if !m.ReadOnly {
		d["retrySafety"] = "inspect-before-retry"
		d["sideEffects"] = []string{"可能修改本机资源或创建后台任务；超时或断线后先检查实际状态"}
	}
	if strings.Contains(m.Method, "/exec") || m.Method == "console/run" {
		d["sideEffects"] = []string{"执行任意命令，可读写文件、访问网络或删除数据"}
		d["retrySafety"] = "never-automatic"
	}
	if strings.Contains(m.Method, "/remove") || strings.Contains(m.Method, "/prune") || m.Method == "wsl/distros/unregister" {
		d["sideEffects"] = []string{"删除资源，可能永久删除数据；先确认具体目标和备份"}
		d["retrySafety"] = "never-automatic"
	}
	switch m.Method {
	case "templates/catalog":
		d["description"] = "列出 PostgreSQL/Redis 模板及本机创建记录。记录不代表运行或就绪，使用 templates/status 检查。"
	case "templates/plan", "templates/create":
		required("kind", "name", "port")
		field("kind", map[string]any{"enum": []string{"postgres", "redis"}})
		field("name", map[string]any{"pattern": "^[a-z][a-z0-9-]{2,30}$", "description": "唯一环境名称；创建后保留记录，重建使用新名称"})
		field("port", map[string]any{"minimum": 1, "maximum": 65535, "description": "必填本机端口；推荐 PostgreSQL 5432、Redis 6379，仅绑定 127.0.0.1"})
		d["description"] = "预览本机开发环境的固定镜像、数据卷、端口及副作用；不访问网络或写入资源。"
		if m.Method == "templates/create" {
			required("kind", "name", "port", "password")
			field("password", map[string]any{"writeOnly": true, "description": "12–128 UTF-8 字节，无 CR/LF/NUL；不保存在记录或任务输出。容器 inspect 和宿主输入可能包含凭据。"})
			d["description"] = "创建带密码认证和持久化卷的开发容器，等待健康检查；不创建 DBX 数据库连接。"
			d["preconditions"] = []string{"已预览并获得具体创建授权；WSLC 可用，注册表可访问，本机端口可用", "同名容器或数据卷不得属于其他资源；已有记录只可复用完全匹配且健康的环境"}
			d["sideEffects"] = []string{"拉取镜像、保存不含密码的环境记录、创建数据卷和后台容器", "临时环境文件传递密码并在正常退出/取消时清理；进程被强制结束可能遗留文件", "失败保留已创建资源；同名重试不修改密码，不自动重新初始化"}
			d["completion"] = "新建返回 jobId，查询 jobs/get 至 completed，再查询 templates/status。复用返回 reused=true 和 ready=true，无 jobId。失败/取消可能已有资源。"
		}
	case "templates/status":
		required("name")
		d["description"] = "按本地记录检查资源归属、容器运行与健康状态，返回不含密码的连接信息。"
	case "system/resources/read":
		d["description"] = "读取当前用户 .wslconfig 的资源限制和 revision；不会修改配置或启动 WSL。"
	case "system/resources/preview", "system/resources/apply":
		required("expectedRevision")
		field("memoryMB", map[string]any{"minimum": 256, "maximum": 1048576, "description": "单位 MiB；省略恢复默认"})
		field("swapMB", map[string]any{"minimum": 0, "maximum": 1048576, "description": "单位 MiB；0 禁用，省略恢复默认"})
		field("processors", map[string]any{"minimum": 1, "maximum": runtime.NumCPU(), "description": "逻辑 CPU 数量；省略恢复默认"})
		d["description"] = "基于最新 revision 预览或保存内存、CPU、交换空间配置，保留其他配置与注释。"
		d["preconditions"] = []string{"先调用 system/resources/read，传回其 expectedRevision；冲突时重新读取并重新确认差异"}
		if m.Method == "system/resources/apply" {
			d["sideEffects"] = []string{"备份并修改当前用户 .wslconfig，影响其全部 WSL 2 发行版", "不会自动关闭 WSL；须另行重启才能应用"}
		}
	case "system/config/write", "system/wsl/idle/write":
		required("expectedRevision")
		d["preconditions"] = []string{"先读取对应配置及 revision；不能在冲突后用新 revision 直接覆盖旧草稿"}
		d["sideEffects"] = []string{"备份并修改配置文件；不自动关闭 WSL"}
		if m.Method == "system/config/write" {
			required("target", "content", "expectedRevision")
			field("target", map[string]any{"enum": []string{"wslconfig", "wslc"}})
		} else {
			required("mode", "expectedRevision")
			field("mode", map[string]any{"enum": []string{"default", "keepAlive", "custom"}})
		}
	case "backups/create":
		required("name")
		field("stopBeforeBackup", map[string]any{"default": false, "description": "运行中必须显式同意停止所选发行版；会中断其中的进程"})
		d["sideEffects"] = []string{"写入 tar 备份和 SHA-256 记录；选择 stopBeforeBackup 时停止源发行版，不自动重新启动"}
		d["preconditions"] = []string{"源发行版存在，备份盘空间足够；运行中数据库先做应用级备份"}
	case "backups/verify":
		required("id")
		d["description"] = "异步检查备份文件大小和 SHA-256；返回 jobId，不代表校验通过。"
	case "backups/restore":
		required("id", "name", "location")
		d["preconditions"] = []string{"备份完整；目标发行版名和安装目录均为新值"}
		d["sideEffects"] = []string{"校验备份、导入新 WSL 2 发行版并检查启动；失败保留新资源供检查，不覆盖原发行版"}
	case "diagnostics/run":
		field("ports", map[string]any{"maxItems": 16, "items": map[string]any{"type": "integer", "minimum": 1, "maximum": 65535}})
		field("registry", map[string]any{"description": "可选主机名或主机:端口，不含协议、路径或凭据；GET HTTPS /v2/ 不跟随重定向"})
		d["description"] = "脱敏诊断 WSL/WSLC、用户盘空间、回环端口和可选注册表；通过 MCP 必须使用 wsl_write，只代表检查时刻。"
		d["preconditions"] = []string{"先取得用户对具体诊断目标和端口的授权；MCP 只读工具不可调用，包括未指定 registry 或 ports 时"}
		d["sideEffects"] = []string{"临时绑定后释放指定回环端口；可选匿名 HTTPS 请求；不自动修复"}
	case "jobs/get", "jobs/cancel", "wslc/events/stop":
		required("jobId")
		d["preconditions"] = []string{"jobId 必须来自当前 sidecar；结束任务最多保留 32 个且最长 24 小时，重启会清空"}
	case "wslc/containers/logs":
		d["retrySafety"] = "read-only-if-follow-false"
		d["completion"] = "follow=false 同步返回；follow=true 必须用 wsl_write 并返回 jobId，结束后 jobs/cancel。"
	}
	return d
}
