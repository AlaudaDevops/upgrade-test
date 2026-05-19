---
status: pending
priority: p3
issue_id: "020"
tags: [code-review, testing, pr-17]
dependencies: []
---

# 测试覆盖完整性补充（transient types / label drift / regex edge）

## Problem Statement

PR 单测覆盖了主路径，但有几条 cheap insurance 没加：

1. **`isTransientAPIError` 三种类型只测了 1 种**：当前 `TestPreflightBaseline_TransientErrorWrapsAsRetryHint` 只用 `apierrors.NewServerTimeout`。`TooManyRequests` 和 `ServiceUnavailable` 也是 transient 但无测试。
2. **InstallPlan label fixture drift**：`newIPWithPhase` 把 label key 写死 `"operators.coreos.com/tektoncd-operator.test-ns"`，与 `preflight.go` 中 `fmt.Sprintf(...)` 拼接逻辑不一致时不会被发现。
3. **`bundleVersionRegex` 边缘**：5 个 shell-meta sub-case 覆盖 ASCII，没测 `\x00` NUL byte 和 unicode lookalike（如 `1.0＄(x)`，U+FF04）。这些都该被 regex 拒绝，pin behavior。
4. **InstallPlan 无 label 的 negative case**：测试都给 IP 加了 OLM label，没有"label 缺失 → 应该被 labelSelector 过滤"的负 case。

## Findings

来源：pr-test-analyzer #4, #5, #6, #9

## Proposed Solutions

### 选项 1（推荐）：批量补单测

按主题各加 2-3 个 sub-case：
- 在 `TestPreflightBaseline_TransientErrorWrapsAsRetryHint` 用 t.Run 加 TooManyRequests / ServiceUnavailable
- `newIPWithPhase` 改签名接受 `(operatorName, namespace string)`，内部用同样的 `fmt.Sprintf` 拼 label key
- 在 `TestLoadConfig_RejectsShellMetaCharacterInBundleVersion` 加 NUL byte + unicode-dollar 2 个 sub-case
- 新增 `TestPreflightBaseline_InstallPlanWithoutOLMLabelIgnored`

- pros: cheap insurance，回归保护
- cons: 测试代码 +30~50 LOC
- effort: Small

### 选项 2：分散到各 todo（与对应实现 PR 一起）

把每条测试拆分到对应实现修改的 todo（如 `#018` 同时补 NestedString 测试）。

- pros: 测试与实现同 commit
- cons: 部分纯增强测试（如 transient types）没有对应实现 todo

## Recommended Action

待 triage。

## Acceptance Criteria

- [ ] `TestPreflightBaseline_TransientError*` 覆盖 3 种 transient 类型
- [ ] `newIPWithPhase` 与 impl 共享 label key 拼接
- [ ] `bundleVersionRegex` 测试覆盖 NUL byte + unicode
- [ ] 新增 IP-without-label 测试

## Work Log

_待开始_

## Resources

- PR #17
- pr-test-analyzer findings #4, #5, #6, #9
