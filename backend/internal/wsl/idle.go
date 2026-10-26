package wsl

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/w89588/dbx-plugin-wsl/internal/apperr"
	"github.com/w89588/dbx-plugin-wsl/internal/execx"
	"github.com/w89588/dbx-plugin-wsl/internal/winenv"
)

// IdleWriteParams changes only the two WSL idle-policy keys. A revision is
// required to prevent overwriting edits made outside the plugin.
type IdleWriteParams struct {
	Mode             string `json:"mode"`
	ExpectedRevision string `json:"expectedRevision"`
	InstanceSeconds  *int64 `json:"instanceSeconds"`
	VMSeconds        *int64 `json:"vmSeconds"`
}

type idleDocument struct {
	path, content, revision string
	exists                  bool
}

func readIdleDocument() (idleDocument, *apperr.Error) {
	doc := idleDocument{path: filepath.Join(winenv.HomeDir(), ".wslconfig")}
	raw, err := os.ReadFile(doc.path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return doc, apperr.Internal("读取 WSL 配置失败", err.Error())
	}
	if len(raw) > maxConfigBytes {
		return doc, apperr.InvalidParams("配置内容超过 256 KiB 上限", "请先精简 .wslconfig", "")
	}
	doc.exists = err == nil
	doc.content = execx.DecodeText(raw)
	if bytes.HasPrefix(raw, []byte{0xef, 0xbb, 0xbf}) {
		doc.content = "\ufeff" + doc.content
	}
	// Include existence so deleting/creating an empty file also changes revision.
	hash := sha256.Sum256(append([]byte(strconv.FormatBool(doc.exists)+":"), raw...))
	doc.revision = hex.EncodeToString(hash[:])
	return doc, nil
}

func idleLine(line, section string) (next, key, value string, valid bool) {
	t := strings.TrimSpace(strings.TrimPrefix(line, "\ufeff"))
	if t == "" || strings.HasPrefix(t, "#") || strings.HasPrefix(t, ";") {
		return section, "", "", true
	}
	if strings.HasPrefix(t, "[") {
		end := strings.IndexByte(t, ']')
		if end < 2 {
			return section, "", "", false
		}
		tail := strings.TrimSpace(t[end+1:])
		return strings.ToLower(strings.TrimSpace(t[1:end])), "", "", tail == "" || strings.HasPrefix(tail, "#") || strings.HasPrefix(tail, ";")
	}
	parts := strings.SplitN(t, "=", 2)
	if len(parts) != 2 || section == "" {
		return section, "", "", false
	}
	value = parts[1]
	if at := strings.IndexAny(value, "#;"); at >= 0 {
		value = value[:at]
	}
	return section, strings.ToLower(strings.TrimSpace(parts[0])), strings.TrimSpace(value), true
}

func (doc idleDocument) state() map[string]any {
	instance, vm := int64(15000), int64(60000)
	section, count := "", 0
	valid := true
	seen := map[string]bool{}
	for _, line := range strings.Split(doc.content, "\n") {
		next, key, value, ok := idleLine(line, section)
		section = next
		valid = valid && ok
		if (section == "general" && key == "instanceidletimeout") || (section == "wsl2" && key == "vmidletimeout") {
			count++
			id := section + "." + key
			if seen[id] {
				valid = false
			}
			seen[id] = true
			n, err := strconv.ParseInt(value, 10, 64)
			if err != nil || n < -1 || n > 2147483647 {
				valid = false
				continue
			}
			if section == "general" {
				instance = n
			} else {
				vm = n
			}
		}
	}
	mode := "custom"
	if count == 0 {
		mode = "default"
	}
	if instance == -1 {
		mode = "keepAlive"
	}
	if !valid {
		mode = "custom"
	}
	return map[string]any{"path": doc.path, "revision": doc.revision, "mode": mode, "valid": valid,
		"instanceIdleTimeoutMs": instance, "vmIdleTimeoutMs": vm}
}

// IdleRead never launches WSL, so viewing the policy cannot keep a distro alive.
func (s *Service) IdleRead() (any, *apperr.Error) {
	configMu.Lock()
	defer configMu.Unlock()
	doc, appErr := readIdleDocument()
	if appErr != nil {
		return nil, appErr
	}
	return doc.state(), nil
}

