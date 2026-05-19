---
status: pending
priority: p3
issue_id: "022"
tags: [code-review, agent-native, ergonomics, pr-17]
dependencies: []
---

# Agent-friendliness + docs gap 综合改进（多项建议合并）

## Problem Statement

来自 agent-native-reviewer + security-sentinel 的小改进合集：

1. **`--preflight-output=json` flag**（agent-native #1）：subprocess 调用方（CI / agent）只能 regex-parse stderr 的 markdown 文本。加一个 JSON 输出模式让 `jq -r '.residuals[].name'` 这种调用方便很多。~30 LOC。

2. **exit code 区分**（agent-native #2）：当前 preflight 失败 / violet 失败 / kubeconfig 错都 exit=1。在 `main.go` 加 `errors.As` 区分：PreflightError → exit 2，cluster mismatch → exit 3，其他保持 1。让 agent retry policy 能区分。

3. **`--preflight-only` flag**（agent-native #3 vs simplicity-reviewer 立场对立）：agent 想"只验证不升级"目前要让 violet 阶段崩，浪费 + 部分副作用。simplicity 立场是"5 行可未来补，YAGNI"。**这是真正决策点**——录此 todo 让 triage 拍板。

4. **`--confirm-cluster` 信任 kubeconfig 内容假设文档化**（security MEDIUM #4）：guard 依赖"操作者拥有 kubeconfig 文件"。文档化这条假设；可选 defense-in-depth 是同时比 `Contexts[ctx].Cluster.Server`。

5. **`--skip-preflight` audit log 落到外部系统**（security MEDIUM #6）：当前只有 `log.Warn` 到 process stderr。共享 CI runner 上无法外部审计。建议要么需 env `UPGRADE_ALLOW_SKIP_PREFLIGHT=1` 显式开关；要么至少 log 时带 `os.Getenv("USER")`、kubeconfig context 等结构化字段方便 Loki/CloudWatch alert。

## Findings

来源（合并）：agent-native-reviewer warnings #1/#2/#3 + security-sentinel MEDIUM #4/#6

## Proposed Solutions

各条独立 trade-off，建议 triage 时分别决策：

| # | 选项 | 推荐 |
|---|------|------|
| 1 | `--preflight-output=json` flag | Yes, fix-now (~30 LOC) |
| 2 | exit code 区分 | Yes if main.go in scope (~10 LOC)；否则 defer |
| 3 | `--preflight-only` flag | **决策点**（reviewers 立场对立） |
| 4 | kubeconfig trust doc | Yes — 一段 README 即可 |
| 5 | `--skip-preflight` 加 env 守门 | Defer or accept doc trade-off |

## Recommended Action

待 triage。

## Acceptance Criteria

按 triage 决定后再补。

## Work Log

_待开始_

## Resources

- PR #17
- agent-native-reviewer findings #1/#2/#3
- security-sentinel verdicts #4/#6
