package wsl

import (
	"context"
	"fmt"
	"github.com/w89588/dbx-plugin-wsl/internal/apperr"
	"runtime"
	"strconv"
	"strings"
)

type ResourceConfigParams struct {
	ExpectedRevision string `json:"expectedRevision"`
	MemoryMB         *int64 `json:"memoryMB"`
	Processors       *int   `json:"processors"`
	SwapMB           *int64 `json:"swapMB"`
}

func resourceConfig(content string, p ResourceConfigParams) (string, *apperr.Error) {
	if p.MemoryMB != nil && (*p.MemoryMB < 256 || *p.MemoryMB > 1048576) {
		return "", apperr.InvalidParams("内存须为 256–1048576 MiB", "留空恢复 WSL 默认值", "")
	}
	if p.SwapMB != nil && (*p.SwapMB < 0 || *p.SwapMB > 1048576) {
		return "", apperr.InvalidParams("交换空间须为 0–1048576 MiB", "0 表示禁用，留空恢复默认", "")
	}
	if p.Processors != nil && (*p.Processors < 1 || *p.Processors > runtime.NumCPU()) {
		return "", apperr.InvalidParams("CPU 数量超出本机范围", fmt.Sprintf("填写 1–%d 或留空", runtime.NumCPU()), "")
	}
	values := map[string]*string{"memory": nil, "processors": nil, "swap": nil}
	if p.MemoryMB != nil {
		v := strconv.FormatInt(*p.MemoryMB, 10) + "MB"
		values["memory"] = &v
	}
	if p.Processors != nil {
		v := strconv.Itoa(*p.Processors)
		values["processors"] = &v
	}
	if p.SwapMB != nil {
		v := strconv.FormatInt(*p.SwapMB, 10) + "MB"
		values["swap"] = &v
	}
	for _, key := range []string{"memory", "processors", "swap"} {
		var e *apperr.Error
		content, e = patchIdleKey(content, "wsl2", key, values[key])
		if e != nil {
			return "", e
		}
	}
	return content, nil
}
func (s *Service) ResourceConfigRead() (any, *apperr.Error) {
	configMu.Lock()
	defer configMu.Unlock()
	doc, e := readIdleDocument()
	if e != nil {
		return nil, e
	}
	values := map[string]string{}
	section := ""
	for _, line := range strings.Split(doc.content, "\n") {
		next, key, value, _ := idleLine(line, section)
		section = next
		if section == "wsl2" && (key == "memory" || key == "processors" || key == "swap") {
			values[key] = value
		}
	}
	return map[string]any{"revision": doc.revision, "content": doc.content, "values": values, "maxProcessors": runtime.NumCPU()}, nil
}
func (s *Service) ResourceConfigPreview(p ResourceConfigParams) (any, *apperr.Error) {
	configMu.Lock()
	defer configMu.Unlock()
	doc, e := readIdleDocument()
	if e != nil {
		return nil, e
	}
	if p.ExpectedRevision == "" || p.ExpectedRevision != doc.revision {
		return nil, apperr.Conflict("配置已变化或缺少修订号", "重新读取后预览", "")
	}
	content, e := resourceConfig(doc.content, p)
	if e != nil {
		return nil, e
	}
	return map[string]any{"before": doc.content, "after": content, "revision": doc.revision, "changed": content != doc.content, "restartRequired": content != doc.content, "scope": "当前 Windows 用户的全部 WSL 2 发行版"}, nil
}
func (s *Service) ResourceConfigApply(ctx context.Context, p ResourceConfigParams) (any, *apperr.Error) {
	preview, e := s.ResourceConfigPreview(p)
	if e != nil {
		return nil, e
	}
	content := preview.(map[string]any)["after"].(string)
	result, e := s.ConfigWrite(ctx, ConfigWriteParams{Target: "wslconfig", Content: content, ExpectedRevision: p.ExpectedRevision})
	if e != nil {
		return nil, e
	}
	return map[string]any{"saved": result, "restartRequired": preview.(map[string]any)["changed"], "appliedToRunningInstances": false}, nil
}
