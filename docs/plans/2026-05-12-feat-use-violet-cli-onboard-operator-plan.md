---
title: Use violet CLI to onboard operator instead of direct Artifact creation
type: feat
status: active
date: 2026-05-12
---

# Use violet CLI to onboard operator instead of direct Artifact creation

## Enhancement Summary

**Deepened on:** 2026-05-12（两轮深化）
**Focus 第一轮：** integration-test 集群 + upgrade-test 流水线兼容性
**Focus 第二轮：** marketplace controller 实际行为 → 引入 OLM 原生 rolling 升级语义

### 第一轮关键发现（流水线兼容）

1. **流水线压力在仓库外**：本仓库自身**无** Tekton Pipeline 引用 `upgrade` 二进制（`.tekton/pr-manage.yaml` 只是 PR 命令管理；`.github/workflows/build.yml` 只发布 GitHub Release）。upgrade-test 工具被**外部测试镜像**（例：gitlab-operator 的 testing 镜像，参 README 第 107-152 行）通过 `wget` 拉取二进制嵌入。这意味着：本仓库的兼容性改动必须**默认无副作用**，让所有上游外部镜像零改动继续工作。
2. **k8sadmin token 标准路径已固化**：`kubectl get secret k8sadmin -n cpaas-system -o jsonpath='{.data.token}' | base64 -D`（参 `~/.claude/skills/violet-onboard/SKILL.md:46`）—— Go 实现复用此路径即可，无需另立约定。
3. **integration-test 集群启动 PipelineRun 走 Thanos API**（参 `/devops-release:trigger-test`），不是直接 `kubectl apply PipelineRun`。本仓库不直接生产 PipelineRun YAML，所以与该侧无耦合，但需保证 upgrade 二进制在 Tekton Task 容器内可重入运行。
4. **conditional-rules 三条硬规则适用**（`~/.claude/conditional-rules/k8s-deploy.md`）：不要猜 channel、先验证凭据、OLM Subscription ≠ Tekton Trigger Subscription。落到 plan 里：channel 默认 `stable` 与现状一致；启动时验证凭据；只创建 `operators.coreos.com/v1alpha1/Subscription`。

### 第二轮关键发现（marketplace 行为 + 升级语义）

**调研对象**：`/Users/alauda/Projects/DevOps/AlaudaDevops/marketplace`（Alauda 平台 OLM 插件市场 controller）

5. **marketplace 自己起 grpc registry server 作为 CatalogSource backend**。ArtifactVersion 创建 → controller 把 bundle 加入 SQLite DB → RegistrySyncer 每 **30 秒** 同步并 patch CatalogSource annotation 触发 OLM 刷新（`marketplace/pkg/grpcserver/syncer.go:128-164`）。这意味着升级测试的 wait timeout 必须 ≥ 30s。
6. **marketplace 不做主动升级，完全依赖 OLM 标准 channel 行为**（`marketplace/pkg/controllers/subscription/controller.go:48-106`）。Subscription controller 只管 finalizer、OperatorGroup 同步、CSV 清理，**从未 patch `startingCSV` 或主动触发升级**。
7. **webhook 强制 platform operator 必须 `installPlanApproval: Manual`**（`marketplace/pkg/webhooks/subscription/handler.go:240-243`）：
   ```go
   if isPaltformOperator(pkg) && subscription.Spec.InstallPlanApproval == operatorsv1alpha1.ApprovalAutomatic {
     return fmt.Errorf("%s is a platform operator and is not allowed to installed in %s mode", ...)
   }
   ```
   → 当前 upgrade-test `subscription.go:130` 的 `installPlanApproval: "Manual"` 决策**已正确**，**不能改为 Automatic**。
8. **当前 upgrade-test 的 InstallSubscription 是「重装」不是「升级」**：`pkg/operator/operatorhub/subscription.go:25-32` 先删旧 Subscription + 旧 CSV，再创建新 Subscription with `startingCSV`。这绕开了 OLM 真实升级路径（channel 内 CSV rolling），**测试语义错误** —— 生产环境是 OLM 通过 channel 自动升级（marketplace 调研结论 #4），而非重建 Sub。本 plan 必须新增 **rolling 升级模式**才能真正覆盖生产升级行为。
9. **ArtifactVersion 失败模式**：手动 kubectl create 无 ownerRef → phase=Failed，message=`aritfactVersion not have OwnerReference`（`marketplace/pkg/controllers/artifactversion/controller.go:494-501`）。现状代码已手动 set ownerRef（`artifact_versiong.go:80-87`），但缺 label/annotation 仍不被 marketplace 标准识别 → violet 路径解决。
10. **CSV `skipRange` 与 `replaces` 来自 ArtifactVersion annotation**（`marketplace/pkg/controllers/artifactversion/controller.go:350`，via `annotation.GetExpectedValues(av)`）。这两个字段是 OLM channel 升级路径解析的关键 —— violet 的 `--reset-bundle-version`（默认 true）会按 bundle tag 重写它们；本 plan 早前已明确**不能关闭**。

## Overview

把 `pkg/operator/operatorhub/` 现在直接用 dynamic client `kubectl create` ArtifactVersion 的做法，替换为通过 Alauda 内部 `violet` CLI 完成标准上架。新增 `OperatorType=violet` 实现 `OperatorInterface`，与现有 `operatorhub` / `local` 并列；`OperatorInterface` 契约不变。Subscription/InstallPlan/CSV 阶段不变，复用现有逻辑（必要时从 `operatorhub` 抽取到共享包）。

## Problem Statement / Motivation

### 问题一：上架链路非标

当前 `pkg/operator/operatorhub/artifact_versiong.go:51-90` 通过 dynamic client 手写一个 `ArtifactVersion` 资源（label 只有 `cpaas.io/artifact-version` 和 `cpaas.io/library`，annotation 只有 `kubectl-artifact: kubectl-artfact`），并假设 `Artifact` 资源已由外部预先创建。这违反了 Alauda 平台标准上架路径，存在两类已知问题（详见 `~/.claude/skills/deploy-operator/SKILL.md`）：

1. **Artifact 未由 violet 创建** → 缺 `cpaas.io/managed-by: violet` label，marketplace-controller 会把 `present` 持续设回 `false`，进而**级联清理 ArtifactVersion**。
2. **ArtifactVersion 缺关键元数据** → 缺 `cpaas.io/bundle-version`、`cpaas.io/channels`、`cpaas.io/default-channel`、`cpaas.io/present`、`cpaas.io/builtin`、`cpaas.io/type` 等字段，CatalogSource 同步与 packagemanifest 生成行为不稳定。

平台官方推荐路径是 `violet create → violet package → violet push --skip-push`（三步上架）。

### 问题二：升级语义非生产

`pkg/operator/operatorhub/subscription.go:25-32` 的 `InstallSubscription` 当前做的是「**重装**」而不是「**升级**」：

```go
// 现状（artifact_versiong.go 不变，subscription.go 这段）：
// 1. delete old Subscription
// 2. delete old CSV
// 3. create new Subscription with startingCSV=<new version>
```

