---
status: complete
priority: p1
issue_id: "013"
tags: [code-review, security, preflight, pr-17]
dependencies: []
---

# Cluster guard 在 in-cluster + `violet.clusters` 非空时静默放行

## Problem Statement

`assertClusterMatch` 当 `cfg.OperatorConfig.Violet.Clusters` 非空但 KUBECONFIG 是空（in-cluster 模式）时，只打了一行 `log.Warn` 就 `return nil` 放行。

但是 —— **`violet.clusters` 被显式设置正是 "我在多集群 ACP 环境，必须确认目标"**。in-cluster 模式恰恰是这条 guard 最该硬拦的场景：CI pod 里 `KUBECONFIG=` 但 `violet.clusters=devops` → 跑 `upgrade` 直接写到 prod，warn 没人看。

这是 PR 自己的 value proposition 反语义：preflight 设计目的是"silent 假成功的对称防御"，结果该防御本身在最重要的场景下 silently 放行。

## Findings

来源：silent-failure-hunter 评审 #2（FIX 级别）+ security-sentinel #4（MEDIUM）

- `cmd/upgrade_command.go:189-195` 当前实现：
  ```go
  if kubeconfig == "" {
      uc.logger.Warn(
          "violet.clusters is set but running in-cluster (no KUBECONFIG file); cluster identity NOT verified",
          zap.String("violet.clusters", violetClusters),
      )
      return nil
  }
  ```

## Proposed Solutions

### 选项 1（推荐）：require `--confirm-cluster` 即使 in-cluster

当 `violet.clusters != ""` 且无 kubeconfig 时，把 `--confirm-cluster` 升级为**必填**，且与 `cfg.OperatorConfig.Violet.Clusters` 做精确比对（即用户必须显式申明"我知道这是 devops 集群"）。

- pros: 真实硬拦在场景下生效；与 file-mode 行为对称（都强 require --confirm-cluster）
- cons: 现有 in-cluster CI job 全部需要更新 spec 加 `--confirm-cluster=<name>`
- effort: Small (~10 LOC)
- risk: 破坏性 — 已有 CI 流水线会一次性 fail，需要发 release note

### 选项 2：env-only fallback for CI

新增 env `UPGRADE_CONFIRM_CLUSTER=<name>` 作为 in-cluster 场景下 `--confirm-cluster` 的替代——pod 注入 env 更符合 CI/Tekton 用法。

- pros: 不破坏 in-cluster 流水线（升级 env injection 即可）
- cons: 又多一个配置入口
- effort: Small

## Recommended Action

待 triage。

## Technical Details

- 文件：`cmd/upgrade_command.go::assertClusterMatch`
- 测试：`cmd/upgrade_command_test.go` 不存在，本 todo 同时是触发 #016 的依赖

## Acceptance Criteria

- [ ] `violet.clusters != "" && kubeconfig == "" && --confirm-cluster == ""` 必须 return error
- [ ] `violet.clusters != "" && kubeconfig == "" && --confirm-cluster == violet.clusters` 通过（in-cluster 显式承认）
- [ ] 上述两条用单测覆盖
- [ ] README 更新 in-cluster 场景说明

## Work Log

_待开始_

## Resources

- PR #17: https://github.com/AlaudaDevops/upgrade-test/pull/17
- silent-failure-hunter review verdict #2
- security-sentinel review verdict #4
