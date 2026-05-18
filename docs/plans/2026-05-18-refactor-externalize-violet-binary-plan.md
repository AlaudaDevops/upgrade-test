---
title: "refactor: externalize violet binary for operator onboarding"
type: refactor
status: active
date: 2026-05-18
origin: docs/brainstorms/2026-05-18-violet-external-binary-brainstorm.md
---

# refactor: externalize violet binary for operator onboarding

## Overview

把 `upgrade` CLI 里"创建 Alauda `Artifact` / `ArtifactVersion` CR"那段硬编码 unstructured 替换成**外调本机/流水线上的 `violet` 二进制**。OLM `Subscription` / `InstallPlan` / `CSV` 流程保持 Go 实现不变。配置层把 violet 相关字段收敛进**嵌套结构体** `OperatorConfig.Violet *VioletConfig`；`Version` 不动。

机制与现有 `testCommand`（`pkg/exec/exec.go:43-80`）一致——`exec.Command{Name: "violet", Args: [...]}` 起子进程，stdout/stderr 双写。**Env 不再全量透传**，改成 allowlist（最小权限原则，见 §Security）。

origin：`docs/brainstorms/2026-05-18-violet-external-binary-brainstorm.md`。本 plan 整合了 brainstorm 决策 + Repo Pattern 研究 + SpecFlow gap 分析 + Technical Review（simplicity / architecture / security）三方反馈。

## Problem Statement / Motivation

当前 `pkg/operator/operatorhub/artifact_versiong.go:51-90` 把 `Artifact`/`ArtifactVersion` 的 CRD 字段（apiVersion / labels / annotations / spec.present / spec.tag / OwnerReferences 等）**硬编码**在 Go 里。

问题：
1. **schema 漂移风险** — Alauda CRD 演进时 upgrade CLI 必须跟着改、重发版
2. **能力残缺** — violet 已经实现的 `--reset-bundle-version`、`--force`、镜像 push、`--image-pull-secret` 等开关都要在 Go 里重写一遍才能用
3. **职责重复** — violet 才是平台官方的"上架"工具

这次重构把"创建 AV"的写路径**外包给 violet 子进程**，CLI 退回到自己擅长的事：编排升级路径 + 等 OLM 资源就绪 + 跑 testCommand。

## Proposed Solution

**新 `InstallArtifactVersion` 流程**（替换 `pkg/operator/operatorhub/artifact_versiong.go:15-49`）：

```
1. resolve URL      : 按 <prefix>/<name>/<channel>/<name>.latest.ALL.<bundleVersion>.tgz 拼
2. download tgz     : net/http GET → 写到 os.MkdirTemp("upgrade-violet-*")
                       若 Version.ExpectedSha256 非空，下载后强制比对
3. ensure clean AV  : 若旧 AV 同名存在，先 Delete + Poll 至 NotFound
                       （避免 waitArtifactVersionPresent 误判残留为新成功）
4. exec violet push : 子进程 + allowlist env，见下文 §violet 命令拼装
5. wait AV Present  : 复用现有 waitArtifactVersionPresent
6. wait Pkg Manifest: 复用现有 waitPackageManifest
7. return AV        : Subscription 流程不变
```

### 边界明确（架构 reviewer 要求）

- Go 端对 `Artifact` / `ArtifactVersion` CR 的**写路径**全部归 violet：不再 Create / Patch / Update
- Go 端对 AV CR 的访问**只剩**：`GetResource`（查 Artifact 在/查 AV phase）、`Delete`（清理残留 AV）。写边界腐蚀的代码 review 时直接拒绝

### violet 命令拼装

```
[violetBin 或 PATH 上的 violet] push <local.tgz>
    --target-catalog-source platform        # 引用 const targetCatalogSource，与 operator.go 同源
    --skip-push                             # 当 OperatorConfig.Violet.SkipPush=true（默认）时加
    [--username $VIOLET_REGISTRY_USERNAME]  # env 非空时自动追加；仅在凭证 stdin 不可用时使用
    [--password $VIOLET_REGISTRY_PASSWORD]
    [... OperatorConfig.Violet.PushArgs ...]
```

