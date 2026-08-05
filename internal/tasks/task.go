package tasks

import (
	"context"
	"encoding/base64"
	"fmt"
	"sort"
	"strings"

	"github.com/yuanyp8/bootstrapctl/internal/config"
	"github.com/yuanyp8/bootstrapctl/internal/remote"
	"github.com/yuanyp8/bootstrapctl/internal/report"
)

// Mode 表示任务执行模式。
type Mode string

const (
	ModePlan   Mode = "plan"
	ModeApply  Mode = "apply"
	ModeVerify Mode = "verify"
)

// Task 是 bootstrapctl 的最小执行单元。
// 每个任务都必须具备：
// 1. Check：判断是否存在漂移，并尽量记录 before/desired/effective
// 2. Apply：真正落变更，并记录 after/effective/verified
//
// 旧任务可以继续只返回 Summary；新任务应逐步补齐 Changes。
type Task interface {
	Key() string
	Title() string
	Node() string
	Check(ctx context.Context, exec remote.Executor) (CheckResult, error)
	Apply(ctx context.Context, exec remote.Executor) (ApplyResult, error)
}

type CheckResult struct {
	Needed         bool
	Summary        string
	Changes        []report.ChangeRecord
	Warnings       []string
	PendingActions []string
}

type ApplyResult struct {
	Changed        bool
	Summary        string
	Changes        []report.ChangeRecord
	Warnings       []string
	PendingActions []string
}

