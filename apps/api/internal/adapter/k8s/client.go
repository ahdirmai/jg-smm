// Package k8s implements port.K8sClient against a real Kubernetes cluster
// (production path). Local dev uses the static driver instead: PROVISIONER_MODE
// selects which one the API wires up.
package k8s

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"golang.org/x/time/rate"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm-automation/apps/api/internal/port"
)

// GenerationLabel is the pod label carrying Worker.Generation. The reconciler
// reads it to diff desired vs observed state (idempotent reconcile).
const GenerationLabel = "smm.generation"

// WorkerLabel selects every resource belonging to a worker.
const WorkerLabel = "smm.worker-id"

const (
	WorkerContainerName = "worker"
	SessionVolumeName   = "sessions"
	DefaultImage        = "ghcr.io/ahdirmai/jg-smm-worker:v0.1"
)

// Client provisions worker pods + their session PVC and noVNC service. It is the
// production implementation of port.K8sClient.
type Client struct {
	core    kubernetes.Interface
	ns      string
	image   string
	limiter *rate.Limiter
	logger  *slog.Logger
}

// Config tunes the provisioner.
type Config struct {
	Namespace        string
	Image            string
	CreatesPerMinute int
	Logger           *slog.Logger
}

// New builds a client from the in-cluster config. It fails fast outside a
// cluster; local dev must use the static driver instead.
func New(ctx context.Context, cfg Config) (*Client, error) {
	if cfg.Namespace == "" {
		return nil, fmt.Errorf("k8s: namespace is required")
	}
	restCfg, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("k8s: not in a cluster (%w); use PROVISIONER_MODE=static locally", err)
	}
	clientset, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("k8s: build clientset: %w", err)
	}
	image := cfg.Image
	if image == "" {
		image = DefaultImage
	}
	cpm := cfg.CreatesPerMinute
	if cpm <= 0 {
		cpm = 10
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Client{
		core:    clientset,
		ns:      cfg.Namespace,
		image:   image,
		limiter: rate.NewLimiter(rate.Every(time.Minute/time.Duration(cpm)), cpm),
		logger:  logger,
	}, nil
}

var _ port.K8sClient = (*Client)(nil)

// CreateWorker provisions the PVC + Pod + Service at the given generation.
// Idempotent per (workerID, generation): an existing pod at the same or newer
// generation is left untouched, so a retry never recreates a live pod.
func (c *Client) CreateWorker(ctx context.Context, w domain.Worker) error {
	if err := c.limiter.Wait(ctx); err != nil {
		return fmt.Errorf("k8s: rate limit: %w", err)
	}
	if err := c.ensurePVC(ctx, w); err != nil {
		return err
	}
	if err := c.ensurePod(ctx, w); err != nil {
		return err
	}
	if err := c.ensureService(ctx, w); err != nil {
		return err
	}
	c.logger.Info("worker provisioned", "workerId", w.ID, "generation", w.Generation)
	return nil
}

// DeleteWorker removes all resources for a worker. Missing resources are not an
// error: the op converges toward "absent", which is what the reconciler wants.
func (c *Client) DeleteWorker(ctx context.Context, workerID string) error {
	name := workerName(workerID)
	for _, del := range []struct {
		kind string
		fn   func() error
	}{
		{"service", func() error {
			return ignoreMissing(c.core.CoreV1().Services(c.ns).Delete(ctx, name, metav1.DeleteOptions{}))
		}},
		{"pod", func() error {
			return ignoreMissing(c.core.CoreV1().Pods(c.ns).Delete(ctx, name, metav1.DeleteOptions{}))
		}},
		{"pvc", func() error {
			return ignoreMissing(c.core.CoreV1().PersistentVolumeClaims(c.ns).Delete(ctx, domain.SessionPVCName(workerID), metav1.DeleteOptions{}))
		}},
	} {
		if err := del.fn(); err != nil {
			return fmt.Errorf("k8s: delete %s for %s: %w", del.kind, workerID, err)
		}
	}
	c.logger.Info("worker deprovisioned", "workerId", workerID)
	return nil
}

// Observe returns the generation currently running for a worker, or
// (0, false) when nothing exists. This is the reconciler's "actual" side.
func (c *Client) Observe(ctx context.Context, workerID string) (int, bool, error) {
	pod, err := c.core.CoreV1().Pods(c.ns).Get(ctx, workerName(workerID), metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("k8s: observe %s: %w", workerID, err)
	}
	return generationOf(pod), true, nil
}

