package report

import "strings"

const (
	ChangeStatusCompliant             = "compliant"
	ChangeStatusNeedsChange           = "needs-change"
	ChangeStatusWouldChange           = "would-change"
	ChangeStatusChangedVerified       = "changed-verified"
	ChangeStatusChangedPendingRelogin = "changed-pending-relogin"
	ChangeStatusChangedPendingRestart = "changed-pending-restart"
	ChangeStatusChangedPendingReboot  = "changed-pending-reboot"
	ChangeStatusPreserved             = "preserved"
	ChangeStatusObservedWarning       = "observed-warning"
	ChangeStatusSkipped               = "skipped"
	ChangeStatusFailed                = "failed"
)

// ChangeRecord 描述一个可审计的主机状态变化。
//
// 每条记录应该尽量回答：
// 1. 原值是什么
// 2. 目标值是什么
// 3. 做了什么动作
// 4. 修改后是什么
// 5. 当前是否已经真正生效
//
// 字段暂时统一使用字符串，避免不同任务把 JSON 报告变成难以稳定消费的弱类型结构。
// 对复杂值可以存放规范化 JSON、摘要或哈希，并把原始证据放到 Evidence。
type ChangeRecord struct {
	Category      string `json:"category,omitempty"`
	Resource      string `json:"resource"`
	Path          string `json:"path,omitempty"`
	Operation     string `json:"operation,omitempty"`
	Before        string `json:"before,omitempty"`
	Desired       string `json:"desired,omitempty"`
	After         string `json:"after,omitempty"`
	Effective     string `json:"effective,omitempty"`
	Changed       bool   `json:"changed"`
	Verified      bool   `json:"verified"`
	Status        string `json:"status"`
	Evidence      string `json:"evidence,omitempty"`
	Message       string `json:"message,omitempty"`
	PendingAction string `json:"pending_action,omitempty"`
}

// MergeChanges 把 Check 阶段的 before/desired 与 Apply 阶段的 after/effective 合并。
// Resource + Path 被视为一条记录的稳定身份。
func MergeChanges(checked, applied []ChangeRecord) []ChangeRecord {
	if len(checked) == 0 && len(applied) == 0 {
		return nil
	}

	merged := make([]ChangeRecord, len(checked))
	copy(merged, checked)
	indexes := make(map[string]int, len(merged))
	for idx := range merged {
		indexes[changeKey(merged[idx])] = idx
	}

	for _, update := range applied {
		key := changeKey(update)
		idx, exists := indexes[key]
		if !exists {
			indexes[key] = len(merged)
			merged = append(merged, update)
			continue
		}
		merged[idx] = mergeChange(merged[idx], update)
	}
	return merged
}

func changeKey(change ChangeRecord) string {
	return strings.TrimSpace(change.Resource) + "\x00" + strings.TrimSpace(change.Path)
}

func mergeChange(base, update ChangeRecord) ChangeRecord {
	if update.Category != "" {
		base.Category = update.Category
	}
	if update.Resource != "" {
		base.Resource = update.Resource
	}
	if update.Path != "" {
		base.Path = update.Path
	}
	if update.Operation != "" {
		base.Operation = update.Operation
	}
	if update.Before != "" && base.Before == "" {
		base.Before = update.Before
	}
	if update.Desired != "" {
		base.Desired = update.Desired
	}
	if update.After != "" {
		base.After = update.After
	}
	if update.Effective != "" {
		base.Effective = update.Effective
	}
	base.Changed = base.Changed || update.Changed
	base.Verified = base.Verified || update.Verified
	if update.Status != "" {
		base.Status = update.Status
	}
	if update.Evidence != "" {
		base.Evidence = update.Evidence
	}
	if update.Message != "" {
		base.Message = update.Message
	}
	if update.PendingAction != "" {
		base.PendingAction = update.PendingAction
	}
	return base
}
