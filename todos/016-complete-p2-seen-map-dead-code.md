---
status: complete
priority: p2
issue_id: "016"
tags: [code-review, simplicity, yagni, pr-17]
dependencies: []
---

# `runPreflight` 的 `seen` map 是 fail-fast 下的 dead code

## Problem Statement

`cmd/upgrade_command.go:233-258` 的 `runPreflight` 用 `seen map[string]struct{}` 去重 Residuals。但实现是 **fail-fast 跨 path**——第一条 path 产生非空 unique 就 return。map 永远只有 0 或 1 个 entry，dedup 行为永远不触发。

注释 "harmless when fail-fast triggers, pays off if loop is ever changed to aggregate" 是教科书 YAGNI —— 为不存在且不在 roadmap 的行为埋复杂度。

两位独立 reviewer（architecture-strategist + code-simplicity-reviewer）给出相同结论。

## Findings

- architecture-strategist Verdict #5: CHANGE — 唯一 must-fix
- code-simplicity-reviewer DROP #1: 减 14 LOC
- 数字：当前 ~25 LOC 含 map + key 拼接 + dup 检测；瘦版 ~12 LOC

## Proposed Solutions

### 选项 1（推荐，两位 reviewer 一致）：删 seen map

```go
func (uc *UpgradeCommand) runPreflight(ctx context.Context) error {
    for _, path := range uc.config.UpgradePaths {
        if len(path.Versions) == 0 {
            continue
        }
        residuals, err := uc.operator.PreflightBaseline(ctx, path.Versions[0])
        if err != nil {
            return err
        }
        if len(residuals) > 0 {
            return &PreflightError{Residuals: residuals}
        }
    }
    return nil
}
```

- pros: 减 14 LOC，意图清晰
- cons: 改聚合模式时要重新加 dedup（可逆代价 5 行）
- effort: Trivial

### 选项 2：保留 + 单测覆盖 dedup

加单测证明 dedup 真的工作，把 dead code 转为活代码。但需要先决定"为什么 fail-fast"——如果保留 fail-fast 则 dedup 仍 dead。

- 否决，与 fail-fast 设计矛盾

## Recommended Action

待 triage。推荐选项 1。

## Technical Details

- 文件：`cmd/upgrade_command.go::runPreflight`
- 单测 dependency：#017（runPreflight 当前无单测）

## Acceptance Criteria

- [ ] 删除 `seen` map 及相关 dedup 逻辑
- [ ] 行为保持："fail-fast 在第一条有残留的 path 即停"
- [ ] 单测覆盖（与 #017 合并）

## Work Log

_待开始_

## Resources

- PR #17
- architecture-strategist verdict #5
- code-simplicity-reviewer DROP #1
