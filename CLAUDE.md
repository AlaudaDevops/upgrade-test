# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## 这个仓库是什么

Go 编写的 CLI 工具 `upgrade`，用于按既定路径升级 Alauda 平台上的 Operator，并在每个版本边界执行用户定义的测试命令，以此验证升级后历史数据仍然兼容。它本身不包含被测产品的测试用例 —— 测试用例由 `testCommand`（通常是 `make prepare`/`make upgrade`/`make test`）在外部工作目录中执行。

## 常用命令

```bash
go build -o upgrade ./main.go            # 构建二进制
go test ./...                            # 跑单元测试（当前 pkg/ 下无 _test.go，仅作语法检查）
go test ./pkg/operator/operatorhub/...   # 跑单个包的测试（包含子目录）
go vet ./...                             # 静态检查
go mod tidy                              # 同步依赖

# 运行升级（需要 KUBECONFIG）
export KUBECONFIG=<path>
./upgrade --config upgrade.yaml --log-level debug
./upgrade --workspace /app/testing       # 覆盖配置中的 workspace
```

发版由 `.github/workflows/build.yml` 在打 `v*` tag 时触发，矩阵构建 linux/darwin × amd64/arm64，并自动创建 GitHub Release。

## 架构（big picture）

### 数据流

`config.yaml` → `cmd.UpgradeCommand.Execute` → 对每条 `UpgradePath`，按序遍历 `Versions`：

1. `operator.UpgradeOperator(ctx, version)` —— 把集群升级到该版本
2. `bash -c <testCommand>` 在 `OperatorConfig.Workspace`（可被 `version.TestSubPath` 进一步嵌套）下执行，stdout/stderr 通过 `io.MultiWriter` 同时打印和捕获（见 `pkg/exec/exec.go`）

第一个版本的默认 `testCommand` 是 `REPO=allure make prepare`（准备测试数据），后续版本默认是 `REPO=allure make upgrade`（验证数据 + 升级断言）。`config.Immediate=true` 时遇到错误立即停止；否则继续下一条 path。

### Operator 抽象

`pkg/operator/interface.go` 定义了仅一个方法的接口 `OperatorInterface.UpgradeOperator`。Factory（`factory.go`）根据 `operatorConfig.type` 选择实现：

- **`operatorhub`（默认）** —— `pkg/operator/operatorhub/`。生产路径。Artifact / ArtifactVersion 的**写路径**已外包给 `violet` 二进制（子进程，见 `violet.go::installViaViolet`）；Go 侧仍用 dynamic client 做以下三类只读 / OLM 资源：
  - Alauda 自定义资源 `app.alauda.io/v1alpha1` 下的 `Artifact` / `ArtifactVersion`：**Go 端只剩 Get + Delete**（清理残留 AV、等 AV phase=Present、读 status.version 拿 CSV）；Create / Patch / Update 全归 violet。命名空间硬编码 `cpaas-system`，OLM 源名硬编码 `platform`（const `targetCatalogSource` in `operator.go`）。
  - OLM 标准资源 `Subscription` / `InstallPlan` / `ClusterServiceVersion` / `PackageManifest`
  - 升级流程：`InstallArtifactVersion`（下载 .tgz → 可选 sha256 校验 → 删除同名 AV 残留 → 调 `violet push` → 等 AV phase=Present 并 cross-check spec.tag → 等 PackageManifest 出现对应 CSV）→ `InstallSubscription`（先删旧 Sub + 旧 CSV → 创建 Subscription `installPlanApproval: Manual` → 等 InstallPlan → 把 `spec.approved=true` → 等 CSV phase=Succeeded）
- **`local`** —— `pkg/operator/local/operator.go`。本地开发用，直接在 workspace 里跑 `make deploy`（可被 `operatorConfig.command` 覆盖），不接 OLM。

新增 Operator 类型时：在 `pkg/operator/` 下加子包实现 `OperatorInterface`，并在 `factory.go` 的 `CreateOperator` switch 中注册。

### 配置

`pkg/config/config.go` 是单一事实源。注意 `defaultConfig()` 会填充默认值（workspace=`./`、type=`operatorhub`、artifactPrefix=`operatorhub`、interval=5s、timeout=10m）；新增字段时务必在这里加默认值，避免空值穿透到 client 调用。

