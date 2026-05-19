---
name: wait-av-no-retry-on-transient-errors
description: waitArtifactVersionPresent 把任何 Get 错误（包括 NotFound 与 5xx/429/timeout）直接终结轮询，apiserver 短暂抖动就杀掉整个升级
status: pending
priority: p2
issue_id: "005"
tags: [code-review, reliability, correctness, p2]
dependencies: []
---

# Problem Statement

`pkg/operator/operatorhub/artifact_version.go:75-78`：

```go
obj, err := o.client.Resource(artifactVersionGVR).Namespace(systemNamespace).Get(ctx, name, metav1.GetOptions{})
if err != nil {
    return false, err
}
```

两个问题：
1. **NotFound**：violet push 完成到 ArtifactVersion 在 etcd 可见有个最终一致性窗口；此处 NotFound 不应该终止轮询，应当继续等。
2. **transient errors**：apiserver leader election、webhook timeout、429（throttle）、5xx、连接 reset——都会立即把 `wait.PollUntilContextTimeout` 杀掉，触发"莫名其妙的升级失败"，需要手动重跑。

# Findings

- **silent-failure-hunter** F3（HIGH）独立命中
- 文件：`pkg/operator/operatorhub/artifact_version.go:72-93`
- 影响：CI 升级假阴性、可靠性下降、对工具的信心受损

# Proposed Solutions

**Option A（推荐）**：分类错误，NotFound 与 transient 继续轮询。

```go
obj, err := o.client.Resource(...).Get(ctx, name, metav1.GetOptions{})
switch {
case errors.IsNotFound(err):
    return false, nil
case errors.IsServerTimeout(err) || errors.IsTooManyRequests(err) || errors.IsServiceUnavailable(err):
    log.Warnw("transient API error while polling AV, will retry", "err", err)
    return false, nil
case err != nil:
    return false, fmt.Errorf("get AV %s: %w", name, err)
}
```

- 优点：贴近 K8s 客户端语义；保留 timeout 作为最终 backstop
- 缺点：需要补测试覆盖每个分支
- effort: Small
- risk: Low

**Option B**：所有 Get 错误统一吞为 `(false, nil)`，由 `o.timeout` 兜底。

- 优点：极简
- 缺点：掩盖了真正的非 transient 错误（如 RBAC 拒绝），timeout 后 user 拿不到根因
- effort: Negligible
- risk: Medium

# Recommended Action

(待 triage)

# Technical Details

- 影响文件：`pkg/operator/operatorhub/artifact_version.go:72-93`
- 同样的模式可顺便审视：`waitPackageManifest` 已经对 NotFound 单独处理（line 98），但其他 transient 没有，建议一并对齐

# Acceptance Criteria

- [ ] NotFound 视为继续轮询
- [ ] ServerTimeout / TooManyRequests / ServiceUnavailable 视为继续轮询并打 WARN log
- [ ] RBAC denied 等永久错误立即返回
- [ ] 新增单元测试用 reactor 注入各类错误验证

# Work Log

- 2026-05-18: code review 发现 by silent-failure-hunter

# Resources

- PR: https://github.com/AlaudaDevops/upgrade-test/pull/14
- K8s client-go errors package
