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

## 配置文件

```yaml
operatorConfig:
  workspace: /Users/mac/Desktop/kychen/alaudadevops/gitlab-operator
  namespace: gitlab-ce-operator # operator 部署 ns
  name: gitlab-ce-operator # operator 名称
upgradePaths: # 定义升级路径，可以包含多个
   - name: v17.8 upgrade to v17.11 # 升级名称
     versions: # 定义升级路经
       - name: v17.8 # 版本名称
         testCommand: | # 执行测试指令
          # git checkout feat/upgrade-case-17.8
          # export REPORT=allure
          # make prepare
          gitlab17.8.test --godog.concurrency=1 --godog.format=allure --godog.tags=@prepare --bdd.cleanup=false
        #  testSubPath: v17.8.10
         bundleVersion: v17.8.10 # bundle 版本号
       - name: v17.11 # 版本名称
         testCommand: |
           # git checkout feat/upgrade-case-17.11
           # export REPORT=allure
           # make upgrade
           gitlab17.8.test --godog.concurrency=1 --godog.format=allure --godog.tags=@prepare --bdd.cleanup=false
           gitlab17.11.test --godog.concurrency=1 --godog.format=allure --godog.tags=@upgrade --bdd.cleanup=false
        #  testSubPath: v17.11.1
         bundleVersion: v17.11.1 # bundle 版本号
```

## 测试用例的编写

测试用例分文两个部分：

1. 数据准备。

  - 数据准备多次执行应该具备幂等性，确保可以重复运行。
  - 需要添加 skip-clean-namespace 的 Tag 确保执行后实例不被清理。

2. 检查升级数据。

  - 和数据准备相对应，负责升级实例以及检查准备的数据是否丢失。
  - 存在多次升级时，需要添加 skip-clean-namespace 的 Tag 确保执行后实例不被清理。

## 本地运行

本地可以使用 git 命令切换分支，达到更换测试case的目的，然后使用 make 命令执行case。配置示例：

```yaml
operatorConfig:
  workspace: /Users/mac/Desktop/kychen/alaudadevops/gitlab-operator
  namespace: gitlab-ce-operator # operator 部署 ns
  name: gitlab-ce-operator # operator 名称
upgradePaths: # 定义升级路径，可以包含多个
   - name: v17.8 upgrade to v17.11 # 升级名称
     versions: # 定义升级路经
       - name: v17.8 # 版本名称
         testCommand: | # 执行测试指令
          git checkout feat/upgrade-case-17.8
          export REPORT=allure
          make prepare
         testSubPath: testing
         bundleVersion: v17.8.10 # bundle 版本号
       - name: v17.11 # 版本名称
         testCommand: |
           git checkout feat/upgrade-case-17.11
           export REPORT=allure
           make upgrade
         testSubPath: testing
         bundleVersion: v17.11.1 # bundle 版本号
```

## 流水线中运行

流水线中运行需要提前构建好执行测试的二进制，以及升级工具。镜像目录结构如下：

```sh
├── /bin/
│   └── gitlab17.11.test # 17.11 用例执行文件
│   └── gitlab17.8.test # #17.8 用例执行文件
│   └── upgrade # 升级测试工具
├── /app/testing
│   └── v17.8
│     └──── allure-results... # 测试所需文件
│   └── v17.11
│     └──── allure-results... # 测试所需文件
│   └── config.yaml # 测试执行的 yaml 配置
```

config yaml 配置如下：

```yaml
operatorConfig:
  workspace: /app/testing
  namespace: gitlab-ce-operator # operator 部署 ns
  name: gitlab-ce-operator # operator 名称
upgradePaths: # 定义升级路径，可以包含多个
   - name: v17.8 upgrade to v17.11 # 升级名称
     versions: # 定义升级路经
       - name: v17.8 # 版本名称
         testCommand: | # 执行测试指令
          gitlab17.8.test --godog.concurrency=1 --godog.format=allure --godog.tags=@prepare --bdd.cleanup=false
          mv test_dump common
         testSubPath: v17.8
         bundleVersion: v17.8.10 # bundle 版本号
       - name: v17.11 # 版本名称
         testCommand: |
           
           gitlab17.11.test --godog.concurrency=1 --godog.format=allure --godog.tags=@upgrade --bdd.cleanup=false
         testSubPath: v17.11
         bundleVersion: v17.11.1 # bundle 版本号
```
