package facts

import (
	"encoding/base64"
	"fmt"
	"strings"
)

// HostFacts 是 Kubernetes preinstall 和生产主机基线共同依赖的规范化事实。
// 原始命令输出在远端编码后传回，解析层只负责结构化和发行版归类，不做修改。
type HostFacts struct {
	OSID             string `json:"os_id"`
	OSVersion        string `json:"os_version"`
	OSIDLike         string `json:"os_id_like"`
	OSPrettyName     string `json:"os_pretty_name"`
	DistroFamily     string `json:"distro_family"`
	Arch             string `json:"arch"`
	Kernel           string `json:"kernel"`
	InitSystem       string `json:"init_system"`
	PackageManager   string `json:"package_manager"`
	CgroupVersion    string `json:"cgroup_version"`
	ResolvConfTarget string `json:"resolv_conf_target"`
	ResolvConfLink   string `json:"resolv_conf_link"`
	Nameservers      string `json:"nameservers"`
	SearchDomains    string `json:"search_domains"`
	DefaultRoute     string `json:"default_route"`
	Timezone         string `json:"timezone"`
	NTPSynchronized string `json:"ntp_synchronized"`
	TimeSyncService  string `json:"time_sync_service"`
}

// ParseHostFactsOutput 解析 __BT_FACT__|key|base64(value) 格式。
func ParseHostFactsOutput(output string) (HostFacts, error) {
	values := map[string]string{}
	for _, rawLine := range strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(rawLine)
		if !strings.HasPrefix(line, "__BT_FACT__|") {
			continue
		}
		parts := strings.SplitN(line, "|", 3)
		if len(parts) != 3 {
			continue
		}
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(parts[2]))
		if err != nil {
			return HostFacts{}, fmt.Errorf("解析主机事实 %s 失败: %w", parts[1], err)
		}
		values[strings.TrimSpace(parts[1])] = strings.TrimSpace(string(decoded))
	}
	if len(values) == 0 {
		return HostFacts{}, fmt.Errorf("未找到主机事实标记")
	}

	result := HostFacts{
		OSID:             values["os_id"],
		OSVersion:        values["os_version"],
		OSIDLike:         values["os_id_like"],
		OSPrettyName:     values["os_pretty_name"],
		Arch:             NormalizeArch(values["arch"]),
		Kernel:           values["kernel"],
		InitSystem:       values["init_system"],
		PackageManager:   values["package_manager"],
		CgroupVersion:    values["cgroup_version"],
		ResolvConfTarget: values["resolv_conf_target"],
		ResolvConfLink:   values["resolv_conf_link"],
		Nameservers:      values["nameservers"],
		SearchDomains:    values["search_domains"],
		DefaultRoute:     values["default_route"],
		Timezone:         values["timezone"],
		NTPSynchronized: normalizeYesNo(values["ntp_synchronized"]),
		TimeSyncService:  values["time_sync_service"],
	}
	result.DistroFamily = DetectDistroFamily(result.OSID, result.OSIDLike)
	return result, nil
}

func NormalizeArch(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "x86_64", "x86-64", "amd64":
		return "amd64"
	case "aarch64", "arm64":
		return "arm64"
	case "armv7l", "armv7":
		return "arm"
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func DetectDistroFamily(id, idLike string) string {
	candidates := strings.Fields(strings.ToLower(strings.TrimSpace(id + " " + idLike)))
	for _, candidate := range candidates {
		switch candidate {
		case "ubuntu", "debian", "linuxmint", "uos":
			return "debian"
		case "rhel", "fedora", "centos", "rocky", "almalinux", "anolis", "opencloudos", "openeuler", "kylin":
			return "rpm"
		case "sles", "suse", "opensuse", "opensuse-leap":
			return "suse"
		case "alpine":
			return "alpine"
		case "arch", "manjaro":
			return "arch"
		}
	}
	return "unknown"
}

func normalizeYesNo(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "yes", "true", "1", "active", "synchronized":
		return "yes"
	case "no", "false", "0", "inactive", "unsynchronized":
		return "no"
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}
