---
status: complete
priority: p2
issue_id: "018"
tags: [code-review, robustness, preflight, pr-17]
dependencies: []
---

# `checkInstallPlanResidue` 把 `NestedString` 的 err 用 `_, _, _` 丢弃

## Problem Statement

`pkg/operator/operatorhub/preflight.go:131` 当前：

```go
phase, _, _ := unstructured.NestedString(ip.Object, "status", "phase")
```

第三个返回值 `err` 被丢弃。两种风险：
1. **当前**：如果 `status.phase` 是错误类型（OLM CRD schema 变更把 phase 改成 `[]interface{}` 或对象），err 被吞，phase="" → 被认为非终态 → 误报为残留 → 用户白浪费时间
2. **前瞻**：未来 OLM 重命名字段时每个 IP 都会 phase=""，preflight 直接 jam 整个升级流（噪音风暴）

CRD schema drift blast radius。

## Findings

来源：silent-failure-hunter 评审 #5（FIX 级别）

## Proposed Solutions

### 选项 1（推荐）：传播 err + 区分 found vs missing

```go
phase, found, err := unstructured.NestedString(ip.Object, "status", "phase")
if err != nil {
    return nil, fmt.Errorf("InstallPlan %s/%s: unexpected status.phase type: %w",
        ip.GetNamespace(), ip.GetName(), err)
}
if !found {
    logging.FromContext(ctx).Infow(
        "InstallPlan has no status.phase yet, treating as live",
        "name", ip.GetName(),
    )
}
```

- pros: 类型错误直接 fail loud；缺字段保留旧 fallthrough 行为但显式 log
- cons: 略增几行 + 引入 logging.FromContext 调用
- effort: Small (~8 LOC)

### 选项 2：把"无 phase 字段"也当作终态忽略

更保守但不准——刚创建的 IP 还没 reconcile 时 phase 缺，会被误认为终态忽略。

- 否决

## Recommended Action

待 triage。选项 1。

## Technical Details

- 文件：`pkg/operator/operatorhub/preflight.go::checkInstallPlanResidue`
- 测试：在 `preflight_test.go` 加：(a) status.phase 类型错误返回 error；(b) status 缺 phase 字段，记 log 后视为 live

## Acceptance Criteria

- [ ] `status.phase` 类型非 string 时 PreflightBaseline 返回 error
- [ ] `status.phase` 字段缺失时 logs Info + 当作非终态处理
- [ ] 两个单测覆盖

## Work Log

_待开始_

## Resources

- PR #17
- silent-failure-hunter verdict #5
