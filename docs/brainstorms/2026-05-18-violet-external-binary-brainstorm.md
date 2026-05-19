# Brainstorm: upgrade CLI 外调 violet 二进制做 Operator 上架

- **日期**：2026-05-18
- **范围**：仅讨论"上架"这一步（Artifact / ArtifactVersion 创建）；OLM Subscription / InstallPlan / CSV 不在本次重构内
- **现状代码锚点**：
  - `pkg/operator/operatorhub/artifact_version.go:15-89`（`InstallArtifactVersion` + `createArtifactVersion` 硬编码 unstructured）
  - `pkg/exec/exec.go:43-80`（已有的子进程执行机制，testCommand 走这条）
  - `pkg/config/config.go:29-52`（`OperatorConfig`）

---

## What We're Building

让 upgrade CLI **不把 violet 的逻辑打包进 Go 代码**，改成**外调用户环境里的 violet 二进制**完成"创建 Artifact + ArtifactVersion"这一步。机制和现有 `testCommand` 一致 —— 通过 `pkg/exec` 子进程调用。

### 行为变化

**升级路径里每个版本，upgrade CLI 要做的事**：

1. 按约定拼出 .tgz 的 MinIO URL：`<prefix>/<name>/<channel>/<name>.latest.ALL.<bundleVersion>.tgz`
2. 下载 .tgz 到临时目录（推荐 `os.MkdirTemp`，跑完即删；同一个 PathRun 内可缓存复用）
3. 调用 `violet push <local.tgz> --target-catalog-source platform --skip-push [--skip-push=false 私有场景]`，外加 platform-address / token 等鉴权参数
4. 继续走 Go 现有的 `waitArtifactVersionPresent` + `waitPackageManifest`（这部分保留，violet 命令返回不代表 OLM 已就绪）
5. 继续走 `InstallSubscription`（完全不变）

### 不做的事（YAGNI）

- 不把 violet 二进制嵌进 upgrade CLI（go-embed / wrapper script 都不做）
- 不写 violet 输出解析器；失败只透传 stderr + exit code
- 不引入"violet sidecar / 守护进程"之类的复用机制 —— 每个版本一次新进程
- 不支持其他打包来源（HTTP URL 已能覆盖私有/公共两种场景）

---

## Why This Approach

| 维度 | 嵌入 Go 代码（现状） | 外调 violet（拟采纳） |
|------|---------------------|-----------------------|
| Artifact/AV CRD 字段变更 | 改 Go + 重新发版 upgrade CLI | violet 自己跟进，CLI 无感 |
| 镜像 push 能力 | 没有，靠用户预先 push | violet push 内置（私有场景受益）|
| `--reset-bundle-version`、`--force` 等开关 | 要自己实现 | violet 已提供 |
| 错误诊断颗粒度 | 字段级精确 | 进程级（stderr）|
| 部署复杂度 | 单二进制 | 要求 violet 在 PATH 或显式路径 |
| 升级耦合 | upgrade CLI ↔ Alauda CRD | upgrade CLI ↔ violet CLI |

**核心理由**（按 CLAUDE.md "依赖方向指向稳定" 原则）：Alauda 的 Artifact CRD 仍在演进，upgrade CLI 不该被绑定到具体 schema；violet 是这类演进的天然"防火墙"。再加上 `pkg/exec` 已经在用同样的模式跑 `testCommand`，引入额外抽象成本几乎为零（架构对称）。

---

## Key Decisions

### 1. violet 接管范围：**只接管"创建 Artifact/ArtifactVersion"**

- 替换：`createArtifactVersion`（`artifact_version.go:51-90`）
- 保留：`waitArtifactVersionPresent`、`waitPackageManifest`、`InstallSubscription`、InstallPlan 手动审批
- 原因：violet 物理上不管 OLM 资源；保留 Go 控制 OLM 流程 = 升级时序仍然由 upgrade CLI 把控

### 2. .tgz 包来源：**MinIO URL 约定拼接 + prefix 可配**

- 默认 prefix：`http://package-minio.alauda.cn:9199/packages/`
- 拼接公式：`<prefix>/<name>/<channel>/<name>.latest.ALL.<bundleVersion>.tgz`
- 已验证可覆盖两种 channel 形态（`v4.6` 大版本 + `rc` 滚动通道）
- `Version` 不加新字段；`Channel`+`BundleVersion` 已足够

### 3. push 模式默认 `--skip-push=true`

- 默认场景：CI/集成环境，镜像已预先入库，仅需创建 CR
- 私有场景：`violetSkipPush: false`，让 violet 同时 push 镜像
- 在 `OperatorConfig` 里加开关，不进 Version

### 4. violet 二进制定位策略：**PATH 优先 + 配置覆盖**

