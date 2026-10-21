package parse

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
)

// ErrEmptyJSON means the inspected object did not exist.
var ErrEmptyJSON = errors.New("empty JSON output")

// SplitJSONLines returns the non-empty lines of a JSONL output.
func SplitJSONLines(output string) []string {
	var lines []string
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(strings.TrimRight(line, "\r"))
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

// ParseJSONLines parses a JSONL output. Lines that fail to parse are returned
// separately so callers can surface them as warnings (contract 4.3).
func ParseJSONLines(output string) ([]json.RawMessage, []string) {
	var values []json.RawMessage
	var warnings []string
	for _, line := range SplitJSONLines(output) {
		if !json.Valid([]byte(line)) {
			warnings = append(warnings, line)
			continue
		}
		values = append(values, json.RawMessage(line))
	}
	return values, warnings
}

// FirstJSONValue returns the first object of an inspect output. `wslc inspect`
// prints an array; object output is accepted as a fallback.
func FirstJSONValue(data []byte) (json.RawMessage, error) {
	trimmed := strings.TrimSpace(strings.TrimRight(string(data), "\r\n"))
	if trimmed == "" {
		return nil, ErrEmptyJSON
	}
	switch trimmed[0] {
	case '[':
		var values []json.RawMessage
		if err := json.Unmarshal([]byte(trimmed), &values); err != nil {
			return nil, err
		}
		if len(values) == 0 {
			return nil, ErrEmptyJSON
		}
		return values[0], nil
	case '{':
		if !json.Valid([]byte(trimmed)) {
			return nil, errors.New("invalid JSON object")
		}
		return json.RawMessage(trimmed), nil
	default:
		return nil, errors.New("unsupported JSON output")
	}
}

// FlexBool accepts JSON booleans as well as "true"/"false" strings.
type FlexBool bool

// UnmarshalJSON implements json.Unmarshaler.
func (b *FlexBool) UnmarshalJSON(data []byte) error {
	text := strings.TrimSpace(string(data))
	if text == "" || text == "null" {
		*b = false
		return nil
	}
	if text[0] == '"' {
		var value string
		if err := json.Unmarshal(data, &value); err != nil {
			return err
		}
		parsed, err := strconv.ParseBool(strings.TrimSpace(value))
		if err != nil {
			*b = false
			return nil
		}
		*b = FlexBool(parsed)
		return nil
	}
	parsed, err := strconv.ParseBool(text)
	if err != nil {
		*b = false
		return nil
	}
	*b = FlexBool(parsed)
	return nil
}

// Platform is the container platform object.
type Platform struct {
	Architecture string `json:"architecture"`
	OS           string `json:"os"`
}

// Container is the normalized container list row.
type Container struct {
	ID         string    `json:"id"`
	ShortID    string    `json:"shortId"`
	Name       string    `json:"name"`
	Image      string    `json:"image"`
	Command    string    `json:"command"`
	CreatedAt  string    `json:"createdAt"`
	RunningFor string    `json:"runningFor"`
	State      string    `json:"state"`
	Status     string    `json:"status"`
	Ports      string    `json:"ports"`
	Networks   string    `json:"networks"`
	Size       string    `json:"size"`
	Health     string    `json:"health"`
	Platform   *Platform `json:"platform,omitempty"`
}

type rawContainer struct {
	ID           string `json:"ID"`
	Names        string `json:"Names"`
	Image        string `json:"Image"`
	Command      string `json:"Command"`
	CreatedAt    string `json:"CreatedAt"`
	RunningFor   string `json:"RunningFor"`
	State        string `json:"State"`
	Status       string `json:"Status"`
	Ports        string `json:"Ports"`
	Networks     string `json:"Networks"`
	Size         string `json:"Size"`
	HealthStatus string `json:"HealthStatus"`
	Platform     struct {
		Architecture string `json:"architecture"`
		OS           string `json:"os"`
	} `json:"Platform"`
}

// NormalizeContainer converts one wslc container JSON object.
func NormalizeContainer(data []byte) (Container, error) {
	var raw rawContainer
	if err := json.Unmarshal(data, &raw); err != nil {
		return Container{}, err
	}
	platform := &Platform{Architecture: raw.Platform.Architecture, OS: raw.Platform.OS}
	if platform.Architecture == "" && platform.OS == "" {
		platform = nil
	}
	name := strings.TrimPrefix(strings.TrimSpace(raw.Names), "/")
	return Container{
		ID:         raw.ID,
		ShortID:    ShortID(raw.ID),
		Name:       name,
		Image:      raw.Image,
		Command:    raw.Command,
		CreatedAt:  raw.CreatedAt,
		RunningFor: raw.RunningFor,
		State:      raw.State,
		Status:     raw.Status,
		Ports:      raw.Ports,
		Networks:   raw.Networks,
		Size:       raw.Size,
		Health:     raw.HealthStatus,
		Platform:   platform,
	}, nil
}

// Containers parses and normalizes JSONL container output.
func Containers(output string) ([]Container, []string) {
	values, warnings := ParseJSONLines(output)
	containers := make([]Container, 0, len(values))
	for _, value := range values {
		container, err := NormalizeContainer(value)
		if err != nil {
			warnings = append(warnings, string(value))
			continue
		}
		containers = append(containers, container)
	}
	return containers, warnings
}

// Image is the normalized image list row.
type Image struct {
	ID           string `json:"id"`
	Repository   string `json:"repository"`
	Tag          string `json:"tag"`
	Digest       string `json:"digest"`
	CreatedAt    string `json:"createdAt"`
	CreatedSince string `json:"createdSince"`
	Size         string `json:"size"`
	Containers   string `json:"containers"`
	UniqueSize   string `json:"uniqueSize"`
}

type rawImage struct {
	ID           string `json:"ID"`
	Repository   string `json:"Repository"`
	Tag          string `json:"Tag"`
	Digest       string `json:"Digest"`
	CreatedAt    string `json:"CreatedAt"`
	CreatedSince string `json:"CreatedSince"`
	Size         string `json:"Size"`
	Containers   string `json:"Containers"`
	UniqueSize   string `json:"UniqueSize"`
}

// NormalizeImage converts one wslc image JSON object.
func NormalizeImage(data []byte) (Image, error) {
	var raw rawImage
	if err := json.Unmarshal(data, &raw); err != nil {
		return Image{}, err
	}
	return Image{
		ID:           raw.ID,
		Repository:   raw.Repository,
		Tag:          raw.Tag,
		Digest:       raw.Digest,
		CreatedAt:    raw.CreatedAt,
		CreatedSince: raw.CreatedSince,
		Size:         raw.Size,
		Containers:   raw.Containers,
		UniqueSize:   raw.UniqueSize,
	}, nil
}

// Images parses and normalizes JSONL image output.
func Images(output string) ([]Image, []string) {
	values, warnings := ParseJSONLines(output)
	images := make([]Image, 0, len(values))
	for _, value := range values {
		image, err := NormalizeImage(value)
		if err != nil {
			warnings = append(warnings, string(value))
			continue
		}
		images = append(images, image)
	}
	return images, warnings
}

// Volume is the normalized volume list row.
type Volume struct {
	Name       string `json:"name"`
	Driver     string `json:"driver"`
	Mountpoint string `json:"mountpoint"`
	Scope      string `json:"scope"`
	Size       string `json:"size"`
	Labels     string `json:"labels"`
}

type rawVolume struct {
	Name       string `json:"Name"`
	Driver     string `json:"Driver"`
	Mountpoint string `json:"Mountpoint"`
	Scope      string `json:"Scope"`
	Size       string `json:"Size"`
	Labels     string `json:"Labels"`
}

// NormalizeVolume converts one wslc volume JSON object.
func NormalizeVolume(data []byte) (Volume, error) {
	var raw rawVolume
	if err := json.Unmarshal(data, &raw); err != nil {
		return Volume{}, err
	}
	return Volume{
		Name:       raw.Name,
		Driver:     raw.Driver,
		Mountpoint: raw.Mountpoint,
		Scope:      raw.Scope,
		Size:       raw.Size,
		Labels:     raw.Labels,
	}, nil
}

// Volumes parses and normalizes JSONL volume output.
func Volumes(output string) ([]Volume, []string) {
	values, warnings := ParseJSONLines(output)
	volumes := make([]Volume, 0, len(values))
	for _, value := range values {
		volume, err := NormalizeVolume(value)
		if err != nil {
			warnings = append(warnings, string(value))
			continue
		}
		volumes = append(volumes, volume)
	}
	return volumes, warnings
}

// Network is the normalized network list row.
type Network struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Driver    string `json:"driver"`
	Scope     string `json:"scope"`
	IPv4      bool   `json:"ipv4"`
	IPv6      bool   `json:"ipv6"`
	Internal  bool   `json:"internal"`
	CreatedAt string `json:"createdAt"`
	Labels    string `json:"labels"`
}

