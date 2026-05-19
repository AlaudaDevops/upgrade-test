# Default Values

下面所有字段 skill **不问用户**——按表直接填进 yaml 模板，或按"不写 yaml"留空。

**为什么这些可以默认**：要么是 alauda 内部场景下的"高概率值"（95%+ 一致），要么是用户改起来比 skill 问起来还快（vim 1 行 vs 一次 AskUserQuestion 阻塞）。

---

## 写进 yaml 的默认值

| 字段 | 默认值 | 为什么这么默认 |
|------|--------|---------------|
| `type` | `operatorhub` | 95%+ 场景；要 local 让用户生成后改 1 行 yaml |
| `logLevel` | `debug` | 首次跑建议看详细日志；用户嫌吵后改成 `info` |
| `immediate` | `true` | 遇错即停最容易定位问题。多 path 场景想全跑用户改 yaml 一字符 |
| `operatorConfig.workspace` | `./testing` | 与 README 范例对齐。镜像内场景（`/app/testing`）用户改 |
| `operatorConfig.timeout` | `20m` | 比 Go 端默认 10m 宽。OLM replace chain 多跳 + 镜像下载 + AV 就绪通常需要超过 10m |
| `operatorConfig.interval` | 不写（用 Go 默认 5s） | Go 默认已合适；写出来反而增加 yaml 噪音 |
| `operatorConfig.artifactPrefix` | 不写（用 Go 默认 `operatorhub`） | 同上 |
| `operatorConfig.artifact` | 不写（让 Go 自动拼） | Go 端会拼 `<artifactPrefix>-<name>`，写出来就是冗余 |
| `violet.packagePrefix` | `http://package-minio.alauda.cn:9199/packages/` | alauda 内网共享 MinIO，95%+ 场景；离线/区域镜像让用户改 |
| `violet.platformAddress` | 步骤 1 Q1 推导 | 见 mappings.md |
| `violet.clusters` | 步骤 1 Q3 答案 | 由用户选 |
| `violet.localPackageDir` | `.` | 启用本地 .tgz 缓存；重复跑同版本节省下载。`.` = 当前工作目录，不污染 `/tmp` |
| `violet.bin` | 不写（让 violet 从 PATH 查） | 大多数环境 `violet` 在 `$PATH`；用户用绝对路径让用户改 |

---

## 不写 yaml 的字段（也不问用户）

| 字段 | 为什么不写 |
|------|-----------|
| `violet.platformUsername` | 凭证写 yaml 一旦提交 git 即泄露——`configs/tektoncd-4.0.17-to-4.6.3-rc.91.yaml:19` 已有先例。skill 把步骤 2 收集到的凭证只在步骤 4 输出为 `export VIOLET_PLATFORM_USERNAME=...` 让用户复制粘贴执行 |
| `violet.platformPassword` | 同上 |
| `violet.skipPush` | 用 Go 端默认 `true`（适配 CI 共享 catalog 场景）。私有 registry 场景需要 `false`，让用户改 yaml |
| `violet.pushArgs` | 仅私有 registry 场景需要，大多数场景留空更干净 |
| `version.expectedSha256` | 步骤 2 用户没给 sha256 的版本**不写**该字段（绝不蒙编假值）。CLI 会跳过校验。强烈建议用户跑一次后从日志拿到 sha 再回填 |
| `version.testSubPath` | 默认 `testing`，写出来是冗余 |

---

## 推算项

参见 [mappings.md](mappings.md) 末尾的"推算项"小节。最重要的一条：**跳数 = 版本列表行数**，永远不问用户。

---

## 用户最常需要修改的字段（在步骤 4 默认决策清单里着重提示）

按改动频次降序：

1. `operatorConfig.workspace` —— 镜像内 `/app/testing`、流水线 `/workspace` 等
2. `violet.clusters` —— 多集群环境从 `global` 改成 `devops` 之类
3. `immediate` —— 多 path 场景想全跑改 `false`
4. `operatorConfig.timeout` —— 长升级链改 `30m`/`45m`
5. `logLevel` —— 不需要详细日志改 `info`

清单格式建议：

```
默认决策（如需改请编辑对应行）：
  configs/<file>:L<n>  workspace: ./testing       # 镜像内常需改 /app/testing
  configs/<file>:L<n>  clusters: global           # 多集群改 devops
  configs/<file>:L<n>  immediate: true            # 多 path 全跑改 false
  configs/<file>:L<n>  timeout: 20m               # 长链改 30m/45m
  configs/<file>:L<n>  logLevel: debug            # 不要详细日志改 info
  configs/<file>:L<n>  packagePrefix: http://package-minio.alauda.cn:9199/packages/
  (其他默认: type=operatorhub, localPackageDir=., artifact 自动拼)

凭证: 走 env (yaml 里无 username/password)
```
