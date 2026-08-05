# Production Host Baseline 重构计划

## 目标

本分支把 `bootstrapctl` 从“若干初始化脚本的 Go 封装”逐步升级为：

> 面向离线、半离线和受限网络环境的主机事实采集、状态收敛与交付证据工具。

保留现有 `inventory + profile + scan/plan/apply/verify` 使用方式，不直接引入
Ansible/Kubespray 运行时。成熟项目用于提取规则、发行版差异和测试场景。

## 参考项目的职责

| 项目 | 使用方式 |
|---|---|
| k0sctl | 借鉴 phase、facts、validate、cleanup、dry-run 和并发模型 |
| rig v2 | 后续评估复用 SSH、sudo、远程文件、OS、包管理、服务管理能力 |
| Kubespray | 提取 kubernetes/preinstall、bootstrap-os 等成熟规则，不直接执行 playbook |
| dev-sec hardening | 提取 SSH、auditd、PAM、sysctl 和文件权限基线 |
| ComplianceAsCode/OpenSCAP | 作为外部合规验证和规则映射来源 |

## 规则转换流程

```text
Kubespray role/task
    -> 提取目标状态、发行版条件、依赖和验证命令
    -> 转换成 bootstrapctl Policy
    -> 转换成 Go Task 的 Check/Apply
    -> 记录 before/desired/after/effective/evidence
    -> plan/apply/verify 报告
```

每条规则都必须明确：

1. 适用的 OS、版本、架构和节点角色
2. 检查命令与原始证据
3. 当前值和期望值
4. 修改动作
5. 是否立即生效
6. 是否需要重新登录、重启服务或重启主机
7. 对现有业务的兼容风险

## 第一阶段：报告协议

新增结构化 `ChangeRecord`，任务逐步输出：

- `category`
- `resource`
- `path`
- `operation`
- `before`
- `desired`
- `after`
- `effective`
- `changed`
- `verified`
- `status`
- `evidence`
- `message`
- `pending_action`

旧任务仍可只返回 `Summary`，后续逐项迁移。

建议状态：

- `compliant`
- `needs-change`
- `would-change`
- `changed-verified`
- `changed-pending-relogin`
- `changed-pending-restart`
- `changed-pending-reboot`
- `preserved`
- `observed-warning`
- `skipped`
- `failed`

## 容器运行时存储语义

现有 `StorageLayoutTask` 管理：

- graph root 目录
- CRI root 目录
- `/etc/containers/storage.conf`

但三类运行时使用不同配置：

| 运行时 | 实际配置入口 |
|---|---|
| Docker | `/etc/docker/daemon.json` 的 `data-root` |
| containerd | `/etc/containerd/config.toml` 的顶层 `root`、`state` |
| Podman/CRI-O | `/etc/containers/storage.conf` 的 `graphroot`、`runroot` |

仅创建 `/data/containerd` 并不代表 containerd 已经使用该目录；修改
`containers/storage.conf` 也不会改变 Docker 的镜像目录。

第一步增加 `runtime-storage-audit`：

- 采集 Docker `DockerRootDir`
- 采集 containerd 实际 `root`
- 采集 containers/storage `graphroot`
- 与 profile 期望值对比
- 当前仅观测，不自动改写 Docker/containerd，避免覆盖已有 JSON/TOML 配置

下一步增加显式策略：

```yaml
storage:
  docker:
    mode: observe       # observe | managed | preserve
    data_root: /data/docker
    config_path: /etc/docker/daemon.json
    restart_policy: report-only

  containerd:
    mode: observe
    root: /data/containerd
    state: /run/containerd
    config_path: /etc/containerd/config.toml
    restart_policy: report-only

  containers_storage:
    mode: managed
    graph_root: /data/containers/storage
    run_root: /run/containers/storage
```

修改配置时必须做“语义合并”，不能直接覆盖完整 daemon.json/config.toml。

## Kubernetes preinstall 领域拆分

不要建立一个巨大的 `kubernetes-preinstall` 任务。按可独立检查和报告的资源拆分：

### 主机事实

- OS ID / VERSION_ID / family
- architecture
- kernel
- init system
- package manager
- cgroup version/driver
- container runtime
- network interfaces and routes
- DNS resolver implementation

### 基础身份和时间

- hostname
- `/etc/hosts`
- timezone
- chrony/systemd-timesyncd
- NTP source and synchronization state

### Kubernetes 内核前置

- swap
- required kernel modules
- sysctl
- cgroup
- br_netfilter
- ip_forward
- conntrack
- inotify
- pid/file descriptor limits

### DNS 和 resolv.conf

- `/etc/resolv.conf` 是否为 symlink
- systemd-resolved / NetworkManager / resolvconf 管理关系
- nameserver/search/options
- loopback resolver 风险
- kubelet `resolvConf` 推荐值

### systemd 和服务限制

- system manager defaults
- containerd/docker/kubelet service drop-ins
- `LimitNOFILE`
- `LimitNPROC`
- `TasksMax`
- daemon-reload/restart pending

### 必需软件

- conntrack
- socat
- ipset
- iptables/nft
- ebtables
- ethtool
- chrony
- audit/logging packages

包名必须通过发行版映射表解析，不能把 apt/yum 判断散落在各任务脚本中。

## 发行版适配方向

后续建立：

```text
internal/platform
  release.go
  capabilities.go
  package_names.go
  service_names.go
  paths.go
```

任务依赖 capability，而不是在每个任务里重复：

```bash
if command -v apt ...
elif command -v dnf ...
```

示例 capability：

- `systemd`
- `selinux`
- `apparmor`
- `firewalld`
- `ufw`
- `systemd-resolved`
- `network-manager`
- `dnf`
- `apt`
- `rpm`
- `deb`

## 资源限制迁移

当前 `ulimit` 只管理 PAM 登录会话。目标拆分：

1. `login-resource-limits`
2. `kernel-resource-limits`
3. `systemd-default-limits`
4. `systemd-service-limits`

报告必须区分：

- 配置文件值
- 当前 SSH/sudo 会话生效值
- systemd manager 默认值
- 具体服务进程值
- 待重新登录/待重启状态

本分支已先迁移 `ulimit`，输出配置值与当前会话 `ulimit` 生效值。

## 后续实施顺序

1. 结构化报告协议
2. runtime storage audit
3. 主机事实与发行版 capability
4. 完整资源限制
5. timezone/time sync
6. DNS/resolv.conf
7. Kubernetes 必需包
8. kernel modules/sysctl 规则重构
9. journald/logrotate/auditd/rsyslog
10. rig v2 POC 与远程执行层迁移决策

每一阶段都要求：单元测试、至少 Ubuntu/Rocky 两类真机验证、重复 apply 幂等验证、JSON/Markdown 报告样例。
