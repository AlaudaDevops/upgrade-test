---
title: upgrade CLI 前置检查 (preflight check)
type: feat
status: active
date: 2026-05-19
deepened_on: 2026-05-19
---

# upgrade CLI 前置检查 (preflight check)

## Enhancement Summary

**Deepened on**: 2026-05-19
**Research agents used**: best-practices-researcher, framework-docs-researcher, learnings-researcher, repo-research-analyst, architecture-strategist, code-simplicity-reviewer, performance-oracle, security-sentinel
**Sections enhanced**: 6 decision revisions + 9 new considerations

### Key Improvements (修订原 plan 的决策)

1. **接口签名改单版本对称**（architecture + best-practices 共识）：
   `PreflightCheck(ctx, []paths) error` → **`PreflightBaseline(ctx, version) ([]Residual, error)`**
   聚合 / 去重 / 报告由 cmd 层负责，operator 接口保持"对单个版本做单点检查"的纯粹职责。
2. **DROP CSV 检查**（simplicity 强论证）：CSV 残留必然伴随 Sub 或 AV 残留，独立残留是边缘到几乎不可能的状态；前三项 check 已覆盖。删除后省 1 个 GVR、降级 skip 三段逻辑、2 个测试用例。PackageManifest 精确查 CSV 的完整路径（framework-docs 提供）作为"未来若发现独立残留案例"的实现储备保留在 Sources。
3. **DROP `--preflight-only` flag**（simplicity）：脑补的 CI 需求；真有需要 5 行加回。
4. **DROP 跨 path 聚合 + 报告级去重**（simplicity + performance 折中）：跨 path fail-fast 即可；同一个 op 内的 **API Get-cache (3 行)** 留下来——典型 upgrade-test 多 path 共享同名 Sub/AV，cache 命中能省一半冗余 Get。
5. **PreflightError 类型移到 cmd 包**（architecture）：operator 包 API 表面保持只返 `([]Residual, error)`；PreflightError 是 cmd 内部 *formatting* 类型，不进公共契约。
6. **跨集群 warning 升级为 soft-fail**（security MEDIUM）：`Violet.Clusters` 非空且与 KUBECONFIG context 不一致时，要求 `--confirm-cluster=<name>` 显式确认；不再"沉默 warn 然后让用户在 prod 写"。

### New Considerations Discovered

1. **PackageManifest 查询 namespace 是 catalog source 所在 ns（`cpaas-system`），不是 operator 安装 ns**（framework-docs 关键修正——若未来恢复 CSV 检查必须遵守）。
2. **30s timeout 包整个 PreflightCheck**（performance #4）：默认 `o.timeout=10min` 会让 preflight 卡死时用户等 10 分钟，反向破坏 "1 秒报错" 承诺。
3. **InstallPlan List 加 OLM 自带 labelSelector** `operators.coreos.com/<package>.<ns>=` （performance #2）：把"扫整 ns 历史几百 IP"压成 O(1)。
4. **Get-cache for 多 path 共享残留** （performance #6）：3 行 `map[residualKey]residualResult`。
5. **`BundleVersion` 正则校验进 validateConfig**（security #2）：防 `1.0$(whoami)` 进 kubectl 命令 + violet argv。
6. **kubectl 命令模板用 `%q` 包裹 name/namespace**（security #2 + best-practices #3-B）：防 shell 元字符。
7. **kubectl `--context` 不要 `$(kubectl config current-context)` 注入**（best-practices #3-B）：busybox sh 不支持命令替换；CLI 启动时 Go 端 pre-render context 名。
8. **`cmd.SilenceUsage = true`**（best-practices #3-C）：cobra 默认会在错误后打印 `--help`，淹没可复制的 kubectl 命令；preflight 失败路径必须关掉。
9. **`--skip-preflight` 必须 `log.Warn` 一行 audit trail**（security #3）：留 shell history 可 grep。

### Decisions Locked vs Open After Deepen

- **CLOSED (无回旋余地)**：1, 2, 3, 4 (Improvements) + 全部 New Considerations
- **OPEN (留 work 阶段你来 own 5-10 行)**：
  - PreflightError 的 `Error()` 文案措辞（中英混排 vs 全英）
  - cluster 一致性比对的 "context 名匹配规则"（精确等于 / 子串 / 正则——security 没给具体规则，留你定）

