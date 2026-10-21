package wslc

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/w89588/dbx-plugin-wsl/internal/apperr"
	"github.com/w89588/dbx-plugin-wsl/internal/parse"
)

// NetworksList implements wslc/networks/list.
func (s *Service) NetworksList(ctx context.Context) (any, *apperr.Error) {
	result, appErr := s.run(ctx, 60*time.Second, "network", "list", "--format", "json")
	if appErr != nil {
		return nil, appErr
	}
	if result.ExitCode != 0 {
		return nil, failure("列出网络失败", result.Stdout, result.Stderr)
	}
	networks, warnings := parse.Networks(result.Stdout)
	return map[string]any{"networks": networks, "warnings": warnings}, nil
}

// NetworkInspectParams is the request of wslc/networks/inspect.
type NetworkInspectParams struct {
	Name string `json:"name"`
}

// NetworksInspect implements wslc/networks/inspect.
func (s *Service) NetworksInspect(ctx context.Context, name string) (any, *apperr.Error) {
	network, appErr := requireText("name", name)
	if appErr != nil {
		return nil, appErr
	}
	result, appErr := s.run(ctx, 60*time.Second, "network", "inspect", "--format", "json", network)
	if appErr != nil {
		return nil, appErr
	}
	if result.ExitCode != 0 {
		return nil, inspectFailure("检查网络失败", result)
	}
	value, err := parse.FirstJSONValue([]byte(result.Stdout))
	if err != nil {
		return nil, apperr.Internal("解析网络详情失败", result.Stdout)
	}
	return map[string]any{"raw": value}, nil
}

// CreateNetworkParams is the request of wslc/networks/create.
type CreateNetworkParams struct {
	Name     string `json:"name"`
	Driver   string `json:"driver"`
	Internal bool   `json:"internal"`
	IPv6     bool   `json:"ipv6"`
}

// NetworksCreate implements wslc/networks/create.
func (s *Service) NetworksCreate(ctx context.Context, params CreateNetworkParams) (any, *apperr.Error) {
	name, appErr := requireText("name", params.Name)
	if appErr != nil {
		return nil, appErr
	}
	if params.IPv6 {
		// wslc 3.0.3 network create exposes no --ipv6 switch; report the missing
		// capability instead of silently creating an IPv4-only network.
		return nil, apperr.Unsupported(
			"当前 wslc 版本不支持创建 IPv6 网络",
			"请升级 WSL，或创建 IPv4 网络后手动启用 IPv6",
			"wslc network create 选项: --driver/--gateway/--internal/--ip-range/--subnet",
		)
	}
	driver := strings.TrimSpace(params.Driver)
	if driver == "" {
		driver = "bridge"
	}
	args := []string{"network", "create", "--driver", driver}
	if params.Internal {
		args = append(args, "--internal")
	}
	args = append(args, name)
	result, appErr := s.run(ctx, 120*time.Second, args...)
	if appErr != nil {
		return nil, appErr
	}
	if result.ExitCode != 0 {
		return nil, failure("创建网络失败", result.Stdout, result.Stderr)
	}
	identifier := outputText(result)
	// `wslc network create` prints the network name; resolve the real network
	// ID through inspect so the response matches the contract.
	if inspectResult, inspectErr := s.run(ctx, 30*time.Second, "network", "inspect", "--format", "json", name); inspectErr == nil && inspectResult.ExitCode == 0 {
		if value, err := parse.FirstJSONValue([]byte(inspectResult.Stdout)); err == nil {
			var detail struct {
				ID   string `json:"Id"`
				Name string `json:"Name"`
			}
			if json.Unmarshal(value, &detail) == nil && detail.ID != "" {
				identifier = detail.ID
			}
		}
	}
	return map[string]any{
		"id":     identifier,
		"name":   name,
		"ipv6":   false,
		"driver": driver,
	}, nil
}

// RemoveNetworksParams is the request of wslc/networks/remove.
type RemoveNetworksParams struct {
	Names []string `json:"names"`
}

// NetworksRemove implements wslc/networks/remove.
func (s *Service) NetworksRemove(ctx context.Context, params RemoveNetworksParams) (any, *apperr.Error) {
	names, appErr := requireList("names", params.Names)
	if appErr != nil {
		return nil, appErr
	}
	args := append([]string{"network", "remove"}, names...)
	result, appErr := s.run(ctx, 120*time.Second, args...)
	if appErr != nil {
		return nil, appErr
	}
	if result.ExitCode != 0 {
		return nil, failure("移除网络失败", result.Stdout, result.Stderr)
	}
	return map[string]any{"removed": names, "output": outputText(result)}, nil
}

// NetworkLinkParams is the request of wslc/networks/connect and disconnect.
type NetworkLinkParams struct {
	Network   string `json:"network"`
	Container string `json:"container"`
}

// NetworksConnect implements wslc/networks/connect.
func (s *Service) NetworksConnect(ctx context.Context, params NetworkLinkParams) (any, *apperr.Error) {
	return s.networkLink(ctx, "connect", params)
}

// NetworksDisconnect implements wslc/networks/disconnect.
func (s *Service) NetworksDisconnect(ctx context.Context, params NetworkLinkParams) (any, *apperr.Error) {
	return s.networkLink(ctx, "disconnect", params)
}

func (s *Service) networkLink(ctx context.Context, action string, params NetworkLinkParams) (any, *apperr.Error) {
	network, appErr := requireText("network", params.Network)
	if appErr != nil {
		return nil, appErr
	}
	container, appErr := requireText("container", params.Container)
	if appErr != nil {
		return nil, appErr
	}
	result, appErr := s.run(ctx, 120*time.Second, "network", action, network, container)
	if appErr != nil {
		return nil, appErr
	}
	if result.ExitCode != 0 {
		return nil, failure("网络操作失败", result.Stdout, result.Stderr)
	}
	return map[string]any{
		"network":   network,
		"container": container,
		"output":    outputText(result),
	}, nil
}

// NetworksPrune implements wslc/networks/prune.
func (s *Service) NetworksPrune(ctx context.Context) (any, *apperr.Error) {
	result, appErr := s.run(ctx, 120*time.Second, "network", "prune", "--force")
	if appErr != nil {
		return nil, appErr
	}
	if result.ExitCode != 0 {
		return nil, failure("清理网络失败", result.Stdout, result.Stderr)
	}
	return map[string]any{"output": outputText(result)}, nil
}
