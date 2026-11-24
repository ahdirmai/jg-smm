package http

import (
	"testing"

	"github.com/ahdirmai/jg-smm/apps/api/internal/domain"
	"github.com/ahdirmai/jg-smm/apps/api/internal/http/oapigen"
)

// The queue status crosses a case boundary: the DB/domain enum is lower-case
// and the OpenAPI/UI contract is upper-case. The old pass-through made the
// dashboard's FAILED tally zero and hid the Retry controls.
func TestJobToDTOUppercasesQueueStatus(t *testing.T) {
	for _, tc := range []struct {
		in   domain.JobStatus
		want oapigen.JobStatus
	}{
		{domain.JobStatusPending, oapigen.JobStatusPENDING},
		{domain.JobStatusRunning, oapigen.JobStatusRUNNING},
		{domain.JobStatusSuccess, oapigen.JobStatusSUCCESS},
		{domain.JobStatusFailed, oapigen.JobStatusFAILED},
		{domain.JobStatusCancelled, oapigen.JobStatusCANCELLED},
	} {
		got := jobToDTO(domain.ActionJob{Status: tc.in}).Status
		if got != tc.want {
			t.Errorf("jobToDTO(%q).Status = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// The queue history must name the machine that accepted the job. worker_id was
// persisted but omitted from the wire, so every row rendered "unassigned".
func TestJobToDTOExposesWorkerID(t *testing.T) {
	row := jobToDTO(domain.ActionJob{ID: "j1", WorkerID: "w-123"})
	if !row.WorkerId.IsSpecified() || row.WorkerId.IsNull() || row.WorkerId.MustGet() != "w-123" {
		t.Fatalf("workerId = %+v, want w-123", row.WorkerId)
	}
	empty := jobToDTO(domain.ActionJob{ID: "j2"})
	if empty.WorkerId.IsSpecified() && !empty.WorkerId.IsNull() {
		t.Fatalf("unassigned job leaked workerId = %+v", empty.WorkerId)
	}
}
