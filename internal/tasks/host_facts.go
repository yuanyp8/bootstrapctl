package tasks

import (
	"context"
	"fmt"
	"strings"

	"github.com/yuanyp8/bootstrapctl/internal/config"
	"github.com/yuanyp8/bootstrapctl/internal/facts"
	"github.com/yuanyp8/bootstrapctl/internal/remote"
	"github.com/yuanyp8/bootstrapctl/internal/report"
)

// HostFactsTask 对应 k0sctl gather-facts / Kubespray preinstall 的第一层：
// 先识别发行版和宿主机能力，后续任务再基于 capability 做条件化收敛。
type HostFactsTask struct {
	NodeSpec config.NodeConnection
}

func (t *HostFactsTask) Key() string   { return "host-facts" }
func (t *HostFactsTask) Title() string { return "采集主机平台与 preinstall 事实" }
func (t *HostFactsTask) Node() string  { return t.NodeSpec.Name }

func (t *HostFactsTask) Check(ctx context.Context, exec remote.Executor) (CheckResult, error) {
	result, err := runScript(ctx, exec, t.NodeSpec, hostFactsScript())
	if err != nil {
		return CheckResult{}, err
	}
	if result.ExitCode != 0 {
		return CheckResult{}, fmt.Errorf("采集主机事实失败: %s", strings.TrimSpace(result.Output))
	}

	hostFacts, err := facts.ParseHostFactsOutput(result.Output)
	if err != nil {
		return CheckResult{}, err
	}
	changes, warnings := hostFactsChangeRecords(hostFacts)
	return CheckResult{
		Needed:   false,
		Summary:  fmt.Sprintf("已识别 %s %s / %s / %s", hostFacts.OSID, hostFacts.OSVersion, hostFacts.Arch, hostFacts.InitSystem),
		Changes:  changes,
		Warnings: warnings,
	}, nil
}

func (t *HostFactsTask) Apply(context.Context, remote.Executor) (ApplyResult, error) {
	return ApplyResult{Changed: false, Summary: "主机事实任务只读，不执行修改"}, nil
}

func hostFactsScript() string {
	return `
emit_fact() {
  key="$1"
  value="$2"
  encoded="$(printf '%s' "$value" | base64 | tr -d '\n')"
  printf '__BT_FACT__|%s|%s\n' "$key" "$encoded"
}

os_id="unknown"
os_version="unknown"
os_id_like=""
os_pretty_name="unknown"
if [ -f /etc/os-release ]; then
  . /etc/os-release
  os_id="${ID:-unknown}"
  os_version="${VERSION_ID:-unknown}"
  os_id_like="${ID_LIKE:-}"
  os_pretty_name="${PRETTY_NAME:-unknown}"
fi

package_manager="unknown"
for candidate in apt-get dnf yum zypper apk pacman; do
  if command -v "$candidate" >/dev/null 2>&1; then
    package_manager="$candidate"
    break
  fi
done

init_system="$(ps -p 1 -o comm= 2>/dev/null | xargs || true)"
[ -n "$init_system" ] || init_system="unknown"

cgroup_version="v1"
if [ -f /sys/fs/cgroup/cgroup.controllers ]; then
  cgroup_version="v2"
elif mount 2>/dev/null | grep -q 'type cgroup2'; then
  cgroup_version="v2"
fi

resolv_conf_target="$(readlink -f /etc/resolv.conf 2>/dev/null || echo /etc/resolv.conf)"
resolv_conf_link="$(readlink /etc/resolv.conf 2>/dev/null || echo regular-file)"
nameservers="$(awk '/^[[:space:]]*nameserver[[:space:]]+/ {print $2}' /etc/resolv.conf 2>/dev/null | paste -sd ',' - || true)"
search_domains="$(awk '/^[[:space:]]*(search|domain)[[:space:]]+/ {$1=""; sub(/^[[:space:]]+/, ""); print}' /etc/resolv.conf 2>/dev/null | paste -sd ',' - || true)"
default_route="$(ip route show default 2>/dev/null | head -n 1 || true)"

timezone="$(timedatectl show -p Timezone --value 2>/dev/null || true)"
if [ -z "$timezone" ] && [ -f /etc/timezone ]; then
  timezone="$(cat /etc/timezone 2>/dev/null || true)"
fi
[ -n "$timezone" ] || timezone="unknown"

ntp_synchronized="$(timedatectl show -p NTPSynchronized --value 2>/dev/null || true)"
[ -n "$ntp_synchronized" ] || ntp_synchronized="unknown"

time_sync_service="missing"
if command -v systemctl >/dev/null 2>&1; then
  for service in chronyd chrony systemd-timesyncd ntpd; do
    if systemctl is-active "$service" >/dev/null 2>&1; then
      time_sync_service="$service"
      break
    fi
  done
fi

emit_fact os_id "$os_id"
emit_fact os_version "$os_version"
emit_fact os_id_like "$os_id_like"
emit_fact os_pretty_name "$os_pretty_name"
emit_fact arch "$(uname -m 2>/dev/null || echo unknown)"
emit_fact kernel "$(uname -r 2>/dev/null || echo unknown)"
emit_fact init_system "$init_system"
emit_fact package_manager "$package_manager"
emit_fact cgroup_version "$cgroup_version"
emit_fact resolv_conf_target "$resolv_conf_target"
emit_fact resolv_conf_link "$resolv_conf_link"
emit_fact nameservers "$nameservers"
emit_fact search_domains "$search_domains"
emit_fact default_route "$default_route"
emit_fact timezone "$timezone"
emit_fact ntp_synchronized "$ntp_synchronized"
emit_fact time_sync_service "$time_sync_service"
echo OK
`
}