**生产环境的真实升级路径是 OLM 的 channel rolling**（marketplace 调研结论 #4-6）：
- 用户 `violet push` 新 ArtifactVersion → marketplace registry-syncer ~30s 后同步到 CatalogSource
- OLM catalog-operator 检测 channel 内有更新 CSV → **自动**为已存在的 Subscription 生成新 InstallPlan
- `installPlanApproval: Manual` 模式下，运维人员或工具审批 InstallPlan
- OLM 滚动升级 CSV（旧 CSV 被替换/skipped，新 Pod 启动，业务无重新部署）

当前实现「删 Sub + 删 CSV → 重建 Sub」绕过了 OLM 标准升级路径，**测试的是 fresh install 而不是 in-place upgrade**。这意味着：
- 历史数据兼容性的验证场景**与生产路径不一致**
- OLM 的 `skipRange` / `replaces` 升级路径解析逻辑**没有被测试覆盖**
- 用户在 Production 遇到的升级 bug（例如 channel resolve 失败、conversion webhook 阻塞）**测不出来**

本 plan 必须同时引入 **violet 上架 + OLM 原生 rolling 升级模式**，才能让 upgrade-test 真正做"升级测试"。

参考文档：
- `~/.claude/skills/violet-onboard/SKILL.md` — violet 三步流程与 `--skip-push` 模式
- `~/.claude/skills/deploy-operator/SKILL.md` — 强调 "Artifact 必须通过 violet 创建" 的根本原因
- `~/.claude/skills/upgrade-operator/SKILL.md` — 升级阶段的 ArtifactVersion 必要 label/annotation 清单

## Proposed Solution

### 高层设计

新增 `pkg/operator/violet/` 包，实现 `OperatorInterface`。`UpgradeOperator(ctx, version)` 内部按序执行：

1. **凭据准备**：从 `OperatorConfig` 读取 `platformAddress`/`platformToken`，缺失时从 `kubeconfig` 自动提取（移植 `hack/common.sh` 的 yq 逻辑到 Go）
2. **`violet create`**：生成 bundle manifest 元数据。MVP 选用 `--skip-package-images` 跳过镜像打包，加快 CI（前提：bundle 镜像已在 platform registry）
3. **`violet package`**：生成 `.tgz`（即使 skip 镜像，仍是 push 的入参）
4. **`violet push --skip-push`**：通过 platform API 创建 `Artifact` + `ArtifactVersion`
5. **等待 Artifact `present=true`**：现有实现没有此校验，补齐
6. **等待 ArtifactVersion `phase=Present`** + **等待 marketplace registry-syncer 把 CatalogSource 刷新**（30s 窗口）+ **等待 PackageManifest 包含目标 CSV**
7. **升级阶段** —— 根据 `upgradeStrategy` 选择实现：
   - **`recreate` 模式**（**保留原行为**）：复用 `subscription.go`（抽取到 `pkg/operator/olm/`）：删旧 Sub + 旧 CSV → 创建新 Sub with `startingCSV`
   - **`rolling` 模式**（**本次新增**）：不删 Sub；等 OLM 自动生成 InstallPlan → 审批 → 等待 CSV `currentCSV` 滚动到目标版本 → 等待新 CSV phase=Succeeded（详见下面 "OLM Native Rolling Upgrade" 章节）

### 升级模式选择

| 场景 | 推荐 `upgradeStrategy` | 原因 |
|------|----------------------|------|
| 一条 path 中的**首版本**（prepare 阶段） | `recreate`（自动） | Subscription 还不存在，rolling 无意义；恒等于 fresh install |
| 一条 path 中的**后续版本**（upgrade 阶段） | `rolling`（**新默认**） | 复现生产 in-place 升级行为；测真实 OLM channel/skipRange 解析 |
| 强制重建（诊断/回归） | `recreate`（显式） | 紧急回退 / 对照试验 |

由 `pkg/operator/violet/operator.go` 自动判断（基于该 path 内 version 索引），用户可在 config 显式覆盖。

### 关键文件改动清单

| 路径 | 操作 | 说明 |
|------|------|------|
| `pkg/operator/violet/operator.go` | **新增** | `VioletOperator` 实现 `OperatorInterface`，编排三步上架 |
| `pkg/operator/violet/cli.go` | **新增** | 封装 violet 命令拼装与执行（基于 `pkg/exec`） |
| `pkg/operator/violet/credentials.go` | **新增** | platform URL/token 提取（kubeconfig + k8sadmin secret） |
| `pkg/operator/violet/wait.go` | **新增** | Artifact `present=true` 等待（新增），ArtifactVersion/PM 等待逻辑（迁移自 operatorhub） |
| `pkg/operator/olm/subscription.go` | **新增（抽取）** | 把 `pkg/operator/operatorhub/subscription.go` 移过来，operatorhub 和 violet 共享 |
| `pkg/operator/operatorhub/subscription.go` | **改动** | 改为 thin wrapper，调 `pkg/operator/olm` 的实现；或者完全删除并改 `operatorhub.Operator` 直接引用新包 |
| `pkg/operator/factory.go` | **改动** | 注册 `OperatorTypeViolet = "violet"` |
| `pkg/config/config.go` | **改动** | 新增 violet 相关字段；`defaultConfig` 填默认值 |
| `Dockerfile` | **改动** | 在 final stage 安装 violet 二进制（从 release artifact 下载） |
| `README.md` | **改动** | 新增 violet 模式 config 示例 |
| `configs/demo-violet.yaml` | **新增** | violet 模式样例配置 |
| `CLAUDE.md` | **改动** | 把 violet 标记为推荐路径；新增 violet 包架构说明 |

### 配置字段新增（`pkg/config/config.go`）

```go
type OperatorConfig struct {
    // 现有字段保持
    Type           string
    Artifact       string
    Namespace      string
    Name           string
    Workspace      string
    ArtifactPrefix string
    Interval       time.Duration
    Timeout        time.Duration
    Command        string

    // 新增：violet 模式专用
    Registry    string   `yaml:"registry,omitempty"`     // e.g. 152-231-registry.alauda.cn:60070
    Repository  string   `yaml:"repository,omitempty"`   // e.g. devops/<operator>-bundle, 默认拼成 devops/<name>-bundle
    Platforms   []string `yaml:"platforms,omitempty"`    // 默认 ["linux/amd64", "linux/arm64"]
    CatalogSource string `yaml:"catalogSource,omitempty"` // violet --default-catalog-source, 默认 "platform"

    // platform 凭据，缺省时从 kubeconfig 自动提取
    PlatformAddress  string `yaml:"platformAddress,omitempty"`
    PlatformToken    string `yaml:"platformToken,omitempty"`
    PlatformUsername string `yaml:"platformUsername,omitempty"`
    PlatformPassword string `yaml:"platformPassword,omitempty"`

    // violet 工作目录（生成的 <operator>/ 与 .tgz 落地位置），默认 mktemp -d
    VioletWorkDir string `yaml:"violetWorkDir,omitempty"`

    // 是否跳过镜像打包（fast path），默认 true。bundle 镜像不在 platform registry 时设 false
    SkipPackageImages *bool `yaml:"skipPackageImages,omitempty"`
}

// Version 新增字段
type Version struct {
    // ... 现有字段
    // upgradeStrategy 选择升级模式，仅 violet operator 生效。可选 "recreate" 或 "rolling"
    // 缺省时：首版本（index=0）→ "recreate"；后续版本 → "rolling"
    UpgradeStrategy string `yaml:"upgradeStrategy,omitempty"`
}
```

