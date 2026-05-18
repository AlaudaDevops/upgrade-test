---
name: silent-error-cleanup
description: 多处 `_, _, _` 丢弃 unstructured.Nested* 的 err、cleanup 丢弃 RemoveAll 错误、两条 sentinel 错误没有 stage 标签——单个都不致命，叠在一起拉低可调试性
status: pending
priority: p3
issue_id: "013"
tags: [code-review, quality, observability, p3]
dependencies: []
---

# Problem Statement

一组小的 "silent failure" 模式集合，单个都是 P3，但合在一起影响 debug 体验：

**A. `status, _, _ := unstructured.NestedMap(...)` 把类型错误也吞掉** (`artifact_versiong.go:80-81`)
若 AV `status` 字段不是 map（schema drift / controller bug），Go 读 nil map 不 panic，但 polling 看不到 "Present" 就轮询到 `o.timeout`，最终给用户的是 `context deadline exceeded`——根本没说 "status 类型错"。

**B. `cleanup := func() { _ = os.RemoveAll(dir) }`** (`violet.go:214`)
CI runner 长时间 /tmp 撑爆时，留下的孤儿 dir 累积，将来 mkdir 失败时已无线索。最小成本：log.Warnw 即可。

**C. Sentinel 错误没有 stage 标签** (`violet.go:188, 194`)
其他 stage 都是 `fmt.Errorf("violet push: %w", ...)` 这样带短标签的 wrap；spec.tag mismatch 和 status.version empty 这两条仅返回数据级断言，grep 失败根因时找不到统一标签。

# Findings

- F1 (silent-failure) + F6 (silent-failure) + A5 (architecture) 三处独立命中
- 同一类别（silent / 不可 grep）的不同表现形式

# Proposed Solutions

**A**：把 NestedMap 的 err 也接住，类型错立即返回硬错误：

```go
status, found, err := unstructured.NestedMap(obj.Object, "status")
if err != nil {
    return false, fmt.Errorf("AV %s status is malformed: %w", name, err)
}
if !found { return false, nil }
```

**B**：closure 捕获 log，best-effort cleanup 加 WARN：

```go
cleanup := func() {
    if err := os.RemoveAll(dir); err != nil {
        log.Warnw("failed to remove temp dir; manual cleanup may be needed", "dir", dir, "err", err)
    }
}
```

**C**：两条 sentinel 加 stage 标签：

```go
return nil, fmt.Errorf("cross-check spec.tag: AV %s has spec.tag=%q, expected %q ...", ...)
return nil, fmt.Errorf("read status.version: AV %s status.version is empty", avName)
```

- effort: Small（三处都是 1-3 行）
- risk: Low

# Recommended Action

(待 triage)

# Technical Details

- 影响文件：
  - `pkg/operator/operatorhub/artifact_versiong.go:74-93`（A）
  - `pkg/operator/operatorhub/violet.go:214`（B）
  - `pkg/operator/operatorhub/violet.go:188, 194`（C）
- 测试：补一个 NestedMap 返回非 map 的负向用例

# Acceptance Criteria

- [ ] (A) NestedMap 错误传播
- [ ] (B) cleanup 失败有 WARN log
- [ ] (C) 两条 sentinel 错误均带 stage 标签
- [ ] 现有测试不退化

# Work Log

- 2026-05-18: code review 合并 finding F1+F6+A5

# Resources

- PR: https://github.com/AlaudaDevops/upgrade-test/pull/14
