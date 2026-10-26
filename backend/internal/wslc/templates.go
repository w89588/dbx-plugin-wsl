package wslc

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	dbxpluginsdk "github.com/t8y2/dbx/plugins/sdk/go/dbx-plugin-sdk"
	"github.com/w89588/dbx-plugin-wsl/internal/apperr"
	"github.com/w89588/dbx-plugin-wsl/internal/jobs"
	"github.com/w89588/dbx-plugin-wsl/internal/parse"
	"github.com/w89588/dbx-plugin-wsl/internal/winenv"
)

// TemplatePlanParams contains no credentials, so previews can be copied safely.
type TemplatePlanParams struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	Port int    `json:"port"`
}
type TemplateCreateParams struct {
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	Port     int    `json:"port"`
	Password string `json:"password"`
}
type TemplateStatusParams struct {
	Name string `json:"name"`
}
type templateRecord struct {
	TemplatePlanParams
	Token     string `json:"token"`
	CreatedAt string `json:"createdAt"`
}
type templateSpec struct {
	Kind, Image, Mount, Health string
	Port                       int
}

var templateSpecs = []templateSpec{
	{"postgres", "postgres:17-alpine", "/var/lib/postgresql/data", `pg_isready -U app -d app`, 5432},
	{"redis", "redis:7-alpine", "/data", `REDISCLI_AUTH="$REDIS_PASSWORD" redis-cli ping | grep -qx PONG`, 6379},
}
var templateName = regexp.MustCompile(`^[a-z][a-z0-9-]{2,30}$`)

func templateDir() string {
	return filepath.Join(winenv.LocalAppData(), "dbx-wsl-manager", "environments")
}
func templatePath(name string) string            { return filepath.Join(templateDir(), name+".json") }
func templateNames(name string) (string, string) { return "dbx-" + name, "dbx-" + name + "-data" }
func templateValidate(p TemplatePlanParams) (templateSpec, *apperr.Error) {
	if !templateName.MatchString(p.Name) || p.Port < 1 || p.Port > 65535 {
		return templateSpec{}, apperr.InvalidParams("环境名称或端口无效", "名称使用 3–31 位小写字母、数字或连字符，首位为字母；端口为 1–65535", "")
	}
	for _, spec := range templateSpecs {
		if spec.Kind == p.Kind {
			return spec, nil
		}
	}
	return templateSpec{}, apperr.InvalidParams("不支持的模板", "kind 必须为 postgres 或 redis", "")
}
func templateFailure(message string) *apperr.Error {
	return apperr.ProcessFailed(message, "检查模板状态及容器页面；已创建的容器和数据卷会保留，不要盲目重试", "")
}
func templateSummary(r templateRecord) map[string]any {
	c, v := templateNames(r.Name)
	return map[string]any{"name": r.Name, "kind": r.Kind, "host": "127.0.0.1", "port": r.Port, "container": c, "volume": v, "createdAt": r.CreatedAt, "username": map[string]string{"postgres": "app", "redis": "default"}[r.Kind], "database": map[string]string{"postgres": "app", "redis": "0"}[r.Kind]}
}
func (s *Service) TemplateCatalog() (any, *apperr.Error) {
	presets := []any{}
	for _, spec := range templateSpecs {
		presets = append(presets, map[string]any{"kind": spec.Kind, "image": spec.Image, "defaultPort": spec.Port})
	}
	environments := []any{}
	entries, err := os.ReadDir(templateDir())
	if err != nil && !os.IsNotExist(err) {
		return nil, templateFailure("读取环境记录失败")
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		b, e := os.ReadFile(filepath.Join(templateDir(), entry.Name()))
		var r templateRecord
		if e == nil && json.Unmarshal(b, &r) == nil {
			if _, e := templateValidate(r.TemplatePlanParams); e == nil {
				environments = append(environments, templateSummary(r))
			}
		}
	}
	return map[string]any{"presets": presets, "environments": environments}, nil
}
func (s *Service) TemplatePlan(p TemplatePlanParams) (any, *apperr.Error) {
	spec, e := templateValidate(p)
	if e != nil {
		return nil, e
	}
	out := templateSummary(templateRecord{TemplatePlanParams: p})
	out["image"] = spec.Image
	out["mount"] = spec.Mount
	out["effects"] = []string{"拉取官方镜像，创建命名数据卷和后台容器", "仅绑定 127.0.0.1；启用密码认证", "失败保留资源；同名记录只允许查询或复用，不重新初始化", "密码通过临时环境文件传递，容器 inspect 及宿主工具参数仍可能包含凭据", "适用于本机开发；不会自动创建 DBX 数据库连接"}
	return out, nil
}

