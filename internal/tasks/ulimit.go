package tasks

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/yuanyp8/bootstrapctl/internal/config"
	"github.com/yuanyp8/bootstrapctl/internal/remote"
	"github.com/yuanyp8/bootstrapctl/internal/report"
)

const ulimitConfigPath = "/etc/security/limits.d/99-bootstrapctl.conf"

type UlimitTask struct {
	NodeSpec config.NodeConnection
	NoFile   int
	NProc    int
}

func (t *UlimitTask) Key() string   { return "ulimit" }
func (t *UlimitTask) Title() string { return "收敛登录用户资源限制" }
func (t *UlimitTask) Node() string  { return t.NodeSpec.Name }

func (t *UlimitTask) desiredContent() string {
	return fmt.Sprintf(`* soft nofile %d
* hard nofile %d
* soft nproc %d
* hard nproc %d
root soft nofile %d
root hard nofile %d
root soft nproc %d
root hard nproc %d`, t.NoFile, t.NoFile, t.NProc, t.NProc, t.NoFile, t.NoFile, t.NProc, t.NProc)
}

func (t *UlimitTask) Check(ctx context.Context, exec remote.Executor) (CheckResult, error) {
	result, err := runScript(ctx, exec, t.NodeSpec, t.renderObservationScript())
	if err != nil {
		return CheckResult{}, err
	}
	if result.ExitCode != 0 {
		return CheckResult{}, fmt.Errorf("检查登录用户资源限制失败: %s", strings.TrimSpace(result.Output))
	}

	status := parseStatusLine(result.Output, "OK", "CHANGE")
	changes, pendingActions := parseUlimitChanges(result.Output, false)
	switch status {
	case "OK":
		summary := "登录用户 limits 配置已满足要求"
		if len(pendingActions) > 0 {
			summary += "，但当前会话仍需重新登录后验证生效值"
		}
		return CheckResult{
			Needed:        false,
			Summary:       summary,
			Changes:       changes,
			PendingActions: pendingActions,
		}, nil
	case "CHANGE":
		return CheckResult{
			Needed:  true,
			Summary: "登录用户 limits 配置需要更新",
			Changes: changes,
		}, nil
	default:
		return CheckResult{}, fmt.Errorf("无法解析 ulimit 检查结果: %s", status)
	}
}

func (t *UlimitTask) Apply(ctx context.Context, exec remote.Executor) (ApplyResult, error) {
	expectedB64 := encodeBase64(t.desiredContent())
	script := fmt.Sprintf(`
set -e
target=%q
mkdir -p /etc/security/limits.d
printf '%%s' '%s' | base64 -d > "$target"
chmod 644 "$target"
`, ulimitConfigPath, expectedB64) + t.renderObservationScript()

	result, err := runScript(ctx, exec, t.NodeSpec, script)
	if err != nil {
		return ApplyResult{}, err
	}
	if result.ExitCode != 0 {
		return ApplyResult{}, fmt.Errorf("写入 ulimit 配置失败: %s", strings.TrimSpace(result.Output))
	}

	changes, pendingActions := parseUlimitChanges(result.Output, true)
	summary := "登录用户 limits 配置已写入 " + ulimitConfigPath
	if len(pendingActions) > 0 {
		summary += "；新登录会话生效后需再次 verify"
	}
	return ApplyResult{
		Changed:        true,
		Summary:       summary,
		Changes:       changes,
		PendingActions: pendingActions,
	}, nil
}

