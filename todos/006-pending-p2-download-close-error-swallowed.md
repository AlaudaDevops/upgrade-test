---
name: download-close-error-swallowed
description: downloadToTemp 的 defer f.Close() 吞掉 Close 返回的 flush 错误，磁盘满或 NFS 重置时静默写出截断的 .tgz
status: pending
priority: p2
issue_id: "006"
tags: [code-review, quality, correctness, p2]
dependencies: []
---

# Problem Statement

`pkg/operator/operatorhub/violet.go:246-256`：

```go
f, err := os.Create(filePath)
// ...
defer f.Close()

if _, err := io.Copy(f, resp.Body); err != nil {
    cleanup()
    return "", nil, fmt.Errorf("copy body to %s: %w", filePath, err)
}

return filePath, cleanup, nil
```

许多文件系统在 `Close()` 时才真正 flush write buffer（不是 `Write()`）。如果磁盘恰好在最后一段 flush 时撑爆（ENOSPC）/ NFS 服务器重置 / quota exceeded，`io.Copy` 返回 success，函数返回 "success" 但落地的是截断/损坏的 .tgz。

如果 `ExpectedSha256` 未设置（todo 003 默认场景），违法包直接进入 violet push，产生一个完全指错方向的 "violet push: ..." 错误（实际根因是磁盘）。

# Findings

- **silent-failure-hunter** F4（HIGH）独立命中
- 文件：`pkg/operator/operatorhub/violet.go:246-256`
- 与 todo 003（SHA 强制）相互独立但相互补强

# Proposed Solutions

**Option A（推荐）**：显式 close-and-check 替代 defer。

```go
if _, err := io.Copy(f, resp.Body); err != nil {
    f.Close()
    cleanup()
    return "", nil, fmt.Errorf("copy body to %s: %w", filePath, err)
}
if err := f.Close(); err != nil {
    cleanup()
    return "", nil, fmt.Errorf("close %s after download: %w", filePath, err)
}
```

- 优点：直击根因；与 io.Copy 错误路径并列，可读性好
- 缺点：失去 defer 的 panic-safe 兜底（但本函数无 panic 风险点）
- effort: Small
- risk: Low

**Option B**：用 `defer func() { if cerr := f.Close(); cerr != nil && err == nil { err = cerr } }()` 模式，让命名返回参数捕获 Close 错误。

- 优点：保持 defer 的对称性
- 缺点：需要把返回签名改为命名返回，可读性略降
- effort: Small
- risk: Low

# Recommended Action

(待 triage)

# Technical Details

- 影响文件：`pkg/operator/operatorhub/violet.go:246-256`
- 测试：mock `os.File` 不容易；可用 `httptest` 配合 `t.TempDir()` 做正向测试。负向测试（Close 失败）可暂缓——主要靠 code review 保证不退化

# Acceptance Criteria

- [ ] Close 返回的错误会被传播为函数返回值
- [ ] 错误信息明确指向 `close %s after download` 阶段
- [ ] 现有 `TestDownloadToTemp_Success` / `_404` / `_5xx` / `_DefaultFilename` 保持通过

# Work Log

- 2026-05-18: code review 发现 by silent-failure-hunter

# Resources

- PR: https://github.com/AlaudaDevops/upgrade-test/pull/14
- 关联 todo: 003（SHA 强制可在配置层兜底，但 Close 修复仍是独立必要）