### violet 命令拼装示例

```go
// step 1: create
exec.Command{
    Name: "violet",
    Args: []string{
        "create", cfg.Name,
        "--artifact", fmt.Sprintf("%s/%s:%s", cfg.Registry, cfg.Repository, version.BundleVersion),
        "--no-auth",
        "--platforms", strings.Join(cfg.Platforms, ","), // violet 接受 csv 或多次 --platforms
        "--default-catalog-source", cfg.CatalogSource,
        "--skip-package-images", // fast path
    },
    Dir: workDir,
}

// step 2: package
exec.Command{
    Name: "violet",
    Args: []string{
        "package", cfg.Name,
        "--no-auth", "--plain",
        "--output", fmt.Sprintf("%s-%s", cfg.Name, version.BundleVersion),
    },
    Dir: workDir,
}

// step 3: push
exec.Command{
    Name: "violet",
    Args: []string{
        "push", fmt.Sprintf("%s-%s.tgz", cfg.Name, version.BundleVersion),
        "--skip-push",
        "--platform-address", platformAddr,
        "--platform-token", platformToken,
        "--no-auth", "--plain",
    },
    Dir: workDir,
}
```

## Technical Considerations

### violet 二进制依赖
- Dockerfile final stage 新增安装步骤；通过 `ARG VIOLET_VERSION` 锁版本，便于 renovate 升级
- 启动时 `exec.LookPath("violet")` 做 fail-fast 检查，错误信息提示安装方式
- 本地开发：在 `README.md` 注明 `violet` 安装指引

### `violet create` 默认 platforms 是 `darwin/arm64/v8` —— 必须显式覆盖
违反此项会导致 `package` 下载错误平台镜像。`OperatorConfig.Platforms` 默认 `["linux/amd64", "linux/arm64"]`，且在配置加载时验证非空。

### `violet push --reset-bundle-version` 默认 true
该 flag 会按 bundle 镜像 tag 重写 CSV 的 `version` 和 `skipRange`。对 upgrade test 是**关键依赖**——升级 v17.8 → v17.11 时，OLM 通过 `skipRange` 判断升级路径。**不要显式关闭**。

### 临时目录管理
每个 version 用独立 `mktemp -d` 子目录，避免跨版本 manifest 串扰。升级路径全部完成后清理（成功）或保留（失败用于诊断），由 `cfg.VioletWorkDir` 与 `defer` 控制。

### violet 输出结构化分类
violet 仅返回 exit code + 文本日志。在 `cli.go` 内对 stderr buffer 做简单 keyword 匹配，区分：
- `digest mismatch` / `failed to download` → **retriable**
- `failed to get user info` / `unauthorized` → **fatal (凭据)**
- 其他 → 默认 fatal

retriable 错误按 exponential backoff 重试最多 3 次，复用现有 `subscription.go:139-160` 的重试模式。

### Subscription 代码抽取
当前 `subscription.go` 在 `operatorhub` 包内，violet 实现复用需要：
- 选项 A：把 `Operator` 结构（dynamic client + GVR 常量 + namespace）抽到 `pkg/operator/olm/`，`operatorhub` 和 `violet` 都嵌入它
- 选项 B：保留 `operatorhub` 不动，violet 内部直接 `import` 同一包并构造一个临时的 `*operatorhub.Operator` 来调 `InstallSubscription`

**选 A**：包结构更清晰；violet 不应"逻辑上依赖 operatorhub"。重构限定在 ~50 LOC 文件移动 + import path 更新。

### kubeconfig 凭据提取（Go 实现）
- `rest.Config.Host` 形如 `https://192.168.x.x/kubernetes/<cluster>` → 用 `url.Parse` + 截断 path 得到 `https://192.168.x.x`
- token 优先级：`OperatorConfig.PlatformToken` > 从 `cpaas-system/k8sadmin` Secret 读取 `data.token`（base64 decode）
- username/password 模式作为 fallback：仅当配置中显式给出时使用
- 检查同时给出 token 与 username/password 时，token 优先，记录 warning
- **integration-test 集群已固化使用此路径**（参 `~/.claude/skills/violet-onboard/SKILL.md:46`），不另立约定

## System-Wide Impact

### 交互图（升级单个 version 的调用链）

```
cmd.Execute()
 └─ factory.CreateOperator("violet", opts)
     └─ violet.NewOperator(cfg)
         ├─ exec.LookPath("violet")                       # fail-fast 检查
         └─ credentials.Resolve(cfg, kubeconfig)          # platform addr/token
 └─ for each version in path:
     ├─ violet.UpgradeOperator(ctx, version)
     │   ├─ mktemp -d $workDir
     │   ├─ exec "violet create ..."                      # 生成 <operator>/manifest
     │   ├─ exec "violet package ..."                     # 生成 .tgz
     │   ├─ exec "violet push --skip-push ..."            # 创建 Artifact + AV
     │   ├─ wait Artifact present=true                    # 【新增】
     │   ├─ wait ArtifactVersion phase=Present            # 复用
     │   ├─ wait PackageManifest contains CSV             # 复用
     │   └─ olm.InstallSubscription(...)                  # 抽取后复用
     │       ├─ delete old Sub + CSV
     │       ├─ create Subscription installPlanApproval=Manual
     │       ├─ wait InstallPlan
     │       ├─ patch InstallPlan approved=true
     │       └─ wait CSV phase=Succeeded
     └─ exec.RunCommand(testCommand)                       # 跑 make prepare/upgrade
```

跨进程 / 跨 namespace 副作用：
- 平台 API 调用（violet → ACP）创建 `cpaas-system` 下的 `Artifact` + `ArtifactVersion`
- OLM 控制器在 `cfg.Namespace` 创建 InstallPlan / CSV / 触发 Pod 部署
- 本地 fs：violet 在 workDir 生成 `<operator>/` 目录 + `<output>.tgz`

### 错误与失败传播

| 阶段 | 失败现象 | 处理策略 |
|------|----------|---------|
| `exec.LookPath` | violet 缺失 | 启动时 fail-fast，明确提示安装 |
| `violet create` exit≠0 | 镜像不存在/认证失败 | 解析 stderr，凭据错误 → fatal，其他 → fatal |
| `violet package` exit≠0 | digest mismatch / 网络中断 | retriable，指数退避重试 ≤3 次 |
| `violet push` exit≠0 | platform token 失效 / Artifact 已存在冲突 | 凭据→fatal；冲突→检查 idempotency 路径 |
| wait Artifact present | 超时 | 报 marketplace-controller 状态，fatal |
| wait AV phase=Present | 超时 | 与现有 operatorhub 行为一致，fatal |
| Subscription/InstallPlan/CSV | 与现有 operatorhub 行为一致 | 复用现有错误路径 |
| 测试命令失败 | testCommand exit≠0 | 由 `cmd/upgrade_command.go:127` 现有逻辑处理（依 `cfg.Immediate`） |

### 状态生命周期风险

