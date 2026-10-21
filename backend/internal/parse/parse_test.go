package parse

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join("..", "..", "testdata", name)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return string(raw)
}

func TestParseVersionChinese(t *testing.T) {
	output := "WSL 版本: 3.0.3.0\n内核版本: 6.18.54.1-1\nWSLg 版本: 1.0.79\n" +
		"MSRDC 版本: 1.2.7391\nDirect3D 版本: 1.611.1-81528511\n" +
		"DXCore 版本: 10.0.26100.1-240331-1435.ge-release\nWindows 版本: 10.0.26300.9711\n"
	version := ParseVersion(output)
	if version.WSL != "3.0.3.0" || version.Kernel != "6.18.54.1-1" || version.WSLg != "1.0.79" {
		t.Fatalf("unexpected version: %#v", version)
	}
	if version.MSRDC != "1.2.7391" || version.Direct3D != "1.611.1-81528511" {
		t.Fatalf("unexpected version: %#v", version)
	}
	if version.DXCore != "10.0.26100.1-240331-1435.ge-release" || version.Windows != "10.0.26300.9711" {
		t.Fatalf("unexpected version: %#v", version)
	}
}

func TestParseVersionEnglish(t *testing.T) {
	output := "WSL version: 2.6.1.0\nKernel version: 6.6.87.2-1\nWSLg version: 1.0.66\n" +
		"MSRDC version: 1.2.6353\nDirect3D version: 1.611.1-81528511\n" +
		"DXCore version: 10.0.26100.1-240331-1435.ge-release\nWindows version: 10.0.26100.3915\n"
	version := ParseVersion(output)
	if version.WSL != "2.6.1.0" || version.Kernel != "6.6.87.2-1" || version.Windows != "10.0.26100.3915" {
		t.Fatalf("unexpected version: %#v", version)
	}
}

func TestParseStatusChinese(t *testing.T) {
	output := "默认分发: Debian\n默认版本: 2\n当前计算机配置不支持 WSL1。\n" +
		"若要使用 WSL1，请启用“Windows Subsystem for Linux”可选组件。\n"
	status := ParseStatus(output)
	if status.DefaultDistro != "Debian" || status.DefaultVersion != 2 {
		t.Fatalf("unexpected status: %#v", status)
	}
	if status.WSL1Supported {
		t.Fatal("expected WSL1 to be unsupported")
	}
	if status.Raw != output {
		t.Fatal("raw output must be preserved")
	}
}

func TestParseStatusEnglish(t *testing.T) {
	output := "Default Distribution: Debian\nDefault Version: 2\n"
	status := ParseStatus(output)
	if status.DefaultDistro != "Debian" || status.DefaultVersion != 2 {
		t.Fatalf("unexpected status: %#v", status)
	}
	if !status.WSL1Supported {
		t.Fatal("expected WSL1 supported when no negative notice is printed")
	}
}

func TestParseDistroList(t *testing.T) {
	output := "  NAME      STATE           VERSION\n* Debian    Stopped         2\n  Ubuntu    Running         2\n"
	entries := ParseDistroList(output)
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %#v", entries)
	}
	if !entries[0].IsDefault || entries[0].Name != "Debian" || entries[0].State != "Stopped" || entries[0].Version != 2 {
		t.Fatalf("unexpected first entry: %#v", entries[0])
	}
	if entries[1].IsDefault || entries[1].Name != "Ubuntu" || entries[1].State != "Running" {
		t.Fatalf("unexpected second entry: %#v", entries[1])
	}
}

func TestParseDistroListLocalizedStates(t *testing.T) {
	output := "  NAME      STATE           VERSION\n* Debian    运行中          2\n"
	entries := ParseDistroList(output)
	if len(entries) != 1 || entries[0].State != "Running" {
		t.Fatalf("unexpected entries: %#v", entries)
	}
}

func TestParseOnlineList(t *testing.T) {
	output := "以下是可安装的有效分发的列表。\n使用“wsl.exe --install <Distro>”安装。\n\n" +
		"NAME                            FRIENDLY NAME\n" +
		"Ubuntu                          Ubuntu\n" +
		"Debian                          Debian GNU/Linux\n" +
		"AlmaLinux-8                     AlmaLinux OS 8\n"
	entries := ParseOnlineList(output)
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %#v", entries)
	}
	if entries[0].Name != "Ubuntu" || entries[0].FriendlyName != "Ubuntu" {
		t.Fatalf("unexpected first entry: %#v", entries[0])
	}
	if entries[2].Name != "AlmaLinux-8" || entries[2].FriendlyName != "AlmaLinux OS 8" {
		t.Fatalf("unexpected last entry: %#v", entries[2])
	}
}

