---
status: complete
priority: p2
issue_id: "017"
tags: [code-review, testing, pr-17]
dependencies: []
---

# `cmd/upgrade_command.go` 新加的硬错误逻辑 0 单测

## Problem Statement

PR 在 `cmd/upgrade_command.go` 新增 ~75 LOC 真实硬错误逻辑（`assertClusterMatch` + `runPreflight`），但**没有 cmd 包单测文件**。pr-test-analyzer 评 criticality 9（最高）。

具体未覆盖分支：

### `assertClusterMatch`
- (a) `Violet == nil` → no-op
- (b) `Clusters == ""` → no-op
- (c) 空 kubeconfig → warn-and-pass（**#013 待 fix 后**：error）
- (d) `LoadFromFile` error → 包错返回
- (e) 空 `CurrentContext` → warn-and-pass（**#014 待 fix 后**：error）
- (f) `--confirm-cluster == ""` → error（"required" message）
- (g) mismatch → error（"does not match" message）
- (h) exact match → pass

### `runPreflight`
- (i) `len(Versions) == 0` 路径跳过
- (j) 只传 baseline (`Versions[0]`)，不传 v1/v2
- (k) fail-fast — 第二条 path 的 PreflightBaseline **不被调用**（用 fake `OperatorInterface` 计数验证）
- (l) `PreflightBaseline` 返回 error 时原样 return
- (m) `--skip-preflight` 设置时整段 bypass

## Findings

来源：pr-test-analyzer Critical Gap #1 + #2

回归保护价值：guard 反向条件（如 #013/#014 修复后倒退回 warn）会被这些测试 catch 住。

## Proposed Solutions

### 选项 1（推荐）：新建 `cmd/upgrade_command_test.go`

- 用 fake `OperatorInterface`（mock `UpgradeOperator` + `PreflightBaseline`）
- 用 `t.TempDir()` 写 minimal kubeconfig YAML fixture
- table-driven 覆盖上述 12 个分支
- 估算 ~150-200 LOC

### 选项 2：把 assertClusterMatch / runPreflight 重构成可独立测试的 pure-ish 函数

剥离对 `UpgradeCommand` struct 的依赖（接收明确 params 而非 receiver）。便于测试但破坏现有内聚。

- 否决：测试不该驱动 over-refactor

## Recommended Action

待 triage。选项 1。

## Technical Details

- 新建文件：`cmd/upgrade_command_test.go`
- 与 #013/#014（修 silent pass）同 PR 完成，可同时验证修复 + 测试

## Acceptance Criteria

- [ ] 12 个 listed 分支都有单测
- [ ] 单测命名：`TestAssertClusterMatch_*` / `TestRunPreflight_*`
- [ ] fake OperatorInterface 至少能 count 调用次数
- [ ] kubeconfig fixture 用 t.TempDir，不污染仓库

## Work Log

_待开始_

## Resources

- PR #17
- pr-test-analyzer Critical Gap #1 + #2
