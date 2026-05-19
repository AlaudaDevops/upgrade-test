---
name: drop-violet-bin-and-validator
description: VioletConfig.Bin + validateVioletBin 是 YAGNI——无现有 caller 设 Bin，自定义路径已经被 $PATH 标准机制满足；删掉可省 ~75 LoC
status: pending
priority: p2
issue_id: "011"
tags: [code-review, simplicity, yagni, p2]
dependencies: []
---

# Problem Statement

两个相关字段/函数纯粹为彼此而生：
- `VioletConfig.Bin` (config.go:64-65)
- `validateVioletBin` (violet.go:340-355) + 5 个 sub-test（violet_test.go:237-282，~46 行）

仓库内 zero caller 设 `Bin`；如果用户想用自定义 violet 路径，`PATH=$HOME/bin:$PATH violet` 是标准 shell 解决方案。`validateVioletBin` 的 "must be absolute" 规则反而阻止了合理的 `./bin/violet` 相对路径开发场景。

# Findings

- **code-simplicity-reviewer** C1 + C2 双重命中（同一根因）
- 可删 LoC 总计 ~75（~30 prod + ~46 test）

# Proposed Solutions

**Option A（推荐）**：删 `Bin` 字段 + `validateVioletBin` + 相关测试 + 调用点。直接 `bin := "violet"`。

- 优点：~75 LoC 减负、config 表面收缩、消除 todo 010 中 Validate 的一个分支
- 缺点：未来如果真有"自定义路径"需求需要再加回——按 YAGNI 原则那是未来该解决的问题
- effort: Small
- risk: Low

**Option B**：保留 Bin 字段、删 validateVioletBin（让 os/exec 自己报错）。

- 优点：保留扩展点
- 缺点：保留了无人使用的字段；validateVioletBin 的"absolute path 强制"消失也是好事，但 Bin 自己仍是 dead config
- effort: Small
- risk: Low

# Recommended Action

(待 triage)

# Technical Details

- 影响文件：`pkg/config/config.go`、`pkg/operator/operatorhub/violet.go`、`pkg/operator/operatorhub/violet_test.go`、README
- 与 todo 010（Validate 入口）有交集——若选 A，VioletConfig.Validate 不再需要 Bin 分支

# Acceptance Criteria

- [ ] `VioletConfig.Bin` 字段移除（若选 A）
- [ ] `validateVioletBin` 函数 + 测试移除（若选 A）
- [ ] `execVioletPush` 简化为 `bin := "violet"`
- [ ] README config 示例同步
- [ ] `go test ./pkg/operator/operatorhub/...` 全绿

# Work Log

- 2026-05-18: code review 发现 by code-simplicity-reviewer

# Resources

- PR: https://github.com/AlaudaDevops/upgrade-test/pull/14
- CLAUDE.md "简洁优先" §7