凭证不入 config、不入日志；命令日志 render 时 mask `--password` 后一段。

### 配置 schema 增量

```go
type OperatorConfig struct {
    // ... 现有字段保持不变 ...
    Violet *VioletConfig `yaml:"violet,omitempty"`  // nil 表示不使用 violet（如 local operator）
}

type VioletConfig struct {
    Bin           string   `yaml:"bin,omitempty"`            // 可选，空时 $PATH 查找
    PackagePrefix string   `yaml:"packagePrefix,omitempty"`  // 默认 http://package-minio.alauda.cn:9199/packages/
    SkipPush      bool     `yaml:"skipPush,omitempty"`       // 默认 true
    PushArgs      []string `yaml:"pushArgs,omitempty"`       // 私有场景透传给 violet push 的额外参数
}

type Version struct {
    // ... 现有字段保持不变 ...
    ExpectedSha256 string `yaml:"expectedSha256,omitempty"`  // 可选；非空则下载后强制比对（HTTP 防投毒）
}
```

YAML 示例：

```yaml
operatorConfig:
  type: operatorhub
  name: tektoncd-operator
  violet:
    packagePrefix: "http://package-minio.alauda.cn:9199/packages/"
    skipPush: true

upgradePaths:
  - name: v4.5-to-v4.7
    versions:
      - bundleVersion: v4.6.0
        channel: v4.6
        expectedSha256: a3f...   # 可选
```

## Technical Considerations

### Architecture impacts

- 替换点单一（`createArtifactVersion`），不动 `OperatorInterface`
- 不动 factory（`operatorhub` / `local`）；violet 是 operatorhub 子包内部细节
- 不动 `cmd/upgrade_command.go` 入口

### Performance implications

- 每个版本多 1 次 HTTP 下载（~10-50MB tgz）+ 1 次子进程
- 同 URL 在 CLI run 内通常**不会**被请求两次（不同 version → 不同 URL），故不实现缓存（YAGNI）

### Security considerations（见 §Security 详细审）

最小权限子进程、HTTP 投毒缓解、argv 凭证风险接受边界、env allowlist。

## System-Wide Impact

### Interaction graph

`cmd.UpgradeCommand.Execute` → 遍历 `UpgradePath.Versions` → `operator.UpgradeOperator` → `InstallArtifactVersion(ctx, version)` → URL → download → ensure clean → **[新]** `violet push` 子进程（allowlist env）→ `waitArtifactVersionPresent` → `waitPackageManifest` → `InstallSubscription` → `testCommand`。

KUBECONFIG 仍由 `cmd/upgrade_command.go` 设置到 os env，**只是 child env 改为 allowlist**，因此需要把 `KUBECONFIG` 显式纳入白名单。

### Error propagation

- 各阶段错误用 `fmt.Errorf("download %s: %w", url, err)` / `fmt.Errorf("violet push: %w", err)` 链式 wrap（不引入自定义 StageError 类型，避免抽象成本）
- `pkg/exec` 内部改造：`CommandResult.Err` wrap stderr 末尾 ~20 行进 error message（当前丢失 stderr 上下文，所有 exec 调用方都受益）

### State lifecycle risks

- **关键风险（SpecFlow 抓出，已纳入 acceptance criteria）**：上一次升级残留的同名 `ArtifactVersion` 会让 `waitArtifactVersionPresent` 立即匹配旧 AV 误判成功
- **缓解**：步骤 3 ensure clean — 先 GET 同名 AV，存在则 Delete + Poll 至 NotFound，再调 violet push
- 临时下载目录用 `defer os.RemoveAll` 在每个版本结束清理（不跨版本保留）

### API surface parity