func (t *UlimitTask) renderObservationScript() string {
	return fmt.Sprintf(`
target=%q
read_limit() {
  domain="$1"
  level="$2"
  item="$3"
  if [ ! -f "$target" ]; then
    printf 'missing'
    return
  fi
  value="$(awk -v domain="$domain" -v level="$level" -v item="$item" '$1 == domain && $2 == level && $3 == item {print $4; exit}' "$target")"
  [ -n "$value" ] && printf '%%s' "$value" || printf 'missing'
}

emit_limit() {
  item="$1"
  level="$2"
  desired="$3"
  effective="$4"
  star="$(read_limit '*' "$level" "$item")"
  root="$(read_limit 'root' "$level" "$item")"
  printf '__BT_LIMIT__|%%s|%%s|%%s|%%s|%%s|%%s\n' "$item" "$level" "$star" "$root" "$desired" "$effective"
  if [ "$star" != "$desired" ] || [ "$root" != "$desired" ]; then
    need=1
  fi
}

need=0
emit_limit nofile soft %q "$(ulimit -Sn 2>/dev/null || echo unknown)"
emit_limit nofile hard %q "$(ulimit -Hn 2>/dev/null || echo unknown)"
emit_limit nproc soft %q "$(ulimit -Su 2>/dev/null || echo unknown)"
emit_limit nproc hard %q "$(ulimit -Hu 2>/dev/null || echo unknown)"

if [ "$need" -eq 0 ]; then
  echo OK
else
  echo CHANGE
fi
`, ulimitConfigPath, strconv.Itoa(t.NoFile), strconv.Itoa(t.NoFile), strconv.Itoa(t.NProc), strconv.Itoa(t.NProc))
}

func parseUlimitChanges(output string, changed bool) ([]report.ChangeRecord, []string) {
	var changes []report.ChangeRecord
	pendingRelogin := false

	for _, rawLine := range strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(rawLine)
		if !strings.HasPrefix(line, "__BT_LIMIT__|") {
			continue
		}
		parts := strings.SplitN(line, "|", 7)
		if len(parts) != 7 {
			continue
		}

		item := strings.TrimSpace(parts[1])
		level := strings.TrimSpace(parts[2])
		star := strings.TrimSpace(parts[3])
		root := strings.TrimSpace(parts[4])
		desired := strings.TrimSpace(parts[5])
		effective := strings.TrimSpace(parts[6])
		configured := star == desired && root == desired
		effectiveNow := effective == desired

		change := report.ChangeRecord{
			Category:  "resource-limit",
			Resource:  fmt.Sprintf("login-limits.%s.%s", item, level),
			Path:      ulimitConfigPath,
			Operation: "write-managed-file",
			Before:    fmt.Sprintf("*=%s,root=%s", star, root),
			Desired:   desired,
			Effective: effective,
			Changed:   changed,
			Verified:  configured && effectiveNow,
			Evidence:  fmt.Sprintf("%s；ulimit -%s%s", ulimitConfigPath, limitLevelFlag(level), limitItemFlag(item)),
		}
		if changed {
			change.After = fmt.Sprintf("*=%s,root=%s", desired, desired)
		}

		switch {
		case !configured:
			change.Status = report.ChangeStatusNeedsChange
			change.Message = "配置文件中的 wildcard/root 值尚未达到目标"
		case effectiveNow && changed:
			change.Status = report.ChangeStatusChangedVerified
			change.Message = "配置值和当前会话生效值均达到目标"
		case effectiveNow:
			change.Status = report.ChangeStatusCompliant
			change.Message = "配置值和当前会话生效值均达到目标"
		case changed:
			change.Status = report.ChangeStatusChangedPendingRelogin
			change.Message = "配置文件已更新，当前 SSH/sudo 会话仍保留旧限制"
			change.PendingAction = "重新登录后执行 bootstrapctl verify"
			pendingRelogin = true
		default:
			change.Status = "configured-pending-relogin"
			change.Message = "配置文件已满足目标，但当前会话仍保留旧限制"
			change.PendingAction = "重新登录后执行 bootstrapctl verify"
			pendingRelogin = true
		}
		changes = append(changes, change)
	}

	var pendingActions []string
	if pendingRelogin {
		pendingActions = []string{"重新建立 SSH 登录会话后执行 bootstrapctl verify，确认 PAM limits 的实际生效值"}
	}
	return changes, pendingActions
}

func limitLevelFlag(level string) string {
	if level == "hard" {
		return "H"
	}
	return "S"
}

func limitItemFlag(item string) string {
	if item == "nproc" {
		return "u"
	}
	return "n"
}
