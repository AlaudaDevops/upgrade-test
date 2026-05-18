---
name: field-format-validation
description: ExpectedSha256 缺 hex64 regex 校验、PushArgs 没有 credential-flag 黑名单——典型 "字段格式正确"但 "字段内容危险"的两个空缺
status: pending
priority: p3
issue_id: "016"
tags: [code-review, type-design, security, p3]
dependencies: []
---

# Problem Statement

两个独立但同源（"字段值合法性校验"）的小问题：

**A. `ExpectedSha256` 无格式校验** (`config.go:106-109`)
用户写 `expectedSha256: "abc"` 不会在加载时报错；要等到 `VerifySha256` 真正下载完文件，做 hex 比对失败，才能拿到"sha256 mismatch"——可能是合法 mismatch 也可能是用户手抖，混在一起。规则：64 个 hex 字符，regex `^[0-9a-fA-F]{64}$`。

**B. `PushArgs []string` 是凭证黑洞** (`config.go:79-83`)
README 已经明文要求"凭证必须走 env 不进 config"，但 `PushArgs` 完全自由——粗心 caller 把 `--password foo` 塞进去就绕过了所有 mask 逻辑（实际 MaskCommand 仍能匹配 `--password`，但 PushArgs 进 YAML 后会留下凭证文件，这是文档层的违例，不是技术层）。

# Findings

- T4 + T3 独立命中
- 与 todo 010（VioletConfig.Validate）自然合流

# Proposed Solutions

**A**：在 `Version.Validate()`（或 `Config.Validate()`）中加 regex 校验：

```go
var sha256HexRe = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)

func (v Version) Validate() error {
    if v.ExpectedSha256 != "" && !sha256HexRe.MatchString(v.ExpectedSha256) {
        return fmt.Errorf("version %s: expectedSha256 must be 64 hex chars, got %q", v.BundleVersion, v.ExpectedSha256)
    }
    return nil
}
```

**B**：在 `VioletConfig.Validate()` 扫描 `PushArgs`：

```go
forbidden := []string{"--password", "-p", "--username", "-u"}
for _, arg := range v.PushArgs {
    for _, f := range forbidden {
        if arg == f {
            return fmt.Errorf("credentials must come from env vars, not pushArgs; remove %q", f)
        }
    }
}
```

- effort: Small
- risk: Low

# Recommended Action

(待 triage)

# Technical Details

- 影响文件：`pkg/config/config.go`
- 与 todo 010（Validate 入口）合流
- 测试覆盖每个失败分支

# Acceptance Criteria

- [ ] A: 非 hex64 的 ExpectedSha256 在 LoadConfig 失败
- [ ] B: PushArgs 含 `--password` / `--username` 等凭证 flag 时 LoadConfig 失败
- [ ] 单元测试覆盖

# Work Log

- 2026-05-18: code review 合并 finding T3+T4

# Resources

- PR: https://github.com/AlaudaDevops/upgrade-test/pull/14
- 关联 todo: 010
