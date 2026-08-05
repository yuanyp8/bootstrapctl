package tasks

import (
	"testing"

	"github.com/yuanyp8/bootstrapctl/internal/report"
)

func TestParseUlimitChangesReportsPendingRelogin(t *testing.T) {
	output := `
__BT_LIMIT__|nofile|soft|1048576|1048576|1048576|65535
__BT_LIMIT__|nofile|hard|1048576|1048576|1048576|65535
__BT_LIMIT__|nproc|soft|1048576|1048576|1048576|4096
__BT_LIMIT__|nproc|hard|1048576|1048576|1048576|4096
OK
`
	changes, pending := parseUlimitChanges(output, true)
	if len(changes) != 4 {
		t.Fatalf("expected four changes, got %d", len(changes))
	}
	if len(pending) != 1 {
		t.Fatalf("expected one pending action, got %+v", pending)
	}
	for _, change := range changes {
		if change.Status != report.ChangeStatusChangedPendingRelogin {
			t.Fatalf("unexpected status: %+v", change)
		}
		if change.After == "" || change.Effective == "" {
			t.Fatalf("expected after/effective evidence: %+v", change)
		}
	}
}

func TestParseRuntimeStorageChangesDetectsMismatch(t *testing.T) {
	output := `
__BT_RUNTIME_STORAGE__|docker|ready|/var/lib/docker|/data/graphroot
__BT_RUNTIME_STORAGE__|containerd|ready|/data/containerd|/data/containerd
__BT_RUNTIME_STORAGE__|containers-storage|missing||/data/graphroot/containers/storage
OK
`
	changes, warnings := parseRuntimeStorageChanges(output)
	if len(changes) != 3 {
		t.Fatalf("expected three runtime records, got %d", len(changes))
	}
	if len(warnings) != 1 {
		t.Fatalf("expected one mismatch warning, got %+v", warnings)
	}
	if changes[0].Status != report.ChangeStatusObservedWarning {
		t.Fatalf("expected docker mismatch warning, got %+v", changes[0])
	}
	if changes[1].Status != report.ChangeStatusCompliant || !changes[1].Verified {
		t.Fatalf("expected containerd compliant result, got %+v", changes[1])
	}
	if changes[2].Status != report.ChangeStatusSkipped {
		t.Fatalf("expected missing containers storage to be skipped, got %+v", changes[2])
	}
}
