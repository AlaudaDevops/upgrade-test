---
name: env-allowlist-threat-model-doc
description: pkg/exec.Command.EnvAllowlist 默认 nil=全量透传是兼容默认；本身未变更但 PR 让它成为安全边界——值得在包文档里写清威胁模型，方便未来 caller 不重蹈覆辙
status: pending
priority: p3
issue_id: "017"
tags: [code-review, documentation, security, p3]
dependencies: []
---

# Problem Statement

`pkg/exec.Command.EnvAllowlist`（本 PR 未改动，但 violet 流程依赖它）的默认 nil/empty 行为是 **全量透传 os.Environ()**——为了兼容旧的 testCommand caller。当前 violet 路径正确地显式设了 allowlist `["KUBECONFIG", "PATH", "HOME", "USER", "VIOLET_*"]`，但这个"必须设"的纪律只在 code review 里口口相传。

未来新加一个 `exec.RunCommand` 调用点的同事可能忘记设 allowlist，子进程立即继承所有 CI secret（GITHUB_TOKEN / AWS_* / NPM_TOKEN ...）——这是一个静默退化路径。

# Findings

- **type-design-analyzer** T7 独立命中
- 仅文档层；不修改逻辑

# Proposed Solutions

**Option A（推荐）**：在 `pkg/exec/doc.go` 或 `exec.go` 顶部加包级文档，写明：

```
Package exec wraps os/exec.CommandContext with stdio capture + tee, and an
opt-in environment allowlist.

THREAT MODEL: callers that exec third-party binaries (e.g. uploaders,
registries) SHOULD set Command.EnvAllowlist to restrict which host env vars
reach the child process. The default (empty allowlist == full passthrough)
exists for test-harness callers that intentionally inherit the full
environment (e.g. `make test` running under CI with bespoke env wiring).

NEW CALL SITES: prefer setting EnvAllowlist; the empty default is for
backward compatibility only.
```

- 优点：零运行时改动；为未来新 caller 提供 norm
- 缺点：不能强制
- effort: Negligible
- risk: Zero

**Option B**：再加一个 `RunStrict` 函数（`EnvAllowlist []string` 必填）作为推荐入口；保留 `RunCommand` 为兼容性 API。

- 优点：API 引导
- 缺点：双入口面，违反"窄接口"
- effort: Small
- risk: Low

# Recommended Action

(待 triage)

# Technical Details

- 影响文件：`pkg/exec/exec.go`（或新增 `doc.go`）
- 不影响行为

# Acceptance Criteria

- [ ] `pkg/exec` 包有顶部包文档说明 EnvAllowlist 威胁模型
- [ ] CLAUDE.md 项目根部约束（可选）：新 RunCommand 调用点必须明示 EnvAllowlist 决策

# Work Log

- 2026-05-18: code review 发现 by type-design-analyzer

# Resources

- PR: https://github.com/AlaudaDevops/upgrade-test/pull/14
- 类似设计：Go x/exp/slog 早期对 default Handler 的文档化做法
