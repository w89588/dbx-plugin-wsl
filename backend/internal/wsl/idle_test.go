package wsl

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIdlePolicyLifecycle(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("USERPROFILE", dir)
	path := filepath.Join(dir, ".wslconfig")
	original := "# user preferences\r\n[wsl2]\r\nmemory=8GB\r\nvmIdleTimeout=90000\r\n[experimental]\r\nautoMemoryReclaim=gradual\r\n"
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	service := &Service{} // A nil executor proves policy reads/writes never launch WSL.
	read, err := service.IdleRead()
	if err != nil {
		t.Fatal(err)
	}
	before := read.(map[string]any)
	if before["instanceIdleTimeoutMs"] != int64(15000) || before["vmIdleTimeoutMs"] != int64(90000) {
		t.Fatal(before)
	}
	value, err := service.IdleWrite(IdleWriteParams{Mode: "keepAlive", ExpectedRevision: before["revision"].(string)})
	if err != nil {
		t.Fatal(err)
	}
	result := value.(map[string]any)
	backup, readErr := os.ReadFile(result["backupPath"].(string))
	if readErr != nil || string(backup) != original {
		t.Fatalf("backup mismatch: %v", readErr)
	}
	content, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(content), original) || !strings.Contains(string(content), "[general]\r\ninstanceIdleTimeout=-1\r\n") {
		t.Fatalf("unrelated config changed: %q", content)
	}
	policy := result["policy"].(map[string]any)
	if policy["mode"] != "keepAlive" || policy["vmIdleTimeoutMs"] != int64(90000) || result["restartRequired"] != true {
		t.Fatal(result)
	}
	// Repeating an already-saved policy does not rewrite it or create a backup.
	value, err = service.IdleWrite(IdleWriteParams{Mode: "keepAlive", ExpectedRevision: policy["revision"].(string)})
	if err != nil || value.(map[string]any)["changed"] != false {
		t.Fatalf("no-op: %v %v", value, err)
	}
	// Another application's edit must be preserved even when the UI is stale.
	changed := string(content) + "# external change\r\n"
	if e := os.WriteFile(path, []byte(changed), 0600); e != nil {
		t.Fatal(e)
	}
	_, err = service.IdleWrite(IdleWriteParams{Mode: "default", ExpectedRevision: policy["revision"].(string)})
	if err == nil || err.Code != "CONFLICT" {
		t.Fatalf("expected conflict: %v", err)
	}
	content, _ = os.ReadFile(path)
	if string(content) != changed {
		t.Fatal("stale request modified config")
	}
	read, _ = service.IdleRead()
	value, err = service.IdleWrite(IdleWriteParams{Mode: "default", ExpectedRevision: read.(map[string]any)["revision"].(string)})
	if err != nil {
		t.Fatal(err)
	}
	content, _ = os.ReadFile(path)
	if strings.Contains(string(content), "IdleTimeout=") || !strings.Contains(string(content), "memory=8GB\r\n") || !strings.Contains(string(content), "# external change") {
		t.Fatalf("default restore: %q", content)
	}
	if value.(map[string]any)["policy"].(map[string]any)["mode"] != "default" {
		t.Fatal(value)
	}
}

func TestIdleCustomAndValidation(t *testing.T) {
	t.Setenv("USERPROFILE", t.TempDir())
	service := &Service{}
	i, v := int64(-1), int64(120)
	read, err := service.IdleRead()
	if err != nil {
		t.Fatal(err)
	}
	revision := read.(map[string]any)["revision"].(string)
	for _, request := range []IdleWriteParams{
		{Mode: "other"}, {Mode: "custom"}, {Mode: "keepAlive"},
		{Mode: "custom", ExpectedRevision: revision, InstanceSeconds: &i, VMSeconds: new(int64)},
	} {
		if _, err := service.IdleWrite(request); err == nil {
			t.Fatalf("accepted %#v", request)
		}
	}
	value, err := service.IdleWrite(IdleWriteParams{Mode: "custom", ExpectedRevision: revision, InstanceSeconds: &i, VMSeconds: &v})
	if err != nil {
		t.Fatal(err)
	}
	result := value.(map[string]any)
	if result["backupPath"] != nil || result["policy"].(map[string]any)["vmIdleTimeoutMs"] != int64(120000) {
		t.Fatal(result)
	}
	read, _ = service.IdleRead()
	i, v = 2147484, 60
	if _, err := service.IdleWrite(IdleWriteParams{Mode: "custom", ExpectedRevision: read.(map[string]any)["revision"].(string), InstanceSeconds: &i, VMSeconds: &v}); err == nil {
		t.Fatal("overflow accepted")
	}
}

func TestIdleINIHandling(t *testing.T) {
	never := "-1"
	for _, input := range []string{
		"", "# comment\n[wsl2]\nmemory=4GB", "\ufeff[general]\ninstanceIdleTimeout= \n",
		"[General] ; comment\n  INSTANCEIDLETIMEOUT = 15000 # reason\ninstanceIdleTimeout=45000\n[custom]\nkey=value\n",
	} {
		output, err := patchIdleKey(input, "general", "instanceIdleTimeout", &never)
		if err != nil {
			t.Fatalf("%q: %v", input, err)
		}
		if !strings.Contains(strings.ToLower(output), "instanceidletimeout") || !strings.Contains(output, "=-1") {
			t.Fatal(output)
		}
		if strings.Contains(input, "# reason") && !strings.Contains(output, "# reason") {
			t.Fatal("lost comment")
		}
	}
	for _, input := range []string{"[general\ninstanceIdleTimeout=15", "no section", "[general] garbage\na=1"} {
		if _, err := patchIdleKey(input, "general", "instanceIdleTimeout", &never); err == nil {
			t.Fatalf("malformed config accepted: %q", input)
		}
	}
	for _, value := range []string{"", "bad", "9999999999999999999999", "-2"} {
		state := (idleDocument{content: "[general]\ninstanceIdleTimeout=" + value}).state()
		if state["valid"] != false {
			t.Fatal(state)
		}
	}
}

func TestMalformedIdleConfigIsNotWritten(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("USERPROFILE", dir)
	path := filepath.Join(dir, ".wslconfig")
	original := "[wsl2\nmemory=8GB"
	os.WriteFile(path, []byte(original), 0600)
	service := &Service{}
	read, _ := service.IdleRead()
	_, err := service.IdleWrite(IdleWriteParams{Mode: "keepAlive", ExpectedRevision: read.(map[string]any)["revision"].(string)})
	if err == nil {
		t.Fatal("expected error")
	}
	content, _ := os.ReadFile(path)
	if string(content) != original {
		t.Fatal("changed malformed config")
	}
	backups, _ := filepath.Glob(path + ".bak-*")
	if len(backups) != 0 {
		t.Fatal("created backup despite rejected request")
	}
}

func TestProjectLinks(t *testing.T) {
	for target, suffix := range map[string]string{"repository": "", "issues": "/issues", "bug": "/issues/new?template=bug_report.yml", "feature": "/issues/new?template=feature_request.yml"} {
		url, err := ProjectLink(target)
		if err != nil || url != projectURL+suffix {
			t.Fatalf("%s: %s %v", target, url, err)
		}
	}
	for _, target := range []string{"", "https://example.com", "file:///C:/", "issues & calc.exe"} {
		if _, err := ProjectLink(target); err == nil {
			t.Fatalf("accepted arbitrary destination: %s", target)
		}
	}
}
