package handler

import (
	"github.com/w89588/dbx-plugin-wsl/internal/wsl"
	"github.com/w89588/dbx-plugin-wsl/internal/wslc"
)

type nameParams struct {
	Name string `json:"name"`
}
type jobParams struct {
	JobID string `json:"jobId"`
}
type projectParams struct {
	Target string `json:"target"`
}

// Explicit allowlist: MCP cannot invoke bridge methods or arbitrary host RPC.
// Parameter schemas use the same Go types as dispatch.
var mcpMethods = []mcpMethod{
	{"system/info", true, struct{}{}},
	{"system/project/open", false, projectParams{}},
	{"system/wsl/status", true, struct{}{}},
	{"system/config/read", true, struct{}{}},
	{"system/wsl/idle/read", true, struct{}{}},
	{"system/wsl/idle/write", false, wsl.IdleWriteParams{}},
	{"system/config/write", false, wsl.ConfigWriteParams{}},
	{"system/wsl/online", true, struct{}{}},
	{"wsl/distros/list", true, struct{}{}},
	{"wsl/distros/start", false, nameParams{}},
	{"wsl/distros/terminate", false, nameParams{}},
	{"wsl/distros/setDefault", false, nameParams{}},
	{"wsl/distros/exec", false, wsl.ExecParams{}},
	{"wsl/distros/info", true, nameParams{}},
	{"wsl/distros/wslconf", true, nameParams{}},
	{"wsl/distros/export", false, wsl.ExportParams{}},
	{"wsl/distros/import", false, wsl.ImportParams{}},
	{"wsl/distros/unregister", false, nameParams{}},
	{"wsl/distros/setVersion", false, wsl.SetVersionParams{}},
	{"wsl/distros/sparse", false, wsl.SparseParams{}},
	{"wsl/distros/compact", false, nameParams{}},
	{"wsl/distros/defaultUser", false, wsl.DefaultUserParams{}},
	{"wsl/distros/resize", false, wsl.ResizeParams{}},
	{"wsl/distros/manage", false, wsl.ManageParams{}},
	{"wsl/distros/openTerminal", false, nameParams{}},
	{"wsl/distros/openExplorer", false, nameParams{}},
	{"wsl/shutdown", false, wsl.ShutdownParams{}},
	{"wsl/update", false, wsl.UpdateParams{}},
	{"wsl/distros/install", false, wsl.InstallParams{}},
	{"wslc/info", true, struct{}{}},
	{"wslc/containers/list", true, wslc.ListParams{}},
	{"wslc/containers/inspect", true, wslc.InspectParams{}},
	{"wslc/containers/start", false, wslc.StartParams{}},
	{"wslc/containers/stop", false, wslc.StopParams{}},
	{"wslc/containers/restart", false, wslc.RestartParams{}},
	{"wslc/containers/kill", false, wslc.KillParams{}},
	{"wslc/containers/remove", false, wslc.RemoveParams{}},
	{"wslc/containers/run", false, wslc.RunParams{}},
	{"wslc/containers/exec", false, wslc.ExecParams{}},
	{"wslc/containers/logs", true, wslc.LogsParams{}},
	{"wslc/containers/stats", true, wslc.StatsParams{}},
	{"wslc/containers/prune", false, struct{}{}},
	{"wslc/images/list", true, struct{}{}},
	{"wslc/images/inspect", true, wslc.ImageInspectParams{}},
	{"wslc/images/pull", false, wslc.PullParams{}},
	{"wslc/images/remove", false, wslc.RemoveImagesParams{}},
	{"wslc/images/prune", false, wslc.PruneImagesParams{}},
	{"wslc/images/tag", false, wslc.TagParams{}},
	{"wslc/images/build", false, wslc.BuildParams{}},
	{"wslc/images/save", false, wslc.SaveParams{}},
	{"wslc/images/load", false, wslc.LoadParams{}},
	{"wslc/volumes/list", true, struct{}{}},
	{"wslc/volumes/inspect", true, wslc.VolumeInspectParams{}},
	{"wslc/volumes/create", false, wslc.CreateVolumeParams{}},
	{"wslc/volumes/remove", false, wslc.RemoveVolumesParams{}},
	{"wslc/volumes/prune", false, struct{}{}},
	{"wslc/networks/list", true, struct{}{}},
	{"wslc/networks/inspect", true, wslc.NetworkInspectParams{}},
	{"wslc/networks/create", false, wslc.CreateNetworkParams{}},
	{"wslc/networks/remove", false, wslc.RemoveNetworksParams{}},
	{"wslc/networks/connect", false, wslc.NetworkLinkParams{}},
	{"wslc/networks/disconnect", false, wslc.NetworkLinkParams{}},
	{"wslc/networks/prune", false, struct{}{}},
	{"wslc/sessions/list", true, struct{}{}},
	{"wslc/events/start", false, wslc.EventsParams{}},
	{"wslc/events/stop", false, jobParams{}},
	{"wslc/registry/login", false, wslc.LoginParams{}},
	{"wslc/registry/logout", false, wslc.LogoutParams{}},
	{"console/run", false, ConsoleRunParams{}},
	{"console/presets", true, struct{}{}},
	{"jobs/list", true, struct{}{}},
	{"jobs/get", true, jobParams{}},
	{"jobs/cancel", false, jobParams{}},
}