- **半成品 Artifact**：violet push 部分成功（Artifact 已创建，AV 创建失败）→ 下次重跑遇到"Artifact already exists"。**需要 idempotency**：push 前先检查 Artifact 是否已 managed-by=violet，是则跳过 push（或显式覆盖）。
- **半成品 AV**：AV 已 created 但 phase 未到 Present → 下次重跑会 conflict on create。复用现有 `createArtifactVersion` 的 "if exists return existing" 模式。
- **临时目录污染**：进程异常退出未清理 workDir → 用 `defer os.RemoveAll(workDir)` + 失败时保留路径写入日志（diagnostic）。
- **Subscription 阶段失败但 AV 已建**：升级路径上半部分成功 → 下次重跑要么 idempotent 复用，要么需要清理。当前 `subscription.go` 已经"先删旧 Sub + 旧 CSV"，保持该行为即可。

### API surface 兼容性

- `OperatorInterface` 不变 → 不破坏 `operatorhub` 和 `local`
- `OperatorType` 仅新增枚举值 → 不破坏 factory 调用方
- `OperatorConfig` 仅 append 字段 → 现有 config.yaml 继续可用（无 `registry`/`repository` 时如选 `type: violet` 才报错）
- `defaultConfig()` 不改默认 `Type=operatorhub` → 升级用户主动切到 `type: violet`，无隐式行为变化

### 集成测试场景（unit test 覆盖不到的）

1. **全新部署 → 升级**：空 cluster 第一次跑 violet 三步建 Artifact + AV，第二次（同一个 path 的下一个 version）期望 Artifact 复用，只 AV + Subscription 切换
2. **package 间歇失败重试**：mock violet 第一次返回 `digest mismatch`，第二次成功 → 验证重试逻辑
3. **凭据自动提取**：仅给 kubeconfig，无 `platformAddress`/`platformToken` → 验证从 URL 截断 path、从 k8sadmin secret 取 token
4. **凭据显式覆盖**：config 给 `platformToken` → 不读 kubeconfig secret
5. **violet 不存在**：PATH 移除 violet → 启动报错且**不动集群**
6. **跨 version 临时目录隔离**：v17.8 和 v17.11 用不同 mktemp 目录，互不影响

## Acceptance Criteria

### 功能性要求

- [ ] `operatorConfig.type: violet` 配置生效，factory 返回 `violet.Operator`
- [ ] 完整升级路径 (vN → vN+1) 跑通；Artifact 与 ArtifactVersion 都由 violet 创建
- [ ] Artifact 含 `cpaas.io/managed-by: violet` label（用 `kubectl get artifact -o yaml` 验证）
- [ ] ArtifactVersion `status.phase=Present`
- [ ] Subscription/InstallPlan/CSV 阶段行为与原 `operatorhub` 实现等价（CSV 最终 `Succeeded`）
- [ ] violet 二进制缺失时启动失败，错误明确指向安装步骤
- [ ] violet package 阶段 `digest mismatch` 失败可自动重试且成功；3 次仍失败 fail fast
- [ ] 升级失败时 workDir 路径打印到日志便于复现；成功时清理临时目录
- [ ] 现有 `type=operatorhub`/`type=local` config 文件行为不变（回归）

### 非功能性要求

- [ ] 增量代码 ≤ 500 LOC（含测试）；超出需在 PR 描述说明
- [ ] 单个 violet 三步耗时打 info 日志（便于后续优化决策）
- [ ] Dockerfile 构建出的镜像包含 violet 二进制；layer 增量 ≤ 50MB
- [ ] README 新增 violet 模式配置示例与必填字段说明
- [ ] CLAUDE.md 更新：标注 violet 为推荐路径；新增 `pkg/operator/violet/` 简要架构说明
- [ ] `configs/demo-violet.yaml` 提供可直接套用的范例

### 质量门

- [ ] `go vet ./...` 通过
- [ ] `go test ./pkg/operator/violet/...` 覆盖：命令拼装、凭据解析、retriable 错误分类
- [ ] PR 描述含 `kubectl get artifact` / `kubectl get artifactversion` 实际输出截图（证明 label/annotation 完整）
- [ ] 在一个 cluster 上手工跑通至少一条 upgrade path（如 gitlab v17.8 → v17.11）

## Success Metrics

| 指标 | 目标 |
|------|------|
| ArtifactVersion 在 controller GC 下的稳定性 | 升级流程跑完 24h 后 AV 仍 Present（消除现有"被自动删"风险） |
| 升级测试镜像构建产物 | violet 二进制在 PATH 中；`upgrade --help` 正常显示 |
| 单次升级（不含测试执行）耗时增量 | `--skip-package-images` 模式下 ≤ +90s/version；标准模式下 ≤ +15min/version |
| 用户迁移配置工作量 | 现有 config.yaml 加 3 个字段（type/registry/repository）+ 可选凭据即可 |

## Dependencies & Risks

### 依赖

- **violet CLI**：需要在测试镜像（Dockerfile）与本地开发环境可用。需调研 violet 最低支持版本（建议 pin 当前最新稳定）
- **Platform API 凭据**：k8sadmin SA token 或 username/password。CI pipeline secret 需要更新
- **bundle 镜像已上传到 platform registry**：使用 `--skip-package-images` 时是硬性前提；否则需切回标准 package 模式

### 风险与缓解

| 风险 | 影响 | 缓解 |
|------|------|------|
| violet 输出格式变化 | 错误分类正则失效 | 严格 pin violet 版本；输出格式作为 contract 在 CHANGELOG 记录 |
| package 时间过长导致 CI 超时 | 升级测试 pipeline timeout | 默认 `skipPackageImages=true`；老镜像可关闭 fallback |
| `violet push --reset-bundle-version` 默认开启 | CSV 的 version/skipRange 被重写，可能与历史预期不符 | 文档显式说明；通过升级测试用例覆盖 v→v+1 时 skipRange 行为 |
| 凭据从 kubeconfig 提取失败 | 启动时报错；用户不理解 | 自动提取失败时打印明确指引："请在 config 中显式设置 platformAddress 与 platformToken" |
| Subscription 抽取重构破坏 operatorhub 行为 | 现有用户回归失败 | 重构 PR 与 violet 功能 PR 分开；先做重构 + 等价测试，再叠 violet 实现 |
| violet workDir 写入受限 | 容器内权限不足 | workDir 默认指向 `/tmp/violet-*`（容器内可写）；可被 config 覆盖 |
| 外部测试镜像未装 violet 但配置切到 `type: violet` | 启动 fail，CI red | 默认 `type: operatorhub` 不变；violet 缺失时 fail-fast 报错指向 README 安装段落 |
| Tekton Task 容器禁用 `/tmp` 写入 | violet workDir 创建失败 | 文档说明并提供 `violetWorkDir` 显式配置选项；推荐 Task 挂 `emptyDir` 到自定义路径 |
| violet release 渠道变化（GitHub → 内部 nexus） | 外部镜像下载 url 失效 | 文档化下载 url 由 release owner 确认；不在工具代码硬编码 violet 来源 |

## OLM Native Rolling Upgrade（rolling 模式实现细节）

