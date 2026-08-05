package tasks

import (
	"testing"

	"github.com/yuanyp8/bootstrapctl/internal/facts"
	"github.com/yuanyp8/bootstrapctl/internal/report"
)

func TestHostFactsChangeRecordsWarnOnMissingPreinstallCapabilities(t *testing.T) {
	changes, warnings := hostFactsChangeRecords(facts.HostFacts{
		OSPrettyName:     "Unknown Linux",
		DistroFamily:     "unknown",
		Arch:             "amd64",
		Kernel:           "6.8.0",
		InitSystem:       "openrc",
		PackageManager:   "unknown",
		CgroupVersion:    "v2",
		ResolvConfTarget: "/etc/resolv.conf",
		ResolvConfLink:   "regular-file",
		Nameservers:      "",
		DefaultRoute:     "",
		Timezone:         "UTC",
		NTPSynchronized: "no",
		TimeSyncService:  "missing",
	})
	if len(warnings) != 7 {
		t.Fatalf("expected seven warnings, got %d: %+v", len(warnings), warnings)
	}
	if len(changes) == 0 {
		t.Fatalf("expected structured fact records")
	}
	var familyWarning bool
	for _, change := range changes {
		if change.Resource == "os.family" && change.Status == report.ChangeStatusObservedWarning {
			familyWarning = true
		}
	}
	if !familyWarning {
		t.Fatalf("expected os.family warning record: %+v", changes)
	}
}