- `pkg/operator/local` 不受影响
- `source: platform` 不再字面写两处：抽常量 `const targetCatalogSource = "platform"`，与 `systemNamespace`（`operator.go:28`）放在一起，集中所有平台耦合常量

### Integration test scenarios

1. **正常路径**：minio 上 tgz 存在 + violet 在 PATH + KUBECONFIG 有效 → AV 创建 + Present + Sub Succeeded
2. **AV 残留**：手工预创建 `<artifact>.<bundleVersion>` AV → 跑 upgrade → 期望 Delete 旧 AV 后再创建新 AV
3. **violet 缺失**：从 PATH 移除 violet → upgrade 在第一次 violet 调用时立刻报错（依赖 OS"command not found"语义，不做额外 preflight）
4. **下载 404**：错版本 `bundleVersion: v999.999.999` → download 阶段拿到 404，错误信息含 URL + status code
5. **私有 push 流**：`violet.skipPush: false` + `VIOLET_REGISTRY_USERNAME/PASSWORD` env + `violet.pushArgs: [--dest-repo, ...]` → 镜像推送 + AV 创建成功
6. **sha256 不匹配**：故意写错 `expectedSha256` → download 完成后比对失败 fail-fast，违 violet 不会被调用
7. **env allowlist**：CI runner 上 `GITHUB_TOKEN` / `AWS_*` env 不进 violet 子进程（用 `/proc/<violet-pid>/environ` 验证）

## Security

### Threat 1: argv 凭证泄露 [接受 + 缓解]

`--password $VAL` 进 argv，OS 级 `ps auxe` / `/proc/<pid>/cmdline` 可见。

- **首选缓解**：实施前**必须**确认 violet 是否支持 stdin / 文件方式读凭证（如 `--password-stdin`）。若支持 → 改用 stdin。若不支持 → 列为 Open Question，决定是否阻塞合并
- **接受边界**：仅当 CI runner 独占（一个 pod 一个 job）且 OS 用户独占时可接受 argv 风险
- README 必须明文警告：共享 runner 禁用 `skipPush: false`

### Threat 2: HTTP 下载投毒 [缓解]

`http://package-minio.alauda.cn:9199` 明文 + 内网。DNS 投毒 / ARP 欺骗可能注入恶意 tgz → violet 带凭证推送 → 供应链攻击。

- **缓解**：新增 `Version.ExpectedSha256` 字段，非空时下载后强制比对，不匹配立即 fail
- 默认 prefix 是 `http://`，但 CLI 不主动禁止；用户可显式改 `https://` 或加 sha256
- **不实现**：prefix 白名单 / 自签证书 CA bundle 加载（YAGNI，等真实场景再加）

### Threat 3: env 全量透传 [缓解]

`pkg/exec/exec.go:48` 当前 `os.Environ()` 全量透传 → CI runner 上的 `GITHUB_TOKEN` / `AWS_*` 等无关 secret 都进 violet 子进程。

- **改造**：`pkg/exec` 增加 `EnvAllowlist []string` 字段；调 violet 时只透传 `KUBECONFIG`、`PATH`、`HOME`、`USER`、`VIOLET_*` 前缀
- testCommand 仍走全量透传（保持向后兼容；要不要也改成 allowlist 留作 follow-up）

### Threat 4: 命令注入 [免疫]

`exec.Command{Name, Args[]}` 不经 shell，`os/exec` 直接 `execve`。`violetPushArgs` 里的恶意字符串只会成为 violet 字面参数。**残留风险**：恶意 `Violet.PushArgs` 可注入 violet 自身 flag → 接受（信任 config 来源）。

### Threat 5: `violetBin` 路径校验 [缓解]

用户配置 `violet.bin: /tmp/evil` 会被无验证执行。

- 校验：`filepath.IsAbs` + `os.Stat` 验证存在且可执行 + 警告若不在 trusted location（`$PATH` / `~/.local/bin`）

### Threat 6: violet 子进程 stdout 二次泄露 [可接受]

violet 自身打印的 `--password=xxx`（如有 debug 模式）会进 CLI 日志。