> 本节是第二轮深化的核心产出，基于 marketplace 调研结论。回答"upgrade CLI 应当如何模拟生产环境的真实升级路径"。

### Rolling 模式状态机

```
[已有 Subscription, currentCSV=vN]
        │
        ▼
(violet push 新 ArtifactVersion vN+1)
        │
        ├─ wait ArtifactVersion.status.phase=Present (上架成功)
        ├─ wait Artifact present=true
        ├─ wait marketplace registry-syncer 同步 → CatalogSource annotation 时间戳更新
        │   (~30s 周期，参 marketplace/pkg/grpcserver/syncer.go:158)
        └─ wait PackageManifest 包含 vN+1 的 CSV
        │
        ▼
(OLM catalog-operator 自动检测 channel 内新 CSV)
        │
        ├─ wait Subscription.status.installplan 字段出现新值（区别于旧 InstallPlan）
        │   (复用 subscription.go:164 的 waitInstallPlan, 但要校验 != 旧 InstallPlan name)
        ▼
(获取并审批新 InstallPlan)
        │
        ├─ kubectl patch installplan <new> spec.approved=true
        │   (复用 subscription.go:46-55 的逻辑)
        ▼
(OLM 滚动升级 CSV)
        │
        ├─ wait Subscription.status.currentCSV == vN+1.CSV
        ├─ wait CSV vN+1 phase=Succeeded
        └─ (可选) wait 旧 CSV vN 被 GC 或 phase=Replacing
        │
        ▼
[完成: currentCSV=vN+1]
```

### 与 recreate 模式的关键差异

| 操作 | recreate（现状/保留） | rolling（新增） |
|------|---------------------|----------------|
| 删旧 Subscription | ✅ 删除 | ❌ 不动 |
| 删旧 CSV | ✅ 删除 | ❌ 不动（OLM 自动 GC） |
| 创建新 Subscription | ✅ 创建（startingCSV=新版本） | ❌ 不动 |
| 审批 InstallPlan | ✅ 审批新建 Sub 触发的 InstallPlan | ✅ 审批 OLM channel 触发的新 InstallPlan |
| 等待信号 | 新 CSV phase=Succeeded | `Subscription.status.currentCSV` 切换 + 新 CSV phase=Succeeded |
| 测试语义 | fresh install | in-place upgrade |

### 关键技术点

#### 1. 区分新旧 InstallPlan
rolling 模式下，Subscription 已经存在并指向旧 InstallPlan。当 marketplace 同步出新 CSV 后，OLM 会**新建**一个 InstallPlan。代码必须：
- 记录 rolling 开始前 `Subscription.status.installplan.name`（旧值）
- 轮询直到 `installplan.name` 变化为新值
- 不能简单复用 `subscription.go:164` 的 `waitInstallPlan` —— 该函数仅等 installplan 字段出现，不会区分新旧

#### 2. registry-syncer 30s 窗口
marketplace registry-syncer 默认 **30 秒** 同步周期（`marketplace/pkg/grpcserver/syncer.go:158-164`，可由环境变量 `REGISTRY_SYNCER_INTERVAL` 覆盖但本工具不应假设非默认值）。Wait 超时必须 ≥ 60s，建议复用 `OperatorConfig.Timeout`（默认 10min）已足够。但需在 log 中明确打印 "等待 marketplace registry sync (~30s)" 帮助排错。

#### 3. CatalogSource 同步信号
直接等 CatalogSource 自身的 status 不可靠（OLM 不一定立刻刷新 annotation）。**信号更稳的检测点**是 PackageManifest 是否含目标 CSV —— 这个等待逻辑已存在于 `artifact_versiong.go:115-149` `waitPackageManifest`，rolling 模式可直接复用。

#### 4. CSV 状态机
Rolling 升级期间 CSV 状态会经历：
- 旧 CSV: `Succeeded` → `Replacing`
- 新 CSV: 不存在 → `Pending` → `Installing` → `Succeeded`

Wait 条件应是 **`Subscription.status.currentCSV == 新 CSV name` AND 新 CSV phase=Succeeded**。仅等 CSV phase 不够（可能等到一个尚未被 Subscription 切换过去的 CSV）。

#### 5. installPlanApproval 不变
**关键约束**：marketplace webhook 强制 platform operator 必须 Manual 审批（结论 #7）。rolling 模式继续使用 Manual + 工具内自动 patch approved=true。**不要尝试改为 Automatic**，会被 webhook 直接拒绝。

#### 6. 失败回滚
rolling 模式中途失败（如新 CSV phase=Failed），**不要自动回滚**。upgrade-test 的语义是"暴露升级问题"，自动回滚会掩盖 bug。错误信息应包含：
- 旧 CSV name 与 phase
- 新 CSV name 与 phase
- InstallPlan 状态与 phase
- 建议的人工诊断命令（`kubectl describe csv`, `kubectl logs deploy/<csv-deployment>`）

### Acceptance Criteria（rolling 专项）

- [ ] `version.upgradeStrategy: rolling` 配置生效；自动判断逻辑（首版本 recreate / 后续 rolling）正确
- [ ] rolling 模式下旧 Subscription 不被删除（`kubectl get subscription -n <ns> -o yaml` 资源 uid 升级前后一致）
- [ ] rolling 模式下 `Subscription.status.installplan.name` 升级前后**变化**
- [ ] rolling 模式下 `Subscription.status.currentCSV` 升级后等于新版本 CSV name
- [ ] 旧 CSV 在新 CSV `Succeeded` 后被 OLM 自动清理（不要工具主动删）
- [ ] 升级失败时（任一阶段超时）错误信息含上述五项诊断字段
- [ ] 在 integration-test 集群至少一条 path 跑通 v17.8 → v17.11 rolling 升级

## Implementation Phasing（按 PR 分批交付）

为满足用户「分批次提 PR 交付」的要求，拆分为 **5 个独立可回滚的 PR**：

> **每个 PR 都必须自带 Test Plan**：单元测试在 PR 内提交，端到端验证给出可复制的 shell 命令 + 期望输出。**没有可执行测试验证的 PR 不允许 merge**。

### PR #1：重构 — 抽取 OLM Subscription 逻辑
**范围**：`pkg/operator/operatorhub/subscription.go` → `pkg/operator/olm/subscription.go`；`operatorhub.Operator` 改为嵌入或委托 `olm.Subscription`。
**目标**：纯结构调整，无行为变更。`type=operatorhub` 端到端等价测试通过。
**LOC**：≤ 100 行（主要是 import path 更新 + 接口提取）。
**风险**：极低；最坏情况是 import 错误，CI 编译失败立刻发现。

**Test Plan**：
- 静态检查：`go build ./... && go vet ./...` 通过
- 单元测试：新增 `pkg/operator/olm/subscription_test.go`，使用 `k8s.io/client-go/dynamic/fake` 覆盖：
  - `createSubscription` 拼装的 GVR/labels/spec 字段（特别是 `installPlanApproval: "Manual"` 这个 marketplace webhook 强制约束）
  - `deleteResource` 在 NotFound 时返回 nil
  - 3 次重试退避逻辑（mock failure 两次后成功）
