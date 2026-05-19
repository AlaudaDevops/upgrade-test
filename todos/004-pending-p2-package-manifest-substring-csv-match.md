---
name: package-manifest-substring-csv-match
description: waitPackageManifest 用 strings.Contains 匹配 CSV 名，v0.65.0 会被 v0.65.0-rc1 / v0.65.0.bak 错误命中，导致 Subscription 绑到错误版本
status: pending
priority: p2
issue_id: "004"
tags: [code-review, quality, correctness, p2]
dependencies: []
---

# Problem Statement

`waitPackageManifest` 在 `pkg/operator/operatorhub/artifact_version.go:120-122` 用 substring 匹配 CSV 名：

```go
csvName, _, _ := unstructured.NestedString(entryMap, "name")
if strings.Contains(csvName, csv) {
    return true, nil
}
```

`csv` 来自 `av.status.version`，典型值如 `tektoncd-operator.v0.65.0`。如果 PackageManifest 同时存在 `tektoncd-operator.v0.65.0-rc1`、`tektoncd-operator.v0.65.0.bak`、`tektoncd-operator.v0.65.0-canary`，`Contains` 全部命中——首个出现的就会被当成等待目标返回 OK。后续 `InstallSubscription` 拿到 `csv=v0.65.0` 也会被 OLM 正确解析，但等待信号已不可靠。

# Findings

- **silent-failure-hunter** F2（HIGH）独立命中
- 文件：`pkg/operator/operatorhub/artifact_version.go:106-122`
- 三处 `_, _, _` 同时丢弃了 NestedSlice / NestedString 的错误返回，schema drift 也被静默吞掉

# Proposed Solutions

**Option A（推荐）**：用精确匹配（或带 `.` 边界的 prefix-match），并把 NestedString 的 err 传出来。

```go
csvName, found, err := unstructured.NestedString(entryMap, "name")
if err != nil {
    return false, fmt.Errorf("entry .name has wrong type in PackageManifest %s: %w", o.name, err)
}
if found && csvName == csv {
    return true, nil
}
```

- 优点：精确 + 错误显式
- 缺点：如果 OLM 在 CSV 名上加版本后缀（极少见），需要重新评估匹配策略
- effort: Small
- risk: Low

**Option B**：保留 substring 但加 `.` 边界检查：`strings.HasPrefix(csvName, csv+".")` 兜底，避免 v0.65.0 命中 v0.65.0-rc1。

- 优点：兼容历史命名怪癖
- 缺点：仍然不精确
- effort: Small
- risk: Low-Medium

# Recommended Action

(待 triage)

# Technical Details

- 影响文件：`pkg/operator/operatorhub/artifact_version.go`
- 测试：添加单元测试覆盖 (a) 精确匹配命中 (b) `v0.65.0` 不命中 `v0.65.0-rc1` (c) NestedString 类型错误返回

# Acceptance Criteria

- [ ] CSV 名匹配从 substring 改为精确（或带边界的 prefix-match）
- [ ] NestedString 错误不再被静默丢弃
- [ ] 新增单元测试覆盖三种场景
- [ ] 现有测试通过

# Work Log

- 2026-05-18: code review 发现 by silent-failure-hunter

# Resources

- PR: https://github.com/AlaudaDevops/upgrade-test/pull/14
