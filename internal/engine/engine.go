// Package engine is a thin wrapper around the docker / podman command line.
//
// Shelling out to the CLI keeps the binary tiny (no Docker SDK) and works with
// any runtime that speaks the Docker CLI dialect.
package engine

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// fieldSep separates fields in `ps` output. It does not occur in container
// names, image references or status text.
const fieldSep = ";;"

// listFormat only uses placeholders that both Docker and Podman support.
const listFormat = "{{.ID}};;{{.Names}};;{{.Image}};;{{.State}};;{{.Status}};;{{.Ports}};;{{.CreatedAt}}"

// Container is the subset of container info the UI needs.
type Container struct {
	ID      string
	Name    string
	Image   string
	State   string // running, exited, paused, created, restarting, ...
	Status  string // human readable, e.g. "Up 2 hours"
	Ports   string
	Created string
}

// Running reports whether the container is currently running.
func (c Container) Running() bool { return c.State == "running" }

// Paused reports whether the container is paused.
func (c Container) Paused() bool { return c.State == "paused" }

// ShortID returns the 12 character form of the container ID.
func (c Container) ShortID() string { return ShortID(c.ID) }

// ShortID trims a "sha256:" prefix and shortens an ID to 12 characters.
func ShortID(id string) string {
	id = strings.TrimPrefix(strings.TrimSpace(id), "sha256:")
	if len(id) > 12 {
		id = id[:12]
	}
	return id
}

// Engine runs commands against a container runtime CLI.
type Engine struct {
	Name string // "docker" or "podman", for display
	bin  string // resolved path to the executable
}

// Detect finds a container CLI on PATH. If preferred is empty, docker is tried
// first and then podman.
func Detect(preferred string) (*Engine, error) {
	candidates := []string{"docker", "podman"}
	if preferred != "" {
		candidates = []string{preferred}
	}
	for _, c := range candidates {
		path, err := exec.LookPath(c)
		if err != nil {
			continue
		}
		name := strings.TrimSuffix(strings.ToLower(filepath.Base(path)), ".exe")
		return &Engine{Name: name, bin: path}, nil
	}
	return nil, fmt.Errorf("no container runtime found on PATH (tried: %s)", strings.Join(candidates, ", "))
}

// List returns containers, running ones first. When all is false, stopped
// containers are left out.
func (e *Engine) List(ctx context.Context, all bool) ([]Container, error) {
	args := []string{"ps", "--format", listFormat}
	if all {
		args = append(args, "-a")
	}
	out, err := e.run(ctx, args...)
	if err != nil {
		return nil, err
	}
	cs := parseList(out)
	sortContainers(cs)
	return cs, nil
}

// Start starts a stopped container.
func (e *Engine) Start(ctx context.Context, id string) error { return e.do(ctx, "start", id) }

// Stop stops a running container.
func (e *Engine) Stop(ctx context.Context, id string) error { return e.do(ctx, "stop", id) }

// Restart restarts a container.
func (e *Engine) Restart(ctx context.Context, id string) error { return e.do(ctx, "restart", id) }

// Pause freezes all processes in a running container.
func (e *Engine) Pause(ctx context.Context, id string) error { return e.do(ctx, "pause", id) }

// Unpause resumes a paused container.
func (e *Engine) Unpause(ctx context.Context, id string) error { return e.do(ctx, "unpause", id) }

// Remove force-removes a container (running or not). Volumes are kept.
func (e *Engine) Remove(ctx context.Context, id string) error { return e.do(ctx, "rm", "-f", id) }

// ServerVersion returns the engine version, or "" if it can't be determined.
func (e *Engine) ServerVersion(ctx context.Context) string {
	for _, f := range []string{"{{.Server.Version}}", "{{.Client.Version}}"} {
		out, err := e.run(ctx, "version", "--format", f)
		if v := strings.TrimSpace(out); err == nil && v != "" && !strings.Contains(v, "<no value>") {
			return v
		}
	}
	return ""
}

// ShellCommand builds an interactive shell command for a running container.
// Linux containers get bash when available (falling back to sh); Windows
// containers get cmd.
func (e *Engine) ShellCommand(ctx context.Context, id string) *exec.Cmd {
	out, _ := e.run(ctx, "inspect", "--type", "container", "--format", "{{.Platform}}", id)
	if strings.EqualFold(strings.TrimSpace(out), "windows") {
		return exec.Command(e.bin, "exec", "-it", id, "cmd")
	}
	return exec.Command(e.bin, "exec", "-it", id, "sh", "-c",
		"if command -v bash >/dev/null 2>&1; then exec bash; else exec sh; fi")
}

func (e *Engine) do(ctx context.Context, args ...string) error {
	_, err := e.run(ctx, args...)
	return err
}

func (e *Engine) run(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, e.bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", fmt.Errorf("`%s %s` timed out", e.Name, args[0])
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", errors.New(squash(msg, 3))
	}
	return stdout.String(), nil
}

// LogStream follows a container's logs (stdout and stderr merged).
type LogStream struct {
	// Lines yields log lines. It is closed when the stream ends.
	Lines  <-chan string
	cancel context.CancelFunc
	pr     *io.PipeReader
}

// StreamLogs starts `logs --follow` and returns a stream of lines. The caller
// must call Close when done.
func (e *Engine) StreamLogs(id string, tail int, timestamps bool) (*LogStream, error) {
	ctx, cancel := context.WithCancel(context.Background())
	args := []string{"logs", "--follow", "--tail", strconv.Itoa(tail)}
	if timestamps {
		args = append(args, "--timestamps")
	}
	cmd := exec.CommandContext(ctx, e.bin, append(args, id)...)

	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw // same writer: lines stay in order

	if err := cmd.Start(); err != nil {
		cancel()
		_ = pw.Close()
		return nil, err
	}

	go func() {
		_ = cmd.Wait()
		_ = pw.Close()
	}()

	ch := make(chan string, 256)
	go func() {
		defer close(ch)
		rd := bufio.NewReaderSize(pr, 64*1024)
		for {
			line, err := rd.ReadString('\n')
			if line != "" {
				select {
				case ch <- strings.TrimRight(line, "\r\n"):
				case <-ctx.Done():
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	return &LogStream{Lines: ch, cancel: cancel, pr: pr}, nil
}

// Close stops the underlying process and releases resources. Safe to call on
// a nil stream and safe to call more than once.
func (s *LogStream) Close() {
	if s == nil {
		return
	}
	s.cancel()
	_ = s.pr.Close() // unblocks the process's output copier
}

func parseList(out string) []Container {
	var cs []Container
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		f := strings.Split(line, fieldSep)
		if len(f) < 7 {
			continue
		}
		cs = append(cs, Container{
			ID:      strings.TrimSpace(f[0]),
			Name:    firstName(f[1]),
			Image:   f[2],
			State:   strings.ToLower(strings.TrimSpace(f[3])),
			Status:  f[4],
			Ports:   f[5],
			Created: f[6],
		})
	}
	return cs
}

// firstName returns the first of a comma-separated list of names.
func firstName(s string) string {
	if i := strings.Index(s, ","); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

func sortContainers(cs []Container) {
	sort.SliceStable(cs, func(i, j int) bool {
		ri, rj := cs[i].Running(), cs[j].Running()
		if ri != rj {
			return ri
		}
		return strings.ToLower(cs[i].Name) < strings.ToLower(cs[j].Name)
	})
}

// squash keeps the first n lines of s and joins them with spaces.
func squash(s string, n int) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, " ")
}
