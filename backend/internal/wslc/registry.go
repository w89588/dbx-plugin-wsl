package wslc

import (
	"context"
	"strings"
	"time"
	"unicode"

	"github.com/w89588/dbx-plugin-wsl/internal/apperr"
	"github.com/w89588/dbx-plugin-wsl/internal/execx"
)

const (
	// loginTimeout / logoutTimeout follow the contract (login 60s).
	loginTimeout  = 60 * time.Second
	logoutTimeout = 60 * time.Second

	maxRegistryServerLength   = 255
	maxRegistryUsernameLength = 256
	maxRegistryPasswordLength = 4096

	// defaultRegistryDisplay is reported when the caller omits `server`; the
	// parameter is then not passed to wslc so its session default applies.
	defaultRegistryDisplay = "docker.io"
)

// LoginParams is the request of wslc/registry/login. Password is a secret: it
// is only ever written to the child's stdin and never logged or echoed.
type LoginParams struct {
	Server   string `json:"server"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// RegistryLogin implements wslc/registry/login.
func (s *Service) RegistryLogin(ctx context.Context, params LoginParams) (any, *apperr.Error) {
	server, appErr := validateRegistryServer(params.Server)
	if appErr != nil {
		return nil, appErr
	}
	username, appErr := validateRegistryUsername(params.Username)
	if appErr != nil {
		return nil, appErr
	}
	if appErr := validateRegistryPassword(params.Password); appErr != nil {
		return nil, appErr
	}

	args := []string{"login"}
	if server != "" {
		args = append(args, server)
	}
	// Password goes through stdin only (contract 4.3 / 6.7); never -p / argv.
	args = append(args, "-u", username, "--password-stdin")
	result, err := s.Ex.Run(ctx, execx.Request{
		Tool:    execx.ToolWSLc,
		Args:    args,
		Timeout: loginTimeout,
		Stdin:   []byte(params.Password + "\n"),
	})
	if err != nil {
		return nil, execx.ToAppError(err)
	}
	if result.TimedOut {
		return nil, apperr.Timeout("注册表登录超时", sanitizeSecret(result.Stderr, params.Password))
	}
	if result.ExitCode != 0 {
		detail := sanitizeSecret(registryDetail(result.Stdout, result.Stderr), params.Password)
		if apperr.LooksRegistryAuthFailed(detail) {
			return nil, apperr.RegistryAuthFailed(detail)
		}
		return nil, apperr.ProcessFailed(
			"注册表登录失败",
			"请检查网络、注册表地址与凭据后重试",
			detail,
		)
	}

	output := sanitizeSecret(strings.TrimSpace(outputText(result)), params.Password)
	return map[string]any{
		"server":   displayServer(server),
		"username": username,
		"loggedIn": true,
		"output":   output,
	}, nil
}

// LogoutParams is the request of wslc/registry/logout.
type LogoutParams struct {
	Server string `json:"server"`
}

// RegistryLogout implements wslc/registry/logout. It is idempotent: logging out
// of a registry that has no stored credentials still reports loggedOut:true.
func (s *Service) RegistryLogout(ctx context.Context, params LogoutParams) (any, *apperr.Error) {
	server, appErr := validateRegistryServer(params.Server)
	if appErr != nil {
		return nil, appErr
	}
	args := []string{"logout"}
	if server != "" {
		args = append(args, server)
	}
	result, err := s.Ex.Run(ctx, execx.Request{
		Tool:    execx.ToolWSLc,
		Args:    args,
		Timeout: logoutTimeout,
	})
	if err != nil {
		return nil, execx.ToAppError(err)
	}
	if result.TimedOut {
		return nil, apperr.Timeout("注销注册表超时", result.Stderr)
	}
	detail := sanitizeSecret(registryDetail(result.Stdout, result.Stderr))
	if result.ExitCode != 0 {
		if apperr.LooksNotLoggedIn(detail) {
			// Idempotent logout: wslc exits 1 with "未登录到 <server>".
			return map[string]any{
				"server":    displayServer(server),
				"loggedOut": true,
				"output":    detail,
			}, nil
		}
		return nil, failure("注销注册表失败", result.Stdout, result.Stderr)
	}
	return map[string]any{
		"server":    displayServer(server),
		"loggedOut": true,
		"output":    sanitizeSecret(strings.TrimSpace(outputText(result))),
	}, nil
}

func displayServer(server string) string {
	if server == "" {
		return defaultRegistryDisplay
	}
	return server
}

func validateRegistryServer(value string) (string, *apperr.Error) {
	server := strings.TrimSpace(value)
	if server == "" {
		return "", nil
	}
	if len(server) > maxRegistryServerLength {
		return "", apperr.InvalidParams("server 过长", "server 最长 255 个字符", "")
	}
	if strings.HasPrefix(server, "-") {
		return "", apperr.InvalidParams("server 不合法", "server 不能以 - 开头", "")
	}
	for _, character := range server {
		if unicode.IsSpace(character) || unicode.IsControl(character) {
			return "", apperr.InvalidParams(
				"server 不能包含空白或控制字符",
				"例如 docker.io、ghcr.io、registry.example.com:5000",
				"",
			)
		}
	}
	return server, nil
}

func validateRegistryUsername(value string) (string, *apperr.Error) {
	username := strings.TrimSpace(value)
	if username == "" {
		return "", apperr.InvalidParams("username 不能为空", "请提供注册表用户名或账号", "")
	}
	if len(username) > maxRegistryUsernameLength {
		return "", apperr.InvalidParams("username 过长", "username 最长 256 个字符", "")
	}
	if strings.HasPrefix(username, "-") {
		return "", apperr.InvalidParams("username 不合法", "username 不能以 - 开头", "")
	}
	for _, character := range username {
		if unicode.IsSpace(character) || unicode.IsControl(character) {
			return "", apperr.InvalidParams("username 不能包含空白或控制字符", "请检查用户名/账号", "")
		}
	}
	return username, nil
}

func validateRegistryPassword(value string) *apperr.Error {
	if value == "" {
		return apperr.InvalidParams("password 不能为空", "请提供注册表密码或个人访问令牌", "")
	}
	if len(value) > maxRegistryPasswordLength {
		return apperr.InvalidParams("password 过长", "password 最长 4096 个字符", "")
	}
	if strings.ContainsAny(value, "\r\n\x00") {
		return apperr.InvalidParams("password 不能包含换行或 NUL 字符", "请检查密码/访问令牌", "")
	}
	return nil
}

// classifyRegistryFailure maps pull/build failures to the contract table. Auth
// keywords win over WSLC_E_IMAGE_NOT_FOUND; everything else keeps the generic
// failure classification (CONFLICT / PROCESS_FAILED / ...).
func classifyRegistryFailure(operation, stdout, stderr string) *apperr.Error {
	combined := stdout + "\n" + stderr
	detail := registryDetail(stdout, stderr)
	switch {
	case apperr.LooksRegistryAuthRequired(combined):
		return apperr.RegistryAuthRequired(detail)
	case apperr.LooksRegistryRateLimited(combined):
		return apperr.RegistryRateLimited(detail)
	case apperr.LooksImageNotFound(combined):
		return apperr.NotFound(
			"镜像不存在或名称拼写错误",
			"请检查镜像名称与标签，或到「镜像 → 注册表凭据」登录后重试",
			detail,
		)
	default:
		return failure(operation, stdout, stderr)
	}
}

// registryDetail returns the raw failure text for `detail` (stderr preferred).
func registryDetail(stdout, stderr string) string {
	detail := strings.TrimSpace(stderr)
	if detail == "" {
		detail = strings.TrimSpace(stdout)
	}
	return detail
}

// sanitizeSecret removes a secret value from user visible text. Credentials
// must never appear in RPC errors, job events or logs (contract 6.7).
func sanitizeSecret(value string, secrets ...string) string {
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		value = strings.ReplaceAll(value, secret, "***")
	}
	return value
}