- CLI 日志层 mask 仅覆盖 CLI 自己渲染的字符串，对 violet 输出无能为力
- 不做 secret-scan regex（YAGNI，易误判）；依赖 violet 自身不打印凭证

### Threat 7: brainstorm 阶段已泄露的凭证 [阻塞前置]

本次设计对话中曾贴出 `k8s.token` / `harbor.password` / `gitlab.token` 等真实凭证。

- **升级为合并前阻塞项**：rotate 完成前 PR 不合并

## Acceptance Criteria

### 功能性

- [ ] `OperatorConfig.Violet *VioletConfig` 嵌套结构体；`Version.ExpectedSha256` 新增字段；其余 schema 不变
- [ ] `defaultConfig()` 在 `Violet != nil` 时填充 `PackagePrefix` 默认值；`SkipPush` 默认 true
- [ ] `pkg/operator/operatorhub/createArtifactVersion` 被删除；`InstallArtifactVersion` 走 violet 子进程
- [ ] 新增 `pkg/operator/operatorhub/violet.go`：URL 拼装 + 命令组装 + 内联 HTTP 下载（不开 `pkg/download` 顶层包）+ 日志 mask `--password`
- [ ] `targetCatalogSource = "platform"` 常量与 `systemNamespace` 同位置声明
- [ ] 同名 AV 残留：先 `Delete` + Poll 直至 NotFound，再调 violet
- [ ] `VIOLET_REGISTRY_USERNAME` / `VIOLET_REGISTRY_PASSWORD` 非空时自动加 `--username` / `--password`
- [ ] `Version.ExpectedSha256` 非空时下载后强制比对
- [ ] `violet.bin` 非空时通过 `filepath.IsAbs` + `os.Stat` 校验
- [ ] `pkg/exec` 支持 env allowlist；调 violet 时只透传 `KUBECONFIG` `PATH` `HOME` `USER` `VIOLET_*`
- [ ] README 与 CLAUDE.md 更新：violet 依赖、嵌套 schema、私有场景 env 协议、共享 runner 警告

### 非功能性

- [ ] 临时目录在版本执行结束 / 异常时被清理
- [ ] 错误信息走 `fmt.Errorf` 链式 wrap，约定阶段前缀（"download" / "violet push" / "wait av" 等字符串），但**不**引入自定义 error type

### 质量门

- [ ] `pkg/operator/operatorhub/violet.go` 中 URL 拼装 + 命令组装为纯函数，加 `_test.go` table test（破例：项目无单测惯例，但纯函数测试零成本）
  - 至少覆盖：channel=`v4.6` + bundleVersion=`v4.6.0`；channel=`rc` + bundleVersion=`v4.2.5-rc.76.g976cff6`
- [ ] `go vet ./...` 与 `go build` 通过
- [ ] integration-test 集群跑一次 tektoncd-operator 真实升级（v4.5 → v4.6 → rc）

## Success Metrics

- Artifact CRD schema 加新字段时，upgrade CLI 零修改（violet 跟进即可）
- 私有环境（`skipPush: false`）可直接由 CLI 完成上架
- 上架失败时仅看 CLI stderr 即可定位是哪个阶段（download / violet push / wait av）

## Dependencies & Risks

### 依赖

- `violet` ≥ 支持 `--target-catalog-source` + `--skip-push` 的版本（implementation 时跑 `violet push --help` 锁定，写进 README prereq）
- Tekton task 镜像必须含 violet（见 §Implementation Prerequisites）
- MinIO 站 `package-minio.alauda.cn:9199` 可达

### 风险

| 风险 | 影响 | 缓解 |
|------|------|------|
| AV 残留 + 误判 Present | 静默成功 | Delete + Poll NotFound（acceptance criteria 已覆盖）|
| HTTP 投毒 / 自签 / DNS 欺骗 | 推送恶意 bundle | 可选 `expectedSha256`；README 警告共享网络场景 |
| argv 凭证 OS 级泄露 | 凭证暴露 | violet stdin 探测（implementation Day 1）；不可达则共享 runner 禁用 |
| violet 在 Tekton 镜像里缺失 | CI 直接挂 | 见 §Implementation Prerequisites |

