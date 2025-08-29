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
         testCommand: | # 执行测试指令
          gitlab17.8.test --godog.concurrency=1 --godog.format=allure --godog.tags=@prepare
         bundleVersion: v17.8.10 # bundle 版本号
         channel: stable
       - name: v17.11 # 版本名称
         testCommand: |
           gitlab17.11.test --godog.concurrency=1 --godog.format=allure --godog.tags=@upgrade --bdd.cleanup=false
         bundleVersion: v17.11.1 # bundle 版本号
         channel: stable
```

### 运行测试

```sh
# 配置链接的集群
export KUBECONFIG=<kubeconfig.yaml>
./upgrade --config upgrade.yaml
```
