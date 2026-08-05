package facts

import (
	"encoding/base64"
	"fmt"
	"testing"
)

func TestParseHostFactsOutput(t *testing.T) {
	line := func(key, value string) string {
		return fmt.Sprintf("__BT_FACT__|%s|%s\n", key, base64.StdEncoding.EncodeToString([]byte(value)))
	}
	output := "noise\n" +
		line("os_id", "rocky") +
		line("os_version", "9.4") +
		line("os_id_like", "rhel centos fedora") +
		line("arch", "x86_64") +
		line("init_system", "systemd") +
		line("package_manager", "dnf") +
		line("ntp_synchronized", "yes")

	got, err := ParseHostFactsOutput(output)
	if err != nil {
		t.Fatalf("ParseHostFactsOutput() error = %v", err)
	}
	if got.DistroFamily != "rpm" || got.Arch != "amd64" {
		t.Fatalf("unexpected normalized facts: %+v", got)
	}
	if got.NTPSynchronized != "yes" {
		t.Fatalf("unexpected time sync value: %+v", got)
	}
}

func TestDetectDistroFamily(t *testing.T) {
	tests := []struct {
		id     string
		idLike string
		want   string
	}{
		{id: "ubuntu", idLike: "debian", want: "debian"},
		{id: "rocky", idLike: "rhel centos fedora", want: "rpm"},
		{id: "openEuler", idLike: "", want: "rpm"},
		{id: "alpine", idLike: "", want: "alpine"},
	}
	for _, test := range tests {
		if got := DetectDistroFamily(test.id, test.idLike); got != test.want {
			t.Fatalf("DetectDistroFamily(%q, %q) = %q, want %q", test.id, test.idLike, got, test.want)
		}
	}
}
