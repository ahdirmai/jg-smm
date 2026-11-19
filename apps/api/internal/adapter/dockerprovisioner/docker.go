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
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/port"
)

// fleetTTL bounds how long one /containers/json snapshot serves every read.
// The reconciler ticks once per RECONCILE_INTERVAL and asks Observe for EVERY
// worker: a per-worker lookup meant 2 HTTP round-trips × N workers per tick.
// With the snapshot one pass costs a single list; mutations drop the snapshot
// so a create/stop/delete is never consumed stale.
const fleetTTL = time.Second

// Worker resource ceiling. A worker box runs a headful Chromium stack that
// spikes to ~460MiB measured (INFRA_ANALYST §2.3); the 4GiB cap absorbs the
// spike while a runaway render can no longer starve the host or its siblings.
// ponytail: fixed caps, not env-tuned — revisit when a fleet profile needs a
// smaller worker than this.
const (
	workerMemoryBytes = int64(4 << 30)
	// Chromium forks a short-lived process tree per account action and per
	// login, and the worker's PID 1 is Node, which cannot reap grandchildren it
	// never parented. Without Init: true those processes stay zombies, so the
	// pids cgroup fills up and every later browserType.launch fails with
	// "pthread_create: Resource temporarily unavailable". 256 was low enough to
	// hit that after roughly a dozen logins; Init: true reaps them, and the
	// larger ceiling absorbs the burst in between reaps.
	workerPidsLimit = int64(1024)
)

// fleetEntry is one labelled container as the fleet snapshot saw it.
type fleetEntry struct {
	name    string
	gen     int
	running bool
	// hostPorts are the TCP host ports this container publishes (live view).
	hostPorts []int
}

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
	http           *http.Client
	endpoint       string // scheme+host for the socket-muxed transport, not a real URL
	network        string // compose network the worker joins to reach api/redis/minio.
	image          string
	publicHost     string // host the browser resolves; becomes NOVNC_URL.
	portMin        int
	portMax        int
	platforms      string
	dryRun         bool
	internalSecret string
	proxyFor       func(ctx context.Context, workerID string) (*WorkerProxy, error)
	logger         *slog.Logger

	// fleet snapshot cache: one /containers/json?all=1 list shared by Observe
	// and port allocation. Guarded by mu. Mutations nil it so the next read
	// never consumes a stale view of a create/stop/delete.
	mu      sync.Mutex
	fleet   map[string]fleetEntry // keyed by worker id
	fleetAt time.Time
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
	// InternalSecret is handed to the container so it can authenticate on the
	// API's /internal routes. Empty leaves the API's guard disabled, which is
	// only acceptable when the API port is unreachable off-host.
	InternalSecret string
	// ProxyFor resolves the egress proxy for one worker's account, or nil for
	// direct dial. Injected rather than read from Config because the binding is
	// per-worker (it comes from the account's `proxy_group` row), so the driver
	// cannot know it at construction. A nil resolver means every worker dials
	// direct — correct for a local stack, wrong for a fleet sharing one IP.
	ProxyFor func(ctx context.Context, workerID string) (*WorkerProxy, error)
	Logger   *slog.Logger
}

