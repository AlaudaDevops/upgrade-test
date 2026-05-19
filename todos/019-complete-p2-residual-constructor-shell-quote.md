---
status: complete
priority: p2
issue_id: "019"
tags: [code-review, type-design, security, pr-17]
dependencies: []
---

# `preflight.Residual` 加 constructor 封装 shell-escape 不变量

## Problem Statement

`preflight.Residual` 4 个 exported field 全裸暴露：

```go
type Residual struct {
    Kind, Namespace, Name string
    RecommendedCleanup    string
}
```

`RecommendedCleanup` 必须**已经 shell-escape**（文档注释里写了），但类型本身不保证。security-sentinel 也指出当前用 Go `%q` 不是真正的 shell-safe（`a"b` 这种 K8s 不允许的 name 即便有也会破）。

type-design-analyzer 给 Encapsulation/Invariant 各 2-3/5。

## Findings

来源：type-design-analyzer "small refactor" + security-sentinel verdict #2

## Proposed Solutions

### 选项 1（推荐）：加 constructor + 转用 shellescape

```go
// pkg/operator/preflight/types.go
func NewResidual(kind, namespace, name string) Residual {
    return Residual{
        Kind:               kind,
        Namespace:          namespace,
        Name:               name,
        RecommendedCleanup: fmt.Sprintf("kubectl delete %s %s -n %s",
            strings.ToLower(kind),
            shellescape.Quote(name),
            shellescape.Quote(namespace)),
    }
}
```

引入 `github.com/alessio/shellescape`（small dep, MIT, 单文件实现）。

- pros: 不变量编译期保证；调用方更短；K8s 名字含 quote 等边界 case 也正确（即便理论场景）
- cons: 加一个外部 deps；现有 3 个调用点都要改
- effort: Small (~30 LOC + 3 处调用点改写)

### 选项 2：constructor 但仍用 `%q`

不引入新 dep，仅集中转义到一处。

- pros: 零新 deps
- cons: 没解决 security-sentinel 的 `%q` ≠ shell-safe 论点
- effort: Trivial

### 选项 3：保留现状 + 加单测断言 RecommendedCleanup 形态

- 否决：测试不能弥补类型设计缺陷

## Recommended Action

待 triage。优先 1，可接受 2 作 trade-off。

## Technical Details

- 文件：`pkg/operator/preflight/types.go`、`pkg/operator/operatorhub/preflight.go`（3 处调用点）
- go.mod 引入 `github.com/alessio/shellescape v1.4.x`（如选 1）

## Acceptance Criteria

- [ ] 所有 Residual 从 constructor 构造，不再字面量构造（除测试）
- [ ] RecommendedCleanup 即使 name 含 quote / `$` / 空格也能正确 shell-quoted
- [ ] 单测：name 含特殊字符时 cleanup 命令仍然 shell-safe

## Work Log

_待开始_

## Resources

- PR #17
- type-design-analyzer recommendation
- security-sentinel verdict #2
