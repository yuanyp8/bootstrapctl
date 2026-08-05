package report

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Report struct {
	RunID       string       `json:"run_id"`
	Command     string       `json:"command"`
	ClusterName string       `json:"cluster_name"`
	StartedAt   time.Time    `json:"started_at"`
	FinishedAt  time.Time    `json:"finished_at"`
	DryRun      bool         `json:"dry_run"`
	Results     []TaskResult `json:"results"`
}

type TaskResult struct {
	Node           string         `json:"node"`
	TaskKey        string         `json:"task_key"`
	Title          string         `json:"title"`
	Status         string         `json:"status"`
	Summary        string         `json:"summary"`
	Changes        []ChangeRecord `json:"changes,omitempty"`
	Warnings       []string       `json:"warnings,omitempty"`
	PendingActions []string       `json:"pending_actions,omitempty"`
	StartedAt      time.Time      `json:"started_at"`
	FinishedAt     time.Time      `json:"finished_at"`
}

func New(command, clusterName string, dryRun bool) *Report {
	return &Report{
		RunID:       strings.ReplaceAll(time.Now().Format("20060102-150405.000"), ".", "-"),
		Command:     command,
		ClusterName: clusterName,
		StartedAt:   time.Now(),
		DryRun:      dryRun,
	}
}

func (r *Report) Add(result TaskResult) {
	r.Results = append(r.Results, result)
}

func (r *Report) Finalize() {
	r.FinishedAt = time.Now()
}

// SaveJSON 保持原有返回值兼容，同时落一份同 RunID 的 Markdown 交付报告。
func (r *Report) SaveJSON(reportDir string) (string, error) {
	r.Finalize()
	if err := os.MkdirAll(reportDir, 0o755); err != nil {
		return "", fmt.Errorf("创建报告目录失败: %w", err)
	}
	path := filepath.Join(reportDir, r.RunID+".json")
	content, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", fmt.Errorf("生成 JSON 报告失败: %w", err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		return "", fmt.Errorf("写入报告失败: %w", err)
	}
	if _, err := r.SaveMarkdown(reportDir); err != nil {
		return "", fmt.Errorf("JSON 已写入但 Markdown 交付报告生成失败: %w", err)
	}
	return path, nil
}

// SaveMarkdown 输出适合交付审阅的动作报告。
func (r *Report) SaveMarkdown(reportDir string) (string, error) {
	if r.FinishedAt.IsZero() {
		r.Finalize()
	}
	if err := os.MkdirAll(reportDir, 0o755); err != nil {
		return "", fmt.Errorf("创建报告目录失败: %w", err)
	}

	var builder strings.Builder
	builder.WriteString("# bootstrapctl 主机变更报告\n\n")
	builder.WriteString(fmt.Sprintf("- run id: `%s`\n", r.RunID))
	builder.WriteString(fmt.Sprintf("- command: `%s`\n", r.Command))
	builder.WriteString(fmt.Sprintf("- cluster: `%s`\n", r.ClusterName))
	builder.WriteString(fmt.Sprintf("- dry run: `%t`\n", r.DryRun))
	builder.WriteString(fmt.Sprintf("- started at: `%s`\n", r.StartedAt.Format(time.RFC3339)))
	builder.WriteString(fmt.Sprintf("- finished at: `%s`\n\n", r.FinishedAt.Format(time.RFC3339)))

	for _, result := range r.Results {
		builder.WriteString(fmt.Sprintf("## %s / %s\n\n", result.Node, result.Title))
		builder.WriteString(fmt.Sprintf("- task: `%s`\n", result.TaskKey))
		builder.WriteString(fmt.Sprintf("- status: `%s`\n", result.Status))
		builder.WriteString(fmt.Sprintf("- summary: %s\n", result.Summary))
		if len(result.Warnings) > 0 {
			builder.WriteString("- warnings:\n")
			for _, warning := range result.Warnings {
				builder.WriteString(fmt.Sprintf("  - %s\n", warning))
			}
		}
		if len(result.PendingActions) > 0 {
			builder.WriteString("- pending actions:\n")
			for _, action := range result.PendingActions {
				builder.WriteString(fmt.Sprintf("  - %s\n", action))
			}
		}
		builder.WriteString("\n")

		if len(result.Changes) == 0 {
			continue
		}
		builder.WriteString("| 类别 | 资源 | 修改前 | 目标值 | 修改后 | 生效值 | 动作 | 状态 |\n")
		builder.WriteString("|---|---|---|---|---|---|---|---|\n")
		for _, change := range result.Changes {
			builder.WriteString(fmt.Sprintf("| %s | %s | %s | %s | %s | %s | %s | %s |\n",
				markdownCell(change.Category),
				markdownCell(change.Resource),
				markdownCell(change.Before),
				markdownCell(change.Desired),
				markdownCell(change.After),
				markdownCell(change.Effective),
				markdownCell(change.Operation),
				markdownCell(change.Status),
			))
		}
		builder.WriteString("\n")
	}

	path := filepath.Join(reportDir, r.RunID+".md")
	if err := os.WriteFile(path, []byte(builder.String()), 0o644); err != nil {
		return "", fmt.Errorf("写入 Markdown 报告失败: %w", err)
	}
	return path, nil
}

func markdownCell(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "-"
	}
	value = strings.ReplaceAll(value, "|", "\\|")
	value = strings.ReplaceAll(value, "\r\n", "<br>")
	value = strings.ReplaceAll(value, "\n", "<br>")
	return value
}
