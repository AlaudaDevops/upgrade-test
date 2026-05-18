---
name: channel-default-inconsistency
description: BuildPackageURL 对空 channel fail-fast，但 UpgradeOperator 紧接着的 InstallSubscription 把空 channel 静默回退成 "stable"，同一调用链两套策略
status: pending
priority: p2
issue_id: "008"
tags: [code-review, quality, correctness, p2]
dependencies: []
---

# Problem Statement

同一字段 `version.Channel` 在同一次升级流程中被两个 caller 用截然相反的策略处理：

1. `violet.go:55-56` `BuildPackageURL` ——空 channel **直接报错**：`"channel is empty (Version.Channel is required when using violet)"`
2. `operator.go:118-121` `UpgradeOperator` ——空 channel **静默回退到 "stable"**：
   ```go
   channel := version.Channel
   if channel == "" {
       channel = "stable" // default fallback
   }
   ```

当前因为 `installViaViolet` 跑在前面，空 channel 早就被拦在 BuildPackageURL 阶段，所以 (2) 是 dead branch。但只要未来有任何非 violet 的 install 路径（或 violet 路径分支早返回），(2) 立刻生效——用户在 YAML 里手抖打成 `chanel:` 或者忘填，Subscription 静默走 stable channel，**升级在错误的 channel 上"成功"**，后果是装错 bundle。

# Findings

- **silent-failure-hunter** F7（MEDIUM）+ **type-design-analyzer** T5（关于 Channel 字段语义）双重命中
- 文件：`pkg/operator/operatorhub/operator.go:118-121`、`pkg/operator/operatorhub/violet.go:55-56`
- 类别：同一字段、同一调用链上的策略不一致

# Proposed Solutions

**Option A（推荐）**：移除 operator.go 的 silent fallback；与 BuildPackageURL 对齐到 "channel 必填，空即报错"。

```go
if version.Channel == "" {
    return fmt.Errorf("version.channel is required")
}
if err := o.InstallSubscription(ctx, csv, version.Channel); err != nil { ... }
```

或者更进一步，在 `Config.Validate()`（参见 todo 010）阶段提前拦下。

- 优点：单一策略；fail-fast；与 todo 010 天然合流
- 缺点：现有 config 如果省略 channel 会立即失败（实际上必然已经在 BuildPackageURL 失败了，所以是 no-op 用户感知）
- effort: Negligible
- risk: Low

**Option B**：反向对齐——`BuildPackageURL` 也回退到 stable。

- 优点：宽容
- 缺点：把 BuildPackageURL 的 fail-fast 设计意图打掉；rejected
- effort: Negligible
- risk: Medium（弱化错误检测）

# Recommended Action

(待 triage)

# Technical Details

- 影响文件：`pkg/operator/operatorhub/operator.go:118-121`
- 配合 todo 010（VioletConfig.Validate）一并实现，把 `channel != ""` 提到 config 加载阶段

# Acceptance Criteria

- [ ] 移除 operator.go:118-121 的 silent "stable" fallback
- [ ] 空 channel 在 `Config.Validate()` 或 `UpgradeOperator` 入口报错
- [ ] 错误信息明确指向 bundleVersion + Channel 字段
- [ ] 现有测试不退化

# Work Log

- 2026-05-18: code review 发现 by silent-failure-hunter + type-design-analyzer

# Resources

- PR: https://github.com/AlaudaDevops/upgrade-test/pull/14
- 关联 todo: 010（VioletConfig.Validate 总入口）
