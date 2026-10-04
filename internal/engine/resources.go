package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ---------------------------------------------------------------- stats ---

// statsFormat only uses placeholders that both Docker and Podman support.
const statsFormat = "{{.ID}};;{{.CPUPerc}};;{{.MemUsage}};;{{.MemPerc}};;{{.NetIO}};;{{.BlockIO}}"

// Stats is one resource usage sample for a running container.
type Stats struct {
	CPU      float64 // percent; can exceed 100 on multi-core machines
	Mem      string  // e.g. "12.5MiB"
	MemLimit string  // e.g. "7.6GiB"
	MemPerc  float64
	NetIO    string // "rx / tx"
	BlockIO  string // "read / write"
}

// Stats samples resource usage of all running containers, keyed by short ID.
func (e *Engine) Stats(ctx context.Context) (map[string]Stats, error) {
	out, err := e.run(ctx, "stats", "--no-stream", "--format", statsFormat)
	if err != nil {
		return nil, err
	}
	return parseStats(out), nil
}

func parseStats(out string) map[string]Stats {
	m := make(map[string]Stats)
	for _, line := range splitLines(out) {
		f := strings.Split(line, fieldSep)
		if len(f) < 6 {
			continue
		}
		mem, limit := splitPair(f[2])
		m[ShortID(f[0])] = Stats{
			CPU:      parsePercent(f[1]),
			Mem:      mem,
			MemLimit: limit,
			MemPerc:  parsePercent(f[3]),
			NetIO:    strings.TrimSpace(f[4]),
			BlockIO:  strings.TrimSpace(f[5]),
		}
	}
	return m
}

// --------------------------------------------------------------- images ---

const imageFormat = "{{.ID}};;{{.Repository}};;{{.Tag}};;{{.Size}};;{{.CreatedSince}}"

// Image is the subset of image info the UI needs.
type Image struct {
	ID         string
	Repository string
	Tag        string
	Size       string
	Created    string // human readable, e.g. "3 weeks ago"
}

// Dangling reports whether the image has no repository name.
func (i Image) Dangling() bool { return i.Repository == "" || i.Repository == "<none>" }

// Ref is the best reference for the image: repo:tag, or the short ID if it
// is dangling.
func (i Image) Ref() string {
	if i.Dangling() {
		return ShortID(i.ID)
	}
	if i.Tag == "" || i.Tag == "<none>" {
		return i.Repository
	}
	return i.Repository + ":" + i.Tag
}

// Images lists local images, named ones first.
func (e *Engine) Images(ctx context.Context) ([]Image, error) {
	out, err := e.run(ctx, "images", "--format", imageFormat)
	if err != nil {
		return nil, err
	}
	return parseImages(out), nil
}

// RemoveImage removes an image by reference or ID.
func (e *Engine) RemoveImage(ctx context.Context, ref string) error { return e.do(ctx, "rmi", ref) }

func parseImages(out string) []Image {
	var is []Image
	for _, line := range splitLines(out) {
		f := strings.Split(line, fieldSep)
		if len(f) < 5 {
			continue
		}
		is = append(is, Image{
			ID:         ShortID(f[0]),
			Repository: strings.TrimSpace(f[1]),
			Tag:        strings.TrimSpace(f[2]),
			Size:       strings.TrimSpace(f[3]),
			Created:    strings.TrimSpace(f[4]),
		})
	}
	sort.SliceStable(is, func(a, b int) bool {
		if is[a].Dangling() != is[b].Dangling() {
			return !is[a].Dangling()
		}
		return strings.ToLower(is[a].Ref()) < strings.ToLower(is[b].Ref())
	})
	return is
}

// -------------------------------------------------------------- volumes ---

const volumeFormat = "{{.Name}};;{{.Driver}};;{{.Mountpoint}}"

// Volume is the subset of volume info the UI needs.
type Volume struct {
	Name       string
	Driver     string
	Mountpoint string
}

// Anonymous reports whether the volume has a generated (64 hex chars) name.
func (v Volume) Anonymous() bool {
	if len(v.Name) != 64 {
		return false
	}
	for _, r := range v.Name {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return false
		}
	}
	return true
}

// Volumes lists volumes, named ones first.
func (e *Engine) Volumes(ctx context.Context) ([]Volume, error) {
	out, err := e.run(ctx, "volume", "ls", "--format", volumeFormat)
	if err != nil {
		return nil, err
	}
	return parseVolumes(out), nil
}

// RemoveVolume removes a volume. It fails if a container still uses it.
func (e *Engine) RemoveVolume(ctx context.Context, name string) error {
	return e.do(ctx, "volume", "rm", name)
}

func parseVolumes(out string) []Volume {
	var vs []Volume
	for _, line := range splitLines(out) {
		f := strings.Split(line, fieldSep)
		if len(f) < 3 {
			continue
		}
		vs = append(vs, Volume{
			Name:       strings.TrimSpace(f[0]),
			Driver:     strings.TrimSpace(f[1]),
			Mountpoint: strings.TrimSpace(f[2]),
		})
	}
	sort.SliceStable(vs, func(a, b int) bool {
		if vs[a].Anonymous() != vs[b].Anonymous() {
			return !vs[a].Anonymous()
		}
		return strings.ToLower(vs[a].Name) < strings.ToLower(vs[b].Name)
	})
	return vs
}

