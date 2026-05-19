---
status: pending
priority: p3
issue_id: "021"
tags: [code-review, simplicity, pr-17]
dependencies: []
---

# `preflight.go` 内部小简化（checks slice / phases map）

## Problem Statement

code-simplicity-reviewer 提出两处轻量过度抽象：

1. **`checks []struct{name, fn}` 三件套循环**（`preflight.go:52-71`）：3 个静态调用伪装成数据驱动。新增 check = 改 struct slice + 加方法 + 改测试，与直接加一行调用同代价。
2. **`installPlanTerminalPhases` map**（`preflight.go:30-33`）：2 entry 的 `map[string]struct{}` 用 `switch phase { case "Complete", "Failed": continue }` 更直白。

合计减 ~15 LOC，**无行为变化**。

## Findings

来源：code-simplicity-reviewer SIMPLIFY #2 + #3

## Proposed Solutions

### 选项 1（推荐）：双合一改

```go
// preflight.go
func (o *Operator) PreflightBaseline(ctx context.Context, version config.Version) ([]preflight.Residual, error) {
    pCtx, cancel := context.WithTimeout(ctx, preflightTimeout)
    defer cancel()

    var residuals []preflight.Residual
    for _, fn := range []checkFn{
        o.checkSubscriptionResidue,
        o.checkArtifactVersionResidue,
        o.checkInstallPlanResidue,
    } {
        // 错误分类逻辑保持原样
    }
    // ...
}
```

或更直接：3 个独立调用 + helper for err wrapping。

phases map → switch case。

- pros: 减 LOC、意图清晰
- cons: 重构本身需要谨慎不要破坏单测
- effort: Small

### 选项 2：保持现状

- "未来或许会加 check" — 但实际加 check 是单点 commit，今天的复杂度不必为想象的未来买单。
- 否决（按 simplicity 论证）

## Recommended Action

待 triage。

## Acceptance Criteria

- [ ] `checks` slice 删除，3 个 check 直接顺序调用
- [ ] `installPlanTerminalPhases` map 删除，改用 switch
- [ ] 所有现有单测仍 pass

## Work Log

_待开始_

## Resources

- PR #17
- code-simplicity-reviewer SIMPLIFY #2 + #3
