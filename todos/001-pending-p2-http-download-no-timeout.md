---
name: http-download-no-timeout
description: downloadToTemp 使用 http.DefaultClient，无 Client.Timeout，且上游 ctx 也没有 deadline，stalled MinIO 可挂数小时
status: pending
priority: p2
issue_id: "001"
tags: [code-review, performance, security, reliability, p2]
dependencies: []
---

# Problem Statement

`downloadToTemp` 在 `pkg/operator/operatorhub/violet.go:234` 用 `http.DefaultClient.Do(req)` 下载 .tgz 包。`http.DefaultClient.Timeout == 0`（无超时），而上游 `cmd/upgrade_command.go` 传进来的 ctx 也没有 `context.WithTimeout` 包裹（`o.timeout` 只在 `wait.PollUntilContextTimeout` 内被消费，不影响下载阶段）。

结果：若 MinIO 半开 TCP / 反向代理挂起 / 一边写一边断流，HTTP 请求会阻塞到 OS 级 TCP keepalive（Linux 默认 ~2 小时）才返回。`TestDownloadToTemp_ContextCancel` 只证明了 ctx-cancel 通路工作，没有强制 deadline。

# Findings

- **security-sentinel** S8（P3）+ **architecture-strategist** A6（P3）+ **performance-oracle** P1（P2） 同时命中。三方独立观察。
- 文件路径：`pkg/operator/operatorhub/violet.go:234`
- 调用源：`cmd/upgrade_command.go:198` 传入 bare ctx（无 deadline）
- 影响：单次升级阶段挂死数小时，CI runner 阻塞，可用性事故

# Proposed Solutions

**Option A（推荐）**：在 `downloadToTemp` 内部用 `context.WithTimeout(ctx, o.timeout)` 包裹。复用已有 `OperatorConfig.Timeout` 旋钮。

```go
func (o *Operator) downloadToTemp(ctx context.Context, rawURL string) (string, func(), error) {
    dlCtx, cancel := context.WithTimeout(ctx, o.timeout)
    defer cancel()
    req, err := http.NewRequestWithContext(dlCtx, http.MethodGet, rawURL, nil)
    // ...
}
```

- 优点：复用已有配置项，5 行改动，语义清晰
- 缺点：`downloadToTemp` 需要变成 `*Operator` 方法（或者取一个 timeout 参数）
- effort: Small
- risk: Low

**Option B**：构造专用 `&http.Client{Timeout: o.timeout, Transport: ...}`，加 `DialContext` / `ResponseHeaderTimeout` / `IdleConnTimeout`。

- 优点：粒度更细，能区分 dial / header / read 阶段超时
- 缺点：相比 Option A 多写 ~15 行 Transport 配置，本 CLI 单次调用收益有限
- effort: Medium
- risk: Low

# Recommended Action

(待 triage 后填写)

# Technical Details

- 影响文件：`pkg/operator/operatorhub/violet.go`
- 单元测试：可在现有 `TestDownloadToTemp_ContextCancel` 旁加 `TestDownloadToTemp_ParentTimeoutEnforced`，验证即使上游 ctx 没 deadline，downloadToTemp 自己也会在 `o.timeout` 后超时
- 不影响 K8s 资源

# Acceptance Criteria

- [ ] downloadToTemp 在 `o.timeout` 内必返回（即使上游 ctx 没 deadline）
- [ ] 新增测试覆盖该路径
- [ ] `go test ./pkg/operator/operatorhub/...` 全绿
- [ ] 现有 `TestDownloadToTemp_ContextCancel` 保持通过

# Work Log

- 2026-05-18: code review 发现 by 3 个 agent（security/architecture/performance）

# Resources

- PR: https://github.com/AlaudaDevops/upgrade-test/pull/14
- 相关：Release It! "remote calls must assume they will fail"
