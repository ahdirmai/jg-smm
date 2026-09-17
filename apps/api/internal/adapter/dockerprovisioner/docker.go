// Package dockerprovisioner implements port.K8sClient against the Docker Engine
// API. It exists so a workstation can run the whole stack without a cluster:
// the dashboard's Create reaches a real container instead of dying at the
// static driver's "recording intent" log line.
//
// The Engine API is plain HTTP over a unix socket, so this is net/http plus a
// DialContext — no SDK, no extra module (REMEDIATION_PLAN §1.2). The container
// it launches is the same worker image compose builds, labelled so the orphan
// sweeper can tell a stray one from a scaled one.
package dockerprovisioner

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// Labels tie a container back to its Worker row. ListRunning reads them; the
// sweeper treats any labelled container without a row as an orphan.
const (
	LabelWorker     = "smm.worker"
	LabelWorkerID   = "smm.worker.id"
	LabelGeneration = "smm.generation"
)

// Client talks to a local Docker daemon. It is one more port.K8sClient peer —
// the reconciler and services never branch on the tier.
type Client struct {
	http       *http.Client
	endpoint   string // scheme+host for the socket-muxed transport, not a real URL
	network    string // compose network the worker joins to reach api/redis/minio.
	image      string
	publicHost string // host the browser resolves; becomes NOVNC_URL.
	portMin    int
	portMax    int
	platforms  string
	dryRun     bool
	logger     *slog.Logger
}

// Config tunes the driver.
type Config struct {
	// SocketPath is the daemon endpoint, normally /var/run/docker.sock.
	SocketPath string
	// Network is the compose network workers join so service names resolve.
	// Empty falls back to DefaultNetwork.
	Network string
	// Image is the worker image, e.g. "smm-worker".
	Image string
	// PublicHost is the host a browser uses to reach the live view. The driver
	// publishes 6080 to 127.0.0.1:<hostPort> and reports it back through env.
	PublicHost string
	// PortMin/PortMax bound the live-view host port range. The worker row's
	// allocated port is passed in via Worker.NoVNCService-derived env; the
	// driver trusts it and only falls back to its own allocation when unset.
	PortMin int
	PortMax int
	// Platforms is the comma list handed to the container as PLATFORMS.
	Platforms string
	// DryRun keeps the worker from committing real actions (like/comment).
	DryRun bool
	Logger *slog.Logger
}

// DefaultNetwork matches the network `docker compose up` creates for this
// project. Compose prefixes the project name, so "smm" → smm_default.
const DefaultNetwork = "smm_default"

// New builds a driver against a unix socket. The daemon is not contacted, so
// construction succeeds even before Docker is up; every call then reports the
// real error instead of a boot crash.
func New(cfg Config) (*Client, error) {
	if cfg.SocketPath == "" {
		return nil, fmt.Errorf("dockerprovisioner: DOCKER_SOCKET is required")
	}
	if cfg.Image == "" {
		return nil, fmt.Errorf("dockerprovisioner: WORKER_IMAGE is required")
	}
	if cfg.Network == "" {
		cfg.Network = DefaultNetwork
	}
	if cfg.PublicHost == "" {
		cfg.PublicHost = "localhost"
	}
	if cfg.PortMin <= 0 || cfg.PortMax < cfg.PortMin {
		return nil, fmt.Errorf("dockerprovisioner: invalid live-view port range %d-%d", cfg.PortMin, cfg.PortMax)
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	transport := &http.Transport{
		// The engine listens on a unix socket, not TCP: dial it directly and
		// let the wire be HTTP/1.1 to an arbitrary host name.
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			d := net.Dialer{Timeout: 10 * time.Second}
			return d.DialContext(ctx, "unix", cfg.SocketPath)
		},
	}
	return &Client{
		http:       &http.Client{Transport: transport, Timeout: 60 * time.Second},
		endpoint:   "http://docker",
		network:    cfg.Network,
		image:      cfg.Image,
		publicHost: cfg.PublicHost,
		portMin:    cfg.PortMin,
		portMax:    cfg.PortMax,
		platforms:  cfg.Platforms,
		dryRun:     cfg.DryRun,
		logger:     logger,
	}, nil
}

var _ port.K8sClient = (*Client)(nil)