func hostFactsChangeRecords(host facts.HostFacts) ([]report.ChangeRecord, []string) {
	var changes []report.ChangeRecord
	var warnings []string

	observe := func(category, resource, value, desired, evidence string) report.ChangeRecord {
		return report.ChangeRecord{
			Category:  category,
			Resource:  resource,
			Operation: "observe",
			Before:    value,
			After:     value,
			Effective: value,
			Desired:   desired,
			Changed:   false,
			Verified:  true,
			Status:    report.ChangeStatusCompliant,
			Evidence:  evidence,
		}
	}

	changes = append(changes,
		observe("platform", "os.release", strings.TrimSpace(host.OSPrettyName), "supported-distribution", "/etc/os-release"),
		observe("platform", "os.family", host.DistroFamily, "known", "ID/ID_LIKE normalization"),
		observe("platform", "architecture", host.Arch, "amd64-or-arm64", "uname -m"),
		observe("platform", "kernel.release", host.Kernel, "observed", "uname -r"),
		observe("platform", "init.system", host.InitSystem, "systemd", "ps -p 1 -o comm="),
		observe("platform", "package.manager", host.PackageManager, "known", "command discovery"),
		observe("kubernetes-preinstall", "cgroup.version", host.CgroupVersion, "observed", "/sys/fs/cgroup/cgroup.controllers"),
		observe("dns", "resolv.conf.target", host.ResolvConfTarget, "observed", "readlink -f /etc/resolv.conf"),
		observe("dns", "resolv.conf.link", host.ResolvConfLink, "observed", "readlink /etc/resolv.conf"),
		observe("dns", "nameservers", host.Nameservers, "non-empty", "/etc/resolv.conf"),
		observe("network", "default-route", host.DefaultRoute, "present", "ip route show default"),
		observe("time", "timezone", host.Timezone, "configured", "timedatectl show -p Timezone"),
		observe("time", "ntp.synchronized", host.NTPSynchronized, "yes", "timedatectl show -p NTPSynchronized"),
		observe("time", "time-sync.service", host.TimeSyncService, "active", "systemctl is-active chronyd/chrony/systemd-timesyncd/ntpd"),
	)

	warn := func(resource, message string) {
		warnings = append(warnings, message)
		for idx := range changes {
			if changes[idx].Resource != resource {
				continue
			}
			changes[idx].Verified = false
			changes[idx].Status = report.ChangeStatusObservedWarning
			changes[idx].Message = message
		}
	}

	if host.DistroFamily == "unknown" {
		warn("os.family", "无法识别发行版 family，后续包名和服务名不能安全自动映射")
	}
	if host.InitSystem != "systemd" {
		warn("init.system", fmt.Sprintf("PID 1 为 %s，当前 Kubernetes 主机策略主要面向 systemd", host.InitSystem))
	}
	if host.PackageManager == "unknown" {
		warn("package.manager", "未识别包管理器，Kubernetes 必需包只能检查不能自动安装")
	}
	if strings.TrimSpace(host.Nameservers) == "" {
		warn("nameservers", "/etc/resolv.conf 未发现 nameserver")
	}
	if strings.TrimSpace(host.DefaultRoute) == "" {
		warn("default-route", "未发现默认路由")
	}
	if host.NTPSynchronized != "yes" {
		warn("ntp.synchronized", fmt.Sprintf("主机时间尚未确认同步，当前值=%s", host.NTPSynchronized))
	}
	if host.TimeSyncService == "missing" {
		warn("time-sync.service", "未发现处于 active 状态的时间同步服务")
	}
	return changes, warnings
}