func TestParseSessions(t *testing.T) {
	output := "ID        创建者 PID   显示名称\n1         24656     wslc-cli-iaz00\n"
	entries := ParseSessions(output)
	if len(entries) != 1 {
		t.Fatalf("expected 1 session, got %#v", entries)
	}
	if entries[0].ID != 1 || entries[0].CreatorPid != 24656 || entries[0].DisplayName != "wslc-cli-iaz00" {
		t.Fatalf("unexpected session: %#v", entries[0])
	}
}

func TestParseContainersFixture(t *testing.T) {
	containers, warnings := Containers(fixture(t, "containers.jsonl"))
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %#v", warnings)
	}
	if len(containers) != 1 {
		t.Fatalf("expected 1 container, got %d", len(containers))
	}
	container := containers[0]
	if container.ID != "70759157800b" || container.ShortID != "70759157800b" {
		t.Fatalf("unexpected id: %#v", container)
	}
	if container.Name != "dbx-test" || container.Image != "alpine:3.20" || container.State != "running" {
		t.Fatalf("unexpected container: %#v", container)
	}
	if container.Platform == nil || container.Platform.Architecture != "amd64" || container.Platform.OS != "linux" {
		t.Fatalf("unexpected platform: %#v", container.Platform)
	}
}

func TestParseContainersSkipsBrokenLines(t *testing.T) {
	output := "{\"ID\":\"abc\",\"Names\":\"demo\"}\nnot-json\n"
	containers, warnings := Containers(output)
	if len(containers) != 1 || containers[0].ShortID != "abc" {
		t.Fatalf("unexpected containers: %#v", containers)
	}
	if len(warnings) != 1 || warnings[0] != "not-json" {
		t.Fatalf("unexpected warnings: %#v", warnings)
	}
}

func TestParseStatsFixture(t *testing.T) {
	stats, warnings := Stats(fixture(t, "stats.jsonl"))
	if len(warnings) != 0 || len(stats) != 1 {
		t.Fatalf("unexpected stats: %#v warnings=%#v", stats, warnings)
	}
	row := stats[0]
	if row.ShortID != "70759157800b" || row.Name != "dbx-test" || row.CPUPerc != "0.00%" || row.PIDs != 1 {
		t.Fatalf("unexpected stats row: %#v", row)
	}
	if row.MemUsage != "1.461MiB / 31.24GiB" || row.NetIO != "1.04kB / 0B" {
		t.Fatalf("unexpected stats row: %#v", row)
	}
}

func TestParseImagesFixture(t *testing.T) {
	images, warnings := Images(fixture(t, "images.jsonl"))
	if len(warnings) != 0 || len(images) != 1 {
		t.Fatalf("unexpected images: %#v warnings=%#v", images, warnings)
	}
	image := images[0]
	if image.ID != "bf8527eb54c3" || image.Repository != "alpine" || image.Tag != "3.20" {
		t.Fatalf("unexpected image: %#v", image)
	}
	if image.CreatedAt != "2026-04-17 07:53:26 +0800 GMT+8" || image.UniqueSize != "N/A" {
		t.Fatalf("unexpected image: %#v", image)
	}
}

func TestParseVolumesFixture(t *testing.T) {
	volumes, warnings := Volumes(fixture(t, "volumes.jsonl"))
	if len(warnings) != 0 || len(volumes) != 1 {
		t.Fatalf("unexpected volumes: %#v warnings=%#v", volumes, warnings)
	}
	volume := volumes[0]
	if volume.Name != "dbxvol" || volume.Driver != "guest" || volume.Scope != "local" {
		t.Fatalf("unexpected volume: %#v", volume)
	}
}

func TestParseNetworksFixture(t *testing.T) {
	networks, warnings := Networks(fixture(t, "networks.jsonl"))
	if len(warnings) != 0 || len(networks) != 4 {
		t.Fatalf("unexpected networks: %#v warnings=%#v", networks, warnings)
	}
	names := map[string]bool{}
	for _, network := range networks {
		names[network.Name] = true
		if !network.IPv4 || network.IPv6 || network.Internal {
			t.Fatalf("unexpected network flags: %#v", network)
		}
	}
	for _, expected := range []string{"bridge", "host", "none"} {
		if !names[expected] {
			t.Fatalf("missing builtin network %q in %#v", expected, names)
		}
	}
}