- 等价回归：在 integration-test 集群上跑 `gitlab-operator` 现有 demo.yaml（type 缺省=operatorhub）一次 prepare + 一次 upgrade，与 PR #1 之前的 main 分支跑出的 log 做 diff，**关键事件必须一致**（subscription create / installplan approve / CSV phase 变化）

### PR #2：配置扩展 — OperatorConfig + Version 字段
**范围**：
- `pkg/config/config.go`：`OperatorConfig` 增 `Registry/Repository/Platforms/CatalogSource/PlatformAddress/PlatformToken/PlatformUsername/PlatformPassword/VioletWorkDir/SkipPackageImages`
- `pkg/config/config.go`：`Version` 增 `UpgradeStrategy`
- `defaultConfig()` 填充默认值
- `pkg/operator/factory.go`：注册 `OperatorTypeViolet = "violet"`，**实现返回 `errors.New("violet operator not implemented yet")`**
**目标**：开通配置 surface 但不提供运行时能力。现有用户继续可用。
**LOC**：≤ 80 行。
**风险**：低；纯字段叠加，无 breaking change。

**Test Plan**：
- 静态检查：`go build ./... && go vet ./...` 通过
- 单元测试：新增 `pkg/config/config_test.go`，table-driven 覆盖：
  - 老 demo.yaml 加载后所有字段默认值正确（回归）
  - 新字段 `Platforms` 缺省 → `["linux/amd64", "linux/arm64"]`
  - 新字段 `CatalogSource` 缺省 → `"platform"`
  - `SkipPackageImages` 缺省 → `*bool=true`（指针，因为要区分 "未设置" 与 "显式 false"）
  - `Version.UpgradeStrategy` 缺省 → `""`（由 violet operator 运行时按版本索引推断）
- 端到端回归：`./upgrade --config configs/demo.yaml --kubeconfig <test-kubeconfig>` 与 PR #2 前行为完全一致
- 占位验证：`type: violet` 的最小 config 应立刻报错 `violet operator not implemented yet`（断言错误信息精确匹配）

### PR #3：violet 三步上架（recreate 模式）
**范围**：
- `pkg/operator/violet/` 完整实现：`operator.go` / `cli.go` / `credentials.go` / `wait.go`
- 升级阶段**仅支持 recreate**（即调用 PR #1 抽出来的 `olm.Subscription`）
- `configs/demo-violet.yaml`
- 单元测试：命令拼装、凭据解析、retriable 错误分类
**目标**：`type=violet` 可端到端跑通；升级语义先与现状等价（重装），不引入 rolling 风险。
**LOC**：~400 行（含测试）。
**风险**：中等；新增包但不动现有路径。

**Test Plan**：
- 单元测试（全部新增）：
  - `pkg/operator/violet/cli_test.go`：
    - violet create 命令拼装（含 `--skip-package-images` 默认开启、`--platforms linux/amd64,linux/arm64` 强制覆盖 violet 自带的 darwin 默认）
    - violet package 命令拼装（`--output` 命名规则 `<operator>-<version>`）
    - violet push 命令拼装（含 `--skip-push`、`--platform-token` 优先于 `--platform-username/password`）
    - 模拟 `--reset-bundle-version` 默认 true 不被显式关闭
  - `pkg/operator/violet/credentials_test.go`：
    - `rest.Config.Host = "https://1.2.3.4/kubernetes/global"` → 提取 `"https://1.2.3.4"`
    - kubeconfig token 提取从 fake `cpaas-system/k8sadmin` Secret 的 `data.token`（base64）解码
    - 配置显式 platformToken 优先于 secret
    - 同时给 token 和 username/password 时记录 warning + 用 token
  - `pkg/operator/violet/wait_test.go`：fake dynamic client 模拟 ArtifactVersion phase 从 `Pending` → `Present` 的状态机
  - `pkg/operator/violet/errors_test.go`：stderr 含 `digest mismatch` → retriable；含 `unauthorized` → fatal；其它 → fatal
  - retriable 重试 3 次：mock 失败两次后成功，断言总调用次数=3
- 静态检查：`go build ./... && go vet ./...` 通过
- 端到端验证（integration-test 集群）：
  ```bash
  # 准备：kubeconfig 指向 integration-test 集群
  export KUBECONFIG=/path/to/integration-test-kubeconfig
  
  # 跑 violet 模式的 gitlab v17.8 prepare 单版本
  ./upgrade --config configs/demo-violet.yaml
  
  # 断言 1：Artifact 由 violet 创建
  kubectl get artifact gitlab-ce-operator -n cpaas-system \
    -o jsonpath='{.metadata.labels.cpaas\.io/managed-by}'
  # 期望输出: violet
  
  # 断言 2：ArtifactVersion phase=Present
  kubectl get artifactversion gitlab-ce-operator.v17.8.10 -n cpaas-system \
    -o jsonpath='{.status.phase}'
  # 期望输出: Present
  
  # 断言 3：Subscription + InstallPlan + CSV 链路完整
  kubectl get csv -n gitlab-ce-operator | grep gitlab-ce-operator.v17.8.10
  # 期望: ... Succeeded
  ```
- 失败注入测试：故意把 `platformToken` 置错 → 启动报错明确指向凭据问题（不只是 "violet push failed"）

### PR #4：rolling 升级模式
**范围**：
- `pkg/operator/olm/rolling.go`：实现"等新 InstallPlan / 审批 / 等 Subscription.currentCSV 切换 / 等新 CSV Succeeded"
- `pkg/operator/violet/operator.go`：根据 `version.UpgradeStrategy` + 自动判断逻辑选择 recreate / rolling
- 失败诊断 helper：dump CSV/InstallPlan/Subscription 状态到 log
- 单元测试 + 文档
**目标**：实现真实生产升级语义。
**LOC**：~300 行（含测试）。
**风险**：中高（核心测试语义变更）；用 path 内 version 索引自动判断 + 显式 override 双保险。

**Test Plan**：
- 单元测试（新增）：
  - `pkg/operator/olm/rolling_test.go`：fake dynamic client 模拟以下状态机
    - **InstallPlan 切换**：Subscription.status.installplan.name 从 "install-old" → "install-new"，断言代码等到新 name 才动手 patch
    - **审批后等待**：patch `spec.approved=true` 后 mock CSV 创建，断言 Subscription.status.currentCSV 切换到新版本
    - **超时失败诊断**：所有 wait 超时后，错误信息包含 (旧 CSV name / 新 CSV name / InstallPlan name / 各 phase 字段) 这 5 个 dump 字段
  - 升级策略自动判断：
    - path 内 version index=0 + `UpgradeStrategy=""` → recreate
    - path 内 version index=1 + `UpgradeStrategy=""` → rolling
    - `UpgradeStrategy="recreate"` 显式覆盖永远 recreate
