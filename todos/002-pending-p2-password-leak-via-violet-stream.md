---
name: password-leak-via-violet-stream
description: MaskCommand 只 mask 父进程自己的 log，violet 子进程的 stderr/stdout 直通 console + 错误包装链，原始密码可能泄露
status: pending
priority: p2
issue_id: "002"
tags: [code-review, security, p2]
dependencies: []
---

# Problem Statement

`MaskCommand` 在 `violet.go:92` 仅对 **父进程自己 log 出来的 argv** 做 mask（替换 `--password` 后的 token 为 `***`）。但 violet 子进程的 stdout/stderr 通过 `io.MultiWriter(os.Stdout, &stdoutBuf)` 实时直通父进程的 console 和 buffer（见 `pkg/exec/exec.go:70-71`），且 `wrapWithStderrTail` 把最后 20 行 stderr 包进 `result.Err`。

如果 violet 在 stderr 上打印自己解析过的 argv（usage line、`unknown flag --foo near --password XXX`、panic argv dump 等），明文密码会出现在两个泄露面：
1. 父进程 stdout/stderr → CI log
2. 上层错误链 → knative zap logger 顶层 "upgrade failed: violet push: exit status 1; stderr tail: ..." 输出

# Findings

- **security-sentinel** S1（P2）+ S2（P2）双重命中，根因相同
- 文件：`pkg/operator/operatorhub/violet.go:326-333`（调用层）、`pkg/exec/exec.go:67-86`（流转层）
- 现有 README 已警告 `ps auxe` 风险，但本路径绕过该缓解措施
- 现有缓解：`MaskCommand` 仅作用于 `log.Infow("invoking violet", "cmd", MaskCommand(...))` 一处

# Proposed Solutions

**Option A（推荐）**：在 `execVioletPush` 后处理 `result.Err.Error()` 与 stderr 字符串，把当前 `VIOLET_REGISTRY_PASSWORD` 的值替换为 `***`。

```go
func (o *Operator) execVioletPush(ctx context.Context, tgzPath string) error {
    // ...
    result := exec.RunCommand(ctx, exec.Command{ ... })
    if result.Err != nil {
        result.Err = scrubSecretFromErr(result.Err, os.Getenv(EnvVioletRegistryPassword))
    }
    return result.Err
}
```

并对 `os.Stdout` / `os.Stderr` 包装 `sanitizingWriter`，按需替换。

- 优点：从源头闭环，覆盖错误链 + 实时流
- 缺点：~30 行新代码，需要测试覆盖
- effort: Medium
- risk: Low

**Option B**：等 violet 上游支持 `--password-stdin` 或 `--password-file`，从根本上消除 argv 暴露。

- 优点：消除根因，包括 `ps`/`/proc` 暴露面
- 缺点：依赖外部仓库进度；OQ1 已确认当前 violet 不支持
- effort: Large (外部依赖)
- risk: Low

**Option C**：现状 + 文档加强；要求 violet 在 shared runner 上必须 `skipPush: true` 即不传 `--password`。

- 优点：零代码改动
- 缺点：留下泄露窗口，依靠运维纪律
- effort: Negligible
- risk: Medium-High

# Recommended Action

(待 triage)

# Technical Details

- 影响文件：`pkg/operator/operatorhub/violet.go`（execVioletPush 后处理）
- 测试：构造一个 fake violet 脚本把 argv 回显到 stderr，断言上层错误中不含明文密码
- 与 README "violet 依赖与运行环境" 段落同步

# Acceptance Criteria

- [ ] 当 `VIOLET_REGISTRY_PASSWORD=secret` 设置且子进程在 stderr 回显该值时，`result.Err.Error()` 不含 `secret`
- [ ] 同条件下父进程 stdout/stderr 输出（CI log）不含 `secret`
- [ ] 新增测试用 fake-violet 脚本验证
- [ ] README 的"shared runner 风险"段落更新到反映新的兜底

# Work Log

- 2026-05-18: code review 发现 by security-sentinel

# Resources

- PR: https://github.com/AlaudaDevops/upgrade-test/pull/14
- README §violet 依赖与运行环境（已声明 ps 暴露面）
