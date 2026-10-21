// Package apperr defines the structured backend errors described by
// docs/rpc-contract.md section 2. Every failure returned by a service is an
// *apperr.Error carrying a machine readable code, a user facing message, a
// hint and raw diagnostic detail. The RPC handler converts it into the JSON-RPC
// error envelope with data={code,hint,detail}.
package apperr

import (
	"fmt"
	"strings"
)

// Error codes defined by the contract.
const (
	CodeToolNotFound  = "TOOL_NOT_FOUND"
	CodeInvalidParams = "INVALID_PARAMS"
	CodeNotFound      = "NOT_FOUND"
	CodeConflict      = "CONFLICT"
	CodeProcessFailed = "PROCESS_FAILED"
	CodeTimeout       = "TIMEOUT"
	CodeUnsupported   = "UNSUPPORTED"
	CodeInternal      = "INTERNAL"

	// Registry credential errors (contract section 2 / 4.3).
	CodeRegistryAuthRequired = "REGISTRY_AUTH_REQUIRED"
	CodeRegistryRateLimited  = "REGISTRY_RATE_LIMITED"
	CodeRegistryAuthFailed   = "REGISTRY_AUTH_FAILED"
)

// Registry hints mandated by the contract.
const (
	HintRegistryAuthRequired = "该镜像需要登录注册表；请到「镜像 → 注册表凭据」登录后重试"
	HintRegistryRateLimited  = "Docker Hub 匿名拉取触发速率限制；登录（免费账号）可提高限额，或稍后重试"
	HintRegistryAuthFailed   = "请检查用户名/密码或访问令牌是否正确，以及该账号是否有访问权限"
	HintRegistryPullFailed   = "查看任务日志中的原始错误"
)

// DetailLimit caps the raw output carried in detail so a single failure cannot
// flood the JSON-RPC stream.
const DetailLimit = 16 * 1024

// Error is a structured plugin error.
type Error struct {
	Code    string
	Message string
	Hint    string
	Detail  string
}

func (e *Error) Error() string { return e.Message }

// New builds an error with the provided fields.
func New(code, message, hint, detail string) *Error {
	return &Error{Code: code, Message: message, Hint: hint, Detail: Truncate(detail)}
}

// ToolNotFound reports that a required executable could not be resolved.
func ToolNotFound(tool string, candidates []string) *Error {
	detail := ""
	if len(candidates) > 0 {
		detail = "已尝试以下路径:\n" + strings.Join(candidates, "\n")
	}
	return &Error{
		Code:    CodeToolNotFound,
		Message: fmt.Sprintf("%s 未安装或不可用", tool),
		Hint:    fmt.Sprintf("请安装 WSL 3.0+ 或检查 %s 的安装路径", tool),
		Detail:  detail,
	}
}

// InvalidParams reports invalid request parameters.
func InvalidParams(message, hint, detail string) *Error {
	return New(CodeInvalidParams, message, hint, detail)
}

// NotFound reports a missing container/image/volume/network/distribution.
func NotFound(message, hint, detail string) *Error {
	return New(CodeNotFound, message, hint, detail)
}

// Conflict reports a state conflict such as removing a running container.
func Conflict(message, hint, detail string) *Error {
	return New(CodeConflict, message, hint, detail)
}

// ProcessFailed reports a non-zero child process exit that is a business error.
func ProcessFailed(message, hint, detail string) *Error {
	return New(CodeProcessFailed, message, hint, detail)
}

// Timeout reports that a synchronous operation exceeded its deadline.
func Timeout(message, detail string) *Error {
	return New(CodeTimeout, message, "操作超时，可稍后重试；长操作请使用任务（job）", detail)
}

// Unsupported reports a capability missing from the installed WSL/wslc.
func Unsupported(message, hint, detail string) *Error {
	return New(CodeUnsupported, message, hint, detail)
}

// Internal is the catch-all error.
func Internal(message, detail string) *Error {
	return New(CodeInternal, message, "", detail)
}

// RegistryAuthRequired reports that pulling/building needs a registry login.
func RegistryAuthRequired(detail string) *Error {
	return New(CodeRegistryAuthRequired, "拉取镜像需要注册表登录", HintRegistryAuthRequired, detail)
}

// RegistryRateLimited reports a Docker Hub anonymous pull rate limit.
func RegistryRateLimited(detail string) *Error {
	return New(CodeRegistryRateLimited, "镜像拉取被限流", HintRegistryRateLimited, detail)
}

