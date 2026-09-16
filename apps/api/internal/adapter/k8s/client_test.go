package k8s

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
)

func TestWorkerName(t *testing.T) {
	if got, want := workerName("abc-123"), "smm-worker-abc-123"; got != want {
		t.Fatalf("workerName = %q, want %q", got, want)
	}
}

func TestWorkerLabelsCarryGeneration(t *testing.T) {
	w := domain.Worker{ID: "w1", Generation: 7}
	lbl := workerLabels(w)
	if lbl[WorkerLabel] != "w1" {
		t.Fatalf("worker label = %q", lbl[WorkerLabel])
	}
	if lbl[GenerationLabel] != "7" {
		t.Fatalf("generation label = %q", lbl[GenerationLabel])
	}
}

// TestGenerationOf covers the label parsing the reconciler's diff depends on:
// a malformed or missing label must read as 0, never panic or misreport.
func TestGenerationOf(t *testing.T) {
	cases := []struct {
		name string
		pod  *corev1.Pod
		want int
	}{
		{"valid", podWithGeneration("4"), 4},
		{"missing", &corev1.Pod{}, 0},
		{"malformed", podWithGeneration("abc"), 0},
		{"zero", podWithGeneration("0"), 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := generationOf(c.pod); got != c.want {
				t.Fatalf("generationOf = %d, want %d", got, c.want)
			}
		})
	}
}

func podWithGeneration(s string) *corev1.Pod {
	return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{GenerationLabel: s}}}
}
