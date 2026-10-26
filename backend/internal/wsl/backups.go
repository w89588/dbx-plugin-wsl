package wsl

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	sdk "github.com/t8y2/dbx/plugins/sdk/go/dbx-plugin-sdk"
	"github.com/w89588/dbx-plugin-wsl/internal/apperr"
	"github.com/w89588/dbx-plugin-wsl/internal/execx"
	"github.com/w89588/dbx-plugin-wsl/internal/jobs"
	"github.com/w89588/dbx-plugin-wsl/internal/winenv"
)

type BackupRecord struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Path      string `json:"path"`
	SHA256    string `json:"sha256"`
	Bytes     int64  `json:"bytes"`
	CreatedAt int64  `json:"createdAt"`
	Status    string `json:"status"`
	Stage     string `json:"stage"`
	Message   string `json:"message,omitempty"`
}
type BackupCreateParams struct {
	Name             string `json:"name"`
	Directory        string `json:"directory"`
	StopBeforeBackup bool   `json:"stopBeforeBackup"`
}
type BackupIDParams struct {
	ID string `json:"id"`
}
type BackupRestoreParams struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Location string `json:"location"`
}

var backupIDPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)

func backupStore() string               { return filepath.Join(winenv.LocalAppData(), "dbx-wsl-manager", "backups") }
func backupRecordPath(id string) string { return filepath.Join(backupStore(), id+".json") }
func writeBackup(record BackupRecord) error {
	if err := os.MkdirAll(backupStore(), 0700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(backupStore(), ".record-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(raw); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), backupRecordPath(record.ID))
}
func readBackup(id string) (BackupRecord, *apperr.Error) {
	var record BackupRecord
	if !backupIDPattern.MatchString(id) {
		return record, apperr.InvalidParams("备份 ID 无效", "请从备份列表选择", "")
	}
	raw, err := os.ReadFile(backupRecordPath(id))
	if err != nil {
		return record, apperr.NotFound("找不到备份记录", "请刷新备份列表", "")
	}
	if json.Unmarshal(raw, &record) != nil || record.ID != id {
		return record, apperr.InvalidParams("备份记录损坏", "请检查备份元数据", "")
	}
	return record, nil
}
func (s *Service) BackupList() (any, *apperr.Error) {
	entries, err := os.ReadDir(backupStore())
	if err != nil && !os.IsNotExist(err) {
		return nil, apperr.Internal("读取备份记录失败", err.Error())
	}
	records := []BackupRecord{}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		r, e := readBackup(strings.TrimSuffix(entry.Name(), ".json"))
		if e == nil {
			if r.Status == "running" {
				r.Message = "操作尚未记录终态；若宿主已重启，请检查文件后重新创建备份"
			}
			records = append(records, r)
		}
	}
	sort.Slice(records, func(i, j int) bool { return records[i].CreatedAt > records[j].CreatedAt })
	return map[string]any{"backups": records, "defaultDirectory": filepath.Join(winenv.HomeDir(), "WSL Backups")}, nil
}
func hashBackup(ctx context.Context, path string) (string, int64, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", 0, err
	}
	if !info.Mode().IsRegular() {
		return "", 0, fmt.Errorf("backup must be a regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	hash := sha256.New()
	buf := make([]byte, 1024*1024)
	var size int64
	for {
		if err := ctx.Err(); err != nil {
			return "", size, err
		}
		n, err := file.Read(buf)
		if n > 0 {
			hash.Write(buf[:n])
			size += int64(n)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", size, err
		}
	}
	if size == 0 {
		return "", 0, fmt.Errorf("backup is empty")
	}
	return hex.EncodeToString(hash.Sum(nil)), size, nil
}
func (s *Service) BackupCreate(ctx context.Context, emitter *sdk.Emitter, p BackupCreateParams) (any, *apperr.Error) {
	name, e := validateName(p.Name)
	if e != nil {
		return nil, e
	}
	_, _, found, e := s.lookup(ctx, name)
	if e != nil {
		return nil, e
	}
	if !found {
		return nil, apperr.NotFound("发行版不存在", "请刷新发行版", "")
	}
	directory := winenv.ExpandPath(strings.TrimSpace(p.Directory))
	if directory == "" {
		directory = filepath.Join(winenv.HomeDir(), "WSL Backups")
	}
	if !filepath.IsAbs(directory) {
		return nil, apperr.InvalidParams("备份目录必须为绝对路径", "请选择 Windows 目录", "")
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, apperr.Internal("无法创建备份目录", err.Error())
	}
	if free, err := winenv.FreeDiskBytes(directory); err == nil && free < 512*1024*1024 {
		return nil, apperr.Conflict("备份盘可用空间不足 512 MiB", "请释放空间或更换目录", "")
	}
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return nil, apperr.Internal("创建备份标识失败", err.Error())
	}
	id := hex.EncodeToString(token[:])
	folder := filepath.Join(directory, id)
	if err := os.Mkdir(folder, 0700); err != nil {
		return nil, apperr.Internal("创建独立备份目录失败", err.Error())
	}
	r := BackupRecord{ID: id, Name: name, Path: filepath.Join(folder, "distribution.tar"), CreatedAt: time.Now().UnixMilli(), Status: "running", Stage: "preflight"}
	if err := writeBackup(r); err != nil {
		return nil, apperr.Internal("保存备份记录失败", err.Error())
	}
	return s.startJob("backups/create", emitter, func(jobCtx context.Context, job *jobs.Job) (result any, failure *apperr.Error) {
		defer func() {
			if jobCtx.Err() != nil {
				r.Status = "cancelled"
				r.Message = "备份已取消；可能保留部分文件"
			} else if failure != nil {
				r.Status = "failed"
				r.Message = failure.Message
			}
			if err := writeBackup(r); err != nil && failure == nil {
				failure = apperr.Internal("保存备份结果失败", err.Error())
			}
		}()
		listing, e := s.run(jobCtx, 30*time.Second, "--list", "--running", "--quiet")
		if e != nil {
			return nil, e
		}
		if e = s.launchFailure("检查运行状态", listing); e != nil {
			return nil, e
		}
		running := false
		for _, line := range strings.Split(listing.Stdout, "\n") {
			if strings.EqualFold(strings.TrimSpace(line), name) {
				running = true
			}
		}
		if running && !p.StopBeforeBackup {
			return nil, apperr.Conflict("请先停止发行版再备份", "或选择停止后备份；数据库建议先执行应用自身的备份", "")
		}
		if running {
			r.Stage = "stop"
			if _, e = s.DistroTerminate(jobCtx, name); e != nil {
				return nil, e
			}
		}
		r.Stage = "export"
		if err := writeBackup(r); err != nil {
			return nil, apperr.Internal("保存备份阶段失败", err.Error())
		}
		job.Line("stdout", "正在导出发行版；原发行版保留。\n")
		_, e = s.streamJob(jobCtx, job, execx.Request{Tool: execx.ToolWSL, Args: []string{"--export", name, r.Path, "--format", "tar"}}, job.Line)
		if e != nil {
			return nil, e
		}
		r.Stage = "verify"
		writeBackup(r)
		job.Line("stdout", "正在计算 SHA-256。\n")
		digest, size, err := hashBackup(jobCtx, r.Path)
		if err != nil {
			return nil, apperr.Internal("校验备份失败", err.Error())
		}
		r.SHA256 = digest
		r.Bytes = size
		r.Status = "completed"
		r.Stage = "completed"
		return r, nil
	})
}
func verifyBackup(ctx context.Context, r BackupRecord) *apperr.Error {
	if r.Status != "completed" || r.SHA256 == "" {
		return apperr.Conflict("备份尚未成功完成", "请选择完整备份", "")
	}
	hash, size, err := hashBackup(ctx, r.Path)
	if err != nil {
		return apperr.Internal("读取备份失败", err.Error())
	}
	if hash != r.SHA256 || size != r.Bytes {
		return apperr.Conflict("备份校验不通过", "文件已变化或损坏，禁止恢复", "")
	}
	return nil
}
func (s *Service) BackupVerify(emitter *sdk.Emitter, id string) (any, *apperr.Error) {
	r, e := readBackup(id)
	if e != nil {
		return nil, e
	}
	return s.startJob("backups/verify", emitter, func(ctx context.Context, j *jobs.Job) (any, *apperr.Error) {
		if e := verifyBackup(ctx, r); e != nil {
			return nil, e
		}
		return map[string]any{"id": id, "verified": true, "sha256": r.SHA256}, nil
	})
}
func (s *Service) BackupRestore(ctx context.Context, emitter *sdk.Emitter, p BackupRestoreParams) (any, *apperr.Error) {
	r, e := readBackup(p.ID)
	if e != nil {
		return nil, e
	}
	name, e := validateName(p.Name)
	if e != nil {
		return nil, e
	}
	if strings.EqualFold(name, r.Name) {
		return nil, apperr.Conflict("恢复必须使用新名称", "原发行版始终保留", "")
	}
	_, _, found, e := s.lookup(ctx, name)
	if e != nil {
		return nil, e
	}
	if found {
		return nil, apperr.Conflict("目标发行版已存在", "请选择新名称", "")
	}
	location := winenv.ExpandPath(strings.TrimSpace(p.Location))
	if !filepath.IsAbs(location) {
		return nil, apperr.InvalidParams("安装目录必须为绝对路径", "请选择尚不存在的新目录", "")
	}
	if _, err := os.Lstat(location); !os.IsNotExist(err) {
		return nil, apperr.Conflict("安装目录已存在或不可访问", "请选择尚不存在的新目录", "")
	}
	return s.startJob("backups/restore", emitter, func(jobCtx context.Context, j *jobs.Job) (any, *apperr.Error) {
		result := map[string]any{"name": name, "location": location, "sourcePreserved": true, "started": false}
		j.Line("stdout", "恢复前校验备份。\n")
		if e := verifyBackup(jobCtx, r); e != nil {
			return result, e
		}
		if err := os.MkdirAll(filepath.Dir(location), 0700); err != nil {
			return result, apperr.Internal("无法创建安装父目录", err.Error())
		}
		if err := os.Mkdir(location, 0700); err != nil {
			return result, apperr.Conflict("无法独占目标目录", "请选择新目录", err.Error())
		}
		j.Line("stdout", "正在导入新发行版。\n")
		if _, e := s.streamJob(jobCtx, j, execx.Request{Tool: execx.ToolWSL, Args: []string{"--import", name, location, r.Path, "--version", "2"}}, j.Line); e != nil {
			return result, e
		}
		j.Line("stdout", "正在验证新发行版能否启动。\n")
		check, e := s.run(jobCtx, 120*time.Second, "--distribution", name, "--exec", "/bin/sh", "-c", "true")
		if e != nil {
			return result, e
		}
		if e = s.launchFailure("恢复已导入，但启动验证失败（保留新发行版供检查）", check); e != nil {
			return result, e
		}
		result["started"] = true
		result["verified"] = true
		return result, nil
	})
}