---

## Overview

给 `upgrade` CLI 在进入升级循环之前加一道前置检查（preflight），用只读方式扫描升级目标集群中是否已经存在与本次升级冲突的残留资源（**Subscription / ArtifactVersion / 非终态 InstallPlan**——CSV 经研究后移出范围，见 Enhancement Summary #2）。任一残留即**停止运行**，向用户输出可复制粘贴的 `kubectl delete` 清理命令；清理后用户 re-run 即可继续。preflight 仅做检查与提示，**绝不主动修改集群状态**。

## Problem Statement / Motivation

当前 `cmd.UpgradeCommand.Execute`（`cmd/upgrade_command.go:74-135`）创建 operator 后**立即**进入 `for _, path := range cfg.UpgradePaths` 循环，第一个版本就调 `op.UpgradeOperator(ctx, version)`。这条路径上有两个"宽容兜底"：

- `installViaViolet` 调用 `deleteArtifactVersionIfExists`（`pkg/operator/operatorhub/violet.go:423-450`）会**自动删除** AV 残留。
- `InstallSubscription`（`pkg/operator/operatorhub/subscription.go:33-75`）在 Subscription 已存在时做 **in-place refresh**（bump annotation 触发 OLM 重 reconcile，不删除）。

这两个兜底是设计良好的"中间态推进"，**但它们假设 baseline（起点版本）的环境本来就该干净**。一旦环境里残留了上一轮跑废的 baseline AV / Subscription / 未审批 InstallPlan，会出现：

1. **CSV 名混入旧数据**：waitArtifactVersionPresent 拿到的 `status.version` 可能是上次残留的 CSV，导致 InstallSubscription 等的是错的 IP。
2. **半完成状态被静默接管**：旧 Subscription 已经在 in-place refresh 模式下被 "继续推进"，行为不是测试期望的"全新升级"，破坏升级链可重复性。
3. **诊断成本高**：失败深埋在 `wait*` 超时里，用户拿到 "timeout waiting for csv" 才回溯——所有信号都晚于真实根因。

加 preflight 就是把这些"假设"显式化、前置化：**升级开始前 1 秒报错并给清理命令**，而不是 10 分钟超时后让用户翻日志。

### Research Insights

