---
status: complete
priority: p1
issue_id: "014"
tags: [code-review, security, preflight, pr-17]
dependencies: []
---

# Cluster guard 在 `current-context: ""` 时静默放行

## Problem Statement

`assertClusterMatch` 当 KUBECONFIG 能读到但 `apiCfg.CurrentContext == ""` 时（多 context 文件且未 `kubectl config use-context`），只 log.Warn 后 `return nil`。

但**空 current-context 是用户配置错误**，不是环境约束。允许 silent pass = 跑过 preflight → 进入升级 → kubectl-client 自己选默认 context（或失败），又一次"silent 假成功"。

## Findings

来源：silent-failure-hunter 评审 #4（FIX 级别）

- `cmd/upgrade_command.go:200-208` 当前实现：
  ```go
  if currentCtx == "" {
      uc.logger.Warn(
          "violet.clusters is set but kubeconfig has no current-context; cluster identity NOT verified",
          ...
      )
      return nil
  }
  ```

## Proposed Solutions

### 选项 1（推荐）：直接报错

```go
return fmt.Errorf(
    "violet.clusters=%q but kubeconfig %s has no current-context; run `kubectl config use-context <name>` first",
    violetClusters, kubeconfig,
)
```

- pros: 用户拿到 actionable 命令；不再 silent
- cons: 已有"裸 multi-context kubeconfig + 多个 path"的工作流会断
- effort: Trivial (~5 LOC)
- risk: 用户工作流断裂 — 但本就是用户应该 fix 的问题

### 选项 2：强制要求 `--confirm-cluster` 已设（与 #013 一致策略）

current-context 空 → 退化到 in-cluster 行为（必须有 --confirm-cluster），用 --confirm-cluster 与 violet.clusters 比对。

- pros: 与 #013 行为对称（in-cluster vs empty-ctx 走同一处理）
- cons: 略难解释（为什么 empty ctx 等于 in-cluster）

## Recommended Action

待 triage。推荐选项 1（fail-loud 比 fail-symmetric 更易解释）。

## Technical Details

- 文件：`cmd/upgrade_command.go::assertClusterMatch`
- 与 #013 共享文件，建议同一 PR 修复

## Acceptance Criteria

- [ ] `violet.clusters != "" && CurrentContext == ""` 必须 return error
- [ ] 错误消息含 "run `kubectl config use-context` first" actionable hint
- [ ] 单测覆盖

## Work Log

_待开始_

## Resources

- PR #17
- silent-failure-hunter verdict #4