Artifact 名称约定：未显式给 `artifact` 字段时，自动拼成 `<artifactPrefix>-<name>`（例如 `operatorhub-gitlab-ce-operator`）。ArtifactVersion 名约定为 `<artifact>.<bundleVersion>`。

## 写代码时要注意的硬约束

- **Artifact / ArtifactVersion 写路径由 violet 子进程负责** —— Go 端只剩 Get + Delete。如果未来要重新加 Create / Patch / Update 必须先和用户确认是否要回退 violet 委托。`violet.go::installViaViolet` 是唯一调用点。
- **不要把命名空间或 OLM source 写成参数** —— 当前实现里 `cpaas-system` 和 `source: platform`（`const targetCatalogSource` in `operator.go`）是硬编码常量，跨 Operator 共用，并与 violet 命令 `--target-catalog-source` 共享同一来源。如果业务上需要支持其他来源，先和用户确认。
- **InstallPlan 一定是手动审批模式** —— `installPlanApproval: "Manual"`，由 `InstallSubscription` 自己 patch `spec.approved=true`。不要改成 Automatic，否则升级时序会乱。
- **新加 GVR 时统一在 `operator.go` 顶部声明** —— 见 `artifactGVR` / `subscriptionGVR` 等，不要在调用点 inline。
- **轮询用 `wait.PollUntilContextTimeout`** —— 间隔/超时都从 `OperatorConfig.Interval` / `Timeout` 拿，不要写常量。
- **日志走 knative `logging.FromContext(ctx)`** —— `cmd/upgrade_command.go` 在 ctx 上注入了 zap sugar logger，子包不要再自己起 logger。
- **YAML 字段大小写**：`config.go` 用的是 `yaml:"xxx,omitempty"` 小写驼峰（`operatorConfig`、`upgradePaths`、`bundleVersion`、`violet`），demo 配置与 README 也是这套；改字段时同步改三处。
- **凭证不进 config（两套都不进）** —— platform 凭证 `VIOLET_PLATFORM_USERNAME` / `VIOLET_PLATFORM_PASSWORD` 和 registry 凭证 `VIOLET_REGISTRY_USERNAME` / `VIOLET_REGISTRY_PASSWORD` 必须走环境变量，由 `BuildVioletPushArgs` 自动追加进 violet argv。日志层 mask `--password` 和 `--platform-password` 后的值（`sensitivePasswordFlags` map）。`pkg/exec.Command.EnvAllowlist` 限制子进程 env 范围，调 violet 时只透传 `KUBECONFIG` / `PATH` / `HOME` / `USER` / `VIOLET_*`。
- **CLI 永远给 violet 传 `--force`** —— 不传时 violet 误判 "already exist, skip it" 直接 no-op，导致 wait AV Present 超时。upgrade CLI 已经在 `installViaViolet` 里 ensure-clean 删除残留 AV，所以"保留 stale AV"不是合法诉求，没必要开放 Force 配置字段。
- **`Violet.Clusters` 多集群必填** —— violet 默认写 `global` 子集群，跟 kubectl 实际连的子集群（如 `/kubernetes/devops`）可能不一致；不填会出现 "violet 报 success 但 AV 在 kubectl 看不到" 的静默假成功。
- **`Violet.PushArgs` 不允许写凭证 flag** —— `--username` / `--password` / `--platform-username` / `--platform-password` 都被 `BuildVioletPushArgs` 拒绝（包含 `--flag=value` 形式）。凭证只走环境变量，不进 config / git。
- **`packagePrefix` 是必填字段，无默认值** —— MinIO 根地址跨环境不同，CLI 拒绝硬编码任何默认。空值会在 `BuildPackageURL` 阶段返回 "packagePrefix is empty" 错误。

## PR / 协作流程

PR 评论触发的命令（`/lgtm`、`/merge`、`/cherry-pick`、`/retest` 等）由 `.tekton/pr-manage.yaml` 接管，引用外部 pipeline `AlaudaDevops/toolbox/pr-cli`。本地无需关心这些。
