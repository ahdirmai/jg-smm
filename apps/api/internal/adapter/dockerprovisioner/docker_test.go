package dockerprovisioner

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
)

// fakeDaemon stands in for Docker Engine: it records every request and answers
// from a canned table so the tests assert what the driver actually sent.
type fakeDaemon struct {
	posts   map[string][]map[string]any
	started []string
	deletes []string
	// running containers keyed by name → generation label.
	containers map[string]string
}

func newFakeDaemon() *fakeDaemon {
	return &fakeDaemon{
		posts:      map[string][]map[string]any{},
		containers: map[string]string{},
	}
}

func (d *fakeDaemon) handler(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	switch {
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/create"):
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		if err := json.Unmarshal(body, &req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		d.posts[path] = append(d.posts[path], req)
		d.started = append(d.started, path)
		name := r.URL.Query().Get("name")
		if name == "" {
			name = "unnamed"
		}
		d.containers[name] = "1"
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"Id": "abc123"})
	case r.Method == http.MethodPost && strings.Contains(path, "/start"):
		d.started = append(d.started, path)
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodGet && strings.HasSuffix(path, "/json"):
		name := strings.TrimSuffix(strings.TrimPrefix(path, "/containers/"), "/json")
		gen, ok := d.containers[name]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"No such container"}`))
			return
		}
		w.Header().Set("content-type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"State":  map[string]any{"Running": true},
			"Config": map[string]any{"Labels": map[string]any{LabelWorkerID: name, LabelGeneration: gen}},
		})
	case r.Method == http.MethodGet && path == "/containers/json":
		w.Header().Set("content-type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]any{})
	case r.Method == http.MethodDelete:
		d.deletes = append(d.deletes, path)
		name := strings.Split(strings.TrimPrefix(path, "/containers/"), "?")[0]
		delete(d.containers, name)
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func newTestClient(t *testing.T, d *fakeDaemon) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(d.handler))
	t.Cleanup(srv.Close)
	c, err := New(Config{
		SocketPath: "/dev/null", // unused: the transport is swapped below
		Network:    "smm_default",
		Image:      "smm-worker",
		PublicHost: "localhost",
		PortMin:    24100,
		PortMax:    24299,
		Platforms:  "instagram,threads",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Point at the test server instead of a unix socket.
	c.http = srv.Client()
	c.endpoint = srv.URL
	return c
}

func TestCreateWorkerSendsExpectedContainer(t *testing.T) {
	d := newFakeDaemon()
	c := newTestClient(t, d)

	url := "http://localhost:24100"
	w := domain.Worker{ID: "w1", Generation: 3, NoVNCService: &url}
	if err := c.CreateWorker(context.Background(), w); err != nil {
		t.Fatalf("CreateWorker: %v", err)
	}

	var path string
	for p := range d.posts {
		path = p
	}
	req := d.posts[path][0]
	if got := req["Image"]; got != "smm-worker" {
		t.Errorf("Image = %v, want smm-worker", got)
	}
	envs := toStringSlice(req["Env"])
	for _, want := range []string{
		"WORKER_ID=w1",
		"ACTION_QUEUE=queue:action:w1",
		"CONTROL_CHANNEL=control-w1",
		"NOVNC_URL=http://localhost:24100",
		"PLATFORMS=instagram,threads",
	} {
		if !contains(envs, want) {
			t.Errorf("env missing %q; got %v", want, envs)
		}
	}
	hc, _ := req["HostConfig"].(map[string]any)
	binds, _ := hc["Binds"].([]any)
	if len(binds) != 2 {
		t.Errorf("Binds = %v, want session + screenshot volumes", binds)
	}
	if len(d.started) != 2 { // create + start
		t.Errorf("daemon calls = %v, want create then start", d.started)
	}
}

func TestCreateWorkerIsIdempotentAtSameGeneration(t *testing.T) {
	d := newFakeDaemon()
	c := newTestClient(t, d)
	url := "http://localhost:24100"
	w := domain.Worker{ID: "w1", Generation: 1, NoVNCService: &url}

	if err := c.CreateWorker(context.Background(), w); err != nil {
		t.Fatalf("create 1: %v", err)
	}
	if err := c.CreateWorker(context.Background(), w); err != nil {
		t.Fatalf("create 2: %v", err)
	}
	if len(d.posts) != 1 {
		t.Fatalf("creates = %d, want 1 (same generation is a no-op)", len(d.posts))
	}
}

func TestObserveReportsGenerationAndAbsence(t *testing.T) {
	d := newFakeDaemon()
	c := newTestClient(t, d)

	if gen, exists, err := c.Observe(context.Background(), "missing"); err != nil || exists || gen != 0 {
		t.Fatalf("Observe(missing) = (%d,%v,%v), want (0,false,nil)", gen, exists, err)
	}
	url := "http://localhost:24100"
	if err := c.CreateWorker(context.Background(), domain.Worker{ID: "w1", Generation: 2, NoVNCService: &url}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if gen, exists, err := c.Observe(context.Background(), "w1"); err != nil || !exists || gen != 1 {
		t.Fatalf("Observe(w1) = (%d,%v,%v), want (1,true,nil)", gen, exists, err)
	}
}

func TestDeleteWorkerRemovesContainerAndVolumes(t *testing.T) {
	d := newFakeDaemon()
	c := newTestClient(t, d)
	url := "http://localhost:24100"
	if err := c.CreateWorker(context.Background(), domain.Worker{ID: "w1", Generation: 1, NoVNCService: &url}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := c.DeleteWorker(context.Background(), "w1"); err != nil {
		t.Fatalf("DeleteWorker: %v", err)
	}
	if !containsPrefix(d.deletes, "/containers/smm-worker-w1") {
		t.Errorf("deletes = %v, want the container force-removed", d.deletes)
	}
	// Idempotent: deleting again is not an error.
	if err := c.DeleteWorker(context.Background(), "w1"); err != nil {
		t.Fatalf("delete again: %v", err)
	}
}

func TestHostPortForReadsStoredURL(t *testing.T) {
	c := &Client{}
	for _, tc := range []struct {
		url  string
		want int
	}{
		{"http://localhost:24123", 24123},
		{"http://example.com:24100", 24100},
		{"bad", 0},
		{"", 0},
	} {
		var p *string
		if tc.url != "" {
			p = &tc.url
		}
		if got := c.hostPortFor(domain.Worker{NoVNCService: p}); got != tc.want {
			t.Errorf("hostPortFor(%q) = %d, want %d", tc.url, got, tc.want)
		}
	}
}

func toStringSlice(v any) []string {
	raw, _ := v.([]any)
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		out = append(out, item.(string))
	}
	return out
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

func containsPrefix(haystack []string, prefix string) bool {
	for _, h := range haystack {
		if strings.HasPrefix(h, prefix) {
			return true
		}
	}
	return false
}
