---
name: empty-csv-fed-to-subscription
description: operator.go:117 的 `csv, _, _ := NestedString(av.Object, "status", "version")` 完全不检查 csv 是否为空就传给 InstallSubscription，造成下游莫名失败
status: pending
priority: p2
issue_id: "007"
tags: [code-review, quality, correctness, p2]
dependencies: []
---

# Problem Statement

`pkg/operator/operatorhub/operator.go:117-122`：

```go
csv, _, _ := unstructured.NestedString(av.Object, "status", "version")
channel := version.Channel
if channel == "" {
    channel = "stable" // default fallback
}
if err := o.InstallSubscription(ctx, csv, channel); err != nil {
```

`csv` 取值返回三元组 `(value, found, err)` 全部被丢弃；如果 AV 的 `status.version` 类型错（schema drift / controller bug 写了 null）或字段不存在，`csv` 就是空字符串，被原样喂给 `InstallSubscription`，后者远远地以一个"no matching CSV"风格的错误失败，根因离失败点很远。

`installViaViolet` 内部对 `csv` 已经有 `!found || csv == ""` 的强检查（violet.go:192-195），返回了带 AV 名字的明确错误。但 **`UpgradeOperator` 的第二次提取没复用前面的强校验**，相当于在同一条调用链上做了两次相同操作，第二次完全裸奔。

# Findings

- **silent-failure-hunter** F5（MEDIUM）独立命中
- 文件：`pkg/operator/operatorhub/operator.go:117`
- 同一字段在 `violet.go:192` 已经强校验过

# Proposed Solutions

**Option A（推荐）**：让 `InstallArtifactVersion` 把 `csv` 也作为返回值传出来，避免 caller 二次提取。

```go
func (o *Operator) InstallArtifactVersion(ctx context.Context, version config.Version) (*unstructured.Unstructured, string, error) {
    // ...
    return av, csv, nil
}

// UpgradeOperator:
av, csv, err := o.InstallArtifactVersion(ctx, version)
```

- 优点：单一来源，杜绝二次提取漂移
- 缺点：签名变更，影响接口兼容性
- effort: Small
- risk: Low

**Option B**：在 `UpgradeOperator` 内复用 NestedString 但走强校验。

```go
csv, found, err := unstructured.NestedString(av.Object, "status", "version")
if err != nil {
    return fmt.Errorf("AV %s status.version has wrong type: %w", av.GetName(), err)
}
if !found || csv == "" {
    return fmt.Errorf("AV %s status.version is empty", av.GetName())
}
```

- 优点：签名不变
- 缺点：与 `installViaViolet` 内部的同样代码重复
- effort: Negligible
- risk: Low

# Recommended Action

(待 triage)

# Technical Details

- 影响文件：`pkg/operator/operatorhub/operator.go:109-127`、`pkg/operator/operatorhub/artifact_versiong.go:18`
- 测试：现有 unit 覆盖偏少；可加 fake-client 注入 status.version=null 的 AV，断言 UpgradeOperator 在 AV 阶段就报错

# Acceptance Criteria

- [ ] 空字符串 / 类型错的 `status.version` 在 `UpgradeOperator` 入口就报错（带 AV 名字）
- [ ] 不再有"InstallSubscription with empty csv"的可能路径
- [ ] 单元测试覆盖

# Work Log

- 2026-05-18: code review 发现 by silent-failure-hunter

# Resources

- PR: https://github.com/AlaudaDevops/upgrade-test/pull/14
