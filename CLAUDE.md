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

`config.yaml` → `cmd.UpgradeCommand.Execute` →

1. **Cluster identity guard**（`assertClusterMatch`）—— `operatorConfig.violet.clusters` 非空时要求 `--confirm-cluster=<KUBECONFIG context name>` 精确匹配，防止生产 KUBECONFIG 被误用；in-cluster 运行降级为 warn
2. **Preflight**（`runPreflight` → `op.PreflightBaseline`，可被 `--skip-preflight` 关闭）—— 对每条 path 的 `Versions[0]` 只读扫描残留 Subscription / ArtifactVersion / 非终态 InstallPlan，30s 超时；任一残留构造 `*cmd.PreflightError` 直接 return（fail-fast 跨 path）
3. 对每条 `UpgradePath`，按序遍历 `Versions`：
   - `operator.UpgradeOperator(ctx, version)` —— 把集群升级到该版本
   - `bash -c <testCommand>` 在 `OperatorConfig.Workspace`（可被 `version.TestSubPath` 进一步嵌套）下执行，stdout/stderr 通过 `io.MultiWriter` 同时打印和捕获（见 `pkg/exec/exec.go`）

第一个版本的默认 `testCommand` 是 `REPO=allure make prepare`（准备测试数据），后续版本默认是 `REPO=allure make upgrade`（验证数据 + 升级断言）。`config.Immediate=true` 时遇到错误立即停止；否则继续下一条 path。Preflight 阶段**不**复用 `Immediate` —— preflight 失败永远 fail-fast，因为它是"质量门"而非升级路径的一部分。

### Operator 抽象

`pkg/operator/interface.go` 定义了 `OperatorInterface`，包含两个方法：`UpgradeOperator(ctx, version)` 和 `PreflightBaseline(ctx, version) ([]preflight.Residual, error)`（后者用于升级前置检查）。`preflight.Residual` 值类型定义在叶子子包 `pkg/operator/preflight` 里，避免与上层 `pkg/operator` 形成 import cycle。Factory（`factory.go`）根据 `operatorConfig.type` 选择实现：

