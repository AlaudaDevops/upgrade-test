# Field Mappings

步骤 1 AskUserQuestion 的 4 个槽答案 → yaml 字段的推导规则。

## Q1 `ACP 环境` → `violet.platformAddress`

| 用户选项 | platformAddress |
|---------|-----------------|
| `devops-env1` | `https://devops-env1-hcvt43--idp.alaudatech.net`（已知值，来自 `configs/tektoncd-4.0.17-to-4.6.3-rc.91.yaml`） |
| `devops-env2` | 用 `devops-env2-*--idp.alaudatech.net` 前缀，但具体随机串可能不同——若用户没显式给完整 URL，提示他到 IDP 控制台复制后用 Other 填 |
| `devops-env3` | 同上 |
| Other | 用户给的完整 URL |

**强校验**：所有 platformAddress 必须 `^https://`。不满足直接停止，让用户改 Other。

**为什么 env2/env3 不像 env1 一样固化映射**：alauda 内部不同 sprint 会重建 demo / 集成测试环境，URL 中段（`-hcvt43--` 部分）每次部署会变。env1 是相对稳定的开发环境所以可以固化；env2/env3 必须每次确认。

## Q2 `Operator` → `name` + `namespace` + `artifact`

| 用户选项 | `operatorConfig.name` | `operatorConfig.namespace` | `operatorConfig.artifact`（自动） |
|---------|----------------------|---------------------------|----------------------------------|
| `tektoncd-operator` | `tektoncd-operator` | `tekton-operator` | `operatorhub-tektoncd-operator` |
| `gitlab-ce-operator` | `gitlab-ce-operator` | `gitlab-ce-operator` | `operatorhub-gitlab-ce-operator` |
| `katanomi-operator` | `katanomi-operator` | `katanomi-system` | `operatorhub-katanomi-operator` |
| Other（`<name>:<namespace>`） | 冒号前 | 冒号后 | `operatorhub-<name>` |

**注意**：`artifact` 字段在 yaml 里**可以不写**，Go 端 `defaultConfig` 会自动拼成 `<artifactPrefix>-<name>`（即 `operatorhub-<name>`）。skill **不写**这个字段，保持 yaml 简洁。

如果用户在 Other 里写了不带冒号的字符串，停下来问他 namespace 是什么——不要假设 `name == namespace`（gitlab-ce-operator 这种字面相同的是特例不是规律）。

## Q3 `子集群` → `violet.clusters`

直接用用户选的字符串。

| 选项 | clusters |
|------|---------|
| `global` | `global` |
| `devops` | `devops` |
| Other | 用户输入的子集群名 |

**为什么 violet.clusters 重要**：violet 默认写到 `global` 子集群，但 kubectl 实际连的子集群可能是 `devops`（多集群 ACP 部署常见）。不一致会出现"violet 报 success 但 AV 在 kubectl 看不到"的静默假成功。让用户**显式确认**这一字段是设计的核心安全机制，不要默认掉。

## Q4 `test 风格` → 各版本的 `testCommand`

`<ver>` 占位符用 `version.name`（= bundleVersion）替换。第 1 个版本的命令通常是"准备数据"，后续是"升级 + 验证"。

| 选项 | 第 1 个版本（`versions[0]`） | 后续版本（`versions[1..]`） |
|------|-----------------------------|---------------------------|
| `echo 占位` | `echo "[prepare] arrived at <ver>"` | `echo "[upgrade] arrived at <ver>"` |
| `godog @prepare\|@upgrade tag` | `TAGS=@prepare-<ver> GODOG_ARGS="--godog.format=allure" make test` | `TAGS=@upgrade-<ver> GODOG_ARGS="--godog.format=allure --bdd.cleanup=false" make test` |
| `make prepare+upgrade` | `REPO=allure make prepare` | `REPO=allure make upgrade` |
| Other | 用户提供的模板，含 `<ver>` 占位符，skill 逐版本替换 | 同左 |

**`<ver>` 替换示例**：选了 godog tag + 版本 `v4.0.17` → `TAGS=@prepare-v4.0.17 GODOG_ARGS=... make test`。

**风格选哪个**：
- `echo 占位` —— 只想验证 OLM/violet 升级机制本身走通，不跑业务测试（tektoncd 多跳验证常用）
- `godog tag` —— BDD 风格，feature 文件里有 `@prepare-<ver>` / `@upgrade-<ver>` tag（gitlab-ce-operator 是这种）
- `make prepare/upgrade` —— Makefile 实现了 `prepare` / `upgrade` target 的项目
- `Other` —— 任何项目的自定义 testCommand 模板，必须含 `<ver>` 占位符

## 推算项（绝不问用户）

| 推算 | 逻辑 |
|------|------|
| 跳数 | 版本列表的**行数**——用户原话："升级跳数是推算出来的结果，不是用户要回答几级的" |
| `upgradePaths[0].name` | `<v1>-to-<vN>`（如 `v4.0.17-to-v4.6.3-rc.91`） |
| `version.name` | 直接用 `bundleVersion` |
| `version.packageChannel` | 步骤 2 用户没显式给则**留空**（Go 端 `EffectivePackageChannel()` 会 fallback 到 `channel`） |
| 输出文件名 | `configs/<operator>-<v1>-to-<vN>.yaml`；重名追加 `-2`、`-3` |
