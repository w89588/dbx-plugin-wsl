package handler

import (
	"context"
	"fmt"
	"github.com/w89588/dbx-plugin-wsl/internal/apperr"
	"github.com/w89588/dbx-plugin-wsl/internal/execx"
	"github.com/w89588/dbx-plugin-wsl/internal/parse"
	"github.com/w89588/dbx-plugin-wsl/internal/winenv"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type diagnosticParams struct {
	Registry string `json:"registry"`
	Ports    []int  `json:"ports"`
}
type diagnosticCheck struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	Evidence  string `json:"evidence"`
	Action    string `json:"action"`
	CheckedAt int64  `json:"checkedAt"`
}

func registryURL(host string) (string, *apperr.Error) {
	if strings.ContainsAny(host, "/@?#\\\r\n\t ") {
		return "", apperr.InvalidParams("注册表必须为主机名或主机:端口", "不接受 URL、路径或凭据", "")
	}
	u, e := url.Parse("https://" + host + "/v2/")
	if e != nil || u.Hostname() == "" {
		return "", apperr.InvalidParams("注册表地址无效", "例如 registry-1.docker.io", "")
	}
	return u.String(), nil
}
func (h *Handler) diagnostics(parent context.Context, p diagnosticParams) (any, *apperr.Error) {
	if len(p.Ports) > 16 {
		return nil, apperr.InvalidParams("最多检测 16 个端口", "请减少端口数量", "")
	}
	for _, port := range p.Ports {
		if port < 1 || port > 65535 {
			return nil, apperr.InvalidParams("端口范围为 1–65535", "请检查端口", "")
		}
	}
	endpoint := ""
	if p.Registry != "" {
		var e *apperr.Error
		endpoint, e = registryURL(p.Registry)
		if e != nil {
			return nil, e
		}
	}
	ctx, cancel := context.WithTimeout(parent, 50*time.Second)
	defer cancel()
	checks := []diagnosticCheck{}
	add := func(id, status, evidence, action string) {
		checks = append(checks, diagnosticCheck{id, status, evidence, action, time.Now().UnixMilli()})
	}
	for _, tool := range []execx.Tool{execx.ToolWSL, execx.ToolWSLc} {
		if _, e := h.Ex.Resolve(tool); e != nil {
			add(string(tool), "warning", "未找到工具", "安装或更新 WSL；WSLC 为可选组件")
			continue
		}
		args := []string{"--version"}
		if tool == execx.ToolWSLc {
			args = []string{"version"}
		}
		r, e := h.Ex.Run(ctx, execx.Request{Tool: tool, Args: args, Timeout: 10 * time.Second})
		if e != nil || r.TimedOut || r.ExitCode != 0 {
			add(string(tool), "error", "工具存在，但版本查询失败或超时", "检查 WSL 安装和 Windows 虚拟化组件；查看命令控制台")
			continue
		}
		evidence := "工具可执行"
		if tool == execx.ToolWSL {
			version := parse.ParseVersion(r.Stdout).WSL
			if version != "" {
				evidence += "；WSL " + version
			}
		}
		add(string(tool), "ok", evidence, "")
	}
	if free, e := winenv.FreeDiskBytes(winenv.HomeDir()); e == nil {
		status := "ok"
		if free < 5*1024*1024*1024 {
			status = "warning"
		}
		add("profile-disk", status, fmt.Sprintf("用户目录所在磁盘剩余 %.1f GiB", float64(free)/(1024*1024*1024)), "备份和导入前按实际数据量预留空间")
	} else {
		add("profile-disk", "unknown", "无法读取磁盘空间", "检查用户目录和磁盘访问权限")
	}
	for _, port := range p.Ports {
		listener, e := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		if e != nil {
			add("port-"+strconv.Itoa(port), "warning", "本机回环端口无法绑定，可能被占用或保留", "更换端口或检查正在运行的服务")
		} else {
			listener.Close()
			add("port-"+strconv.Itoa(port), "ok", "检测时可绑定；不表示已为后续启动预留", "")
		}
	}
	if endpoint != "" {
		client := &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		response, e := client.Do(request)
		if e != nil {
			add("registry", "error", "HTTPS 连接失败（已省略可能包含主机和代理信息的错误详情）", "检查 DNS、代理、证书和网络；不会发送注册表凭据")
		} else {
			response.Body.Close()
			status, action := "ok", ""
			switch response.StatusCode {
			case 200:
			case 401:
				action = "网络可达，私有镜像拉取前需登录"
			case 429:
				status = "warning"
				action = "注册表限流，登录后或稍后再试"
			default:
				status = "warning"
				action = "检查注册表地址、访问策略或服务状态"
			}
			add("registry", status, fmt.Sprintf("HTTPS /v2/ 返回 HTTP %d", response.StatusCode), action)
		}
	} else {
		add("registry", "skipped", "未指定注册表，未进行网络探测", "填写注册表主机名后重新诊断")
	}
	return map[string]any{"checkedAt": time.Now().UnixMilli(), "checks": checks, "redacted": true, "scope": "本机工具、用户盘空间、指定回环端口与可选注册表 HTTPS；不检查应用数据一致性"}, nil
}