func TestFirstJSONValueInspect(t *testing.T) {
	value, err := FirstJSONValue([]byte(fixture(t, "container-inspect.json")))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var object map[string]any
	if err := json.Unmarshal(value, &object); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if object["Name"] != "/dbx-test" {
		t.Fatalf("unexpected inspect object: %#v", object["Name"])
	}
	if _, err := FirstJSONValue([]byte("[]")); err != ErrEmptyJSON {
		t.Fatalf("expected ErrEmptyJSON, got %v", err)
	}
	if _, err := FirstJSONValue([]byte(fixture(t, "volume-inspect.json"))); err != nil {
		t.Fatalf("unexpected volume inspect error: %v", err)
	}
}

func TestParseInfo(t *testing.T) {
	output := `{"Client":{"Direct3DVersion":"1.611.1-81528511","DxCoreVersion":"10.0.26100.1","KernelVersion":"6.18.54.1-1","SettingsFile":"C:\\Users\\u\\AppData\\Local\\wslc\\settings.yaml","Version":"3.0.3.0","WindowsVersion":"10.0.26300.9711"},"Server":{"SessionManagerVersion":"3.0.3","Sessions":[{"CreatorPid":24656,"ID":1,"Name":"wslc-cli-u"}]}}`
	info, err := ParseInfo([]byte(output))
	if err != nil {
		t.Fatalf("parse info: %v", err)
	}
	if info.Version != "3.0.3.0" || info.Client == nil || info.Client.KernelVersion != "6.18.54.1-1" {
		t.Fatalf("unexpected info: %#v", info)
	}
	if info.Server == nil || info.Server.SessionManagerVersion != "3.0.3" || len(info.Server.Sessions) != 1 {
		t.Fatalf("unexpected server: %#v", info.Server)
	}
	session := info.Server.Sessions[0]
	if session.ID != 1 || session.CreatorPid != 24656 || session.Name != "wslc-cli-u" {
		t.Fatalf("unexpected session: %#v", session)
	}
}

func TestParseEventsFixture(t *testing.T) {
	lines := SplitJSONLines(fixture(t, "events.jsonl"))
	if len(lines) != 5 {
		t.Fatalf("expected 5 event lines, got %d", len(lines))
	}
	for _, line := range lines {
		var event struct {
			Action string `json:"Action"`
			Type   string `json:"Type"`
			Status string `json:"status"`
		}
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("event line is not valid JSON: %v", err)
		}
		if (event.Type != "container" && event.Type != "network") || event.Action == "" {
			t.Fatalf("unexpected event: %#v", event)
		}
	}
}

func TestParseSectionsAndKeyValue(t *testing.T) {
	output := "profile noise\n" +
		"@@OS_RELEASE@@\n" +
		"PRETTY_NAME=\"Debian GNU/Linux 13 (trixie)\"\nVERSION_ID=\"13\"\nID=debian\n" +
		"@@KERNEL@@\n6.18.54.1-microsoft-standard-WSL2\n" +
		"@@UID@@\n1000\n" +
		"@@WSLCONF@@\n[boot]\nsystemd=true\n" +
		"@@END@@\n"
	sections := ParseSections(output, []string{"@@OS_RELEASE@@", "@@KERNEL@@", "@@UID@@", "@@WSLCONF@@", "@@END@@"})
	if sections["@@KERNEL@@"] != "6.18.54.1-microsoft-standard-WSL2\n" {
		t.Fatalf("unexpected kernel: %q", sections["@@KERNEL@@"])
	}
	if sections["@@UID@@"] != "1000\n" {
		t.Fatalf("unexpected uid: %q", sections["@@UID@@"])
	}
	if sections["@@WSLCONF@@"] != "[boot]\nsystemd=true\n" {
		t.Fatalf("unexpected wsl.conf: %q", sections["@@WSLCONF@@"])
	}
	values := ParseKeyValueBlock(sections["@@OS_RELEASE@@"])
	if values["PRETTY_NAME"] != "Debian GNU/Linux 13 (trixie)" || values["VERSION_ID"] != "13" || values["ID"] != "debian" {
		t.Fatalf("unexpected os-release: %#v", values)
	}
}

func TestFlexBoolAndShortID(t *testing.T) {
	var network rawNetwork
	if err := json.Unmarshal([]byte(`{"IPv4":true,"IPv6":false,"Internal":"true"}`), &network); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !bool(network.IPv4) || bool(network.IPv6) || !bool(network.Internal) {
		t.Fatalf("unexpected flex bools: %#v", network)
	}
	if ShortID("70759157800b83586540cae6189530a296d94e27ff6a9289d4f85e92077f0391") != "70759157800b" {
		t.Fatal("unexpected short id")
	}
	if ShortID("abc") != "abc" {
		t.Fatal("short ids must be preserved")
	}
}
