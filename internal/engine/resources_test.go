package engine

import (
	"strings"
	"testing"
)

func TestParseStats(t *testing.T) {
	out := "abc123def4567890;;12.50%;;45.3MiB / 7.6GiB;;0.58%;;1.2kB / 3.4kB;;0B / 8.19kB\r\n" +
		"bad line\n"
	got := parseStats(out)
	if len(got) != 1 {
		t.Fatalf("want 1 entry, got %d: %+v", len(got), got)
	}
	s, ok := got["abc123def456"]
	if !ok {
		t.Fatalf("stats should be keyed by short ID: %+v", got)
	}
	if s.CPU != 12.5 || s.Mem != "45.3MiB" || s.MemLimit != "7.6GiB" || s.MemPerc != 0.58 {
		t.Errorf("unexpected stats: %+v", s)
	}
	if s.NetIO != "1.2kB / 3.4kB" {
		t.Errorf("net io = %q", s.NetIO)
	}
}

func TestParseImages(t *testing.T) {
	out := "sha256:1111111111111111;;<none>;;<none>;;5MB;;2 days ago\n" +
		"2222222222222222;;nginx;;latest;;187MB;;3 weeks ago\n" +
		"3333333333333333;;alpine;;3.20;;7.8MB;;2 months ago\n"
	got := parseImages(out)
	if len(got) != 3 {
		t.Fatalf("want 3 images, got %d", len(got))
	}
	want := []string{"alpine:3.20", "nginx:latest", "111111111111"} // named first, dangling last
	for i, w := range want {
		if got[i].Ref() != w {
			t.Errorf("position %d: want %s, got %s", i, w, got[i].Ref())
		}
	}
	if !got[2].Dangling() {
		t.Errorf("<none> image should be dangling")
	}
}

func TestParseVolumes(t *testing.T) {
	anon := strings.Repeat("ab", 32)
	out := anon + ";;local;;/var/lib/docker/volumes/" + anon + "/_data\n" +
		"pgdata;;local;;/var/lib/docker/volumes/pgdata/_data\n"
	got := parseVolumes(out)
	if len(got) != 2 {
		t.Fatalf("want 2 volumes, got %d", len(got))
	}
	if got[0].Name != "pgdata" || got[0].Anonymous() {
		t.Errorf("named volume should sort first: %+v", got)
	}
	if !got[1].Anonymous() {
		t.Errorf("64-hex volume should be anonymous")
	}
}

func TestParseInspect(t *testing.T) {
	data := `[{
		"Id": "abc123def4567890",
		"Name": "/web-1",
		"Created": "2026-10-02T04:30:00.123456789Z",
		"Path": "/docker-entrypoint.sh",
		"Args": ["nginx", "-g", "daemon off;"],
		"Platform": "linux",
		"RestartCount": 2,
		"State": {"Status": "running", "ExitCode": 0, "StartedAt": "2026-10-02T04:30:01Z", "FinishedAt": "0001-01-01T00:00:00Z", "Health": {"Status": "healthy"}},
		"Config": {"Image": "nginx:latest", "Env": ["B=2", "A=1"], "Cmd": null, "Entrypoint": "/docker-entrypoint.sh", "Labels": {"tier": "web"}},
		"HostConfig": {"RestartPolicy": {"Name": "unless-stopped"}},
		"Mounts": [{"Type": "volume", "Name": "data", "Source": "/var/lib/x", "Destination": "/data", "RW": true}],
		"NetworkSettings": {
			"Ports": {"80/tcp": [{"HostIp": "", "HostPort": "8080"}], "443/tcp": null},
			"Networks": {"bridge": {"IPAddress": "172.17.0.2"}}
		}
	}]`
	d, err := parseInspect([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	if d.Name != "web-1" || d.Health != "healthy" || d.RestartPolicy != "unless-stopped" || d.RestartCount != 2 {
		t.Errorf("unexpected details: %+v", d)
	}
	if d.Command != "/docker-entrypoint.sh" {
		t.Errorf("command = %q (string entrypoint should be accepted)", d.Command)
	}
	if d.Finished != "" {
		t.Errorf("zero time should be blank, got %q", d.Finished)
	}
	if len(d.Ports) != 2 || d.Ports[0] != "0.0.0.0:8080 → 80/tcp" {
		t.Errorf("ports = %q", d.Ports)
	}
	if len(d.Mounts) != 1 || d.Mounts[0] != "volume\tdata → /data (rw)" {
		t.Errorf("mounts = %q", d.Mounts)
	}
	if d.Env[0] != "A=1" {
		t.Errorf("env should be sorted: %q", d.Env)
	}
	if _, err := parseInspect([]byte("[]")); err == nil {
		t.Errorf("empty inspect output should be an error")
	}
}

func TestShortID(t *testing.T) {
	if got := ShortID("sha256:0123456789abcdef"); got != "0123456789ab" {
		t.Errorf("ShortID = %q", got)
	}
	if got := ShortID("abc"); got != "abc" {
		t.Errorf("ShortID of short id = %q", got)
	}
}