// ListRunning returns every worker ID the cluster currently hosts. It is the
// orphan sweeper's source of truth for what exists on the platform.
func (c *Client) ListRunning(ctx context.Context) ([]string, error) {
	pods, err := c.core.CoreV1().Pods(c.ns).List(ctx, metav1.ListOptions{
		LabelSelector: WorkerLabel,
	})
	if err != nil {
		return nil, fmt.Errorf("k8s: list worker pods: %w", err)
	}
	ids := make([]string, 0, len(pods.Items))
	for i := range pods.Items {
		if id := pods.Items[i].Labels[WorkerLabel]; id != "" {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// ensurePVC creates the session volume if absent. Sessions survive pod restarts,
// so a leftover PVC from a previous generation is intentionally reused.
func (c *Client) ensurePVC(ctx context.Context, w domain.Worker) error {
	name := domain.SessionPVCName(w.ID)
	_, err := c.core.CoreV1().PersistentVolumeClaims(c.ns).Get(ctx, name, metav1.GetOptions{})
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("k8s: get pvc: %w", err)
	}
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: c.ns,
			Labels:    workerLabels(w),
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("1Gi")},
			},
		},
	}
	if _, err := c.core.CoreV1().PersistentVolumeClaims(c.ns).Create(ctx, pvc, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("k8s: create pvc: %w", err)
	}
	return nil
}

// ensurePod creates the worker pod at the requested generation. An existing pod
// at the same or newer generation is a no-op; an older one is stale and deleted
// so a later pass recreates it fresh (self-healing without a separate op).
func (c *Client) ensurePod(ctx context.Context, w domain.Worker) error {
	name := workerName(w.ID)
	if existing, err := c.core.CoreV1().Pods(c.ns).Get(ctx, name, metav1.GetOptions{}); err == nil {
		if generationOf(existing) >= w.Generation {
			return nil
		}
		if err := ignoreMissing(c.core.CoreV1().Pods(c.ns).Delete(ctx, name, metav1.DeleteOptions{})); err != nil {
			return fmt.Errorf("k8s: delete stale pod: %w", err)
		}
	} else if !apierrors.IsNotFound(err) {
		return fmt.Errorf("k8s: get pod: %w", err)
	}

	allowPrivilegeEscalation := false
	runAsNonRoot := true
	runAsUser := int64(10001)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: c.ns,
			Labels:    workerLabels(w),
		},
		Spec: corev1.PodSpec{
			ServiceAccountName: "smm-provisioner",
			SecurityContext: &corev1.PodSecurityContext{
				RunAsNonRoot: &runAsNonRoot,
				RunAsUser:    &runAsUser,
			},
			Containers: []corev1.Container{{
				Name:  WorkerContainerName,
				Image: c.image,
				SecurityContext: &corev1.SecurityContext{
					AllowPrivilegeEscalation: &allowPrivilegeEscalation,
					Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
				},
				VolumeMounts: []corev1.VolumeMount{{
					Name:      SessionVolumeName,
					MountPath: "/data/sessions",
				}},
			}},
			Volumes: []corev1.Volume{{
				Name: SessionVolumeName,
				VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
						ClaimName: domain.SessionPVCName(w.ID),
					},
				},
			}},
		},
	}
	if _, err := c.core.CoreV1().Pods(c.ns).Create(ctx, pod, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("k8s: create pod: %w", err)
	}
	return nil
}

// ensureService exposes the noVNC live-screen port inside the cluster only
// (INFRA_ANALYST.md §7: noVNC must never be published).
func (c *Client) ensureService(ctx context.Context, w domain.Worker) error {
	name := workerName(w.ID)
	_, err := c.core.CoreV1().Services(c.ns).Get(ctx, name, metav1.GetOptions{})
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("k8s: get service: %w", err)
	}
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: c.ns,
			Labels:    workerLabels(w),
		},
		Spec: corev1.ServiceSpec{
			Selector: workerLabels(w),
			Ports: []corev1.ServicePort{{
				Name:       "novnc",
				Port:       6080,
				TargetPort: intOrString(6080),
				Protocol:   corev1.ProtocolTCP,
			}},
		},
	}
	if _, err := c.core.CoreV1().Services(c.ns).Create(ctx, svc, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("k8s: create service: %w", err)
	}
	return nil
}

// workerLabels are the selectors tying a worker's resources together.
func workerLabels(w domain.Worker) map[string]string {
	return map[string]string{
		WorkerLabel:     w.ID,
		GenerationLabel: fmt.Sprintf("%d", w.Generation),
	}
}

// workerName is the pod/service name (the PVC keeps its smm-session-<id> name).
func workerName(workerID string) string { return "smm-worker-" + workerID }

// generationOf reads the generation label, tolerating malformed values.
func generationOf(pod *corev1.Pod) int {
	if pod == nil || pod.Labels == nil {
		return 0
	}
	var n int
	if _, err := fmt.Sscanf(pod.Labels[GenerationLabel], "%d", &n); err != nil {
		return 0
	}
	return n
}
