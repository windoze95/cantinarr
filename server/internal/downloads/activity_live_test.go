package downloads

import (
	"encoding/json"
	"flag"
	"net/url"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/windoze95/cantinarr-server/internal/instance"
)

var activityCanary = flag.String("downloads-canary", "", "private manifest for disposable loopback Radarr/NZBGet activity validation")

// This opt-in lane uses actual providers, not HTTP fixtures. It only controls
// the one explicitly marked synthetic NZB job on an acknowledged disposable
// loopback service. The manifest and provider credentials stay outside git.
func TestLiveDownloadsActivity(t *testing.T) {
	if *activityCanary == "" {
		t.Skip("requires disposable live providers")
	}
	data, err := os.ReadFile(*activityCanary)
	if err != nil {
		t.Fatal("read private canary manifest")
	}
	var manifest struct {
		Disposable bool                `json:"disposable"`
		Instances  []instance.Instance `json:"instances"`
	}
	if json.Unmarshal(data, &manifest) != nil || !manifest.Disposable {
		t.Fatal("explicit disposable manifest required")
	}
	e := newActivityEnv(t)
	var target instance.Instance
	for _, inst := range manifest.Instances {
		u, err := url.Parse(inst.URL)
		if err != nil || u.Hostname() != "127.0.0.1" || (inst.ServiceType != "nzbget" && inst.ServiceType != "radarr") {
			t.Fatal("only disposable loopback Radarr/NZBGet instances allowed")
		}
		if err := e.h.store.Create(&inst); err != nil {
			t.Fatal("create canary instance")
		}
		if inst.ServiceType == "nzbget" {
			target = inst
		}
	}
	if target.ID == "" {
		t.Fatal("NZBGet canary required")
	}
	backend, err := backendFor(e.h.registry, target)
	if err != nil {
		t.Fatal("create live backend")
	}
	queue, err := backend.Snapshot()
	if err != nil {
		t.Fatal("read live queue")
	}
	if len(queue.Items) != 1 || queue.Items[0].CorrelationID != "codex-downloads-632" || queue.Items[0].Name != "Codex Downloads Fixture 632" {
		t.Fatal("refusing actions: exact synthetic queue fixture required")
	}
	job := queue.Items[0]
	if _, err := strconv.Atoi(job.ID); err != nil || job.ID == job.CorrelationID {
		t.Fatal("alias/control identity lost")
	}
	view, w := e.get(t, 1, "/api/downloads/activity")
	if w.Code != 200 || view.Count == nil || *view.Count != 1 || len(view.Groups) != 1 || view.Groups[0].MediaType != "unmatched" {
		t.Fatal("live admin count/projection mismatch")
	}
	requester, rw := e.get(t, 2, "/api/downloads/activity")
	if rw.Code != 200 || requester.Count == nil || *requester.Count != 0 || len(requester.Groups) != 0 {
		t.Fatal("live requester unmatched boundary mismatch")
	}
	for _, action := range []struct {
		name string
		run  func(string) error
	}{{"resume", backend.ResumeItem}, {"pause", backend.PauseItem}} {
		if err := action.run(job.ID); err != nil {
			t.Fatalf("live %s failed", action.name)
		}
		e.h.activity.Invalidate()
		view, w = e.get(t, 1, "/api/downloads/activity")
		if w.Code != 200 || view.Count == nil || *view.Count != 1 {
			t.Fatalf("live %s lost unfinished job", action.name)
		}
		if action.name == "pause" && view.Jobs[0].Status != "paused" {
			t.Fatal("live pause not reflected")
		}
		t.Logf("live %s: count=1, numeric control ID preserved", action.name)
	}
	if err := backend.DeleteItem(job.ID, false); err != nil {
		t.Fatal("live remove failed")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		e.h.activity.Invalidate()
		view, w = e.get(t, 1, "/api/downloads/summary")
		if w.Code == 200 && view.Count != nil && *view.Count == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("live removal never reached confirmed zero")
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Log("live Radarr empty queue and NZBGet alias, unmatched filtering, resume, pause, removal, and confirmed zero verified")
}