// CreateWorker creates and starts the container for w at its generation.
// Idempotent per (workerID, generation): a container at the same generation is
// left alone, an older one is replaced, so a reconciler retry converges.
func (c *Client) CreateWorker(ctx context.Context, w domain.Worker) error {
	hostPort := c.hostPortFor(w)
	if hostPort == 0 {
		// No port persisted on the row: allocate one against the range. The
		// row normally gets it at create time (container service), so this is
		// the backfill path for rows created before allocation existed.
		p, err := c.allocatePort(ctx)
		if err != nil {
			return err
		}
		hostPort = p
	}

	// A container at this or a newer generation is already doing the job.
	if gen, exists, err := c.Observe(ctx, w.ID); err != nil {
		return err
	} else if exists && gen >= w.Generation {
		return nil
	} else if exists {
		if err := c.deleteByName(ctx, workerContainerName(w.ID), "stale-generation"); err != nil {
			return err
		}
	}

	labels := map[string]string{
		LabelWorker:     "true",
		LabelWorkerID:   w.ID,
		LabelGeneration: fmt.Sprintf("%d", w.Generation),
	}
	env := []string{
		"WORKER_ID=" + w.ID,
		"ACTION_QUEUE=queue:action:" + w.ID,
		"CONTROL_CHANNEL=control-" + w.ID,
		"SESSION_PVC=smm-session-" + w.ID,
		"API_URL=http://api:8080",
		"REDIS_URL=redis://redis:6379",
		"S3_ENDPOINT=http://minio:9000",
		"PLATFORMS=" + c.platforms,
		"ACTION_DRY_RUN=" + boolStr(c.dryRun),
		"NOVNC_URL=http://" + c.publicHost + ":" + fmt.Sprintf("%d", hostPort),
	}
	body := map[string]any{
		"Image":  c.image,
		"Labels": labels,
		"Env":    env,
		"HostConfig": map[string]any{
			"RestartPolicy": map[string]any{"Name": "unless-stopped"},
			"Binds": []string{
				"smm-session-" + w.ID + ":/data/sessions",
				"smm-screenshot-" + w.ID + ":/data/screenshots",
			},
			// Loopback only (docs/INFRA_ANALYST.md §7): the live view is for the
			// operator's own browser, never another machine.
			"PortBindings": map[string]any{
				"6080/tcp": []map[string]any{{"HostIp": "127.0.0.1", "HostPort": fmt.Sprintf("%d", hostPort)}},
			},
		},
		"NetworkingConfig": map[string]any{
			"EndpointsConfig": map[string]any{c.network: map[string]any{}},
		},
	}

	var created struct{ ID string }
	if err := c.postJSON(ctx, "/containers/create?name="+workerContainerName(w.ID), body, &created); err != nil {
		return fmt.Errorf("docker: create container: %w", err)
	}
	if err := c.postEmpty(ctx, "/containers/"+created.ID+"/start"); err != nil {
		return fmt.Errorf("docker: start container: %w", err)
	}
	c.logger.Info("worker container started", "workerId", w.ID, "container", shortID(created.ID), "novncPort", hostPort)
	return nil
}

// DeleteWorker removes the container and its volumes. A missing container is
// success: the desired end state is "absent".
func (c *Client) DeleteWorker(ctx context.Context, workerID string) error {
	if err := c.deleteByName(ctx, workerContainerName(workerID), "deprovision"); err != nil {
		return err
	}
	// Named volumes outlive the container; drop them so sessions/screenshots do
	// not pile up across recreations.
	for _, vol := range []string{"smm-session-" + workerID, "smm-screenshot-" + workerID} {
		if err := c.deleteEmpty(ctx, "/volumes/"+vol); err != nil {
			// Not fatal: a leaked volume wastes disk, it does not break the row.
			c.logger.Warn("docker: volume left behind", "volume", vol, "err", err)
		}
	}
	return nil
}

// Observe returns the running generation, or (0,false) when absent.
func (c *Client) Observe(ctx context.Context, workerID string) (int, bool, error) {
	var info struct {
		State struct {
			Running bool `json:"Running"`
		} `json:"State"`
		Config struct {
			Labels map[string]string `json:"Labels"`
		} `json:"Config"`
	}
	err := c.getJSON(ctx, "/containers/"+workerContainerName(workerID)+"/json", &info)
	if isNotFound(err) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return parseGeneration(info.Config.Labels), info.State.Running, nil
}