// Build 根据 inventory 与 profile 展开完整任务列表。
// 当前执行顺序是有意编排的：
// - 先做 SSH 连通性和主机事实采集
// - 再做 SSH、账号、主机名和 hosts
// - 再做 swap / SELinux / 防火墙 / 内核网络
// - 最后做目录、容器运行时存储观测与资源限制
func Build(inventory config.Inventory, profile config.Profile) []Task {
	nodes := inventory.ResolveNodes()
	taskList := make([]Task, 0, len(nodes)*16)
	controllerKeyTargets := map[string]struct{}{}
	controllerSSHConfigTargets := map[string]struct{}{}

	for _, node := range nodes {
		if profile.Features.SSHConnectivityEnabled() {
			taskList = append(taskList, &SSHConnectivityTask{NodeSpec: node})
		}
		// 其它任务都依赖发行版、init、包管理器、DNS、cgroup 和时间事实。
		// 即使关闭单独的 connectivity 展示任务，也仍然需要执行 facts 采集。
		taskList = append(taskList, &HostFactsTask{NodeSpec: node})

		if profile.Features.SSHAuthorizedKeyEnabled() {
			if node.Bastion != nil && strings.TrimSpace(node.Bastion.Host) != "" {
				bastionNode := bastionConnectionForNode(node)
				if _, exists := controllerKeyTargets[nodeIdentityKey(bastionNode)]; !exists {
					taskList = append(taskList, &SSHAuthorizedKeyTask{
						NodeSpec:       bastionNode,
						AuthorizedUser: resolveAuthorizedUser(profile.SSHKey.AuthorizedUser, bastionNode.SSHUser),
						PublicKey:      profile.SSHKey.ResolvedPublicKey,
					})
					controllerKeyTargets[nodeIdentityKey(bastionNode)] = struct{}{}
					if profile.SSHKey.ManageControllerSSHConfigEnabled() {
						taskList = append(taskList, &SSHControllerClientConfigTask{
							TargetNodeSpec:          bastionNode,
							ControllerKeyPath:       profile.SSHKey.GeneratedKeyPath,
							ControllerSSHConfigPath: profile.SSHKey.ControllerSSHConfigPath,
						})
						controllerSSHConfigTargets[nodeIdentityKey(bastionNode)] = struct{}{}
					}
				}
			}
			if _, exists := controllerKeyTargets[nodeIdentityKey(node)]; !exists {
				taskList = append(taskList, &SSHAuthorizedKeyTask{
					NodeSpec:       node,
					AuthorizedUser: resolveAuthorizedUser(profile.SSHKey.AuthorizedUser, node.SSHUser),
					PublicKey:      profile.SSHKey.ResolvedPublicKey,
				})
				controllerKeyTargets[nodeIdentityKey(node)] = struct{}{}
				if profile.SSHKey.ManageControllerSSHConfigEnabled() {
					taskList = append(taskList, &SSHControllerClientConfigTask{
						TargetNodeSpec:          node,
						ControllerKeyPath:       profile.SSHKey.GeneratedKeyPath,
						ControllerSSHConfigPath: profile.SSHKey.ControllerSSHConfigPath,
					})
					controllerSSHConfigTargets[nodeIdentityKey(node)] = struct{}{}
				}
			}
		}
		// bastion -> target 的二跳免密与客户端 SSH config，
		// 语义上属于“节点间互信链路”，不应该被“控制端公钥分发”总开关绑死。
		if node.Bastion != nil && strings.TrimSpace(node.Bastion.Host) != "" && profile.SSHKey.EnableBastionHopEnabled() {
			taskList = append(taskList, &SSHBastionHopKeyTask{
				TargetNodeSpec:  node,
				BastionNodeSpec: bastionConnectionForNode(node),
				AuthorizedUser:  resolveAuthorizedUser(profile.SSHKey.AuthorizedUser, node.SSHUser),
				BastionKeyPath:  profile.SSHKey.BastionKeyPath,
			})
			if profile.SSHKey.ManageBastionSSHConfigEnabled() {
				taskList = append(taskList, &SSHBastionClientConfigTask{
					TargetNodeSpec:       node,
					BastionNodeSpec:      bastionConnectionForNode(node),
					BastionKeyPath:       profile.SSHKey.BastionKeyPath,
					BastionSSHConfigPath: profile.SSHKey.BastionSSHConfigPath,
				})
			}
		}
		if profile.Features.ManagedAdminEnabled() {
			taskList = append(taskList, &ManagedAdminUserTask{
				NodeSpec:         node,
				Username:         profile.ManagedAdmin.Username,
				Password:         profile.ManagedAdmin.Password,
				PasswordHash:     profile.ManagedAdmin.PasswordHash,
				Shell:            profile.ManagedAdmin.Shell,
				PrimaryGroup:     profile.ManagedAdmin.PrimaryGroup,
				ExtraGroups:      append([]string(nil), profile.ManagedAdmin.ExtraGroups...),
				CreateHome:       profile.ManagedAdmin.CreateHomeEnabled(),
				GrantSudo:        profile.ManagedAdmin.GrantSudoEnabled(),
				SudoNoPasswd:     profile.ManagedAdmin.SudoNoPasswdEnabled(),
				InstallPublicKey: profile.ManagedAdmin.InstallControllerPublicKeyEnabled(),
				ControllerPubKey: profile.ManagedAdmin.ResolvedPublicKey,
			})
			if profile.ManagedAdmin.DisableRootSSHEnabled() {
				taskList = append(taskList, &RootSSHLoginPolicyTask{
					NodeSpec:        node,
					SSHDConfigPath:  profile.ManagedAdmin.SSHDConfigPath,
					PermitRootLogin: "no",
				})
			}
		}
		if profile.Features.HostnameEnabled() {
			taskList = append(taskList, &HostnameTask{NodeSpec: node, DesiredHostname: node.Name})
		}
		if profile.Features.HostsFileEnabled() {
			taskList = append(taskList, &HostsFileTask{NodeSpec: node, ClusterNodes: nodes})
		}
		if profile.Features.DisableSwapEnabled() {
			taskList = append(taskList, &SwapTask{NodeSpec: node})
		}
		if profile.Features.DisableSELinuxEnabled() {
			taskList = append(taskList, &SELinuxTask{NodeSpec: node})
		}
		if profile.Features.FirewallEnabled() {
			taskList = append(taskList, &FirewallTask{
				NodeSpec:        node,
				Mode:            profile.Firewall.Mode,
				ManageFirewalld: profile.Firewall.ManageFirewalldEnabled(),
				ManageUFW:       profile.Firewall.ManageUFWEnabled(),
				RequireIPTables: profile.Firewall.RequireIPTablesEnabled(),
			})
		}
		if profile.Features.KernelNetworkEnabled() {
			taskList = append(taskList, &KernelNetworkTask{
				NodeSpec: node,
				Modules:  profile.KernelNetwork.SortedModules(),
				Sysctls:  copySysctls(profile.KernelNetwork.Sysctls),
			})
		}
		if profile.Features.StorageEnabled() {
			taskList = append(taskList, &StorageLayoutTask{
				NodeSpec:        node,
				GraphRoot:       profile.Storage.GraphRoot,
				CRIRoot:         profile.Storage.CRIRoot,
				StorageConfPath: profile.Storage.StorageConfPath,
				RunRoot:         profile.Storage.RunRoot,
				GraphDriver:     profile.Storage.GraphDriver,
			})
			// 先把真实运行时消费的目录纳入报告。当前仅观测，不自动改写
			// Docker daemon.json 或 containerd config.toml，避免破坏已有配置。
			taskList = append(taskList, &RuntimeStorageAuditTask{
				NodeSpec:               node,
				ExpectedDockerRoot:     profile.Storage.GraphRoot,
				ExpectedContainerdRoot: profile.Storage.CRIRoot,
				ExpectedContainersRoot: strings.TrimRight(profile.Storage.GraphRoot, "/") + "/containers/storage",
				StorageConfPath:        profile.Storage.StorageConfPath,
			})
		}
		if profile.Features.UlimitEnabled() {
			taskList = append(taskList, &UlimitTask{
				NodeSpec: node,
				NoFile:   profile.Ulimit.NoFile,
				NProc:    profile.Ulimit.NProc,
			})
		}
	}

	return taskList
}

