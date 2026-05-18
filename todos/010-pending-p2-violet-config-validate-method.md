---
name: violet-config-validate-method
description: VioletConfig 缺少集中校验入口；packagePrefix 空、bin 路径错、operatorhub+violet=nil 等失败都拖到 InstallArtifactVersion 才暴露
status: pending
priority: p2
issue_id: "010"
tags: [code-review, type-design, architecture, p2]
dependencies: []
---

# Problem Statement

VioletConfig 的校验目前分布在三个站点：
- `defaultConfig`（pkg/config/config.go:147-152）—— SkipPush 默认值
- `BuildPackageURL`（violet.go:49-62）—— PackagePrefix / Channel / BundleVersion 空字符串检查
- `validateVioletBin`（violet.go:340-355）—— Bin 路径形态

`OperatorConfig.Type == "operatorhub"` 且 `Violet == nil` 的非法组合，则要拖到 `InstallArtifactVersion`（artifact_versiong.go:19-21）才报错——用户跑了一半升级才发现 config 没写完。

# Findings

- **type-design-analyzer** T2 + T6 + **architecture-strategist** A7 三方独立命中
- 类别：失败发现窗口推迟（config 加载 → 实际跑到那一步）

# Proposed Solutions

**Option A（推荐）**：在 `pkg/config/config.go` 添加：

```go
func (c *Config) Validate() error {
    if c.OperatorConfig.Type == "operatorhub" && c.OperatorConfig.Violet == nil {
        return fmt.Errorf("operatorConfig.violet is required when type=operatorhub")
    }
    if v := c.OperatorConfig.Violet; v != nil {
        if err := v.Validate(); err != nil {
            return err
        }
    }
    // Per-version 校验
    for _, p := range c.UpgradePaths {
        for _, ver := range p.Versions {
            if err := ver.Validate(c.OperatorConfig.Violet); err != nil {
                return err
            }
        }
    }
    return nil
}

func (v *VioletConfig) Validate() error {
    if v.PackagePrefix == "" {
        return fmt.Errorf("violet.packagePrefix is required")
    }
    if v.Bin != "" {
        if err := validateVioletBin(v.Bin); err != nil {
            return err
        }
    }
    return nil
}
```

并在 `LoadConfig` 末尾调用。这一并兜住：
- todo 003 的 HTTP+sha 强制（嵌入 Version.Validate）
- todo 008 的 channel 必填（嵌入 Version.Validate）
- A7 的 violet 必填（顶层）

- 优点：单一中央入口；config 加载就 fail-fast
- 缺点：~50 行新代码 + 测试
- effort: Medium
- risk: Low

**Option B**：保留分布式校验现状。

- 优点：零改动
- 缺点：失败时机延迟、debug 成本高、违反 Ousterhout "deep modules" 倾向（窄接口 + 集中行为）
- effort: Negligible
- risk: Medium-Long-term

# Recommended Action

(待 triage)

# Technical Details

- 影响文件：`pkg/config/config.go`、`cmd/upgrade_command.go`（在 LoadConfig 后调 Validate）
- 与 todos 003、008、011、016 自然合并

# Acceptance Criteria

- [ ] `Config.Validate()` 实现并被 `LoadConfig` 或 cmd 调用
- [ ] 所有校验失败在 CLI 启动初期就报错，不留到 InstallArtifactVersion
- [ ] 单元测试覆盖 happy path + 每个失败分支
- [ ] 调用点的旁路校验（BuildPackageURL 等）保留为 defense-in-depth

# Work Log

- 2026-05-18: code review 发现 by type-design-analyzer + architecture-strategist

# Resources

- PR: https://github.com/AlaudaDevops/upgrade-test/pull/14
- 关联 todos: 003、008、011、016