type rawNetwork struct {
	ID        string   `json:"ID"`
	Name      string   `json:"Name"`
	Driver    string   `json:"Driver"`
	Scope     string   `json:"Scope"`
	IPv4      FlexBool `json:"IPv4"`
	IPv6      FlexBool `json:"IPv6"`
	Internal  FlexBool `json:"Internal"`
	CreatedAt string   `json:"CreatedAt"`
	Labels    string   `json:"Labels"`
}

// NormalizeNetwork converts one wslc network JSON object.
func NormalizeNetwork(data []byte) (Network, error) {
	var raw rawNetwork
	if err := json.Unmarshal(data, &raw); err != nil {
		return Network{}, err
	}
	return Network{
		ID:        raw.ID,
		Name:      raw.Name,
		Driver:    raw.Driver,
		Scope:     raw.Scope,
		IPv4:      bool(raw.IPv4),
		IPv6:      bool(raw.IPv6),
		Internal:  bool(raw.Internal),
		CreatedAt: raw.CreatedAt,
		Labels:    raw.Labels,
	}, nil
}

// Networks parses and normalizes JSONL network output.
func Networks(output string) ([]Network, []string) {
	values, warnings := ParseJSONLines(output)
	networks := make([]Network, 0, len(values))
	for _, value := range values {
		network, err := NormalizeNetwork(value)
		if err != nil {
			warnings = append(warnings, string(value))
			continue
		}
		networks = append(networks, network)
	}
	return networks, warnings
}