func bastionConnectionForNode(node config.NodeConnection) config.NodeConnection {
	return config.NodeConnection{
		Name:          "bastion@" + node.Name,
		IP:            node.Bastion.Host,
		HostIP:        node.Bastion.Host,
		Roles:         []string{"bastion"},
		SSHUser:       node.Bastion.SSHUser,
		SSHPort:       node.Bastion.SSHPort,
		SSHPassword:   node.Bastion.SSHPassword,
		SSHPrivateKey: node.Bastion.SSHPrivateKey,
	}
}

func nodeIdentityKey(node config.NodeConnection) string {
	return fmt.Sprintf("%s|%d|%s", node.IP, node.SSHPort, node.SSHUser)
}

func resolveAuthorizedUser(explicit string, sshUser string) string {
	if strings.TrimSpace(explicit) != "" {
		return explicit
	}
	if strings.TrimSpace(sshUser) != "" {
		return sshUser
	}
	return "root"
}

// encodeBase64 用来安全地把多行配置放进 Bash 脚本。
func encodeBase64(content string) string {
	return base64.StdEncoding.EncodeToString([]byte(content))
}

func decodeBase64(content string) (string, error) {
	decoded, err := base64.StdEncoding.DecodeString(content)
	if err != nil {
		return "", err
	}
	return string(decoded), nil
}

func runScript(ctx context.Context, exec remote.Executor, node config.NodeConnection, script string) (remote.Result, error) {
	return exec.Run(ctx, node, script)
}

// parseStatusLine 会从脚本输出的最后几行里回溯查找状态标记。
// 这样即使远端 sudo 因 hostname 未收敛而输出告警，我们仍然能稳定拿到
// 任务脚本真正输出的 OK / CHANGE / ERROR 结果。
func parseStatusLine(output string, markers ...string) string {
	normalized := strings.ReplaceAll(output, "\r\n", "\n")
	lines := strings.Split(normalized, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		for _, marker := range markers {
			if line == marker || strings.HasPrefix(line, marker) {
				return line
			}
		}
	}
	return strings.TrimSpace(output)
}

func copySysctls(input map[string]string) map[string]string {
	out := make(map[string]string, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func renderSysctlFile(sysctls map[string]string) string {
	keys := make([]string, 0, len(sysctls))
	for key := range sysctls {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	lines := []string{
		"# Managed by bootstrapctl",
		"# Kubernetes host baseline sysctls",
	}
	for _, key := range keys {
		lines = append(lines, fmt.Sprintf("%s = %s", key, sysctls[key]))
	}
	return strings.Join(lines, "\n")
}

func renderModulesFile(modules []string) string {
	lines := []string{
		"# Managed by bootstrapctl",
		"# Kubernetes host baseline kernel modules",
	}
	lines = append(lines, modules...)
	return strings.Join(lines, "\n")
}

func renderContainersStorageConfig(graphDriver, runRoot, graphRoot string) string {
	return fmt.Sprintf(`[storage]
driver = %q
runroot = %q
graphroot = %q`, graphDriver, runRoot, graphRoot)
}
