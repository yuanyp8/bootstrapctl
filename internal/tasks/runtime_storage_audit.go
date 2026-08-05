package tasks

import (
	"context"
	"fmt"
	"strings"

	"github.com/yuanyp8/bootstrapctl/internal/config"
	"github.com/yuanyp8/bootstrapctl/internal/remote"
	"github.com/yuanyp8/bootstrapctl/internal/report"
)

// RuntimeStorageAuditTask 观测容器运行时真正消费的数据目录。
//
// 现有 StorageLayoutTask 会创建目录并管理 containers/storage.conf，但 Docker
// 使用 daemon.json，containerd 使用 config.toml。仅仅创建 /data/containerd 并不能
// 证明 containerd 已经使用它。这个任务先把语义缺口暴露到报告中；后续再按明确
// policy 分别实现 Docker/containerd 的安全合并与重启策略。
type RuntimeStorageAuditTask struct {
	NodeSpec               config.NodeConnection
	ExpectedDockerRoot     string
	ExpectedContainerdRoot string
	ExpectedContainersRoot string
	StorageConfPath        string
}

func (t *RuntimeStorageAuditTask) Key() string   { return "runtime-storage-audit" }
func (t *RuntimeStorageAuditTask) Title() string { return "观测容器运行时实际存储目录" }
func (t *RuntimeStorageAuditTask) Node() string  { return t.NodeSpec.Name }

func (t *RuntimeStorageAuditTask) Check(ctx context.Context, exec remote.Executor) (CheckResult, error) {
	script := fmt.Sprintf(`
docker_state="missing"
docker_actual=""
if command -v docker >/dev/null 2>&1; then
  docker_state="installed-unavailable"
  docker_actual="$(docker info --format '{{.DockerRootDir}}' 2>/dev/null || true)"
  if [ -z "$docker_actual" ] && [ -f /etc/docker/daemon.json ]; then
    docker_actual="$(sed -n 's/.*"data-root"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' /etc/docker/daemon.json | head -n 1)"
  fi
  [ -n "$docker_actual" ] && docker_state="ready"
fi

containerd_state="missing"
containerd_actual=""
if command -v containerd >/dev/null 2>&1; then
  containerd_state="installed-unavailable"
  if [ -f /etc/containerd/config.toml ]; then
    containerd_actual="$(awk -F= '/^[[:space:]]*root[[:space:]]*=/ {gsub(/[[:space:]\"]/, "", $2); print $2; exit}' /etc/containerd/config.toml)"
  fi
  if [ -z "$containerd_actual" ]; then
    containerd_actual="$(containerd config dump 2>/dev/null | awk -F= '/^[[:space:]]*root[[:space:]]*=/ {gsub(/[[:space:]\"]/, "", $2); print $2; exit}' || true)"
  fi
  [ -n "$containerd_actual" ] && containerd_state="ready"
fi

containers_state="missing"
containers_actual=""
if [ -f "%s" ]; then
  containers_actual="$(awk -F= '/^[[:space:]]*graphroot[[:space:]]*=/ {gsub(/[[:space:]\"]/, "", $2); print $2; exit}' "%s")"
  if [ -n "$containers_actual" ]; then
    containers_state="ready"
  else
    containers_state="configured-unreadable"
  fi
fi

printf '__BT_RUNTIME_STORAGE__|docker|%%s|%%s|%%s\n' "$docker_state" "$docker_actual" "%s"
printf '__BT_RUNTIME_STORAGE__|containerd|%%s|%%s|%%s\n' "$containerd_state" "$containerd_actual" "%s"
printf '__BT_RUNTIME_STORAGE__|containers-storage|%%s|%%s|%%s\n' "$containers_state" "$containers_actual" "%s"
echo OK
`, t.StorageConfPath, t.StorageConfPath, t.ExpectedDockerRoot, t.ExpectedContainerdRoot, t.ExpectedContainersRoot)

	result, err := runScript(ctx, exec, t.NodeSpec, script)
	if err != nil {
		return CheckResult{}, err
	}
	if result.ExitCode != 0 {
		return CheckResult{}, fmt.Errorf("观测容器运行时存储目录失败: %s", strings.TrimSpace(result.Output))
	}

	changes, warnings := parseRuntimeStorageChanges(result.Output)
	return CheckResult{
		Needed:   false,
		Summary:  "已采集 Docker、containerd 与 containers/storage 的实际存储目录",
		Changes:  changes,
		Warnings: warnings,
	}, nil
}

func (t *RuntimeStorageAuditTask) Apply(context.Context, remote.Executor) (ApplyResult, error) {
	return ApplyResult{
		Changed: false,
		Summary: "运行时存储任务当前为观测模式，未修改 Docker 或 containerd 配置",
	}, nil
}

func parseRuntimeStorageChanges(output string) ([]report.ChangeRecord, []string) {
	var changes []report.ChangeRecord
	var warnings []string

	for _, rawLine := range strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(rawLine)
		if !strings.HasPrefix(line, "__BT_RUNTIME_STORAGE__|") {
			continue
		}
		parts := strings.SplitN(line, "|", 5)
		if len(parts) != 5 {
			continue
		}

		runtimeName := strings.TrimSpace(parts[1])
		state := strings.TrimSpace(parts[2])
		actual := strings.TrimSpace(parts[3])
		desired := strings.TrimSpace(parts[4])
		change := report.ChangeRecord{
			Category:  "container-runtime",
			Resource:  runtimeName + ".data-root",
			Operation: "observe",
			Before:    actual,
			After:     actual,
			Effective: actual,
			Desired:   desired,
			Changed:   false,
			Evidence:  runtimeStorageEvidence(runtimeName),
		}

		switch {
		case state == "missing":
			change.Status = report.ChangeStatusSkipped
			change.Message = "目标主机未检测到该运行时或配置文件"
		case state != "ready" || actual == "":
			change.Status = report.ChangeStatusObservedWarning
			change.Message = "已检测到运行时，但暂时无法确认实际数据目录"
			warnings = append(warnings, fmt.Sprintf("%s 已安装，但无法确认实际数据目录", runtimeName))
		case actual == desired:
			change.Status = report.ChangeStatusCompliant
			change.Verified = true
			change.Message = "运行时实际目录与期望值一致"
		default:
			change.Status = report.ChangeStatusObservedWarning
			change.Message = "运行时实际目录与 profile 期望目录不一致；当前仅报告，不自动改写"
			warnings = append(warnings, fmt.Sprintf("%s 实际目录 %s 与期望目录 %s 不一致", runtimeName, actual, desired))
		}
		changes = append(changes, change)
	}
	return changes, warnings
}

func runtimeStorageEvidence(runtimeName string) string {
	switch runtimeName {
	case "docker":
		return "docker info --format '{{.DockerRootDir}}'；/etc/docker/daemon.json"
	case "containerd":
		return "containerd config dump；/etc/containerd/config.toml"
	default:
		return "/etc/containers/storage.conf"
	}
}