- 默认从 `$PATH` 找 `violet`
- `OperatorConfig.VioletBin` 可显式指定路径（流水线友好）
- 不嵌入、不自动下载（嵌入下载会跟你的 `download-violet` skill 职责冲突）

### 5. 临时目录策略：**进程级 MkdirTemp，结束清理**

- 同一次 `upgrade` 执行内缓存（同一 .tgz 不重复下载）
- 进程结束 `defer RemoveAll`；不写共享缓存（避免跨 run 串扰）

### 6. 错误处理：**透传，不解析**

- violet 退出码非 0 → 直接返回 error，把 stderr 末尾若干行拼进 message
- 不做"按错误码分支重试"之类的细化逻辑
- 复合 CLAUDE.md "失败立即停止等用户指示"

---

## 配置 schema 变更（最终形态）

仅在 `OperatorConfig` 增加 4 个字段，`Version` 不动：

```yaml
operatorConfig:
  type: operatorhub
  name: tektoncd-operator
  # ... 现有字段 ...

  # === 新增 ===
  violetBin: ""                          # 可选，默认从 PATH 找
  violetPackagePrefix: "http://package-minio.alauda.cn:9199/packages/"
  violetSkipPush: true                   # 默认 true；私有环境置 false
  violetPushArgs: []                     # 透传给 violet push 的额外参数列表（私有场景填）
                                         # 例：["--dest-repo", "registry.private/devops",
                                         #      "--plain", "--image-pull-secret", "private-pull",
                                         #      "--force"]
```

`Version` 字段一行不动。

**鉴权两套体系**：
- **k8s API**：KUBECONFIG 由现有流水线注入 → upgrade CLI → `os.Environ()` 透传 → violet 子进程继承。CLI 无需感知。
- **registry 凭证（首期必须）**：环境变量 `VIOLET_REGISTRY_USERNAME` / `VIOLET_REGISTRY_PASSWORD`。upgrade CLI 检测到非空后**自动**为 violet 命令拼上 `--username $USER --password $PASS`，不读 config、不落盘。Args 列表里**不要**手写凭证，避免日志泄露。

---

## 实施轮廓（不细化，留给 plan）

- 新建 `pkg/operator/operatorhub/violet.go`（拼 URL + 下载 + exec violet push）
- 替换 `artifact_version.go` 里 `createArtifactVersion` 的调用点
- `pkg/config/config.go` 增字段 + 默认值
- 文档：更新 README 和 CLAUDE.md "新加 Operator 类型时" 段落（提及 violet 路径）

---

## Resolved Questions

### 1. violet 的鉴权参数怎么传 → **k8s 走 KUBECONFIG；registry 走环境变量**

**默认场景（`--skip-push=true`）**：完全不需要额外 platform / registry 凭证。
- 流水线 `devops/upgrade-test`（integration-test 集群）已经把 `platform_info.{url,token,cluster}` 通过 `hack/common.sh::gen_kubeconfig_base_config` 转为 kubeconfig，`export KUBECONFIG` 给 upgrade CLI
- `pkg/exec/exec.go:48` 调子进程时 `runCmd.Env = os.Environ()` → violet 子进程**天然继承 KUBECONFIG**
- violet `--skip-push=true` 走 k8s API 创建 Artifact/AV CR，KUBECONFIG 即权限

**私有场景（`--skip-push=false`，首期必须实现）**：
- registry 凭证走环境变量 `VIOLET_REGISTRY_USERNAME` / `VIOLET_REGISTRY_PASSWORD`。upgrade CLI 检测到非空时自动给 violet 拼 `--username` / `--password`
- 其他 violet push 参数（`--dest-repo` / `--plain` / `--image-pull-secret` / `--force` 等）由 `OperatorConfig.violetPushArgs` 列表透传 —— violet 演进 / 加新 flag 不需要改 CLI 代码
- 这样 schema 只多 1 个字段，但覆盖所有现有 violet push 私有场景需求

### 2. `--reset-bundle-version` 是否暴露 → **保持 violet 默认（不传参数）**

**结论**：CLI 不传 `--reset-bundle-version` 参数，沿用 violet 自身默认值（true）。

**依据**：
- 升级测试场景下 `BundleVersion` 与 bundle 镜像 tag 通常一致（来自同一发版流水线产物），reset 不会引起字段错位
- 若以后实测发现 `InstallSubscription` 找不到对应 CSV，再加配置开关 —— 符合 YAGNI + 可逆性原则
- 该选项不进 `OperatorConfig` schema，避免过早增加配置面

## Deferred to Plan/Implementation

3. **下载失败 / .tgz 缺失** → fail fast 是默认；是否加内置重试留 plan 决定（最简的实现是不重试，靠流水线 retry）
4. **缓存粒度** → 默认进程级共享（同一次 CLI run 内复用），跨 PathRun 不缓存；并发设计当前不需要
