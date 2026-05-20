# Upgrade Tester

## 测试目标

升级测试主要还是用来验证**程序**升级后对**历史数据**是否兼容的测试。

## 功能要求

1. 升级测试包含： 创建对应版本 operator，部署对应版本实例，准备测试数据，验证测试数据，功能验证
2. 可以自定义升级路基。如存在 4 个版本 gitlab v1.1.0,v1.1.2,v1.2.0,v2.0.0.  升级路径可以是：v1.1.0 -> v2.0.0, v1.1.2 -> v2.0.0, v1.2.0 -> v2.0.0, v1.1.2 -> v1.2.0 -> v2.0.0
3. 支持在不同版本运行不同的测试用例集验证升级。

## 程序处理流程

1. 启动时读取配置文件 config.yaml，确定升级流程
2. 上架 artifactVersion
3. 安装 operator
4. 运行测试，再到 步骤2 升级到新版本 operator

## 运行升级

### 安装升级工具

通过源码安装

```sh
git clone https://github.com/AlaudaDevops/upgrade-test
cd upgrade-test
go build -o upgrade
```

在 [release](https://github.com/AlaudaDevops/upgrade-test/releases) 页面下载对应系统版本的二进制。

```bash
wget https://github.com/AlaudaDevops/upgrade-test/releases/download/v0.0.5/upgrade-ubuntu-latest-amd64
mv upgrade-ubuntu-latest-amd64 upgrade && chmod +x upgrade
./upgrade
```

### 测试用例的编写

测试用例分文两个部分：

1. 数据准备。

  - 数据准备多次执行应该具备幂等性，确保可以重复运行。
  - 需要添加 `skip-clean-namespace` 的 Tag 确保执行后实例不被清理。

```feature
功能: gitlab 升级

    @priority-high
    @e2e
    @prepare-17.8
    @prepare-17.11
    @gitlab-operator-prepare-upgrade
    @skip-cleanup-namespace
    @allure.label.case_id:gitlab-operator-prepare-upgrade
    场景: 安装 gitlab 实例，准备数据
        假定 命名空间 "gitlab-upgrade" 已存在
        并且 集群已安装 ingress controller
        当 已导入 "gitlab 实例" 资源
        """
        yaml:  ./testdata/values-gitlab-upgrade.yaml
        onConflict: ignore
        """
        并且 导入测试数据到 Gitlab 成功
        """
        url: http://test-gitlab-upgrade.example.com
        username: root
        password: redacted-password
        timeout: 15m
        importProjectPath: ./testdata/resources/test-upgrade-repo_export.tar.gz
        """
```

2. 检查升级数据。

  - 和数据准备相对应，负责升级实例以及检查准备的数据是否丢失。
  - 存在多次升级时，需要添加 `skip-clean-namespace` 的 Tag 确保执行后实例不被清理。


### 编写配置文件

将文件保存为 upgrade.yaml

```yaml
operatorConfig:
  workspace: /app/testing/ # test case 执行位置
  namespace: gitlab-ce-operator # operator 部署 ns
  name: gitlab-ce-operator # operator 名称
  violet: # 必填：上架走外部 violet 二进制
    packagePrefix: http://package-minio.alauda.cn:9199/packages/ # 必填，无默认值
    platformAddress: https://my-acp.example.com  # 必填：ACP 平台 URL
    clusters: devops                              # 写入的目标子集群名；默认 "global"，多集群部署必填
    # bin: /usr/local/bin/violet  # 可选；为空则在 $PATH 查 `violet`
    # skipPush: true              # 默认 true；私有 registry 场景置 false
    # pushArgs:                   # 私有场景透传给 `violet push` 的额外非凭证参数
    #   - --dest-repo             # 凭证 (--username/--password/--platform-username/--platform-password)
    #   - registry.private/devops # 必须走环境变量，pushArgs 写入会被 CLI 拒绝
    #   - --plain
    #   - --image-pull-secret
    #   - private-pull
upgradePaths: # 定义升级路径，可以包含多个
  - name: v17.8 upgrade to v17.11 # 升级名称
    versions: # 定义升级路经
      - name: v17.8 # 版本名称
        testCommand: |
          TAGS=@prepare-17.8 GODOG_ARGS="--godog.format=allure" make test
        bundleVersion: v17.8.10
        channel: stable          # OLM Subscription.spec.channel
        # packageChannel: v17    # MinIO URL 段；与 OLM channel 不同时填，省略则 fallback 到 channel
        # expectedSha256: a3f... # 可选；非空时强制校验下载的 .tgz
      - name: v17.11 # 版本名称
        testCommand: |
          TAGS=@upgrade-17.11 GODOG_ARGS="--godog.format=allure --bdd.cleanup=false" make test
        bundleVersion: v17.11.1
        channel: stable
```

**配置说明**：

- **`operatorConfig.violet` 必填**。upgrade CLI 不再在 Go 代码里直接创建 `Artifact`/`ArtifactVersion` CR，而是把这步外包给 `violet` 二进制。`operatorConfig.violet` 为空时 `InstallArtifactVersion` 会立即返回配置错误。
- **`packagePrefix` 没有默认值**。MinIO 根地址跨环境（私有 / 共享 / 区域镜像）不同，CLI 拒绝硬编码任何值。
- **`.tgz` URL 拼接约定**：`<prefix>/<name>/<packageChannel>/<name>.latest.ALL.<bundleVersion>.tgz`。`packageChannel` 是 MinIO 仓库的路径段（如 `v4.0` / `v4.6` / `rc`），**与 OLM Subscription 的 `channel`（如 `stable` / `pipelines-4.0`）不是同一个概念**。当两者字面相同时（少数情况）可只填 `channel`，CLI 会自动 fallback；当两者不同（tektoncd 这类）必须显式填 `packageChannel`。
- **`expectedSha256` 强烈建议为外网下载场景填上**。HTTP 明文 + DNS 投毒可能注入恶意 tgz，违 violet 会带集群凭证把恶意 bundle 上架——内网 MinIO 也建议加防。

### 构建测试镜像

需要在测试镜像中添加升级测试的制品：

```dockerfile
FROM docker-mirrors.alauda.cn/library/golang:1.24-bookworm AS builder

WORKDIR /tools
RUN mkdir -p /tools/bin

COPY testing /app
ENV GOPROXY='https://build-nexus.alauda.cn/repository/golang/,direct'
RUN set -eux; \
    cd /app && \
    go test -c -o /tools/bin/gitlab.test ./

# add content
# renovate: datasource=github-releases depName=upgrade-test packageName=AlaudaDevops/upgrade-test
ARG UPGRADE_TEST_VERSION=v0.0.5
RUN if [ "$(arch)" = "arm64" ] || [ "$(arch)" = "aarch64" ]; then ARCH="arm64"; else ARCH="amd64"; fi; \
    wget https://github.com/AlaudaDevops/upgrade-test/releases/download/${UPGRADE_TEST_VERSION}/upgrade-ubuntu-latest-${ARCH} && \
    mv upgrade-ubuntu-latest-${ARCH} /tools/bin/upgrade && \
    chmod +x /tools/bin/upgrade
# add end

FROM build-harbor.alauda.cn/devops/test-bdd:latest

COPY --from=builder /tools/bin/gitlab.test /tools/bin/gitlab.test
COPY --from=builder /tools/bin/upgrade /tools/bin/upgrade
COPY . /app

WORKDIR /app/testing
ENV TEST_COMMAND="gitlab.test"

ENTRYPOINT ["gitlab.test"]
CMD ["--godog.concurrency=2", "--godog.format=allure", "--godog.tags=@prepare-17.8"]
```

### 运行测试

```sh
# 配置链接的集群
export KUBECONFIG=<kubeconfig.yaml>
./upgrade --config upgrade.yaml
```

## 前置检查 (preflight)

进入升级循环前，upgrade CLI 会对**每条升级路径的起点版本（`versions[0]`）**做一次**只读**扫描，发现下列任一残留就立即停止：

| 检查项 | 资源 | 命名空间 |
|--------|------|----------|
| Subscription 残留 | `subscriptions.operators.coreos.com/<name>` | `operatorConfig.namespace` |
| ArtifactVersion 残留 | `artifactversions.app.alauda.io/<artifact>.<bundleVersion>` | `cpaas-system` |
| 未结束的 InstallPlan | `installplans.operators.coreos.com`（用 OLM label `operators.coreos.com/<package>.<ns>` 精确过滤），`status.phase` 非 `Complete` / `Failed` | `operatorConfig.namespace` |

preflight 设有 **30s 总超时**，超时直接报错避免阻塞升级。任何残留 → 输出每个对象的 `kubectl delete` 命令模板（已用 `%q` 转义、可直接复制粘贴），并附带 finalizer 卡死的兜底指令和"等 OLM settle 30s"提示。

**典型错误信息**：

```
preflight failed: 1 residual resource(s) blocking upgrade:

  Subscription/tektoncd-operator (ns: tektoncd-pipelines)
      kubectl delete subscription "tektoncd-operator" -n "tektoncd-pipelines"

If a delete hangs (finalizer stuck), patch finalizers off:
  kubectl -n <ns> patch <kind> <name> --type=merge -p '{"metadata":{"finalizers":[]}}'

After cleanup, wait ~30s for OLM to settle, then re-run `upgrade`.
To bypass (NOT recommended): re-run with --skip-preflight
```

### preflight 相关 flag

| Flag | 何时用 | 行为 |
|------|--------|------|
| `--skip-preflight` | 已知环境脏但要测某个边界 case；CI 应急 | 跳过整段检查，仅 `WARN` 一行 audit；其余流程不变 |
| `--confirm-cluster=<NAME>` | `operatorConfig.violet.clusters` **非空**时**必填** | `<NAME>` 必须等于 KUBECONFIG 当前 context 名；不匹配直接报错，防止误把生产 KUBECONFIG 当 sprint env 跑（"silent 假成功"的对称防御） |

in-cluster 运行（pod 内，无 kubeconfig 文件）会自动降级为 `WARN` 提示而不是硬 fail。

### 最小 RBAC

只跑 preflight 需要的 verbs（不含升级本身需要的写权限）：

```yaml
rules:
  - apiGroups: ["operators.coreos.com"]
    resources: ["subscriptions", "installplans"]
    verbs: ["get", "list"]
  - apiGroups: ["app.alauda.io"]
    resources: ["artifactversions"]
    verbs: ["get"]
```

升级本身需要的写权限不在此列。

## violet 依赖与运行环境

upgrade CLI 把 `Artifact` / `ArtifactVersion` CR 的创建步骤外包给 `violet` 二进制，因此运行环境必须满足以下条件。

### 二进制依赖

- 运行环境 `$PATH` 上需要有可执行的 `violet`，或者通过 `operatorConfig.violet.bin` 指定绝对路径。Bin 非空时 CLI 会做 `filepath.IsAbs` + `os.Stat` + 可执行位校验，不接受相对路径或非可执行文件。
- 流水线场景：确保 Tekton task 镜像里预装 violet，或在 task 启动 script 里下载安装。本地开发：参考仓库内 `download-violet` skill 或自行从 cloud.alauda.cn 下载。
- 当前实现不做 `violet --version` preflight 检查，依赖 OS "command not found" 错误兜底。

### 子进程环境变量 allowlist

upgrade CLI 调用 violet 时**只透传**以下宿主环境变量：`KUBECONFIG`、`PATH`、`HOME`、`USER`、`VIOLET_*` 前缀。CI runner 上其他 secret（`GITHUB_TOKEN` / `AWS_*` / 等）不会进入 violet 子进程，符合最小权限原则。

### 凭证（两套环境变量）

`violet push` 涉及两类凭证，**都不写进 config.yaml**，改用环境变量：

| 用途 | 环境变量 | 注入的 violet 参数 | 何时需要 |
|------|----------|-------------------|----------|
| 登录 ACP 平台创建 Artifact/AV CR | `VIOLET_PLATFORM_USERNAME` / `VIOLET_PLATFORM_PASSWORD` | `--platform-username` / `--platform-password` | **真实集群必填**（violet 拒绝无凭证启动） |
| 推送镜像到私有 registry | `VIOLET_REGISTRY_USERNAME` / `VIOLET_REGISTRY_PASSWORD` | `--username` / `--password` | 仅 `skipPush: false` 私有场景 |

```sh
export VIOLET_PLATFORM_USERNAME=admin@example.invalid
export VIOLET_PLATFORM_PASSWORD=<password>
# 仅私有 push 场景需要的额外两行:
# export VIOLET_REGISTRY_USERNAME=<user>
# export VIOLET_REGISTRY_PASSWORD=<password>

./upgrade --config upgrade.yaml
```

upgrade CLI 检测到非空时自动追加到 `violet push` 的 argv，日志渲染时把 `--password` 和 `--platform-password` 之后的值替换成 `***`。

⚠️ **共享 CI runner 安全警告**：当前 violet 不支持 stdin / 文件方式读凭证，`--password` 只能进 argv。OS 级 `ps auxe` / `/proc/<pid>/cmdline` / `strace` 都能看到明文密码。**多租户共享 runner 上禁用 `skipPush: false`**，私有 push 场景务必使用独占的 pipeline runner / 独占的 OS 用户账号。

### k8s 凭证

violet 通过 `KUBECONFIG` 连集群（与 upgrade CLI 自身共享同一份）。流水线场景在调 upgrade 前先 `export KUBECONFIG=...` 即可，CLI 透传给子进程不需要额外配置。
