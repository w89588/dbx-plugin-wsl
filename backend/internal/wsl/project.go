package wsl

import (
	"os/exec"
	"runtime"

	"github.com/w89588/dbx-plugin-wsl/internal/apperr"
	"github.com/w89588/dbx-plugin-wsl/internal/winenv"
)

const projectURL = "https://github.com/w89588/dbx-plugin-wsl"

// ProjectLink accepts fixed destinations, never arbitrary URLs or shell input.
func ProjectLink(target string) (string, *apperr.Error) {
	switch target {
	case "repository":
		return projectURL, nil
	case "issues":
		return projectURL + "/issues", nil
	case "bug":
		return projectURL + "/issues/new?template=bug_report.yml", nil
	case "feature":
		return projectURL + "/issues/new?template=feature_request.yml", nil
	case "wslDocs":
		return "https://learn.microsoft.com/en-us/windows/wsl/wsl-config", nil
	default:
		return "", apperr.InvalidParams("未知项目链接", "请选择仓库、Issue 或 WSL 配置文档", "")
	}
}

func (s *Service) OpenProjectLink(target string) (any, *apperr.Error) {
	url, appErr := ProjectLink(target)
	if appErr != nil {
		return nil, appErr
	}
	if runtime.GOOS != "windows" {
		return nil, apperr.Unsupported("仅支持 Windows", "请在浏览器中打开项目链接", "")
	}
	path, err := exec.LookPath("explorer.exe")
	if err == nil {
		err = winenv.StartDetached(path, url)
	}
	if err != nil {
		return nil, apperr.Internal("打开浏览器失败", err.Error())
	}
	return map[string]any{"opened": true, "url": url}, nil
}
