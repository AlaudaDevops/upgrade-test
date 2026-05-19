---
name: sha256-should-be-required-for-http
description: ExpectedSha256 为可选字段，HTTP 明文 + 无校验时，on-path 攻击者可注入任意 .tgz 进而以 OLM 权限在 cpaas-system 部署恶意 operator
status: pending
priority: p2
issue_id: "003"
tags: [code-review, security, p2]
dependencies: []
---

# Problem Statement

`Version.ExpectedSha256` 在 `pkg/config/config.go:106-109` 是可选字段，空字符串 → 跳过验证（`violet.go:111-114`）。

默认部署模式中 `packagePrefix` 是 `http://` 明文：
```yaml
packagePrefix: http://package-minio.alauda.cn:9199/packages/
# expectedSha256: 留空
```

任何 on-path 攻击者（DNS 投毒 / ARP 投毒 / 上游 MinIO 镜像被污染）可投递恶意 .tgz；violet 把它推成 ArtifactVersion，OLM 把它 install 进 `cpaas-system`，命名空间内 cluster-admin 权限的 operator 代码就在集群里跑起来了。

README 中文段落已警告该风险，但代码不强制。

# Findings

- **security-sentinel** S3（P2）独立命中
- 文件：`pkg/operator/operatorhub/violet.go:111-114`、`pkg/config/config.go:106-109`
- 现有缓解：仅 README 文档警告
- 攻击成本：低（on-path attacker on CI subnet）；影响：critical（cluster compromise）

# Proposed Solutions

**Option A（推荐）**：`Config.Validate()` 在 LoadConfig 阶段检查：当 `packagePrefix` 以 `http://` 开头时，所有 `versions[*].expectedSha256` 必须非空且符合 hex64 格式。

```go
func (c *Config) Validate() error {
    if v := c.OperatorConfig.Violet; v != nil && strings.HasPrefix(v.PackagePrefix, "http://") {
        for _, p := range c.UpgradePaths {
            for _, ver := range p.Versions {
                if ver.ExpectedSha256 == "" {
                    return fmt.Errorf("expectedSha256 is required for version %q when packagePrefix uses HTTP", ver.BundleVersion)
                }
            }
        }
    }
    return nil
}
```

- 优点：失败立即可见（config 加载时）；HTTPS+sha 路径不受影响；显式策略
- 缺点：会让已有 HTTP-without-sha 的 config 报错——需要发版说明
- effort: Small
- risk: Low（向后不兼容，但有意为之）

**Option B**：默认强制 `expectedSha256` 必填（不区分 scheme）；HTTPS 路径也强制。

- 优点：最强保证
- 缺点：HTTPS 路径其实有 TLS 已经做 integrity，强制 sha 收益边际；迁移成本大
- effort: Small
- risk: Medium（破坏现有 config）

**Option C**：保留可选 + 启动时 WARN log 提示 sha 未设置。

- 优点：零破坏性
- 缺点：log 容易被忽略，攻击窗口仍开
- effort: Negligible
- risk: Medium-High

# Recommended Action

(待 triage)

# Technical Details

- 影响文件：`pkg/config/config.go`（新增 Validate），`cmd/upgrade_command.go`（调用 Validate）
- 测试：扩展 config 测试，覆盖 HTTP+sha 缺失报错、HTTPS+sha 缺失放行
- 配合 todo 010（VioletConfig.Validate 总入口）一并实现更顺手

# Acceptance Criteria

- [ ] `Config.Validate()` 实现并在 `LoadConfig` 之后被调用
- [ ] HTTP prefix + 缺 sha 时 LoadConfig 失败，错误信息明确指向 bundleVersion
- [ ] HTTPS prefix + 缺 sha 时（暂不强制）通过
- [ ] README 与 docs/plans/ 中的 config 示例同步
- [ ] 单元测试覆盖正反向

# Work Log

- 2026-05-18: code review 发现 by security-sentinel

# Resources

- PR: https://github.com/AlaudaDevops/upgrade-test/pull/14
- README §violet 依赖与运行环境
- 关联 todo: 010（VioletConfig.Validate）