// ListRunning returns every worker id the daemon hosts. Label-derived, so the
// sweeper can finally distinguish an orphan from a scaled replica.
func (c *Client) ListRunning(ctx context.Context) ([]string, error) {
	filter, _ := json.Marshal(map[string]map[string][]string{"label": {LabelWorker: {"true"}}})
	var list []struct {
		Names  []string          `json:"Names"`
		Labels map[string]string `json:"Labels"`
	}
	if err := c.getJSON(ctx, "/containers/json?all=1&filters="+string(filter), &list); err != nil {
		return nil, fmt.Errorf("docker: list containers: %w", err)
	}
	ids := make([]string, 0, len(list))
	for _, ctr := range list {
		if id := ctr.Labels[LabelWorkerID]; id != "" {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// hostPortFor reads the port persisted on the row as a URL
// (http://localhost:24123). 0 when unset or unparseable.
func (c *Client) hostPortFor(w domain.Worker) int {
	if w.NoVNCService == nil {
		return 0
	}
	i := strings.LastIndex(*w.NoVNCService, ":")
	if i < 0 {
		return 0
	}
	var p int
	if _, err := fmt.Sscanf((*w.NoVNCService)[i+1:], "%d", &p); err != nil {
		return 0
	}
	return p
}

// allocatePort walks the range for a host port nothing else has bound. It
// prefers to stay out of Docker's way entirely, so a hit is checked live
// rather than trusting bookkeeping.
func (c *Client) allocatePort(ctx context.Context) (int, error) {
	for p := c.portMin; p <= c.portMax; p++ {
		if c.portTaken(ctx, p) {
			continue
		}
		return p, nil
	}
	return 0, fmt.Errorf("docker: live-view port range %d-%d exhausted", c.portMin, c.portMax)
}

// portTaken reports whether any container already binds the host port.
func (c *Client) portTaken(ctx context.Context, port int) bool {
	var list []struct {
		Ports []struct {
			PublicPort int `json:"PublicPort"`
		} `json:"Ports"`
	}
	if err := c.getJSON(ctx, "/containers/json?all=1", &list); err != nil {
		// If the daemon cannot answer, treat the port as free rather than
		// wedging every create behind a list failure.
		c.logger.Warn("docker: port probe failed, assuming free", "port", port, "err", err)
		return false
	}
	for _, ctr := range list {
		for _, bp := range ctr.Ports {
			if bp.PublicPort == port {
				return true
			}
		}
	}
	return false
}

func (c *Client) deleteByName(ctx context.Context, name, reason string) error {
	if err := c.deleteEmpty(ctx, "/containers/"+name+"?force=1&v=1"); err != nil && !isNotFound(err) {
		return fmt.Errorf("docker: delete container (%s): %w", reason, err)
	}
	return nil
}

// --- transport -----------------------------------------------------------

func (c *Client) getJSON(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint+path, nil)
	if err != nil {
		return err
	}
	return c.do(req, out)
}

func (c *Client) postJSON(ctx context.Context, path string, body any, out any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+path, strings.NewReader(string(payload)))
	if err != nil {
		return err
	}
	req.Header.Set("content-type", "application/json")
	return c.do(req, out)
}

func (c *Client) postEmpty(ctx context.Context, path string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+path, nil)
	if err != nil {
		return err
	}
	return c.do(req, nil)
}

func (c *Client) deleteEmpty(ctx context.Context, path string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.endpoint+path, nil)
	if err != nil {
		return err
	}
	return c.do(req, nil)
}

func (c *Client) do(req *http.Request, out any) error {
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 400 {
		return engineError(res)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(res.Body).Decode(out)
}

// engineError turns a 4xx/5xx into a readable message; the body carries the
// daemon's own reason ("No such container", "conflicting options"...).
func engineError(res *http.Response) error {
	buf := make([]byte, 512)
	n, _ := res.Body.Read(buf)
	msg := strings.TrimSpace(string(buf[:n]))
	if msg == "" {
		return fmt.Errorf("docker engine %s", res.Status)
	}
	return fmt.Errorf("docker engine %s: %s", res.Status, msg)
}

// isNotFound recognises both shapes the daemon uses: a 404 status, and the
// "No such container" text it returns for a name lookup on a deleted id.
func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "404") || strings.Contains(s, "No such container")
}

func parseGeneration(labels map[string]string) int {
	if labels == nil {
		return 0
	}
	var n int
	if _, err := fmt.Sscanf(labels[LabelGeneration], "%d", &n); err != nil {
		return 0
	}
	return n
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func workerContainerName(workerID string) string {
	return "smm-worker-" + workerID
}

// shortID trims a container id to the length `docker ps` shows, tolerating a
// short or empty response so logging never panics on a malformed daemon reply.
func shortID(id string) string {
	if len(id) <= 12 {
		return id
	}
	return id[:12]
}
