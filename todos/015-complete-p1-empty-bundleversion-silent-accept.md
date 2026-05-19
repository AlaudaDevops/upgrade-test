---
status: complete
priority: p1
issue_id: "015"
tags: [code-review, preflight, config, pr-17]
dependencies: []
---

# operatorhub 类型时 `bundleVersion: ""` silent-accept，污染下游

## Problem Statement

`pkg/config/config.go::validateConfig` 中 BundleVersion 校验是 `if v.BundleVersion != "" && !bundleVersionRegex.MatchString(...)` —— 空 BundleVersion 跳过 regex 校验，但**对 operatorhub 类型**是 load-bearing 字段，不该 optional。

下游影响：
- `preflight.checkArtifactVersionResidue` 拼 `<artifact>.<bundleVersion>` → trailing dot → AV name 畸形 → Get NotFound = false clean
- `installViaViolet::BuildPackageURL` 也会拼出畸形 URL，cryptic 404 错误
- `violet push` argv 出现空 bundleVersion 段

preflight 报"无残留"实际上是配置错误的产物。同样是 PR value proposition 自己挫败。

## Findings

来源：silent-failure-hunter 评审 #7（FIX 级别）

- `pkg/config/config.go:246-249`：
  ```go
  if v.BundleVersion != "" && !bundleVersionRegex.MatchString(v.BundleVersion) {
      return fmt.Errorf("...bundleVersion %q must match %s", ...)
  }
  ```

## Proposed Solutions

### 选项 1（推荐）：operatorhub 要求 BundleVersion 非空 + 非法字符

把校验从 "if 非空再校验" 改为 "operatorhub 时必填"：

```go
if cfg.OperatorConfig.Type == "operatorhub" {
    if v.BundleVersion == "" {
        return fmt.Errorf("upgradePaths[%d].versions[%d] (%q): bundleVersion is required for operatorhub type", i, j, v.Name)
    }
    if !bundleVersionRegex.MatchString(v.BundleVersion) {
        return fmt.Errorf(...)
    }
}
```

- local 模式仍允许空（与 channel 一致的"local 不读这字段"原则）
- pros: 早期挡住，下游全部受益
- cons: 已有空 BundleVersion 的 yaml 会一次性 fail
- effort: Trivial (~5 LOC)
- risk: 低 — 真实场景没人会故意填空

### 选项 2（保守）：仅加 warning

仅 `log.Warn` 不 fail。

- 否决 — 沿用了 silent 模式，违背本 PR 哲学

## Recommended Action

待 triage。强烈推荐选项 1。

## Technical Details

- 文件：`pkg/config/config.go::validateConfig`
- 测试：`pkg/config/config_test.go` 补一个负 case + 一个 local-allows-empty 正 case

## Acceptance Criteria

- [ ] operatorhub + 空 BundleVersion → LoadConfig 报错
- [ ] local + 空 BundleVersion → LoadConfig 通过
- [ ] 错误消息指出具体 `path[i].versions[j]` 索引
- [ ] 新测试覆盖

## Work Log

_待开始_

## Resources

- PR #17
- silent-failure-hunter verdict #7