## Open Questions to Resolve During Implementation

1. **violet 是否支持 `--password-stdin` 或文件读凭证**？implementation Day 1 跑 `violet push --help | grep -i stdin` 验证。结果决定是否走 stdin 方案；不支持则在 PR 描述里明示 argv 风险并取得用户确认
2. **violet 创建 AV 的命名规则是否严格 `<artifact>.<bundleVersion>`**？implementation Day 1 在 dev 集群手动 `violet push` 一个 tgz，`kubectl -n cpaas-system get artifactversion` 确认名字。若不一致：改 `waitArtifactVersionPresent` 用 label selector（`cpaas.io/artifact-version=<artifact>`）+ filter `spec.tag == bundleVersion`
3. **Tekton task 镜像 `registry.alauda.cn:60070/devops/gitlab-ce-upgrade-test:vX` 是否已含 violet**？implementation 前先在 integration-test 集群跑 `kubectl run --rm -it test --image=<image> --command -- which violet` 确认。结果决定是否要先改镜像

## Implementation Prerequisites

1. **violet 在 Tekton 镜像里可用**（见 Open Question 3）—— 不可用时需先 rebuild 镜像或在 task script 注入下载
2. **本地 prereq 写进 README**：装 violet（引用 `download-violet` skill）、KUBECONFIG、`VIOLET_REGISTRY_USERNAME/PASSWORD`（私有场景）
3. **rotate brainstorm 阶段泄露的凭证**（合并前阻塞）

## PR Breakdown（拆分为可独立交付的 PR）

每个 PR 独立可 review、可合并、可 revert。PR 间是 stack 关系（按顺序合并）。每个 PR 包含 1-3 个 commit（可拆细，但合并到一个 PR）。

---

### PR 1: 纯加法基础设施（zero behavior change）

**标题**：`feat: add Violet config schema + exec env allowlist + violet helpers`

**Branch**：`feat/violet-helpers-and-config`

**改动范围**：
- `pkg/exec/exec.go`：
  - 新增 `Command.EnvAllowlist []string` 字段；空切片时保持现有 `os.Environ()` 全透传（向后兼容）
  - `CommandResult.Err` wrap stderr 末尾 ~20 行进 error message（所有调用方受益）
- `pkg/config/config.go`：
  - 新增 `VioletConfig` 嵌套结构体；`OperatorConfig.Violet *VioletConfig`
  - 新增 `Version.ExpectedSha256` 字段
  - `defaultConfig` 在 `Violet != nil` 时填充 `PackagePrefix` / `SkipPush` 默认
- `pkg/operator/operatorhub/operator.go`：
  - 新增 `const targetCatalogSource = "platform"`，与 `systemNamespace` 同位置（不改任何调用点，只是声明常量）
- **新建** `pkg/operator/operatorhub/violet.go`：纯函数集
  - `BuildPackageURL(prefix, name, channel, bundleVersion string) string`
  - `BuildVioletPushArgs(cfg *VioletConfig, tgzPath string) []string`（含 env 检测拼凭证 + skip-push 开关 + push-args 追加）
  - `MaskCommand(name string, args []string) string`（log render 用）
  - `VerifySha256(filePath, expected string) error`
- **新建** `pkg/operator/operatorhub/violet_test.go`：table test
  - URL 拼装：`v4.6` channel + `v4.6.0` / `rc` channel + `v4.2.5-rc.76.g976cff6`
  - 命令拼装：env 有 / 无凭证、skipPush true / false、pushArgs 空 / 非空 4 组合
  - mask：`--password xxx` 必须被掩盖
  - sha256：匹配 / 不匹配两 case

