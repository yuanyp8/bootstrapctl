package report

import (
	"os"
	"strings"
	"testing"
)

func TestMergeChangesPreservesBeforeAndAddsAfter(t *testing.T) {
	checked := []ChangeRecord{{
		Resource: "login-limits.nofile.soft",
		Path:     "/etc/security/limits.d/99-bootstrapctl.conf",
		Before:   "*=65535,root=65535",
		Desired:  "1048576",
		Status:   ChangeStatusNeedsChange,
	}}
	applied := []ChangeRecord{{
		Resource:      "login-limits.nofile.soft",
		Path:          "/etc/security/limits.d/99-bootstrapctl.conf",
		After:         "*=1048576,root=1048576",
		Effective:     "65535",
		Changed:       true,
		Status:        ChangeStatusChangedPendingRelogin,
		PendingAction: "重新登录后验证",
	}}

	got := MergeChanges(checked, applied)
	if len(got) != 1 {
		t.Fatalf("expected one change, got %d", len(got))
	}
	if got[0].Before != checked[0].Before || got[0].After != applied[0].After {
		t.Fatalf("unexpected merge result: %+v", got[0])
	}
	if !got[0].Changed || got[0].Status != ChangeStatusChangedPendingRelogin {
		t.Fatalf("unexpected merged status: %+v", got[0])
	}
}

func TestSaveMarkdownIncludesChangeTable(t *testing.T) {
	rep := New("apply", "demo", false)
	rep.Add(TaskResult{
		Node:    "node-01",
		TaskKey: "ulimit",
		Title:   "收敛登录用户资源限制",
		Status:  "changed",
		Summary: "已写入",
		Changes: []ChangeRecord{{
			Category:  "resource-limit",
			Resource:  "login-limits.nofile.soft",
			Before:    "65535",
			Desired:   "1048576",
			After:     "1048576",
			Effective: "65535",
			Operation: "write-managed-file",
			Status:    ChangeStatusChangedPendingRelogin,
		}},
	})

	path, err := rep.SaveMarkdown(t.TempDir())
	if err != nil {
		t.Fatalf("SaveMarkdown() error = %v", err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	text := string(content)
	for _, expected := range []string{"主机变更报告", "login-limits.nofile.soft", "changed-pending-relogin"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("markdown missing %q:\n%s", expected, text)
		}
	}
}