// RegistryAuthFailed reports a failed registry login (bad credentials, denied).
func RegistryAuthFailed(detail string) *Error {
	return New(CodeRegistryAuthFailed, "注册表登录失败", HintRegistryAuthFailed, detail)
}

// RegistryPullFailed is the fallback for a failed pull/build.
func RegistryPullFailed(message, detail string) *Error {
	return New(CodeProcessFailed, message, HintRegistryPullFailed, detail)
}

// Truncate shortens detail to the contract limit.
func Truncate(value string) string {
	if len(value) <= DetailLimit {
		return value
	}
	return value[:DetailLimit] + "\n…(已截断)"
}

// ContainsAny reports whether the lower-cased text contains one of the
// fragments. Fragments are expected to be lower case.
func ContainsAny(text string, fragments ...string) bool {
	lowered := strings.ToLower(text)
	for _, fragment := range fragments {
		if strings.Contains(lowered, fragment) {
			return true
		}
	}
	return false
}

// LooksNotFound classifies stderr text as a missing-object failure. It covers
// both the localized Chinese messages and English equivalents.
func LooksNotFound(text string) bool {
	return ContainsAny(text,
		"找不到对象", "找不到容器", "找不到镜像", "找不到映像", "找不到卷", "找不到网络", "找不到",
		"not found", "no such", "不存在", "does not exist", "no container", "no image", "no volume", "no network",
		"wslc_e_container_not_found", "wslc_e_image_not_found", "wslc_e_volume_not_found", "wslc_e_network_not_found",
		"wsl_e_distro_not_found",
	)
}

// LooksConflict classifies stderr text as a state conflict. Deleting a running
// container, removing a used image/volume/network all land here.
func LooksConflict(text string) bool {
	return ContainsAny(text,
		"正在运行", "运行中", "is running", "running container", "in use", "正在使用", "正在使用中",
		"sharing_violation", "conflict", "依赖", "已存在",
	)
}

// LooksSharingViolation reports a VHD/disk still held open by a running WSL VM.
func LooksSharingViolation(text string) bool {
	return ContainsAny(text, "sharing_violation", "vhd 当前正在使用", "vhd is currently in use", "正在使用中")
}

// LooksUnsupported classifies stderr text as an unsupported capability.
func LooksUnsupported(text string) bool {
	return ContainsAny(text,
		"无效的命令行参数", "invalid command line argument", "unrecognized option", "unknown option", "unknown flag",
		"not supported", "不支持",
	)
}

// LooksPermission classifies stderr text as an elevation/permission problem.
func LooksPermission(text string) bool {
	return ContainsAny(text,
		"管理员", "administrator", "elevat", "access denied", "拒绝访问", "权限",
	)
}

// LooksRegistryAuthRequired classifies pull/build errors that need a registry
// login. Auth keywords take precedence over WSLC_E_IMAGE_NOT_FOUND (contract
// section 4.3 table).
func LooksRegistryAuthRequired(text string) bool {
	return ContainsAny(text,
		"docker login", "may require 'docker login'", "pull access denied",
		"denied: requested access", "requested access to the resource is denied",
		"authentication required", "unauthorized", "no basic auth credentials",
		"401 unauthorized",
	)
}

// LooksRegistryRateLimited classifies Docker Hub anonymous pull rate limits.
func LooksRegistryRateLimited(text string) bool {
	return ContainsAny(text, "toomanyrequests", "rate limit", "pull rate limit", "too many requests")
}

// LooksRegistryAuthFailed classifies registry login failures.
func LooksRegistryAuthFailed(text string) bool {
	return ContainsAny(text,
		"unauthorized", "incorrect username or password", "invalid username or password",
		"authentication failed", "no basic auth credentials", "login failed",
		"denied", "forbidden", "401", "403",
	)
}

// LooksImageNotFound classifies a missing image.
func LooksImageNotFound(text string) bool {
	return ContainsAny(text, "wslc_e_image_not_found", "image not found", "找不到映像", "找不到镜像")
}

// LooksNotLoggedIn classifies the idempotent "logout when not logged in" case.
func LooksNotLoggedIn(text string) bool {
	return ContainsAny(text, "未登录到", "not logged in", "no credentials", "not currently logged in")
}
