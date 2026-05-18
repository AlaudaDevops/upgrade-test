---
name: security-defense-in-depth
description: 三个低概率但廉价可关闭的安全空缺——VIOLET_* 允许列表过宽、删 AV 前未校验 ownerRef、下载无 Content-Length 上限
status: pending
priority: p3
issue_id: "014"
tags: [code-review, security, defense-in-depth, p3]
dependencies: []
---

# Problem Statement

三个独立但都属于 "defense-in-depth" 的小空缺：

**A. `VIOLET_*` 允许列表过宽** (`violet.go:299-305`)
当前 allowlist 用 `VIOLET_*` 前缀通配。攻击模型：未来 violet 引入 `VIOLET_AWS_SESSION_TOKEN` 之类的 env-recognized 名字，或者环境里被人故意命名 `VIOLET_GITHUB_TOKEN`，全部自动透传。

**B. `deleteArtifactVersionIfExists` 不校验 ownerRef** (`violet.go:265-292`)
`avName = <artifact>.<bundleVersion>`，两者都是 config 控制。如果 `bundleVersion` 拼写或 artifact 命名异常导致与他人 AV 名称冲撞，本工具会无差别删之。

**C. `io.Copy(f, resp.Body)` 无大小上限** (`violet.go:253`)
被污染的 MinIO 可以无限流式写 `/tmp` 直到撑爆 tmpfs。todo 001 加了 timeout 后影响降低，但仍是一个独立的失控点。

# Findings

- S5 + S7 + P2(performance Content-Length) 独立命中
- 现实威胁强度：低；修复成本：低

# Proposed Solutions

**A**：把 allowlist 收窄到精确名字。

```go
var violetEnvAllowlist = []string{
    "KUBECONFIG", "PATH", "HOME", "USER",
    EnvVioletRegistryUsername,
    EnvVioletRegistryPassword,
}
```

后续如 violet 引入新的支持 env，**显式**追加。

**B**：删前校验 `metadata.labels["cpaas.io/artifact-version"] == o.artifact` 或 ownerRef 指向预期 Artifact。

```go
existing, _ := o.GetResource(ctx, name, systemNamespace, artifactVersionGVR)
if existing != nil {
    if labels := existing.GetLabels(); labels["cpaas.io/artifact-version"] != o.artifact {
        return fmt.Errorf("refusing to delete AV %s: label cpaas.io/artifact-version=%q does not match %q",
            name, labels["cpaas.io/artifact-version"], o.artifact)
    }
    // ... proceed with delete
}
```

**C**：`io.LimitReader` 加一个慷慨上限：

```go
const maxBundleBytes = 2 << 30 // 2 GiB
if resp.ContentLength > maxBundleBytes {
    return ..., fmt.Errorf("bundle too large: %d", resp.ContentLength)
}
io.Copy(f, io.LimitReader(resp.Body, maxBundleBytes))
```

- effort: A=Negligible / B=Small / C=Small
- risk: Low

# Recommended Action

(待 triage)

# Technical Details

- 影响文件：`pkg/operator/operatorhub/violet.go:299-305, 265-292, 253`
- A 部分需要测试覆盖 `VIOLET_X` 不在 allowlist 时被过滤（依赖 pkg/exec 的 matchAllowlist 行为，已有测试）
- B 部分需要 fake client 测试覆盖 label 不匹配 → refuse

# Acceptance Criteria

- [ ] A: VIOLET_* glob 收窄为精确名字列表
- [ ] B: deleteArtifactVersionIfExists 在 label/ownerRef 不匹配时拒绝并报错
- [ ] C: 加大小上限，超限直接报错；io.Copy 用 LimitReader 包裹
- [ ] 单元测试覆盖

# Work Log

- 2026-05-18: code review 合并 finding S5+S7+P2(perf)

# Resources

- PR: https://github.com/AlaudaDevops/upgrade-test/pull/14
