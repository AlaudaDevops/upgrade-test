---
name: url-scheme-allowlist
description: BuildPackageURL / downloadToTemp 不校验 URL scheme，配置被污染时（多租户 pipeline、PR 注入）可走 SSRF 触达 169.254.169.254 等元数据服务
status: pending
priority: p2
issue_id: "012"
tags: [code-review, security, defense-in-depth, p2]
dependencies: []
---

# Problem Statement

`BuildPackageURL` 不检查 scheme；`downloadToTemp` 用 `http.DefaultClient.Do(req)` 跟随默认最多 10 次重定向，无 `CheckRedirect`。

攻击场景：
1. 多租户 pipeline / 被污染的 PR / 供应链注入 → 攻击者控制 `config.yaml` → 设 `packagePrefix: http://169.254.169.254/latest/meta-data/iam/security-credentials` → 请求云元数据端点。返回的 IAM token 落入临时文件（后续被 cleanup 删除，但请求本身已发生），在 IMDSv1 环境造成凭证签发或泄露。
2. 攻击者在外部 server 上配 redirect 链探测内网（虽然 Go 默认 transport 拒 `file://`，但 HTTP 跳板足以扫描内部 IP）。

"config 是可信的"——这是个假设，不是固有性质；它会随多租户 pipeline / GitOps 演进而漂移。Defense-in-depth 是廉价兜底。

# Findings

- **security-sentinel** S4（P2）独立命中
- 文件：`pkg/operator/operatorhub/violet.go:49-62`（BuildPackageURL）、`pkg/operator/operatorhub/violet.go:228-244`（downloadToTemp）

# Proposed Solutions

**Option A（推荐）**：在 `BuildPackageURL` 或 `downloadToTemp` 解析 URL 时校验 scheme。

```go
parsed, err := url.Parse(rawURL)
if err != nil { return ..., err }
if parsed.Scheme != "http" && parsed.Scheme != "https" {
    return ..., fmt.Errorf("unsupported scheme %q (only http/https allowed)", parsed.Scheme)
}
```

可选叠加自定义 `http.Client.CheckRedirect`：限制 hops <= 3，每跳重新校验 scheme。

- 优点：~5 行；零运行时开销；闭合 SSRF 主要面
- 缺点：仍不能拦截"http→内网 IP"——但那是另一个问题（host allowlist），属本 PR 范围外
- effort: Small
- risk: Low

**Option B**：再加 host allowlist（如必须以 `package-minio.alauda.cn` 结尾）。

- 优点：闭合内网探测面
- 缺点：跨环境的 MinIO host 变化大，难以静态配置；放进 config 又回到信任问题
- effort: Medium
- risk: Low

# Recommended Action

(待 triage)

# Technical Details

- 影响文件：`pkg/operator/operatorhub/violet.go`
- 测试：扩展 `TestBuildPackageURL` 覆盖 `file://` / `ftp://` / 空 scheme 等

# Acceptance Criteria

- [ ] 非 http/https scheme 在 URL 构建或下载阶段被拒
- [ ] 错误信息明确说明 scheme 限制
- [ ] 单元测试覆盖

# Work Log

- 2026-05-18: code review 发现 by security-sentinel

# Resources

- PR: https://github.com/AlaudaDevops/upgrade-test/pull/14
- OWASP SSRF cheat sheet