**验证**：
- `go vet ./...` + `go build` 通过
- `go test ./pkg/...` 通过（新 violet_test 必须全过）
- **不**触发任何 integration 测试 —— 因为代码加了但未被调用
- 手工 review：`grep -r "VioletConfig\|installViaViolet\|targetCatalogSource\|BuildPackageURL"`，只在新增文件 + config.go + operator.go 出现

**回退性**：完全独立 revert；不影响任何现有功能；老 `createArtifactVersion` 路径仍是唯一活跃路径

**预估行数**：+250 / -0（纯加法）

**前置 Open Question 解决**：无（这一 PR 不调用 violet）

---

### PR 2: 切换 InstallArtifactVersion 走 violet（保留 dead code）

**标题**：`refactor(operatorhub): install ArtifactVersion via violet binary`

**Branch**：`refactor/install-via-violet`（基于 PR 1 分支）

**改动范围**：
- `pkg/operator/operatorhub/violet.go`：
  - 新增 `(o *Operator) installViaViolet(ctx, version Version) (*unstructured.Unstructured, error)`
  - 实现 download HTTP GET → 临时目录 → sha256 校验（若 ExpectedSha256 非空）→ ensure clean AV（Get + Delete + Poll NotFound）→ exec violet（用 PR 1 的 EnvAllowlist）→ 复用 `waitArtifactVersionPresent` / `waitPackageManifest`
- `pkg/operator/operatorhub/artifact_versiong.go`：
  - `InstallArtifactVersion` 改走 `o.installViaViolet`
  - **保留** `createArtifactVersion` 函数为 dead code（不删；便于 PR 2 出问题时本地 diff 对比）
- `README.md`：新增章节
  - violet 二进制依赖（最低版本由 implementation 锁定）
  - 嵌套 schema 完整示例
  - `VIOLET_REGISTRY_USERNAME/PASSWORD` env 协议
  - 共享 CI runner 安全警告（argv 凭证泄露）
  - sha256 校验如何使用
- `CLAUDE.md`：
  - 硬约束段加一行："Artifact/ArtifactVersion 写路径由 violet 子进程负责；Go 端只剩 GetResource + Delete"

**验证**（依赖 implementation 阶段先解决 3 个 Open Questions）：
- `go vet ./...` + `go build` 通过
- **integration-test 集群** 跑一次 tektoncd-operator 真实升级（v4.5 → v4.6）
- **手工 AV 残留场景**：预先 `kubectl apply` 一个同名 ArtifactVersion，然后跑 upgrade → 验证旧 AV 被 Delete、新 AV `creationTimestamp` 更新
- **env allowlist 验证**：临时在 runner 注入 `MY_SECRET=xxx`，跑 upgrade，`/proc/<violet-pid>/environ` 确认看不到 `MY_SECRET`
- **CI 镜像**：确认 Tekton task 镜像里 `violet` 可执行（Open Question 3）

**回退性**：可独立 revert，恢复 `createArtifactVersion` 老路径；dead code 仍在不需要重新写

**预估行数**：+200 / -20（旧函数保留）

**前置 Open Question 解决**：
1. violet 是否支持 `--password-stdin` —— 决定是否切换凭证方式 / 留 argv
2. violet 创建 AV 的命名规则 —— 决定 wait 逻辑是否需要改 label selector
3. Tekton task 镜像 violet 是否可用 —— 决定是否要先 PR 改基础镜像

---

### PR 3: 清理 dead code

**标题**：`chore(operatorhub): remove deprecated createArtifactVersion`

**Branch**：`chore/remove-deprecated-create-av`（基于 PR 2 分支）

**改动范围**：
- `pkg/operator/operatorhub/artifact_versiong.go`：
  - 删除 `createArtifactVersion` 函数
  - 删除仅它在用的 imports（`metav1` OwnerReference 等若不再需要）
- 检查 `artifactGVR` 是否还有引用 —— 仍要保留（`InstallArtifactVersion` 的 Get + `installViaViolet` 的 Delete 仍用）

