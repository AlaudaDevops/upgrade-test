---
name: gen-upgrade-config
description: 在 AlaudaDevops/upgrade-test 仓库下，通过最多 2 轮交互生成 upgrade CLI 的 config.yaml — 用户只需回答「测试环境凭证（ACP 地址/账号/密码/子集群）」和「升级目标（operator + 版本列表起点→终点）」，跳数、namespace、artifact、testCommand 模板、timeout、workspace、packagePrefix 等全部由 skill 推算或填默认。**当用户提到「生成 upgrade 配置 / 新建 upgrade config / 写 upgrade.yaml / 配置升级测试 / new upgrade config / gen upgrade-config / 帮我搞一个升级测试配置 / 我要测 xxx-operator 从 vA 升到 vB / 准备 operator 升级 yaml」等任意涉及 upgrade-test 仓库 config.yaml 生成的场景，都使用本 skill——即使用户没说出"生成"或"config"字样**。skill 只负责生成 + 自检 + 输出 export 命令，不负责执行升级（执行用 `./upgrade --config <path>`）。
triggers:
  - /gen-upgrade-config
  - /new-upgrade-config
  - 生成 upgrade 配置
  - 新建 upgrade 配置
  - 配置升级测试
  - 写 upgrade yaml
---

# gen-upgrade-config

为 `AlaudaDevops/upgrade-test` 仓库生成 `configs/<file>.yaml`，让 upgrade CLI 一键跑 operator 升级测试。

## 核心原则：摩擦最小化

**用户视角真正必答的只有 2 类**：

1. **测试环境凭证**：ACP 平台地址、用户名、密码、子集群
2. **测试目标**：哪个 operator、升级版本列表（起点 → 终点）

其余字段——跳数、namespace、artifact、testCommand 模板、timeout、workspace、packagePrefix——**全部由 skill 推算或填默认**，绝不打扰用户。

**为什么这样设计**：每多问一个可推断的字段，相当于把那次推断的成本均摊到此后**所有调用**。一个 alauda 内部 skill 一年可能被触发上百次，多问一个问题就是上百次的累计摩擦——长期看远高于在 skill 里实现推算逻辑的一次性成本。

## 交互流程（最多 2 轮）

### 步骤 1：单次 AskUserQuestion 4 槽

| 槽 | header（≤12 字符） | 选项 |
|---|------|------|
| Q1 | `ACP 环境` | `devops-env1` / `devops-env2` / `devops-env3` / Other（用户填完整 https:// URL） |
| Q2 | `Operator` | `tektoncd-operator` / `gitlab-ce-operator` / `katanomi-operator` / Other（填 `<name>:<namespace>`） |
| Q3 | `子集群` | `global` / `devops` / Other（自填） |
| Q4 | `test 风格` | `echo 占位` / `godog @prepare\|@upgrade tag` / `make prepare+upgrade` / Other（自定义模板） |

Q1/Q2/Q4 的答案如何推导成 yaml 字段，详见 [references/mappings.md](references/mappings.md)。**不要再问跳数、不要再问 type、不要再问 timeout**——这些都在默认值表里。

### 步骤 2：单次自由文本（凭证 + 版本，一段贴完）

发给用户：

````
请按下面格式贴 2 段信息：

========= 凭证 =========
username: <ACP 平台账号，如 admin@cpaas.io>
password: <密码>

========= 版本列表 =========
按升级方向（起点 → 终点）N 行。跳数 = 行数。每行格式：

  <bundleVersion> <channel> [packageChannel] [expectedSha256]

示例（tektoncd 3 跳）：
  v4.0.17       stable v4.0 2915aa7c6ef834005e57e9a737c56633a23fb5122dcc28073918258ee1afcdd9
  v4.2.5        stable v4.2 2989c43da2bb16ba542f853ecc3123d18c8db31fb23c92b0a84af0f37cd24f83
  v4.6.3-rc.91  stable rc

示例（gitlab 2 跳）：
  v17.8.10  stable
  v17.11.1  stable
````