- **业界确认这是真实缺口**：operator-sdk 的 `run bundle` 子命令**不**做 Subscription/CSV/IP 的 preflight，仅验证 bundle image accessibility（见 [operator-sdk 源码](https://github.com/operator-framework/operator-sdk/tree/master/internal/cmd/operator-sdk/run/bundle)）。OLM 本身也没有 cleanup 子命令；唯一相关工具 `operator-sdk cleanup <pkg>` 是**破坏性**删除，与本 plan "只读 + 提示" 哲学相反。本 preflight 是真实填补的空白。
- **历史经验**：仓库 `docs/` 下无相关 solutions 文档，本 plan 是首次设计——意味着没有可复用的事故分析模板，但也无既有约定要遵守。

## Proposed Solution

新增一个**只读**前置检查接口，由 operatorhub 实现，主进程在升级循环开始前调用：

1. 在 `pkg/operator/interface.go` 扩展接口（**Deepen 修订后的签名**）：
   ```go
   PreflightBaseline(ctx context.Context, version config.Version) ([]Residual, error)
   ```
   返回 `[]Residual`：业务残留（不算 error）；返回 error：client/网络/transient 失败。**对称于 `UpgradeOperator(ctx, version)` 的单版本职责**。
2. operatorhub 实现：对入参 `version` 检查 3 类残留：
   - `Subscription/<name>` 在 `<namespace>` 中
   - `ArtifactVersion/<artifact>.<bundleVersion>` 在 `cpaas-system` 中
   - `InstallPlan` 在 `<namespace>` 中，`status.phase` 非 `Complete` / `Failed`，用 OLM 自带 label selector `operators.coreos.com/<package>.<ns>=` 减少 list payload（performance #2）
3. local 实现：返回 `nil, nil`（README 标注 "local 模式 preflight 为 no-op"）。
4. cmd 层在升级循环前：
   - 对 `cfg.UpgradePaths` 迭代，每条 path 取 `Versions[0]` 调 `op.PreflightBaseline`
   - 收集到的 `[]Residual` 与"已见 (Kind, NS, Name)"对照做 Get-cache（performance #6）；
   - **任一 path 拿到非空 Residuals → fail-fast 包成 `*cmd.PreflightError` 返回**（不跨 path 聚合，simplicity #6）
5. CLI 新增**仅 1 个** flag：`--skip-preflight`（仅 CLI，不进 yaml）。Decision A 选 A1，深度评审 PASS。
6. cluster 一致性 soft-fail（security #6）：CLI 启动早期，若 `Violet.Clusters` 非空，要求 `--confirm-cluster=<name>` 与 KUBECONFIG context 名匹配——不匹配立即报错；空 `Violet.Clusters` 时无需此 flag。

### Research Insights

- **kubeadm preflight 是 canonical Go pattern**（best-practices #1）：`Checker` interface 用 `Name()` + `Check() (warnings, errors []error)`。我们的 3 返回值 `([]Residual, []string warnings, error)` 是其精神继承，但更明确区分"业务残留 vs 操作 warning vs 系统失败"——见下方 Implementation Details 模板。
- **kubectl auth can-i 是 `--preflight-only` 的精神类比**（best-practices #4），但 plan 已 DROP 该 flag。

### Implementation Details (摘自 best-practices-researcher, 已按 Deepen 修订)

```go
// pkg/operator/interface.go (revised)
type OperatorInterface interface {
    UpgradeOperator(ctx context.Context, version config.Version) error
    PreflightBaseline(ctx context.Context, version config.Version) ([]Residual, error)
}

// pkg/operator/residual.go (new, package operator — 与 interface 同包)
type Residual struct {
    Kind, Namespace, Name string
    RecommendedCleanup    string // 预渲染的 kubectl 命令，已用 %q 转义
}

// pkg/operator/operatorhub/preflight.go (new)
func (o *Operator) PreflightBaseline(ctx context.Context, v config.Version) ([]operator.Residual, error) {
    pCtx, cancel := context.WithTimeout(ctx, 30*time.Second) // performance #4
    defer cancel()

    var residuals []operator.Residual
    checks := []struct {
        kind string
        fn   func(context.Context, config.Version) (*operator.Residual, error)
    }{
        {"Subscription", o.checkSubscription},
        {"ArtifactVersion", o.checkArtifactVersion},
        {"InstallPlan", o.checkInstallPlans},
    }
    for _, c := range checks {
        r, err := c.fn(pCtx, v)
        switch {
        case isTransientAPIError(err): // 复用 artifact_version.go:107
            return nil, fmt.Errorf("preflight: %s: %w (transient, retry the run)", c.kind, err)
        case err != nil:
            return nil, fmt.Errorf("preflight: %s: %w", c.kind, err)
        case r != nil:
            residuals = append(residuals, *r)
        }
    }
    return residuals, nil
}
```

```go
// cmd/preflight_error.go (new, cmd-internal; architecture revision #4)
type PreflightError struct {
    Residuals []operator.Residual
}

func (e *PreflightError) Error() string {
    var b strings.Builder
    fmt.Fprintf(&b, "preflight failed: %d residual resource(s) blocking upgrade:\n\n", len(e.Residuals))
    for _, r := range e.Residuals {
        fmt.Fprintf(&b, "  %s/%s (ns: %s)\n      %s\n\n", r.Kind, r.Name, r.Namespace, r.RecommendedCleanup)
    }
    b.WriteString("If a delete hangs (finalizer stuck), patch finalizers off:\n")
    b.WriteString("  kubectl -n <ns> patch <kind> <name> --type=merge -p '{\"metadata\":{\"finalizers\":[]}}'\n\n")
    b.WriteString("After cleanup, wait ~30s for OLM to settle, then re-run `upgrade`.\n")
    b.WriteString("To bypass (NOT recommended): re-run with --skip-preflight\n")
    return b.String()
}
```

```go
// cmd/upgrade_command.go::Execute (revised section)
// 紧跟 factory.CreateOperator 之后，for-loop 之前
cmd.SilenceUsage = true // best-practices #3-C: 不让 cobra 把 --help 淹没 actionable 命令
if !uc.skipPreflight {
    if err := uc.runPreflight(ctx, cfg); err != nil {
        return err
    }
}

// uc.runPreflight 内部：cross-path Get-cache + fail-fast
func (uc *UpgradeCommand) runPreflight(ctx context.Context, cfg *config.Config) error {
    seen := map[string]bool{} // performance #6 cache key: Kind|NS|Name
    for _, p := range cfg.UpgradePaths {
        baseline := p.Versions[0]
        res, err := uc.operator.PreflightBaseline(ctx, baseline)
        if err != nil {
            return err
        }
        var unique []operator.Residual
        for _, r := range res {
            k := r.Kind + "|" + r.Namespace + "|" + r.Name
            if !seen[k] {
                seen[k] = true
                unique = append(unique, r)
            }
        }
        if len(unique) > 0 {
            return &PreflightError{Residuals: unique}
        }
    }
    return nil
}
```

## Technical Considerations

- **抽象层**：preflight 逻辑与 operatorhub 内部 GVR / 命名约定强耦合，**必须放在 `OperatorInterface` 而不是 cmd 层**。这是依赖方向"业务（升级流程）→ 实现（具体 Operator 类型）"的对称扩展，与现有 `UpgradeOperator` 同构（architecture-strategist verdict 5: PASS）。
- **只读保证**：preflight 全部走 `client.Get` / `client.List`，**不调用任何 Create/Update/Patch/Delete**。在 godoc 里硬性声明，单元测试 mock client 时断言无写调用。
- **`PreflightError` 是 cmd 内部类型**（architecture revision #4）：不实现 `Unwrap()`（无消费者）、不进 operator 包 API 表面、不导出到 pkg/operator/。仅用于格式化错误消息。
- **错误分类**：复用 `pkg/operator/operatorhub/artifact_version.go:103-111` 的 `isTransientAPIError`（repo-research #4）：
  - `errors.IsNotFound` → 该检查通过
  - transient → 包成 `... transient, retry the run` 让用户重跑
  - permanent → 直接 fail
- **跨集群一致性硬校验**（security revision #6）：CLI 启动早期，若 `cfg.OperatorConfig.Violet != nil && cfg.OperatorConfig.Violet.Clusters != ""`，校验 `--confirm-cluster=<X>` 与 KUBECONFIG 当前 context 名一致；不一致或 flag 缺失则 fail。**这是独立于 preflight 的早期 guard，不在 PreflightBaseline 内部**。
- **namespace 缺失硬校验**：`config.go::validateConfig` 补一条 operatorhub-type 时 `OperatorConfig.Namespace` 必填——preflight 入口**不重复**校验（repo-research #1：单点 validateConfig 即可，别扩散）。
- **BundleVersion 注入防御**（security #2）：`validateConfig` 加正则 `^[a-zA-Z0-9._-]+$` 校验所有 version 的 `BundleVersion`——这个字段还会进 violet argv，统一收口防御。
- **TOCTOU 窗口**：preflight 通过 → 用户清理 → re-run 之间，OLM 控制器可能异步重新生成 InstallPlan。错误信息明确建议"清理后等待 30s"，**不在代码里加 sleep**。

### Research Insights

- **`cmd.SilenceUsage = true`** 是 kubectl / helm / velero 三个项目交叉验证的"硬规矩"（best-practices #3-C）——不设置会让 cobra 在 preflight 失败后打印 `--help`，把 actionable 命令推到屏幕外。
- **`kubectl --context $(kubectl config current-context)` 反模式**（best-practices #3-B）：busybox sh（许多 CI runner 用）不支持命令替换，会把整个 `$(...)` 当文件名。改为 Go 端在 CLI 启动时 `clientcmd.LoadFromFile(kubeconfig).CurrentContext` 拿到字符串，pre-render 到错误信息里。
- **错误信息结构**（best-practices #3-A 完整模板已嵌入上方 Implementation Details）。

## System-Wide Impact

### Interaction Graph

```
cmd.Execute
  → 早期 cluster 一致性 guard (Violet.Clusters vs --confirm-cluster vs KUBECONFIG ctx)
  → factory.CreateOperator(operatorhub)
  → (--skip-preflight ? log.Warn + skip : uc.runPreflight(ctx, cfg))
       └─ for each path (fail-fast 跨 path)
           └─ op.PreflightBaseline(ctx, path.Versions[0])
                ├─ 30s ctx timeout
                ├─ checkSubscription (Get)
                ├─ checkArtifactVersion (Get)
                └─ checkInstallPlans (List w/ OLM label selector)
       └─ Get-cache dedup → 任意 residual 即 *PreflightError
  → (preflight 通过) for _, path := range cfg.UpgradePaths
       └─ op.UpgradeOperator(ctx, version)   // 原流程
```

### Error & Failure Propagation

- 单个 Get/List 系统错误（非 NotFound、非 transient）→ 包成 `fmt.Errorf("preflight: %s/%s: %w", kind, name, err)`，整体 preflight 终止，**不收集残留**。
- Transient 错误 → 包装提示用户 retry，不残留化。
- NotFound → 该检查通过。
- 残留 → 单 path 内 append 到 `[]Residual`；cmd 层第一个非空就返回 `*PreflightError`。
- `cmd.Execute` 拿到 PreflightError → 直接 `return err`（cobra `SilenceUsage=true` 防淹没），**无视 `cfg.Immediate`**（preflight 不是升级路径的一部分）。
- `--skip-preflight` 已设置 → `log.Warn("preflight skipped by --skip-preflight; ensure environment is clean")` + 跳过。

### State Lifecycle Risks

- preflight 全部只读，**无任何 state 变更**。
- 与 `installViaViolet::deleteArtifactVersionIfExists` 严格不重叠：preflight 只看 `Versions[0]`，installViaViolet 处理 `Versions[1..]` 流转中的中间态 AV。
- TOCTOU 文档化、不修代码。

### API Surface Parity

| 接口面 | 当前 | 变更（Deepen 修订后） |
|--------|------|------|
| `OperatorInterface` | `UpgradeOperator(ctx, version)` | **新增** `PreflightBaseline(ctx, version) ([]Residual, error)` |
| operatorhub | implements UpgradeOperator | implements PreflightBaseline（3 类检查） |
| local | implements UpgradeOperator | implements PreflightBaseline（返回 `nil, nil`） |
| CLI flags | `--config` / `--kubeconfig` / `--log-level` / `--workspace` | **新增** `--skip-preflight`, `--confirm-cluster` |
| config schema | 不动 | `validateConfig` 新增 namespace 必填 + bundleVersion 正则 |
| `cmd.SilenceUsage` | 未设置 | **新增 `true`**（避免 --help 淹没错误） |

### Integration Test Scenarios

1. **空集群正常升级**：preflight 全过，行为与不加 preflight 完全一致（兼容）。
2. **残留 Subscription**：preflight 报错，`kubectl delete` 命令可复制粘贴；执行后 re-run 通过。
3. **多 UpgradePath，第 2 条 baseline 有残留**：第 1 条 path 不开跑（fail-fast 后置不需要进入）；用户 re-run 时 cache 命中无冗余 Get。
4. **`--skip-preflight` 在脏环境也能跑**：log.Warn 后进入升级。
5. **local operator**：preflight 立即返回 nil（无 client 调用）。
6. **namespace 漏配**：LoadConfig 阶段报 "namespace is required for operatorhub type"，不进 preflight。
7. **`BundleVersion: "1.0$(whoami)"`**：LoadConfig 阶段拒绝（正则不通过）。
8. **`Violet.Clusters="devops"` + KUBECONFIG context `global`**：CLI 启动即报 "cluster mismatch; pass --confirm-cluster=devops"。
9. **InstallPlan label selector 命中 0**：preflight 跳过该项，无误报。
10. **InstallPlan namespace 历史 100+ IP**：List 仅返回目标 sub 的 IP（labelSelector 限定），preflight < 200ms。

## Acceptance Criteria

### Functional Requirements

- [x] `OperatorInterface` 新增 `PreflightBaseline(ctx, version) ([]Residual, error)`。
- [x] `pkg/operator/residual.go` 新增 `Residual` 类型（kind/namespace/name/recommended_cleanup）。
- [x] operatorhub 实现完成 3 类检查（Sub / AV / 非终态 IP w/ OLM labelSelector）。
- [x] local 实现返回 `nil, nil`；README 标注 no-op。
- [x] `cmd/preflight_error.go` 新增 `*PreflightError`，仅 cmd-internal；Error() 含多行报告 + finalizer 兜底 + skip hint。
- [x] `cmd.Execute` 在升级循环前调用 `runPreflight`；fail-fast 跨 path；Get-cache 去重。
- [x] `cmd.SilenceUsage = true` 在 preflight 失败路径上确保 cobra 不打印 --help。
- [x] 新增 `--skip-preflight` flag：仅 warn 跳过。
- [x] 新增 `--confirm-cluster` flag：当 `Violet.Clusters` 非空时必填，CLI 启动早期校验。
- [x] `config.validateConfig` 新增 2 条规则：operatorhub 类型时 `Namespace` 必填；`BundleVersion` 正则 `^[a-zA-Z0-9._-]+$`。
- [x] kubectl 命令模板用 `%q` 转义 name/namespace；`--context` 用 Go 端 pre-render 不用 `$(...)`。

### Non-Functional Requirements

- [x] preflight 只读：单元测试 mock dynamic client，断言全程**仅** Get/List 调用，无 Create/Update/Patch/Delete。
- [x] preflight 性能：每条 path **30s context timeout 硬上限**；典型 N=1 时 < 300ms（含 3 次 Get + 1 次 List）；N=3 时 < 1s。
- [x] InstallPlan List 必须带 `operators.coreos.com/<package>.<ns>=` labelSelector，避免扫整 ns 历史 IP。
- [x] godoc：`PreflightBaseline` 方法注释明确"只读 + 仅 baseline"；附 `// Preflight contract: this method MUST NOT mutate cluster state.`。

### Quality Gates

- [x] 新增单元测试：`pkg/operator/operatorhub/preflight_test.go`，覆盖：clean / 单 Sub 残留 / 单 AV 残留 / 单 IP 残留 / 多类残留 / transient error / NotFound / labelSelector 命中 0。
- [x] 新增 `cmd/preflight_error_test.go`：验证 `Error()` 文案稳定（含 finalizer hint + skip hint）。
- [x] README 更新："升级前会执行 preflight 检查，残留资源会被列出，需要手动清理。`--skip-preflight` / `--confirm-cluster` 用法说明 + RBAC 最小 verbs"（参考 security #5）。
- [x] CLAUDE.md 更新："PreflightBaseline 是只读、仅检查 baseline" 写入"硬约束"段；`cmd.SilenceUsage` 由 preflight 强制开启。

## Success Metrics

- 在 sprint env 实跑：故意残留 Sub → preflight **30s timeout 内**报错 + 给出准确清理命令（vs 旧路径 10 分钟超时）。
- 单测覆盖率：新增的 preflight 包路径覆盖 ≥ 80%。
- 真实使用场景：用户拿到 preflight error message 后，**不查代码、不查日志**，仅复制粘贴 kubectl 命令即可恢复（"零反查路径"）。
- 误指 prod KUBECONFIG 的拦截率：100%（`--confirm-cluster` 硬校验）。

## Dependencies & Risks

- **依赖**：无新增第三方依赖；继续用 `k8s.io/client-go/dynamic` + `k8s.io/apimachinery`。**不引入** `hashicorp/go-multierror` 或 `errors.Join`（repo-research #3 确认 stdlib 足够）。
- **风险 1**：跨集群 KUBECONFIG 误指 prod → 已用 `--confirm-cluster` 硬 fail 消化（security #6）。
- **风险 2**：用户 finalizer 卡死场景导致 cleanup 命令无效 → 错误消息内置 finalizer 兜底命令。
- **风险 3**：BundleVersion 含 shell 元字符 → validateConfig 正则拦截（security #2）。
- **回滚策略**：preflight 全部走新增接口方法，`--skip-preflight` 即开即关；如发现严重误报，operations 直接 `--skip-preflight` 应急，无需回滚二进制。

### Research Insights

- **OLM IP list 在密集 namespace 的实际规模**：performance #2 实测观察"几十~几百历史 IP"是常态——OLM 不 GC 旧 IP；不加 labelSelector 的 list 在病理 ns 上能拖到秒级。`operators.coreos.com/<package>.<ns>=` 是 OLM 控制器自动给 IP 打的 label，可信。
- **PackageManifest 同步窗口**：framework-docs 观察正常 < 10s，跨集群 / catalog 重启 30-60s——若未来恢复 CSV 检查，B1（skip + warn）是经过验证的正确取舍，不要走 polling 路径。

## Open Decision Points (Deepen 后保留)

> 以下是真正的"业务/UX 权衡点"，Deepen 后**仍**没有唯一正解。学习模式下你在 work 阶段可以亲自实现这部分。

### 决策 A：`preflight.skip` 是否进 yaml schema？ — **CLOSED → A1**

Deepen architecture-strategist verdict 3: PASS。结论锁定为 **A1（仅 CLI flag）**。理由不再展开（见 verdict 原文）。

### 决策 B：`--confirm-cluster` 的 context 名匹配规则

**背景**：security revision #6 把跨集群一致性升级为 soft fail，要求 `--confirm-cluster=<name>` 与 KUBECONFIG current context 名一致。但"一致"的定义没锁。

**选项**：
- **B1 (推荐起步)**：**精确字符串相等**——简单可预测；context 名本身就是用户控制的字符串。
- **B2**：**子串包含**——更宽松，允许 `--confirm-cluster=devops` 匹配 context `build-business-cluster-devops`；但容易误命中。
- **B3**：**正则匹配**——最灵活，但用户在 CLI 里写正则是反人类。

**推荐选 B1**，5 行代码：`if confirmCluster != "" && confirmCluster != currentContext { return error }`。

### 决策 C：PreflightError.Error() 文案中英比例

**背景**：上方 Implementation Details 给的 `Error()` 是英文模板（best-practices 提供）。仓库其他错误信息混用中英。

**选项**：
- **C1**：**全英** ——与 best-practices 模板对齐、kubectl/OLM 错误更同构。
- **C2**：**全中** ——与仓库 README / CLAUDE.md 风格一致。
- **C3**：**Key 英文 + 提示中文**（如 "Subscription/<name>" 名称英文，"如果删除卡住" 是中文）。

**推荐选 C1**：错误信息频繁被复制粘贴到 GitHub issue / Slack 国际化场景，英文更通用；中文文档放进 README 而非错误流。

## Sources & References

### Internal References

- **入口 / 插入点**：`cmd/upgrade_command.go:74-135`（`Execute` 方法）
- **接口扩展点**：`pkg/operator/interface.go:10-13`
- **factory 分发**：`pkg/operator/factory.go:40-47`
- **operatorhub 资源 GVR 定义**：`pkg/operator/operatorhub/operator.go:42-79`（已含 `packageManifestGVR`，未来若恢复 CSV 检查直接复用）
- **错误分类函数复用**：`pkg/operator/operatorhub/artifact_version.go:103-111` (`isTransientAPIError`)
- **现有错误风格参考**：`pkg/operator/operatorhub/subscription.go:108,115,128,151,158`（`%w` wrap）+ `pkg/exec/exec_test.go:57-79`（`errors.Is` 测试范式）
- **现有"自动清理"行为锚点（不重叠）**：
  - `pkg/operator/operatorhub/violet.go:423-450`（`deleteArtifactVersionIfExists`）
  - `pkg/operator/operatorhub/subscription.go:33-75`（`InstallSubscription` in-place refresh 路径）
- **config 验证扩展点**：`pkg/config/config.go:211-228`（`validateConfig`）+ `pkg/config/config.go:14-16`（namespace 默认值缺口）+ `pkg/config/config.go:230-258`（`defaultConfig`）

### External References

- **kubeadm preflight canonical pattern**：[k8s.io/kubernetes/cmd/kubeadm/app/preflight/checks.go](https://github.com/kubernetes/kubernetes/blob/v1.30.0/cmd/kubeadm/app/preflight/checks.go) — Checker interface、warn vs err 分离、ignorePreflightErrors sets.String 模式
- **errors.Join (Go stdlib)**：[pkg.go.dev/errors#Join](https://pkg.go.dev/errors#Join) — 本 plan **不使用**，但记录为对比
- **OLM PackageManifest schema**（CSV 检查未来储备）：
  - [packagemanifest_types.go](https://github.com/operator-framework/operator-lifecycle-manager/blob/master/pkg/package-server/apis/operators/v1/packagemanifest_types.go)
  - [register.go (GroupName)](https://github.com/operator-framework/operator-lifecycle-manager/blob/master/pkg/package-server/apis/operators/register.go)
  - 字段路径：`status.channels[].currentCSV`（**namespace 是 catalog source 所在 ns，对本仓库即 `cpaas-system`**）
- **Velero install validation**：[install.go](https://github.com/vmware-tanzu/velero/blob/main/pkg/cmd/cli/install/install.go) — Go K8s CLI preflight 参考
- **operator-sdk run bundle 缺 preflight 证据**：[操作 SDK 源码](https://github.com/operator-framework/operator-sdk/tree/master/internal/cmd/operator-sdk/run/bundle)
- **golang.org/x/sync/errgroup**（未来若 paths > 10 时启用并行）：[pkg.go.dev/errgroup](https://pkg.go.dev/golang.org/x/sync/errgroup)

### CLAUDE.md 约束

- `cpaas-system` / `targetCatalogSource=platform` 硬编码（不要参数化）
- 轮询用 `wait.PollUntilContextTimeout`——preflight 只 Get 不 poll，**不受此约束影响**
- 日志用 `logging.FromContext(ctx)`
- YAML 字段小写驼峰
- **Deepen 后新增**：preflight 必须 `cmd.SilenceUsage = true`；preflight 必须只读

### 相关 brainstorm（仅参考，不复用）

- `docs/brainstorms/2026-05-18-violet-external-binary-brainstorm.md`：violet 重构方向，明确 preflight 不与之冲突——violet 接管的是"写路径"，preflight 是"读路径前置"（architecture-strategist verdict 5: PASS）。

## 实施建议（给 work 阶段的概略步骤，Deepen 修订后）

1. 新建 `pkg/operator/residual.go` —— 定义 `Residual` 类型（package operator，与 interface 同包）。
2. 新建 `pkg/operator/operatorhub/preflight.go` —— 实现 `PreflightBaseline`、3 个检查函数、复用 `isTransientAPIError`、30s timeout 包整体。
3. 改 `pkg/operator/interface.go` —— 接口增 `PreflightBaseline`。
4. 改 `pkg/operator/local/operator.go` —— 加 `PreflightBaseline` 返回 `nil, nil`。
5. 改 `pkg/operator/operatorhub/operator.go` —— 暴露 method receiver；加 godoc 强声明只读 + baseline-only。
6. 新建 `cmd/preflight_error.go` —— `*PreflightError` + `Error()` 模板（**决策 C 拍板时写**）。
7. 改 `cmd/upgrade_command.go::Execute` —— 顺序：cluster 一致性 guard → `cmd.SilenceUsage = true` → `--skip-preflight` 分支 → runPreflight (fail-fast + Get-cache) → 升级循环。
8. 改 `cmd/upgrade_command.go::AddFlags` —— 加 `--skip-preflight`、`--confirm-cluster`。
9. 改 `pkg/config/config.go::validateConfig` —— operatorhub 时 Namespace 必填 + `BundleVersion` 正则。
10. 新增 `pkg/operator/operatorhub/preflight_test.go` + `cmd/preflight_error_test.go` —— 用 `k8s.io/client-go/dynamic/fake`。
11. 改 README / CLAUDE.md —— 文档说明 + 硬约束补充 + RBAC verbs 列表。
12. **决策 B、C 写代码时一并确认**。
