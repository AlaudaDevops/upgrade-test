---
name: skippush-bool-pointer-overmodel
description: VioletConfig.SkipPush *bool 用三态（nil/*false/*true）模拟"unset=true 默认"，但实际只用到二态；切成 inverted bool 可省掉 defaultConfig 分支和测试的 pointer-to-bool 样板
status: pending
priority: p2
issue_id: "009"
tags: [code-review, quality, type-design, p2]
dependencies: []
---

# Problem Statement

`pkg/config/config.go:74-77`：

```go
// SkipPush controls whether `--skip-push` is passed to `violet push`.
// Pointer so we can distinguish "unset" (treated as true) from "explicit
// false" (private-registry scenario that wants violet to also push images).
SkipPush *bool `yaml:"skipPush,omitempty"`
```

三个站点参与同一个 dance：
- 结构体字段（`*bool`）
- `defaultConfig` 在 `config.go:147-152` 设默认
- 调用点 `violet.go:320-323` 解引用

三态的唯一收益是区分"用户没写"与"用户写了 false"。本 PR **并未利用** 这个区分——`defaultConfig` 把 nil 也变成 `*true`，于是 caller 看到的只有 `*true` / `*false` 两态。

# Findings

- **code-simplicity-reviewer** C3 + **type-design-analyzer** T1（SkipPush 评分 clarity 2/5、test 2/5）独立命中
- 文件：`pkg/config/config.go:74-77, 147-152`、`pkg/operator/operatorhub/violet.go:320-323`、测试 `violet_test.go` 多处需要 `t := true; cfg.SkipPush = &t`

# Proposed Solutions

**Option A（推荐）**：反转字段，零值即安全默认。

```go
// PushImages 控制是否让 violet 把 bundle 内的镜像也推到 registry。
// 默认 false（仅做 OLM bundle push，--skip-push 启用）；私有 registry
// 场景显式设为 true 再配合 PushArgs / 环境变量传凭证。
PushImages bool `yaml:"pushImages,omitempty"`
```

```go
// violet.go:
if !o.violet.PushImages {
    args = append(args, flagSkipPush)
}
```

- 优点：零值即安全默认；删除 `defaultConfig` 的 SkipPush 分支；测试不再 `t := true`；YAML 行为不变（omitempty）
- 缺点：字段重命名属于 config 不兼容变更，需要 release notes 同步
- effort: Small
- risk: Low

**Option B**：保留 `*bool` 但加注释说明"目前 nil/*true 行为等价、保留 *bool 仅为未来扩展"。

- 优点：零代码改动
- 缺点：把不必要的复杂度推给未来——而 PR 1 评审刚刚才删掉一组"为将来准备"的常量
- effort: Negligible
- risk: Low

# Recommended Action

(待 triage)

# Technical Details

- 影响文件：`pkg/config/config.go`、`pkg/operator/operatorhub/violet.go`、`pkg/operator/operatorhub/violet_test.go`、README config 示例
- YAML 字段重命名：`skipPush` → `pushImages`，发版说明需要写

# Acceptance Criteria

- [ ] 配置字段从 `SkipPush *bool` 改为 `PushImages bool`（或保留并加上注释，看 triage 决定）
- [ ] `defaultConfig` 的 SkipPush 分支被清除（若选 A）
- [ ] 测试用例不再使用 `t := true; cfg.SkipPush = &t` 样板
- [ ] README config 示例同步

# Work Log

- 2026-05-18: code review 发现 by code-simplicity-reviewer + type-design-analyzer

# Resources

- PR: https://github.com/AlaudaDevops/upgrade-test/pull/14