// -------------------------------------------------------------- inspect ---

// Details is a readable summary of `inspect` output for one container.
type Details struct {
	ID, Name, Image         string
	State, Health           string
	ExitCode, RestartCount  int
	Created, Started        string
	Finished                string
	Platform, RestartPolicy string
	WorkingDir, User, Host  string
	Command                 string
	Ports, Networks, Mounts []string
	Env, Labels             []string
}

// Inspect returns details about one container.
func (e *Engine) Inspect(ctx context.Context, id string) (*Details, error) {
	out, err := e.run(ctx, "inspect", "--type", "container", id)
	if err != nil {
		return nil, err
	}
	return parseInspect([]byte(out))
}

type inspectJSON struct {
	ID           string `json:"Id"`
	Name         string
	Created      string
	Path         string
	Args         []string
	Platform     string
	RestartCount int
	State        struct {
		Status     string
		ExitCode   int
		StartedAt  string
		FinishedAt string
		Health     *struct{ Status string }
	}
	Config struct {
		Image      string
		Env        []string
		Cmd        json.RawMessage
		Entrypoint json.RawMessage
		WorkingDir string
		User       string
		Hostname   string
		Labels     map[string]string
	}
	HostConfig struct {
		RestartPolicy struct{ Name string }
	}
	Mounts []struct {
		Type, Name, Source, Destination string
		RW                              bool
	}
	NetworkSettings struct {
		Ports    map[string][]struct{ HostIp, HostPort string }
		Networks map[string]struct{ IPAddress string }
	}
}

func parseInspect(data []byte) (*Details, error) {
	var arr []inspectJSON
	if err := json.Unmarshal(data, &arr); err != nil {
		return nil, fmt.Errorf("unexpected inspect output: %w", err)
	}
	if len(arr) == 0 {
		return nil, errors.New("container not found")
	}
	j := arr[0]
	d := &Details{
		ID:            j.ID,
		Name:          strings.TrimPrefix(j.Name, "/"),
		Image:         j.Config.Image,
		State:         j.State.Status,
		ExitCode:      j.State.ExitCode,
		RestartCount:  j.RestartCount,
		Created:       fmtTime(j.Created),
		Started:       fmtTime(j.State.StartedAt),
		Finished:      fmtTime(j.State.FinishedAt),
		Platform:      j.Platform,
		RestartPolicy: j.HostConfig.RestartPolicy.Name,
		WorkingDir:    j.Config.WorkingDir,
		User:          j.Config.User,
		Host:          j.Config.Hostname,
		Env:           append([]string(nil), j.Config.Env...),
	}
	if j.State.Health != nil {
		d.Health = j.State.Health.Status
	}

	cmd := append(stringOrSlice(j.Config.Entrypoint), stringOrSlice(j.Config.Cmd)...)
	if len(cmd) == 0 && j.Path != "" {
		cmd = append([]string{j.Path}, j.Args...)
	}
	d.Command = strings.Join(cmd, " ")

	for port, binds := range j.NetworkSettings.Ports {
		if len(binds) == 0 {
			d.Ports = append(d.Ports, port+" (not published)")
			continue
		}
		for _, b := range binds {
			host := b.HostIp
			if host == "" {
				host = "0.0.0.0"
			}
			d.Ports = append(d.Ports, fmt.Sprintf("%s:%s → %s", host, b.HostPort, port))
		}
	}
	for name, n := range j.NetworkSettings.Networks {
		ip := n.IPAddress
		if ip == "" {
			ip = "-"
		}
		d.Networks = append(d.Networks, name+"\t"+ip)
	}
	for _, mt := range j.Mounts {
		src := mt.Source
		if mt.Type == "volume" && mt.Name != "" {
			src = mt.Name
		}
		mode := "ro"
		if mt.RW {
			mode = "rw"
		}
		d.Mounts = append(d.Mounts, fmt.Sprintf("%s\t%s → %s (%s)", mt.Type, src, mt.Destination, mode))
	}
	for k, v := range j.Config.Labels {
		d.Labels = append(d.Labels, k+"="+v)
	}
	sort.Strings(d.Ports)
	sort.Strings(d.Networks)
	sort.Strings(d.Env)
	sort.Strings(d.Labels)
	return d, nil
}

// stringOrSlice decodes a JSON value that may be a string, a list of
// strings, or null (runtimes disagree on Cmd / Entrypoint).
func stringOrSlice(raw json.RawMessage) []string {
	var list []string
	if json.Unmarshal(raw, &list) == nil {
		return list
	}
	var s string
	if json.Unmarshal(raw, &s) == nil && s != "" {
		return []string{s}
	}
	return nil
}

// --------------------------------------------------------------- helpers ---

func splitLines(out string) []string {
	var lines []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

// splitPair splits "a / b" into "a" and "b".
func splitPair(s string) (string, string) {
	a, b, _ := strings.Cut(s, "/")
	return strings.TrimSpace(a), strings.TrimSpace(b)
}

func parsePercent(s string) float64 {
	v, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(s), "%"), 64)
	if err != nil {
		return 0
	}
	return v
}

func fmtTime(s string) string {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil || t.Year() <= 1 {
		return ""
	}
	return t.Local().Format("2006-01-02 15:04:05")
}