**验证**：
- `grep -r "createArtifactVersion" pkg/` 必须为空
- `go vet ./...` + `go build` 通过
- 与 PR 2 同样跑一次集成升级（验证 dead code 删除未引入回归）

**回退性**：纯删除，git revert 即恢复

**预估行数**：+0 / -45（纯删减）

**合并时机建议**：PR 2 在 production 流水线跑过至少 1-2 次升级测试无问题后再合并 PR 3（≥ 1 周观察窗口）

---

### 集成验证（不进任何 PR，每个 PR 都跑）

- integration-test 集群跑 tektoncd-operator v4.5 → v4.6 → rc 完整升级路径
- 私有场景验证（`skipPush: false` + push-args 含 `--dest-repo` + env 凭证）至少跑一次

### PR 间依赖

```
PR 1 (基础设施) ─┬─→ PR 2 (切换 + dead code 保留) ─→ PR 3 (清理)
                 └─ 独立合并即可，不阻塞其他工作
```

## Sources & References

### Origin

- **Brainstorm 文档**：[docs/brainstorms/2026-05-18-violet-external-binary-brainstorm.md](../brainstorms/2026-05-18-violet-external-binary-brainstorm.md)
  - 承接关键决策：①violet 只接管 Artifact/AV 创建 ②URL 约定拼接公式 ③配置字段限定在 OperatorConfig（本 plan 进一步收敛为嵌套 `Violet`）④鉴权两套（k8s 走 KUBECONFIG / registry 走 env）⑤`--reset-bundle-version` 不暴露

### Technical Review 反馈来源（本 plan 整合点）

- **Architecture reviewer**：嵌套 `Violet *VioletConfig` / `targetCatalogSource` 常量提取 / `pkg/download` 内化进 operatorhub / Phase 3 拆 3a + 3b 保可逆性 / Go 端对 AV 只读+Delete 边界
- **Security reviewer**：env allowlist 改造 pkg/exec / `expectedSha256` 字段防投毒 / violet stdin 凭证探测列阻塞前置 / `violetBin` 路径校验 / brainstorm 凭证泄露阻塞合并
- **Simplicity reviewer**：删 URL 缓存 / 删 resourceVersion 比对 / 删 preflight `violet --version` 缓存 / 错误分层标签从 AC 降为约定 / "AV 名字不一致"从风险表降级为 implementation Open Question

### Internal references

- 现有 unstructured 创建逻辑：`pkg/operator/operatorhub/artifact_versiong.go:51-90`
- 子进程 exec：`pkg/exec/exec.go:43-80`（env 透传现状 line 48）
- 配置默认值入口：`pkg/config/config.go:91-112`
- knative logging 模式：`pkg/operator/operatorhub/subscription.go:23`、`pkg/operator/operatorhub/artifact_versiong.go:16`
- factory：`pkg/operator/factory.go`
- Tekton Pipeline `devops/upgrade-test`（integration-test 集群）：在线读取于 2026-05-18

### External

- violet 帮助：本机 `/Users/alauda/.local/bin/violet --help` / `violet push --help`（无公开 docs；以本机版本为准）

### Decisions made unilaterally (no user confirmation; revisit if needed)

- **不保留 `useViolet: false` dual-path** — YAGNI；git revert 是回退方式
- **AV 残留处理**：Go 端先 Delete + Poll NotFound，不依赖 violet `--force`
- **不引入自定义 stage error type**：用 `fmt.Errorf` 链式 wrap + 字符串前缀约定即可
- **不实现 URL 缓存**：升级路径里同 URL 不会被请求两次（不同 version → 不同 URL）
- **不实现 secret-scan**：YAGNI，易误判
- **保留 `Violet.SkipPush` 显式开关**：而非靠"PushArgs 非空"推断；语义清晰更重要
- **保留 `Violet.Bin` 字段**：但加 `filepath.IsAbs` + `os.Stat` 校验（security 反向要求）
