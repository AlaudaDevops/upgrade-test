---
name: code-organization-and-constants
description: 一组组织/常量化小问题——内联字面量、文件中段 var、test helper 残留、deleteAV 多余 Get、Version 全量传入——单个都琐碎，合起来是文件可读性议题
status: pending
priority: p3
issue_id: "015"
tags: [code-review, quality, organization, p3]
dependencies: []
---

# Problem Statement

一组组织性细节，单个都不重要，但合起来值得在一次小 cleanup commit 里处理：

**A. `violetEnvAllowlist` 是 var 放在文件中段** (`violet.go:299-305`)
读者搜"violet 看得到哪些 env"会找不到——按 CLAUDE.md 的"常量集中在文件顶部"惯例，应当与文件顶部的 `EnvVioletRegistry*` 和 `flag*` 常量块放在一起。

**B. 内联字面量** (`violet.go:210, 220`)
`"upgrade-violet-*"`（temp dir 模式）和 `"package.tgz"`（fallback 文件名）都是有调试意义的常量，应当 hoist 到文件顶部。

**C. 测试 dead weight** (`violet_test.go:408-426`)
`schemaGVR` 局部类型 + `listKinds` map + `_ = listKinds` placeholder 全是给"以后扩展"留的——目前不用。fake client 已经通过 `scheme.AddKnownTypeWithName` 学到了 list kind。

**D. `deleteArtifactVersionIfExists` 多了一次预 Get** (`violet.go:268-282`)
预 Get 只是为了日志带 uid + happy-path 早退；Delete 本身已经 tolerate NotFound，poll loop 也已等到 NotFound。简化后可少一次往返 + 删一个测试用例。

**E. `installViaViolet` 接收 `config.Version` 全量** (`violet.go:142`)
只用 3 个字段（Channel、BundleVersion、ExpectedSha256），但暴露 6 个；窄接口能避免未来 PR 3 / PR 4 引入更多无关字段。可项目化为局部 `bundleSpec` struct。

# Findings

- A1 + A2 + C6 + C7 + A3 + C5 合并

# Proposed Solutions

每项独立、依次实施，建议放在 PR 3 的 cleanup commit 里：

- **A**：把 `var violetEnvAllowlist` 移到文件顶 `const (...)` 旁
- **B**：`const (tempDirPattern = "upgrade-violet-*"; fallbackPackageFilename = "package.tgz")`
- **C**：删 `schemaGVR` 类型 + `listKinds` + `_ = listKinds`
- **D**：去掉预 Get，直接 Delete + poll，可顺手删 `TestDeleteArtifactVersionIfExists_ConcurrentRaceVanishesGracefully`（与 no-residue case 等价）
- **E**：引入 `type bundleSpec struct { Channel, BundleVersion, ExpectedSha256 string }`，让 `InstallArtifactVersion` 做投影

- effort: 各 Small
- risk: Low

# Recommended Action

(待 triage)

# Technical Details

- 影响文件：`pkg/operator/operatorhub/violet.go`、`pkg/operator/operatorhub/violet_test.go`
- 建议作为 PR 3 cleanup commit 一并打包，避免单独的小 PR

# Acceptance Criteria

- [ ] A-E 各项独立勾选
- [ ] 现有测试不退化

# Work Log

- 2026-05-18: code review 合并 finding A1+A2+C6+C7+A3+C5

# Resources

- PR: https://github.com/AlaudaDevops/upgrade-test/pull/14
- CLAUDE.md "硬约束" §"新加 GVR 时统一在 operator.go 顶部声明"