- 端到端验证（integration-test 集群）：
  ```bash
  # 跑两版本完整升级路径
  ./upgrade --config configs/demo-violet.yaml
  # configs/demo-violet.yaml 含 v17.8 (recreate) + v17.11 (rolling) 两步
  
  # 升级前记录 Subscription uid
  OLD_UID=$(kubectl get subscription gitlab-ce-operator -n gitlab-ce-operator \
    -o jsonpath='{.metadata.uid}')
  
  # 升级完成后断言：
  # 断言 1: Subscription uid 不变（证明是 rolling 不是 recreate）
  NEW_UID=$(kubectl get subscription gitlab-ce-operator -n gitlab-ce-operator \
    -o jsonpath='{.metadata.uid}')
  [ "$OLD_UID" = "$NEW_UID" ]  # 必须相等
  
  # 断言 2: currentCSV 切换到新版本
  kubectl get subscription gitlab-ce-operator -n gitlab-ce-operator \
    -o jsonpath='{.status.currentCSV}'
  # 期望: gitlab-ce-operator.v17.11.1
  
  # 断言 3: 旧 CSV 已被 OLM GC（不能由工具主动删）
  kubectl get csv gitlab-ce-operator.v17.8.10 -n gitlab-ce-operator
  # 期望: Error from server (NotFound) 或 phase=Replacing
  
  # 断言 4: 业务历史数据可读（gitlab-operator 测试用例 @upgrade-17.11 通过）
  ```
- 反例验证：刻意把 `version.UpgradeStrategy: recreate` 强制覆盖 v17.11，跑完后断言 `OLD_UID != NEW_UID`（证明 recreate/rolling 切换是真实生效的）

### PR #5：基础设施 + 文档
**范围**：
- `Dockerfile`：final stage 安装 violet（ARG VIOLET_VERSION，renovate 友好）
- `README.md`：第 152 行后新增 "使用 violet 模式"章节，含外部测试镜像 violet 安装 snippet + upgrade.yaml 完整示例
- `CLAUDE.md`：标注 violet 为推荐路径；新增 `pkg/operator/violet/` 与 `pkg/operator/olm/` 架构说明
- 可选：`.github/workflows/build.yml` 加 violet 集成测试 step（如可行）
**LOC**：Dockerfile +5 行，文档 +150 行。
**风险**：极低；纯部署 + 文档变更。

**Test Plan**：
- Dockerfile 构建验证：
  ```bash
  docker build --build-arg VIOLET_VERSION=<pinned> -t upgrade-test:violet-check .
  docker run --rm upgrade-test:violet-check violet --help | head -3
  # 期望: 含 "A simple container platform product packaging tool"
  docker run --rm upgrade-test:violet-check /app/upgrade --help | head -3
  # 期望: 含 "A tool for testing operator upgrades"
  ```
- 多架构构建：在 amd64 + arm64 各 build 一次（CI matrix），都能跑 `violet --help`
- README 端到端 dry-run：按 README 新增章节，构建一个最小外部测试镜像（mock 一个 dummy `gitlab.test`），跑 `./upgrade --config upgrade.yaml`，至少能进到 violet create 阶段
- CLAUDE.md 验证：内部 review；用 `grep` 确认 violet/olm 包都被提到

### PR 顺序约束

```
PR #1 → PR #2 → PR #3 → PR #4 → PR #5
(重构)  (配置)   (violet)  (rolling)  (基础设施)
```

每个 PR 自身可独立 review、独立 merge、独立回滚。**用户可在任一节点停下来不继续**——例如只到 PR #3，升级语义保持现状不变，但已经获得 violet 标准上架链路；只到 PR #4 才完整实现"upgrade CLI 包含插件自动升级"的目标。

## Pipeline Compatibility & Deployment Surface

> 本节针对 integration-test 集群上的 upgrade-test 流水线兼容性。核心约束：**本仓库的改动默认对所有外部消费者透明**。

### 消费拓扑

本仓库的 `upgrade` 二进制有**两类消费者**，行为约束不同：

| 消费者 | 位置 | 拉取方式 | 当前兼容性 |
|--------|------|----------|-----------|
| 本仓库的工具镜像（`Dockerfile`） | `/app/upgrade` | 仓库内 `go build` | 兼容性责任在本 PR |
| 外部测试镜像（如 `gitlab-operator` 的 `testing/Dockerfile`，参 `README.md:107-152`） | `/tools/bin/upgrade` | `wget https://github.com/AlaudaDevops/upgrade-test/releases/.../upgrade-ubuntu-latest-<arch>` | **本 PR 必须不破坏** |

外部消费者的调用约定（来自 README 第 107-152 行模板）：
- Entrypoint: `gitlab.test`（测试用例 Go binary）；CMD 是 godog 参数
- 环境变量：`KUBECONFIG=<kubeconfig.yaml>`、`TEST_COMMAND=gitlab.test`
- upgrade 调用：`./upgrade --config upgrade.yaml`（默认 `type` 缺省 → operatorhub）

**强约束**：外部测试镜像里**没有装 violet**。如果上游消费者升级 upgrade 二进制版本但**未切到 `type: violet`**，行为必须等价于改动前——这正是 plan 中保留 `operatorhub` 实现并默认不切换的根本原因。

### 兼容性矩阵

| 外部镜像版本 | config.yaml `type` 字段 | 期望行为 |
|--------------|------------------------|---------|
| 旧（无 violet） | 缺省 / `operatorhub` | 行为不变，回归现状 |
| 旧（无 violet） | `violet` | **fail-fast**：启动检测到 violet 不在 PATH，明确报错指向"在测试镜像 Dockerfile 中追加 violet 安装" |
| 新（含 violet） | 缺省 / `operatorhub` | 行为不变（违反原则但允许，方便逐步迁移） |
| 新（含 violet） | `violet` | 走新路径，三步上架 |

### 外部测试镜像迁移建议（写入 README）

参考 `README.md:107-152` 的模板，外部消费者切换到 `type: violet` 需要两步：

1. 在测试镜像 Dockerfile 中新增 violet 二进制下载（与现有 upgrade 二进制同模式）：
   ```dockerfile
   # renovate: datasource=github-releases depName=violet packageName=AlaudaDevops/violet
   ARG VIOLET_VERSION=<pin-version>
   RUN if [ "$(arch)" = "arm64" ] || [ "$(arch)" = "aarch64" ]; then ARCH="arm64"; else ARCH="amd64"; fi; \
       wget https://github.com/AlaudaDevops/violet/releases/download/${VIOLET_VERSION}/violet-linux-${ARCH} && \
       mv violet-linux-${ARCH} /tools/bin/violet && \
       chmod +x /tools/bin/violet
   ```
   （violet 实际发布渠道由 release owner 确认；上述假设为 GitHub release，若为内部 nexus 改下载地址）
2. 在 `upgrade.yaml` 加 `operatorConfig.type: violet` + 必要的 `registry`/`repository` 字段，并配置 platform 凭据（推荐 secret-mount，不要硬编码）

### Tekton Task 集成（integration-test 集群）

本仓库**不直接生产 Tekton Task/Pipeline YAML**（仓库内仅有 `.tekton/pr-manage.yaml` 用于 PR 命令）。在 integration-test 集群启动 PipelineRun 走 `/devops-release:trigger-test`（Thanos API），不是直接 `kubectl apply`。所以**本 PR 不会**改任何 Tekton 资源。

但需要文档明确：
- violet 在 Task 容器内执行 `mktemp -d` 默认指向 `/tmp/violet-*`，需确保 Tekton Task 的 securityContext 不禁用 `/tmp` 写入
- platform token 注入推荐通过 Tekton workspace + secret，不要在 PipelineRun spec 内硬编码
- 失败诊断：Task 失败时保留 violet workDir 路径打到 step log，便于事后 attach 调试容器