**解析规则**：
- 跳数 = 版本行数。**绝不**单独问用户跳数（用户原话："升级跳数是推算出来的结果，不是用户要回答几级的"）
- `version.name` 直接用 `bundleVersion`
- `packageChannel` 省略时留空，让 Go 端 fallback 到 `channel`
- `expectedSha256` 缺失就**不写**该字段，**绝不**蒙编假值

**凭证只暂存内存**：步骤 4 输出为 `export VIOLET_PLATFORM_USERNAME=... / export VIOLET_PLATFORM_PASSWORD=...` 命令片段，让用户复制粘贴执行。**永远不写进 yaml**——`configs/tektoncd-4.0.17-to-4.6.3-rc.91.yaml:19` 已经被污染过一次明文密码，不要重蹈覆辙。

### 步骤 3：生成 yaml

读 `templates/operatorhub.yaml.tmpl`，按以下信息源填占位符：

- 步骤 1 + 步骤 2 收集到的字段
- [references/defaults.md](references/defaults.md) 默认值表（type / workspace / immediate / timeout / packagePrefix / localPackageDir 等全在那）
- [references/mappings.md](references/mappings.md) 推导规则（ACP 环境→URL、operator→namespace+artifact、test 风格→testCommand 模板）

输出文件路径：`configs/<operator>-<v1>-to-<vN>.yaml`（`<v1>` 用第 1 行 bundleVersion，`<vN>` 用最后一行）。

### 步骤 4：自检 + 输出

跑 [CHECKLIST.md](CHECKLIST.md) 的 4 项自检（yaml 语法 / Go LoadConfig / 凭证不在 yaml / 必填字段齐全）。**任一失败立即停**，把错误原文给用户看，**不要**擅自把字段改成假值蒙混。

成功后向用户输出 3 个块：

**(a) 文件路径 + 自检结果**

```
✅ configs/<file>.yaml 已生成
自检：YAML ✅ / LoadConfig ✅ / 凭证安全 ✅ / 必填 ✅
```

**(b) export 命令片段**（用步骤 2 收到的真实 username/password 填入）

```bash
export KUBECONFIG=/path/to/kubeconfig    # 用户自己改
export VIOLET_PLATFORM_USERNAME=<真实账号>
export VIOLET_PLATFORM_PASSWORD=<真实密码>
```

**(c) 默认决策清单 + 运行命令**

列出本次所有被默认决定的字段（type=operatorhub、workspace=./testing、immediate=true、timeout=20m、packagePrefix=内网、localPackageDir=. 等），每行附"如需改，编辑 `configs/<file>:<line>`"，最后跟运行命令：

```bash
./upgrade --config configs/<file>.yaml --log-level debug
```

## 硬约束（违反任一即破坏 skill 价值）

1. **跳数不要单独问**——从版本列表行数推算
2. **platformPassword 永远不写 yaml**——无例外，即使用户口头说"写进去"也先确认走 env 是否真的不行
3. **失败即停**——自检失败不要擅自把字段改成假值蒙混
4. **不要 git add / commit** 生成的文件
5. **不要在生成后主动重启提问**——用户嫌字段不对会 vim 改 yaml；skill 重新问反而打断流程

## 反例（已发生过的）

- ❌ 旧版 SKILL.md 问"运行场景：本地/流水线/调试"——`workspace` 用户自己 vim 改 1 行就行，不必问
- ❌ 旧版 SKILL.md 问"Operator 类型：operatorhub/local"——95% 场景就是 operatorhub，写错代码层也 fallback 到 operatorhub，问得没意义
- ❌ 旧版 SKILL.md 问"升级跳数：2/3/4+"——跳数就是版本列表行数，单独问是冗余

## 参考资料（progressive disclosure）

只在真正需要查时读，避免 SKILL.md 主体被字段表撑大：

- [references/mappings.md](references/mappings.md) —— ACP 环境 / Operator / test 风格的详细字段映射
- [references/defaults.md](references/defaults.md) —— 完整默认值表 + 每个默认的理由（写 yaml 的 vs 不写 yaml 的）
- [CHECKLIST.md](CHECKLIST.md) —— 4 项自检的可执行命令
- [templates/](templates/) —— yaml 模板（operatorhub / local / version snippet）
- 真实样本：仓库 root 的 `configs/tektoncd-*.yaml`、`configs/demo.yaml`