// WorkerProxy is the resolved egress for one container. Split from a single URL
// string so credentials stay out of any value that gets logged.
type WorkerProxy struct {
	// Server is scheme://host:port, e.g. socks5://gate.example.com:1080.
	Server string
	// Username/Password are empty for an open or IP-allowlisted proxy.
	Username string
	Password string
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
		http:           &http.Client{Transport: transport, Timeout: 60 * time.Second},
		endpoint:       "http://docker",
		network:        cfg.Network,
		image:          cfg.Image,
		publicHost:     cfg.PublicHost,
		portMin:        cfg.PortMin,
		portMax:        cfg.PortMax,
		platforms:      cfg.Platforms,
		dryRun:         cfg.DryRun,
		internalSecret: cfg.InternalSecret,
		proxyFor:       cfg.ProxyFor,
		logger:         logger,
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
	if gen, state, err := c.Observe(ctx, w.ID); err != nil {
		return err
	} else if state != port.ContainerAbsent && gen >= w.Generation {
		return nil
	} else if state != port.ContainerAbsent {
		if err := c.deleteByWorkerID(ctx, w.ID, "stale-generation"); err != nil {
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
		// Operator wall-clock: logs and auth screenshots read in WIB.
		"TZ=Asia/Jakarta",
		"NOVNC_URL=http://" + c.publicHost + ":" + fmt.Sprintf("%d", hostPort),
	}
	if c.internalSecret != "" {
		env = append(env, "INTERNAL_API_SECRET="+c.internalSecret)
	}
	// Egress proxy, resolved per worker from its account's `proxy_group`. A
	// resolver error downgrades to direct dial with a warning rather than
	// failing the create: a container that boots without a proxy is far better
	// than one that never boots, and the warning is the signal to fix it.
	if c.proxyFor != nil {
		proxy, err := c.proxyFor(ctx, w.ID)
		if err != nil {
			c.logger.Warn("docker provisioner: proxy lookup failed; worker will dial direct",
				"workerId", w.ID, "err", err)
		} else if proxy != nil && proxy.Server != "" {
			env = append(env, "PROXY_URL="+proxy.Server)
			if proxy.Username != "" {
				env = append(env, "PROXY_USERNAME="+proxy.Username)
			}
			if proxy.Password != "" {
				env = append(env, "PROXY_PASSWORD="+proxy.Password)
			}
			// Never the value: the operator only needs to know routing is on.
			c.logger.Info("docker provisioner: worker egress routed through proxy",
				"workerId", w.ID, "proxy", proxy.Server, "auth", proxy.Username != "")
		}
	}
	body := map[string]any{
		"Image":  c.image,
		"Labels": labels,
		"Env":    env,
		"HostConfig": map[string]any{
			// NOT unless-stopped: that hands the daemon the lifecycle. A worker
			// whose heartbeat stopped crashed; unless-stopped resurrected it, so
			// a fleet of 77 that OOM-killed the host kept restarting itself with
			// the API dead and nobody able to stop it. The reconciler and the
			// waker own start/stop; the daemon may only retry a genuine crash.
			"RestartPolicy": map[string]any{"Name": "on-failure", "MaximumRetryCount": 3},
			// PID 1 here is Node, which reaps only the children it forks itself.
			// Chromium's helper processes are reparented to PID 1 when their
			// parent exits, and nothing waits on them, so they accumulate as
			// zombies. Docker's tiny init runs as PID 1 and reaps them.
			"Init": true,
			"Binds": []string{
				"smm-session-" + w.ID + ":/data/sessions",
				"smm-screenshot-" + w.ID + ":/data/screenshots",
			},
			// Hard ceilings: one worker's Chromium stack measured 85MiB idle /
			// ~460MiB after a session; the 4GiB Memory cap absorbs the spike
			// while a runaway render can no longer starve the host or siblings.
			"Memory":    workerMemoryBytes,
			"PidsLimit": workerPidsLimit,
			// Chromium's shared-memory surfaces need far more than the 64MiB
			// /dev/shm a container gets by default; short of it, tabs crash
			// under load.
			"ShmSize": int64(512 << 20),
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
	if err := c.postJSON(ctx, "/containers/create?name="+workerContainerName(w), body, &created); err != nil {
		return fmt.Errorf("docker: create container: %w", err)
	}
	if err := c.postEmpty(ctx, "/containers/"+created.ID+"/start"); err != nil {
		return fmt.Errorf("docker: start container: %w", err)
	}
	c.invalidate()
	c.logger.Info("worker container started", "workerId", w.ID, "name", w.Name, "container", shortID(created.ID), "novncPort", hostPort)
	return nil
}

// DeleteWorker removes the container and its volumes. A missing container is
// success: the desired end state is "absent".
func (c *Client) DeleteWorker(ctx context.Context, workerID string) error {
	if err := c.deleteByWorkerID(ctx, workerID, "deprovision"); err != nil {
		return err
	}
	c.invalidate()
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

// Observe returns the generation on the platform plus the container's run
// state. Served from the cached fleet snapshot: the reconciler calls this once
// per worker per tick, and one shared list replaces N label-filtered lookups.
func (c *Client) Observe(ctx context.Context, workerID string) (int, port.ContainerState, error) {
	fleet, err := c.snapshot(ctx)
	if err != nil {
		return 0, port.ContainerAbsent, err
	}
	e, ok := fleet[workerID]
	if !ok {
		return 0, port.ContainerAbsent, nil
	}
	state := port.ContainerStopped
	if e.running {
		state = port.ContainerRunning
	}
	return e.gen, state, nil
}

// snapshot returns the fleet cache, refreshing it when older than fleetTTL.
func (c *Client) snapshot(ctx context.Context) (map[string]fleetEntry, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fleet != nil && time.Since(c.fleetAt) < fleetTTL {
		return c.fleet, nil
	}
	fresh, err := c.listFleet(ctx)
	if err != nil {
		// Serve the stale map rather than wedging every create behind a list
		// failure; Apply-time ops still hard-fail on their own HTTP errors.
		if c.fleet != nil {
			c.logger.Warn("docker: fleet refresh failed; serving stale snapshot", "err", err)
			return c.fleet, nil
		}
		return nil, err
	}
	c.fleet = fresh
	c.fleetAt = time.Now()
	return c.fleet, nil
}

// listFleet is one /containers/json?all=1 decode into per-worker entries. The
// generation label and running state ride along so Observe and the reconciler's
// stop path need nothing else.
func (c *Client) listFleet(ctx context.Context) (map[string]fleetEntry, error) {
	var list []struct {
		Names  []string          `json:"Names"`
		State  string            `json:"State"`
		Labels map[string]string `json:"Labels"`
		Ports  []struct {
			PublicPort int `json:"PublicPort"`
		} `json:"Ports"`
	}
	if err := c.getJSON(ctx, "/containers/json?all=1", &list); err != nil {
		return nil, fmt.Errorf("docker: list containers: %w", err)
	}
	fleet := make(map[string]fleetEntry, len(list))
	for _, ctr := range list {
		id := ctr.Labels[LabelWorkerID]
		if id == "" {
			continue
		}
		e := fleet[id]
		// A worker can have at most one container; if a stale duplicate ever
		// appears the newest non-running one loses to a running one.
		if e.running {
			continue
		}
		name := ""
		if len(ctr.Names) > 0 {
			name = strings.TrimPrefix(ctr.Names[0], "/")
		}
		for _, p := range ctr.Ports {
			e.hostPorts = append(e.hostPorts, p.PublicPort)
		}
		e.name = name
		e.gen = parseGeneration(ctr.Labels)
		e.running = ctr.State == "running"
		fleet[id] = e
	}
	return fleet, nil
}

// invalidate drops the snapshot after a mutation so reads never act on a
// container state that no longer exists, and so the capacity count sees the
// containers this pass just started. Every path that must be exact takes a fresh
// snapshot rather than reusing one taken before the mutation.
func (c *Client) invalidate() {
	c.mu.Lock()
	c.fleet = nil
	c.mu.Unlock()
}

// fresh reads the fleet with the cache bypassed. The capacity check needs an
// exact count: a snapshot taken before this pass's own creates (or within
// fleetTTL of a previous tick) would still report the pre-pass fleet.
func (c *Client) fresh(ctx context.Context) (map[string]fleetEntry, error) {
	c.invalidate()
	return c.snapshot(ctx)
}

// StopWorker halts a container but keeps it (and its volumes), so StartWorker
// resumes the same instance. Idempotent: an already-stopped or absent container
// is success, because the desired end state is simply "not running".
//
// A `t=10` grace lets the worker flush its Redis claim before the process is
// killed. Docker's default (10s) is the same, but naming it keeps the intent
// explicit if that default ever changes.
func (c *Client) StopWorker(ctx context.Context, workerID string) error {
	// Already stopped is the common steady state for a paused fleet, so read
	// the snapshot first: an Exited container needs no round-trip, and the
	// reconciler would otherwise POST a stop for every paused worker each tick.
	fleet, err := c.snapshot(ctx)
	if err == nil {
		if e, ok := fleet[workerID]; ok && !e.running {
			return nil
		}
	}
	name, err := c.containerByWorkerID(ctx, workerID)
	if err != nil {
		return err
	}
	if name == "" {
		return nil
	}
	if err := c.postEmpty(ctx, "/containers/"+name+"/stop?t=10"); err != nil {
		if isNotFound(err) {
			c.invalidate()
			return nil
		}
		return fmt.Errorf("docker: stop container: %w", err)
	}
	c.invalidate()
	c.logger.Info("worker container stopped", "workerId", workerID, "container", name)
	return nil
}

// StartWorker resumes a stopped container that still exists, and provisions one
// when nothing exists yet.
//
// The create fallback is what makes pausing-by-default affordable: a PAUSED row
// keeps no container (the reconciler no longer materialises the whole fleet), so
// the first wake of a never-used account has to build its container. CreateWorker
// is idempotent per generation, so racing a concurrent create converges instead
// of duplicating.
func (c *Client) StartWorker(ctx context.Context, w domain.Worker) error {
	name, err := c.containerByWorkerID(ctx, w.ID)
	if err != nil {
		return err
	}
	if name == "" {
		return c.CreateWorker(ctx, w)
	}
	if err := c.postEmpty(ctx, "/containers/"+name+"/start"); err != nil {
		// A 304 ("already running") is NOT an error here: do() only fails on
		// >=400, and 304 already means the desired end state.
		return fmt.Errorf("docker: start container: %w", err)
	}
	c.invalidate()
	c.logger.Info("worker container started", "workerId", w.ID, "container", name)
	return nil
}

// ListRunning returns every worker the daemon currently RUNS, and is the
// reconciler's capacity count. Two things matter for correctness:
//
//   - Stopped containers are excluded. They exist but spend no RAM, so counting
//     them would keep the cap permanently full once a few sessions had paused.
//   - It reads the shared fleet snapshot, not its own list. The cap is checked
//     once per create inside one pass, and a fresh list per worker would mean N
//     full /containers/json calls per tick — and, worse, each read would lag the
//     pass's own creates, letting the pass admit N more than the cap.
func (c *Client) ListRunning(ctx context.Context) ([]string, error) {
	fleet, err := c.fresh(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(fleet))
	for id, e := range fleet {
		if e.running {
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

// allocatePort walks the range for a host port nothing else has published. It
// reads ONE fleet snapshot instead of re-listing per candidate port.
func (c *Client) allocatePort(ctx context.Context) (int, error) {
	fleet, err := c.snapshot(ctx)
	if err != nil {
		// Without a snapshot we cannot see what is bound; fail loudly rather
		// than hand out a port another live view already owns.
		return 0, fmt.Errorf("docker: port allocation needs fleet snapshot: %w", err)
	}
	taken := make(map[int]bool)
	for _, e := range fleet {
		for _, p := range e.hostPorts {
			taken[p] = true
		}
	}
	for p := c.portMin; p <= c.portMax; p++ {
		if !taken[p] {
			return p, nil
		}
	}
	return 0, fmt.Errorf("docker: live-view port range %d-%d exhausted", c.portMin, c.portMax)
}

func (c *Client) deleteByWorkerID(ctx context.Context, workerID, reason string) error {
	name, err := c.containerByWorkerID(ctx, workerID)
	if err != nil {
		return fmt.Errorf("docker: delete container (%s): %w", reason, err)
	}
	if name == "" {
		return nil // nothing running for this row
	}
	if err := c.deleteEmpty(ctx, "/containers/"+name+"?force=1&v=1"); err != nil && !isNotFound(err) {
		return fmt.Errorf("docker: delete container (%s): %w", reason, err)
	}
	return nil
}

// containerByWorkerID finds the running container carrying this worker's id
// label. Docker names are not derivable from a workerID alone once they carry
// the operator's free-text name, so the label is the lookup key; it also
// matches containers named the legacy way (smm-worker-<id>) without a
// migration.
func (c *Client) containerByWorkerID(ctx context.Context, workerID string) (string, error) {
	filter := url.QueryEscape(`{"label":["` + LabelWorkerID + `=` + workerID + `"]}`)
	var list []struct {
		Names []string `json:"Names"`
	}
	if err := c.getJSON(ctx, "/containers/json?all=1&filters="+filter, &list); err != nil {
		return "", fmt.Errorf("docker: lookup container: %w", err)
	}
	if len(list) == 0 {
		return "", nil
	}
	// The daemon prefixes names with "/"; strip it so the value is a valid
	// path segment for the /containers/<name> routes.
	name := strings.TrimPrefix(list[0].Names[0], "/")
	return name, nil
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

// workerContainerName is the name `docker ps` shows. It leads with the
// operator's row name (slugified: Docker permits only [a-zA-Z0-9][a-zA-Z0-9_.-]
// and the row name is free text) so the fleet is readable on the command line,
// and ends with a short id tail so the name stays unique even if the row name
// is later edited — the label is the identity, the name is only a label for
// humans.
func workerContainerName(w domain.Worker) string {
	slug := slugify(w.Name)
	if slug == "" {
		slug = "unnamed"
	}
	return "smm-worker-" + slug + "-" + shortID(w.ID)
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

// slugify maps free text onto the character set Docker allows in a name.
func slugify(s string) string {
	return strings.Trim(slugRe.ReplaceAllString(strings.ToLower(s), "-"), "-")
}

// shortID trims a container id to the length `docker ps` shows, tolerating a
// short or empty response so logging never panics on a malformed daemon reply.
func shortID(id string) string {
	if len(id) <= 12 {
		return id
	}
	return id[:12]
}