- **`operatorhub`（默认）** —— `pkg/operator/operatorhub/`。生产路径。Artifact / ArtifactVersion 的**写路径**已外包给 `violet` 二进制（子进程，见 `violet.go::installViaViolet`）；Go 侧仍用 dynamic client 做以下三类只读 / OLM 资源：
  - Alauda 自定义资源 `app.alauda.io/v1alpha1` 下的 `Artifact` / `ArtifactVersion`：**Go 端只剩 Get + Delete**（清理残留 AV、等 AV phase=Present、读 status.version 拿 CSV）；Create / Patch / Update 全归 violet。命名空间硬编码 `cpaas-system`，OLM 源名硬编码 `platform`（const `targetCatalogSource` in `operator.go`）。
  - OLM 标准资源 `Subscription` / `InstallPlan` / `ClusterServiceVersion` / `PackageManifest`
  - 升级流程：`InstallArtifactVersion`（下载 .tgz → 可选 sha256 校验 → 删除同名 AV 残留 → 调 `violet push` → 等 AV phase=Present 并 cross-check spec.tag → 等 PackageManifest 出现对应 CSV）→ `InstallSubscription`：**Subscription 不存在则创建**（`installPlanApproval: Manual`, `startingCSV=目标 CSV`）；**Subscription 已存在则 in-place refresh**（必要时 patch `spec.channel`，并总是 bump `upgrade-test.alauda.io/refresh-trigger` 注解强制 OLM 重新 reconcile，**不删除、不重建**——依赖 OLM replace chain 滚动到新 CSV）→ 等 InstallPlan（用 `waitInstallPlanForCSV` 匹配 `spec.clusterServiceVersionNames` 包含目标 CSV，避免拿到 `status.installplan.name` 短暂指向的旧 IP）→ patch `spec.approved=true` → 等 CSV phase=Succeeded
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
- **凭证注入：platform 双通道，registry 单通道** —— platform 凭证可走 `Violet.PlatformUsername` / `Violet.PlatformPassword`（config，yaml 字段 `platformUsername` / `platformPassword`）**或** `VIOLET_PLATFORM_USERNAME` / `VIOLET_PLATFORM_PASSWORD`（env），由 `BuildVioletPushArgs` 自动追加进 violet argv；两路都设置时 **config 优先，env 兜底**。registry 凭证仍只走环境变量 `VIOLET_REGISTRY_USERNAME` / `VIOLET_REGISTRY_PASSWORD`。写入 config 的凭证会随 yaml 进 git，敏感场景请用 env 注入。日志层 mask `--password` 和 `--platform-password` 后的值（`isPasswordFlag`），同时 `execVioletPush` 把解析后（config 或 env）的 platform 密码加入 `RedactSecrets` 以擦掉 violet 自身 stdout/stderr 的回显。`pkg/exec.Command.EnvAllowlist` 限制子进程 env 范围，调 violet 时只透传 `KUBECONFIG` / `PATH` / `HOME` / `USER` / `VIOLET_*`。
- **CLI 永远给 violet 传 `--force`** —— 不传时 violet 误判 "already exist, skip it" 直接 no-op，导致 wait AV Present 超时。upgrade CLI 已经在 `installViaViolet` 里 ensure-clean 删除残留 AV，所以"保留 stale AV"不是合法诉求，没必要开放 Force 配置字段。
- **`Violet.Clusters` 多集群必填** —— violet 默认写 `global` 子集群，跟 kubectl 实际连的子集群（如 `/kubernetes/devops`）可能不一致；不填会出现 "violet 报 success 但 AV 在 kubectl 看不到" 的静默假成功。
- **`Violet.PushArgs` 不允许写凭证 flag** —— `--username` / `--password` / `--platform-username` / `--platform-password` 都被 `BuildVioletPushArgs` 拒绝（包含 `--flag=value` 形式）。凭证必须走专门的注入入口（platform：`Violet.PlatformUsername`/`PlatformPassword` 或 `VIOLET_PLATFORM_*` env；registry：`VIOLET_REGISTRY_*` env），不准从 `PushArgs` 偷塞，否则会绕过日志屏蔽与 `RedactSecrets`。
- **`packagePrefix` 是必填字段，无默认值** —— MinIO 根地址跨环境不同，CLI 拒绝硬编码任何默认。空值会在 `BuildPackageURL` 阶段返回 "packagePrefix is empty" 错误。
- **`Violet.LocalPackageDir` 是可选的本地 .tgz 缓存根** —— 非空时 `acquirePackage` 用 `<LocalPackageDir>/<operatorName>/<packageChannel>/<operatorName>.latest.ALL.<bundleVersion>.tgz` 这个 mirror MinIO URL 的布局检查缓存：命中跳过 HTTP；miss 直接下载到该路径（父目录自动 mkdir），不再走 `/tmp`，下次自动命中；任一路径下都 **不会清理**（cleanup 为 noop），保留为缓存。`VerifySha256` 即使命中也会执行——避免损坏的缓存文件被静默喂给 violet。留空保持旧行为：下载到一次性 `/tmp/upgrade-violet-*` 并在 `defer cleanup()` 中删除，无跨次复用。下载半途失败时 cache 路径的半成品会被 `os.Remove` 清掉，防止下次假命中。
- **`PreflightBaseline` 是只读、仅检查 baseline** —— 实现在 `pkg/operator/operatorhub/preflight.go`，对每条 path 的 `Versions[0]` 检查 Subscription / ArtifactVersion / 非终态 InstallPlan 三类残留。不调用 Create/Update/Patch/Delete（单测里有 spy reactor 强制断言）。不要扩展为扫所有 versions —— 中间版本是 CLI 自产中间态，归 `installViaViolet::deleteArtifactVersionIfExists` 负责，扫了会双删竞态。CSV 残留**不在 preflight 检查范围内**（独立 CSV 残留必然伴随 Sub 或 AV，已被前三项 check 覆盖；future 若实测出现独立 CSV 残留，按 plan 给的 PackageManifest 路径加，**注意查询 namespace 是 catalog source 的 ns 即 `cpaas-system`，不是 operator 安装 ns**）。
- **`cmd.SilenceUsage = true` 在 AddFlags 强制开启** —— preflight 失败的 `*PreflightError` 包含可复制粘贴的 `kubectl delete` 命令模板，是 PR 设计的核心 UX；cobra 默认在 RunE 返回 error 时打印 --help 会把这些命令淹没。flag-parsing 错误（如 unknown flag）走的是 cobra 内部不受此影响，仍会打印 usage。
- **`--confirm-cluster` 匹配规则**（`cmd/upgrade_command.go::assertClusterMatch`）—— 当前实现是**精确字符串相等**（与 KUBECONFIG `CurrentContext` 比较）。要换成子串/正则等更宽松规则，只需改这一个比较，flag surface 不变。in-cluster 运行（无 kubeconfig 文件）降级为 `WARN`，不阻塞 CI pod。
- **`BundleVersion` 必须符合 `^[a-zA-Z0-9._-]+$`** —— `pkg/config/config.go::validateConfig` 在 LoadConfig 阶段强校验。该字段会被插入 kubectl 命令模板和 violet argv，允许 shell 元字符（`$`/`` ` ``/`;`/quotes）就是 shell 注入。单点 chokepoint 比每个下游 consumer 各自防御更可靠。

## PR / 协作流程

PR 评论触发的命令（`/lgtm`、`/merge`、`/cherry-pick`、`/retest` 等）由 `.tekton/pr-manage.yaml` 接管，引用外部 pipeline `AlaudaDevops/toolbox/pr-cli`。本地无需关心这些。
