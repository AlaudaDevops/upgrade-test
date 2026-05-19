---
status: pending
priority: p3
issue_id: "023"
tags: [code-review, simplicity, cleanup, pr-17]
dependencies: []
---

# 杂项清理（test / log / 注释）

## Problem Statement

多个 reviewer 抓到的低风险机械改动：

1. **DROP `TestPreflightError_ZeroResidualsStillReadable`**（simplicity + pr-test #8）：测试自己注释承认 "not expected at runtime"。Go 的 `len`/`for range`/`fmt.Fprintf` 对空 slice 安全是语言保证，不需要测试。
2. **`cmd.SilenceUsage = true` 挪出 `AddFlags`**（simplicity #7）：`AddFlags` 名字承诺加 flag，把 cobra 行为开关放这里 surprise。改放在 root command 构造处（`cmd/root.go` 的 `cobra.Command{}` 字面量字段）。
3. **`local.PreflightBaseline` 加 Info log**（silent-failure #1）：当前 `return nil, nil` 完全静默。一行 `log.Infow("preflight: local operator — OLM residue scan skipped")` 把"silent skip"转为"documented skip"。
4. **`isTransientAPIError(nil) == false` 注释 lock invariant**（silent-failure #6）：在 PreflightBaseline switch 上方加一行注释明确这个 invariant，未来 reader 不必反查 helper 实现。
5. **`assertClusterMatch` 两个 warn 分支合并**（simplicity #6）：in-cluster 和 empty-currentCtx 现在是两个独立的"无法读到 context"分支，可合并。**注意**：与 #013/#014 修复冲突（那两个 P1 把 warn 改 error）—— 等 #013/#014 落地后再考虑是否还需要合并。

## Findings

来源（合并）：simplicity DROP #4 + #6 + #7 + silent-failure #1 + #6

## Proposed Solutions

按条目分别处理，无 trade-off 争议。可一次性 commit。

## Recommended Action

待 triage。**注意**：第 5 条与 #013/#014 dependency。

## Technical Details

- 文件：`cmd/preflight_error_test.go`（删测试）、`cmd/root.go` + `cmd/upgrade_command.go`（SilenceUsage 挪位）、`pkg/operator/local/operator.go`（Info log）、`pkg/operator/operatorhub/preflight.go`（注释）
- 估算总改动：净 -10 LOC

## Acceptance Criteria

- [ ] `TestPreflightError_ZeroResidualsStillReadable` 删除
- [ ] `SilenceUsage` 不在 `AddFlags` 里
- [ ] `local.PreflightBaseline` 有一行 Info log
- [ ] `PreflightBaseline` switch 上方有 "isTransientAPIError(nil)==false invariant" 注释
- [ ] `assertClusterMatch` 分支合并视 #013/#014 状态决定

## Work Log

_待开始_

## Resources

- PR #17
- code-simplicity-reviewer DROP #4, SIMPLIFY #6 + #7
- silent-failure-hunter #1 + #6