// ContainerStats is the normalized stats row.
type ContainerStats struct {
	ID       string `json:"id"`
	ShortID  string `json:"shortId"`
	Name     string `json:"name"`
	CPUPerc  string `json:"cpuPerc"`
	MemUsage string `json:"memUsage"`
	MemPerc  string `json:"memPerc"`
	NetIO    string `json:"netIO"`
	BlockIO  string `json:"blockIO"`
	PIDs     int    `json:"pids"`
}

type rawStats struct {
	ID       string  `json:"ID"`
	Name     string  `json:"Name"`
	CPUPerc  string  `json:"CPUPerc"`
	MemUsage string  `json:"MemUsage"`
	MemPerc  string  `json:"MemPerc"`
	NetIO    string  `json:"NetIO"`
	BlockIO  string  `json:"BlockIO"`
	PIDs     float64 `json:"PIDs"`
}

// NormalizeStats converts one wslc stats JSON object.
func NormalizeStats(data []byte) (ContainerStats, error) {
	var raw rawStats
	if err := json.Unmarshal(data, &raw); err != nil {
		return ContainerStats{}, err
	}
	return ContainerStats{
		ID:       raw.ID,
		ShortID:  ShortID(raw.ID),
		Name:     strings.TrimPrefix(strings.TrimSpace(raw.Name), "/"),
		CPUPerc:  raw.CPUPerc,
		MemUsage: raw.MemUsage,
		MemPerc:  raw.MemPerc,
		NetIO:    raw.NetIO,
		BlockIO:  raw.BlockIO,
		PIDs:     int(raw.PIDs),
	}, nil
}