// patchIdleKey preserves unrelated lines, comments, and the file's newline style.
// All occurrences of the target key are updated/removed to avoid stale duplicates.
func patchIdleKey(content, targetSection, targetKey string, value *string) (string, *apperr.Error) {
	bom := ""
	if strings.HasPrefix(content, "\ufeff") {
		bom = "\ufeff"
		content = strings.TrimPrefix(content, bom)
	}
	newline := "\n"
	if strings.Contains(content, "\r\n") {
		newline = "\r\n"
	}
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	section, insertAt, found := "", -1, false
	output := make([]string, 0, len(lines)+3)
	for _, line := range lines {
		next, key, _, valid := idleLine(line, section)
		if !valid {
			return "", apperr.InvalidParams(".wslconfig 格式异常", "请先在配置编辑器中修复格式再保存空闲策略", "")
		}
		section = next
		if section == targetSection && key == "" && strings.HasPrefix(strings.TrimSpace(line), "[") {
			insertAt = len(output) + 1
		}
		if section == targetSection && key == strings.ToLower(targetKey) {
			found = true
			if value == nil {
				continue
			}
			// Keep indentation, key spelling and any trailing inline comment.
			eq := strings.IndexByte(line, '=')
			suffix := ""
			if at := strings.IndexAny(line[eq+1:], "#;"); at >= 0 {
				suffix = " " + strings.TrimSpace(line[eq+1+at:])
			}
			line = line[:eq+1] + *value + suffix
		}
		output = append(output, line)
	}
	if !found && value != nil {
		entry := targetKey + "=" + *value
		if insertAt >= 0 {
			output = append(output, "")
			copy(output[insertAt+1:], output[insertAt:])
			output[insertAt] = entry
		} else {
			if len(output) == 1 && output[0] == "" {
				output = nil
			}
			output = append(output, "["+targetSection+"]", entry, "")
		}
	}
	return bom + strings.Join(output, newline), nil
}

func (s *Service) IdleWrite(params IdleWriteParams) (any, *apperr.Error) {
	var instance, vm *string
	changeVM := true
	switch params.Mode {
	case "keepAlive":
		value := "-1"
		instance, changeVM = &value, false // Distro keep-alive does not need a VM timeout change.
	case "default": // Removing the overrides lets WSL supply its defaults.
	case "custom":
		if params.InstanceSeconds == nil || params.VMSeconds == nil ||
			(*params.InstanceSeconds != -1 && (*params.InstanceSeconds < 1 || *params.InstanceSeconds > 2147483)) ||
			*params.VMSeconds < 1 || *params.VMSeconds > 2147483 {
			return nil, apperr.InvalidParams("空闲超时无效", "发行版可填 -1（永不退出）或 1–2147483 秒；虚拟机可填 1–2147483 秒", "")
		}
		i := "-1"
		if *params.InstanceSeconds != -1 {
			i = strconv.FormatInt(*params.InstanceSeconds*1000, 10)
		}
		v := strconv.FormatInt(*params.VMSeconds*1000, 10)
		instance, vm = &i, &v
	default:
		return nil, apperr.InvalidParams("未知空闲策略", "请选择 keepAlive、default 或 custom", "")
	}
	configMu.Lock()
	defer configMu.Unlock()
	doc, appErr := readIdleDocument()
	if appErr != nil {
		return nil, appErr
	}
	if params.ExpectedRevision == "" || params.ExpectedRevision != doc.revision {
		return nil, apperr.Conflict("WSL 配置已更改", "请重新读取配置后再保存，以免覆盖其他修改", "")
	}
	updated, appErr := patchIdleKey(doc.content, "general", "instanceIdleTimeout", instance)
	if appErr == nil && changeVM {
		updated, appErr = patchIdleKey(updated, "wsl2", "vmIdleTimeout", vm)
	}
	if appErr != nil {
		return nil, appErr
	}
	result := map[string]any{"path": doc.path, "backupPath": nil, "changed": updated != doc.content, "restartRequired": updated != doc.content}
	if updated != doc.content {
		result, appErr = writeConfigFile(doc.path, updated)
		if appErr != nil {
			return nil, appErr
		}
		result["changed"], result["restartRequired"] = true, true
	}
	doc, appErr = readIdleDocument()
	if appErr != nil {
		return nil, appErr
	}
	result["policy"] = doc.state()
	return result, nil
}