### Dockerfile 安装策略（本仓库的工具镜像）

现状 final stage（`Dockerfile:41`）已装：`ca-certificates git make kubectl yq jq helm bash`。violet 安装方案：

- **推荐**：从 GitHub/内部 nexus release 下载二进制（与外部测试镜像方式对齐，renovate 友好）
  ```dockerfile
  ARG VIOLET_VERSION
  RUN wget -O /usr/local/bin/violet \
        https://<violet-release-url>/violet-linux-amd64 && \
      chmod +x /usr/local/bin/violet
  ```
- 不用 `apk add violet`（Alpine 仓库目前没有 violet 包）
- 多架构支持参照现有 `upgrade-test` GitHub Actions matrix（`.github/workflows/build.yml:33`）

启动时检查（`pkg/operator/violet/operator.go`）：
```go
if _, err := exec.LookPath("violet"); err != nil {
    return fmt.Errorf("violet binary not found in PATH; install via Dockerfile (see README) or set PATH; underlying error: %w", err)
}
```

### Acceptance Criteria（流水线兼容性专项）

- [ ] **零改动现有外部消费者**：`type` 缺省的旧 config.yaml 不报错、不警告、行为与改动前 byte-equivalent
- [ ] **violet 缺失场景明确报错**：`type: violet` 但 PATH 无 violet 时，错误信息含「在测试镜像 Dockerfile 中加 violet 安装步骤」与最简 dockerfile snippet 链接
- [ ] **k8sadmin token 提取路径**：当 `platformToken` 未配且 kubeconfig 指向 integration-test 集群时，能自动从 `cpaas-system/k8sadmin` Secret 拿到 token
- [ ] **channel 默认值不变**：未显式配 channel 时 fallback 仍是 `stable`（`pkg/operator/operatorhub/operator.go:107-110`），不引入 violet 模式专属默认
- [ ] **README 文档更新**：在第 107-152 行的镜像构建模板之后加一节「使用 violet 模式」，含上述 Dockerfile snippet 与 upgrade.yaml 示例
- [ ] **在 integration-test 集群验证**：至少一条 upgrade path（建议复用 README 里的 gitlab v17.8 → v17.11 范例）端到端跑通 `type: violet` 模式

## Out of Scope

- 不重写或废弃 `operatorhub` 实现（保留以备 fallback / 紧急调试）
- 不优化 violet package 缓存（同 tag 复用 .tgz 等）—— MVP 之后再做
- 不引入 violet 的 Go SDK（即便存在）—— shell-out 已够用且与 skill 行为完全一致
- 不动 `local` operator
- 不为 violet operator 引入新的 mock 框架；命令拼装单测足矣

## Sources & References

### Internal

- 现状代码：
  - `pkg/operator/operatorhub/artifact_versiong.go:51-90` — 手动创建 ArtifactVersion 的逻辑（待替换）
  - `pkg/operator/operatorhub/subscription.go:18-66` — Subscription 安装流程（待抽取）
  - `pkg/operator/factory.go:39-47` — Operator factory 注册点
  - `pkg/config/config.go:29-52` — OperatorConfig 结构
  - `pkg/exec/exec.go:43-80` — `RunCommand` 已支持实时输出 + 捕获，violet shell-out 直接复用
  - `hack/common.sh:9-53` — kubeconfig/platform URL 提取的 shell 参考实现
  - `Dockerfile:33-55` — final stage 安装位置
- 现状 CLAUDE.md：`/Users/alauda/Projects/DevOps/AlaudaDevops/upgrade-test/CLAUDE.md` — 已记录 `cpaas-system` / OLM source `platform` 硬编码约束

### Skills（决策依据）

- `~/.claude/skills/violet-onboard/SKILL.md` — violet 三步流程、`--skip-push` 模式、常见故障（digest mismatch、token 失败）
- `~/.claude/skills/deploy-operator/SKILL.md` — 为何"必须 violet 创建 Artifact"；OperatorGroup AllNamespaces 约束；现有手动创建的失败模式
- `~/.claude/skills/upgrade-operator/SKILL.md` — ArtifactVersion 所需 label/annotation 清单

### violet CLI 实测

- `violet create --help` — 关键 flag：`--skip-package-images`、`--artifact-name`、`--platforms`（默认 `darwin/arm64/v8`，必须覆盖）
- `violet package --help` — `--output`、`--no-auth`、`--plain`
- `violet push --help` — `--skip-push`、`--platform-address`、`--platform-token`、`--reset-bundle-version`（默认 true，**关键依赖**）

### 全局约束

- `~/.claude/CLAUDE.md`：OperatorGroup 必须 AllNamespaces 模式；S3 配置加到已有 DB secret —— 不直接相关但同源约束，验证测试时注意
- `~/.claude/conditional-rules/k8s-deploy.md:14-19`：violet/OLM 部署的三条硬规则（不要猜 channel、先验证凭据、OLM Subscription ≠ Tekton Trigger Subscription）—— **本 plan 已覆盖**
- `~/.claude/conditional-rules/tekton.md:96`：核心工具链含 violet（Operator 打包/发布），证实 violet 是 Alauda 内部标准上架工具

### Pipeline 集成参考

- `~/.claude/skills/devops-release:trigger-test/`（如存在 SKILL.md）：在 Thanos 平台触发测试任务的标准方式；本仓库不直接调用，但流水线消费者需要遵守
- `~/.claude/skills/violet-onboard/SKILL.md:42-47`：platform URL / k8sadmin token 提取的 shell 参考，Go 实现需移植
- `README.md:107-152`：外部测试镜像构建模板，**violet 模式迁移文档应紧随其后**

### Marketplace Controller 实现（第二轮深化依据）

- 路径：`/Users/alauda/Projects/DevOps/AlaudaDevops/marketplace`
- 5 个 binary：`cmd/app/{controller,olmregistry,api,apiserver,upgradehook}` —— upgrade-test 主要面对 controller 与 olmregistry 两个
- 核心 CRD：`Artifact` / `ArtifactVersion` / `Library`（`pkg/apis/artifact/v1alpha1/`）
- `ArtifactVersion.status.phase` 枚举：`Present` / `Pending` / `Failed` / `Unknown` / `Deleted` / `Deleting`（`pkg/apis/artifact/v1alpha1/artifactversion_types.go:38-45`）
- 上架链路：`pkg/controllers/artifactversion/controller.go:76-254`（reconcile）+ `pkg/controllers/artifactversion/controller.go:335-370`（addBundle）+ `pkg/controllers/library/controller.go:192-246`（CatalogSource）
- gRPC registry + 30s 同步：`pkg/grpcserver/server.go:53`、`pkg/grpcserver/syncer.go:128-164`
- Subscription controller **不主动升级**：`pkg/controllers/subscription/controller.go:48-106`
- **Platform operator 必须 Manual 审批**：`pkg/webhooks/subscription/handler.go:240-243`
- CSV `skipRange`/`replaces` 来源：`pkg/controllers/artifactversion/controller.go:350` via `annotation.GetExpectedValues(av)`