// Stats parses and normalizes JSONL stats output.
func Stats(output string) ([]ContainerStats, []string) {
	values, warnings := ParseJSONLines(output)
	stats := make([]ContainerStats, 0, len(values))
	for _, value := range values {
		row, err := NormalizeStats(value)
		if err != nil {
			warnings = append(warnings, string(value))
			continue
		}
		stats = append(stats, row)
	}
	return stats, warnings
}

// ClientInfo is the normalized wslc client block.
type ClientInfo struct {
	Version         string `json:"version"`
	KernelVersion   string `json:"kernelVersion"`
	Direct3DVersion string `json:"direct3DVersion"`
	DXCoreVersion   string `json:"dxCoreVersion"`
	WindowsVersion  string `json:"windowsVersion"`
	SettingsFile    string `json:"settingsFile"`
}

// ServerSession is one server session block.
type ServerSession struct {
	ID         int    `json:"id"`
	CreatorPid int    `json:"creatorPid"`
	Name       string `json:"name"`
}

// ServerInfo is the normalized wslc server block.
type ServerInfo struct {
	SessionManagerVersion string          `json:"sessionManagerVersion"`
	Sessions              []ServerSession `json:"sessions"`
}

// Info is the normalized `wslc info --format json` payload.
type Info struct {
	Version string      `json:"version"`
	Client  *ClientInfo `json:"client"`
	Server  *ServerInfo `json:"server"`
}

type rawClient struct {
	Direct3DVersion string `json:"Direct3DVersion"`
	DxCoreVersion   string `json:"DxCoreVersion"`
	KernelVersion   string `json:"KernelVersion"`
	SettingsFile    string `json:"SettingsFile"`
	Version         string `json:"Version"`
	WindowsVersion  string `json:"WindowsVersion"`
}

type rawServer struct {
	SessionManagerVersion string `json:"SessionManagerVersion"`
	Sessions              []struct {
		CreatorPid int    `json:"CreatorPid"`
		ID         int    `json:"ID"`
		Name       string `json:"Name"`
	} `json:"Sessions"`
}

type rawInfo struct {
	Client rawClient `json:"Client"`
	Server rawServer `json:"Server"`
}

// ParseInfo parses `wslc info --format json`.
func ParseInfo(data []byte) (Info, error) {
	var raw rawInfo
	if err := json.Unmarshal(data, &raw); err != nil {
		return Info{}, err
	}
	client := &ClientInfo{
		Version:         raw.Client.Version,
		KernelVersion:   raw.Client.KernelVersion,
		Direct3DVersion: raw.Client.Direct3DVersion,
		DXCoreVersion:   raw.Client.DxCoreVersion,
		WindowsVersion:  raw.Client.WindowsVersion,
		SettingsFile:    raw.Client.SettingsFile,
	}
	server := &ServerInfo{SessionManagerVersion: raw.Server.SessionManagerVersion}
	for _, session := range raw.Server.Sessions {
		server.Sessions = append(server.Sessions, ServerSession{
			ID:         session.ID,
			CreatorPid: session.CreatorPid,
			Name:       session.Name,
		})
	}
	if server.Sessions == nil {
		server.Sessions = []ServerSession{}
	}
	return Info{Version: client.Version, Client: client, Server: server}, nil
}

// ShortID truncates a container/image ID to the canonical 12 characters.
func ShortID(id string) string {
	if len(id) <= 12 {
		return id
	}
	return id[:12]
}
