package engine

import "testing"

func TestParseList(t *testing.T) {
	out := "abc123;;web-1;;nginx:latest;;running;;Up 2 hours;;0.0.0.0:8080->80/tcp;;2026-10-02 10:00:00 +0530 IST\r\n" +
		"def456;;old-job,alias;;busybox;;exited;;Exited (0) 3 days ago;;;;2026-09-29 08:00:00 +0530 IST\n" +
		"\n" +
		"not a container line\n"

	got := parseList(out)
	if len(got) != 2 {
		t.Fatalf("want 2 containers, got %d: %+v", len(got), got)
	}

	web := got[0]
	if web.ID != "abc123" || web.Name != "web-1" || web.Image != "nginx:latest" {
		t.Errorf("unexpected first container: %+v", web)
	}
	if !web.Running() {
		t.Errorf("web-1 should be running")
	}
	if web.Ports != "0.0.0.0:8080->80/tcp" {
		t.Errorf("ports = %q", web.Ports)
	}

	job := got[1]
	if job.Name != "old-job" {
		t.Errorf("name should be first of comma list, got %q", job.Name)
	}
	if job.Ports != "" {
		t.Errorf("ports should be empty, got %q", job.Ports)
	}
	if job.Created != "2026-09-29 08:00:00 +0530 IST" {
		t.Errorf("created = %q", job.Created)
	}
	if job.Running() {
		t.Errorf("old-job should not be running")
	}
}

func TestSortContainers(t *testing.T) {
	cs := []Container{
		{Name: "zeta", State: "running"},
		{Name: "alpha", State: "exited"},
		{Name: "beta", State: "running"},
	}
	sortContainers(cs)

	want := []string{"beta", "zeta", "alpha"} // running first, then by name
	for i, w := range want {
		if cs[i].Name != w {
			t.Fatalf("position %d: want %s, got %s", i, w, cs[i].Name)
		}
	}
}
