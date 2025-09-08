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
        password: 07Apples@
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
upgradePaths: # 定义升级路径，可以包含多个
  - name: v17.8 upgrade to v17.11 # 升级名称
    versions: # 定义升级路经
      - name: v17.8 # 版本名称
        testCommand: |
          TAGS=@prepare-17.8 GODOG_ARGS="--godog.format=allure" make test
        bundleVersion: v17.8.10
        channel: stable
      - name: v17.11 # 版本名称
        testCommand: |
          TAGS=@upgrade-17.11 GODOG_ARGS="--godog.format=allure --bdd.cleanup=false" make test
        bundleVersion: v17.11.1
        channel: stable
```

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