type templateInspection struct {
	Labels map[string]string
	Config struct{ Labels map[string]string }
	State  struct {
		Running bool
		Status  string
		Health  struct{ Status string }
	}
}

func (s *Service) templateInspect(ctx context.Context, kind, name string) (*templateInspection, *apperr.Error) {
	r, e := s.run(ctx, 30*time.Second, kind, "inspect", "--format", "json", name)
	if e != nil {
		return nil, templateFailure("检查资源失败")
	}
	if r.ExitCode != 0 {
		if apperr.LooksNotFound(r.Stderr + " " + r.Stdout) {
			return nil, nil
		}
		return nil, templateFailure("无法确定资源是否存在")
	}
	b, err := parse.FirstJSONValue([]byte(r.Stdout))
	if err != nil {
		return nil, templateFailure("无法解析资源状态")
	}
	var v templateInspection
	if json.Unmarshal(b, &v) != nil {
		return nil, templateFailure("无法解析资源状态")
	}
	return &v, nil
}
func templateOwned(v *templateInspection, token string) bool {
	return v != nil && (v.Labels["com.dbx.wsl.owner"] == token || v.Config.Labels["com.dbx.wsl.owner"] == token)
}
func (s *Service) templateStatus(ctx context.Context, r templateRecord) (map[string]any, *apperr.Error) {
	out := templateSummary(r)
	c, v := templateNames(r.Name)
	ci, e := s.templateInspect(ctx, "container", c)
	if e != nil {
		return out, e
	}
	vi, e := s.templateInspect(ctx, "volume", v)
	if e != nil {
		return out, e
	}
	out["ready"] = false
	out["status"] = "incomplete"
	out["nextStep"] = "在容器和数据卷页面检查已保留资源；不自动删除或重新初始化。需要重建时使用新环境名。"
	if (ci != nil && !templateOwned(ci, r.Token)) || (vi != nil && !templateOwned(vi, r.Token)) {
		return out, apperr.Conflict("同名资源归属冲突", "不得覆盖现有资源", "")
	}
	if ci != nil {
		out["status"] = ci.State.Status
		out["health"] = ci.State.Health.Status
		out["ready"] = ci.State.Running && ci.State.Health.Status == "healthy" && vi != nil
		if out["ready"] == true {
			out["nextStep"] = "使用创建时的密码连接；连接信息不包含密码。"
		}
	}
	return out, nil
}
func (s *Service) TemplateStatus(ctx context.Context, name string) (any, *apperr.Error) {
	if !templateName.MatchString(name) {
		return nil, apperr.InvalidParams("环境名称无效", "请从模板中心选择环境", "")
	}
	b, err := os.ReadFile(templatePath(name))
	if err != nil {
		return nil, apperr.NotFound("环境记录不存在", "刷新模板中心", "")
	}
	var r templateRecord
	if json.Unmarshal(b, &r) != nil || r.Name != name || r.Token == "" {
		return nil, templateFailure("环境记录不完整")
	}
	return s.templateStatus(ctx, r)
}
func (s *Service) TemplateCreate(ctx context.Context, emitter *dbxpluginsdk.Emitter, p TemplateCreateParams) (any, *apperr.Error) {
	plan := TemplatePlanParams{p.Kind, p.Name, p.Port}
	spec, e := templateValidate(plan)
	if e != nil {
		return nil, e
	}
	if len(p.Password) < 12 || len(p.Password) > 128 || strings.ContainsAny(p.Password, "\r\n\x00") {
		return nil, apperr.InvalidParams("密码必须为 12–128 字节且不含换行或空字符", "密码不会写入环境记录或任务输出", "")
	}
	if b, err := os.ReadFile(templatePath(p.Name)); err == nil {
		var r templateRecord
		if json.Unmarshal(b, &r) != nil || r.TemplatePlanParams != plan || r.Token == "" {
			return nil, apperr.Conflict("同名环境配置冲突", "使用新名称，或查询原环境状态", "")
		}
		out, e := s.templateStatus(ctx, r)
		if e != nil {
			return out, e
		}
		if out["ready"] != true {
			return out, apperr.Conflict("同名环境尚未就绪", "查询状态并检查已有资源；不会再次创建或修改密码", "")
		}
		out["reused"] = true
		return out, nil
	}
	c, v := templateNames(p.Name)
	for _, pair := range [][2]string{{"container", c}, {"volume", v}} {
		found, e := s.templateInspect(ctx, pair[0], pair[1])
		if e != nil {
			return nil, e
		}
		if found != nil {
			return nil, apperr.Conflict("同名容器或数据卷已经存在", "请选择其他环境名称，不覆盖现有资源", "")
		}
	}
	listener, err := net.Listen("tcp4", fmt.Sprintf("127.0.0.1:%d", p.Port))
	if err != nil {
		return nil, apperr.Conflict("本机端口不可用", "选择其他端口；检查仅代表当前时刻", "")
	}
	listener.Close()
	if os.MkdirAll(templateDir(), 0700) != nil {
		return nil, templateFailure("无法保存环境记录")
	}
	nonce := make([]byte, 16)
	if _, err = rand.Read(nonce); err != nil {
		return nil, templateFailure("无法生成资源标识")
	}
	r := templateRecord{plan, hex.EncodeToString(nonce), time.Now().UTC().Format(time.RFC3339)}
	b, _ := json.Marshal(r)
	// Exclusive immutable reservation survives sidecar restarts and prevents blind replay.
	f, err := os.OpenFile(templatePath(p.Name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, apperr.Conflict("该环境名已被占用", "刷新状态或使用新名称", "")
	}
	_, err = f.Write(b)
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		return nil, templateFailure("保存环境记录失败")
	}
	return s.startJob("templates/create", emitter, func(ctx context.Context, job *jobs.Job) (any, *apperr.Error) {
		out := templateSummary(r)
		run := func(stage string, timeout time.Duration, args ...string) *apperr.Error {
			job.Line("stdout", stage)
			result, e := s.run(ctx, timeout, args...)
			if e != nil || result.ExitCode != 0 || result.Canceled || ctx.Err() != nil {
				return templateFailure(stage + "失败")
			}
			return nil
		}
		if e := run("拉取镜像", 10*time.Minute, "pull", spec.Image); e != nil {
			return out, e
		}
		// Check again after the pull; never attach a volume that appeared meanwhile.
		for _, pair := range [][2]string{{"container", c}, {"volume", v}} {
			found, e := s.templateInspect(ctx, pair[0], pair[1])
			if e != nil {
				return out, e
			}
			if found != nil {
				return out, apperr.Conflict("创建期间出现同名资源", "已停止创建，请检查资源归属", "")
			}
		}
		label := "com.dbx.wsl.owner=" + r.Token
		if e := run("创建数据卷", time.Minute, "volume", "create", "--label", label, v); e != nil {
			return out, e
		}
		owned, e := s.templateInspect(ctx, "volume", v)
		if e != nil {
			return out, e
		}
		if !templateOwned(owned, r.Token) {
			return out, templateFailure("数据卷归属校验失败")
		}
		// Never pass the password as a Windows process argument or emit raw command output.
		secret, err := os.CreateTemp(templateDir(), ".env-*")
		if err != nil {
			return out, templateFailure("创建临时凭据失败")
		}
		defer os.Remove(secret.Name())
		contents := "REDIS_PASSWORD=" + p.Password + "\n"
		if p.Kind == "postgres" {
			contents = "POSTGRES_USER=app\nPOSTGRES_DB=app\nPOSTGRES_PASSWORD=" + p.Password + "\n"
		}
		_, err = secret.WriteString(contents)
		closeErr := secret.Close()
		if err != nil || closeErr != nil {
			return out, templateFailure("写入临时凭据失败")
		}
		args := []string{"run", "--detach", "--name", c, "--label", label, "--publish", fmt.Sprintf("127.0.0.1:%d:%d", p.Port, spec.Port), "--volume", v + ":" + spec.Mount, "--env-file", secret.Name(), "--health-cmd", spec.Health, "--health-interval", "2s", "--health-timeout", "3s", "--health-retries", "30", spec.Image}
		if p.Kind == "redis" {
			args = append(args, "sh", "-c", `exec redis-server --appendonly yes --requirepass "$REDIS_PASSWORD"`)
		}
		if e := run("启动容器", 2*time.Minute, args...); e != nil {
			return out, e
		}
		os.Remove(secret.Name())
		p.Password = ""
		contents = ""
		job.Line("stdout", "等待容器健康检查")
		timer := time.NewTimer(90 * time.Second)
		defer timer.Stop()
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			state, e := s.templateStatus(ctx, r)
			if e != nil {
				return state, e
			}
			if state["ready"] == true {
				return state, nil
			}
			select {
			case <-ctx.Done():
				return state, templateFailure("任务已取消")
			case <-timer.C:
				return state, templateFailure("就绪检查超时")
			case <-ticker.C:
			}
		}
	})
}
