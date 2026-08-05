package runner

import (
	"context"
	"fmt"
	"time"

	"github.com/yuanyp8/bootstrapctl/internal/remote"
	"github.com/yuanyp8/bootstrapctl/internal/report"
	"github.com/yuanyp8/bootstrapctl/internal/tasks"
	"github.com/yuanyp8/bootstrapctl/internal/ui"
)

type Engine struct {
	Executor remote.Executor
	Console  *ui.Console
}

// Run 是任务引擎的统一入口。
// 它负责：
// 1. 对每个任务先执行 Check
// 2. 根据执行模式决定是仅展示、校验漂移还是正式 Apply
// 3. 将摘要、结构化变更、告警和待处理动作写入统一报告
func (e *Engine) Run(ctx context.Context, mode tasks.Mode, taskList []tasks.Task, dryRun bool, rep *report.Report) error {
	for _, task := range taskList {
		started := time.Now()
		e.Console.Info("[%s/%s] %s", task.Node(), task.Key(), task.Title())

		check, err := task.Check(ctx, e.Executor)
		if err != nil {
			rep.Add(newTaskResult(task, "failed", err.Error(), started, nil, nil, nil))
			return fmt.Errorf("任务 %s 失败: %w", task.Key(), err)
		}

		switch mode {
		case tasks.ModePlan:
			status := "ok"
			if check.Needed {
				status = "needs-change"
				e.Console.Warn("[%s/%s] %s", task.Node(), task.Key(), check.Summary)
			} else {
				e.Console.Success("[%s/%s] %s", task.Node(), task.Key(), check.Summary)
			}
			rep.Add(newTaskResult(task, status, check.Summary, started, check.Changes, check.Warnings, check.PendingActions))

		case tasks.ModeVerify:
			status := "ok"
			if check.Needed {
				status = "drift"
				e.Console.Warn("[%s/%s] %s", task.Node(), task.Key(), check.Summary)
			} else {
				e.Console.Success("[%s/%s] %s", task.Node(), task.Key(), check.Summary)
			}
			rep.Add(newTaskResult(task, status, check.Summary, started, check.Changes, check.Warnings, check.PendingActions))

		case tasks.ModeApply:
			if !check.Needed {
				e.Console.Success("[%s/%s] %s", task.Node(), task.Key(), check.Summary)
				rep.Add(newTaskResult(task, "ok", check.Summary, started, check.Changes, check.Warnings, check.PendingActions))
				continue
			}

			if dryRun {
				e.Console.Warn("[%s/%s] dry-run: %s", task.Node(), task.Key(), check.Summary)
				rep.Add(newTaskResult(task, "would-change", check.Summary, started, check.Changes, check.Warnings, check.PendingActions))
				continue
			}

			applyResult, err := task.Apply(ctx, e.Executor)
			if err != nil {
				rep.Add(newTaskResult(task, "failed", err.Error(), started, check.Changes, check.Warnings, check.PendingActions))
				return fmt.Errorf("任务 %s 执行失败: %w", task.Key(), err)
			}
			status := "ok"
			if applyResult.Changed {
				status = "changed"
			}
			e.Console.Success("[%s/%s] %s", task.Node(), task.Key(), applyResult.Summary)
			rep.Add(newTaskResult(
				task,
				status,
				applyResult.Summary,
				started,
				report.MergeChanges(check.Changes, applyResult.Changes),
				appendStrings(check.Warnings, applyResult.Warnings),
				appendStrings(check.PendingActions, applyResult.PendingActions),
			))

		default:
			return fmt.Errorf("不支持的执行模式: %s", mode)
		}
	}

	return nil
}

func newTaskResult(
	task tasks.Task,
	status string,
	summary string,
	started time.Time,
	changes []report.ChangeRecord,
	warnings []string,
	pendingActions []string,
) report.TaskResult {
	return report.TaskResult{
		Node:           task.Node(),
		TaskKey:        task.Key(),
		Title:          task.Title(),
		Status:         status,
		Summary:        summary,
		Changes:        append([]report.ChangeRecord(nil), changes...),
		Warnings:       append([]string(nil), warnings...),
		PendingActions: append([]string(nil), pendingActions...),
		StartedAt:      started,
		FinishedAt:     time.Now(),
	}
}

func appendStrings(first, second []string) []string {
	if len(first) == 0 && len(second) == 0 {
		return nil
	}
	result := make([]string, 0, len(first)+len(second))
	seen := make(map[string]struct{}, len(first)+len(second))
	for _, values := range [][]string{first, second} {
		for _, value := range values {
			if value == "" {
				continue
			}
			if _, exists := seen[value]; exists {
				continue
			}
			seen[value] = struct{}{}
			result = append(result, value)
		}
	}
	return result
}
