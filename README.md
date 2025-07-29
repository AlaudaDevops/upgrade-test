# Tool Upgrade Tester

## 测试目标

升级测试主要还是用来验证**程序**升级后对**历史数据**是否兼容的测试。

**程序** 包括：

1. operator
2. gitlab 程序功能

**数据** 包括：

1. operator 部署实例的 CR
2. gitlab 代码仓库、分支等

## 功能要求

1. 升级测试包含： 创建对应版本 operator，部署对应版本实例，准备测试数据，验证测试数据，功能验证
2. 升级测试中断后可恢复执行
3. 可以自定义升级路基。如存在 4 个版本 gitlab v1.1.0,v1.1.2,v1.2.0,v2.0.0.  升级路径可以是：v1.1.0 -> v2.0.0, v1.1.2 -> v2.0.0, v1.2.0 -> v2.0.0, v1.1.2 -> v1.2.0 -> v2.0.0
4. 支持在不同版本运行不同的测试用例集验证升级。

## 程序处理流程

1. 启动时读取配置文件 config.yaml，确定升级流程

```yaml
operatorBundle: build-harbor.alauda.cn/devops/gitlab-ce-operator
operatorGitRepository: https://github.com/AlaudaDevops/gitlab-ce-operator.git # 定义
runType: git # git, image
logLevel: debug
upgradePaths: # 定义升级路径，可以包含多个
   - name: v17.4 -> v17.8 -> v17.11 # 升级名称
     versions: # 定义升级路经
       - name: v17.4 # 版本名称
         git: # 定义 runType 为 Git 时所需参数
            revision: release-v17.4 # 用于 clone 代码，可以是分支，tag，和commit
            buildCommand: "make bundle && make push-bundle-image && make push-operator-image" # 用于构建测试使用的bundle镜像
            casePath: "testing" # 测试代码存放位置，将使用 pytest 进行调用， 默认为：testing
         image:
            image: 152-231-registry.alauda.cn:60070/devops/gitlab-ce-operator-bundle:v17.4.0 # bundle 镜像，runType 为 image 时用户部署 operator
            casePath: "testing" # 测试代码存放位置，将使用 pytest 进行调用， 默认为：testing
       - name: v17.8
         git: 
            revision: release-v17.8 
            buildCommand: "make bundle && make push-bundle-image && make push-operator-image" 
            casePath: "testing" 
         image:
            image: 152-231-registry.alauda.cn:60070/devops/gitlab-ce-operator-bundle:v17.8.0 
            casePath: "testing"
       - name: v17.11
         git: 
            revision: release-v17.11 
            buildCommand: "make bundle && make push-bundle-image && make push-operator-image" 
            casePath: "testing" 
         image:
            image: 152-231-registry.alauda.cn:60070/devops/gitlab-ce-operator-bundle:v17.11.0 
            casePath: "testing"
   - name: v17.8 -> v17.11 # 可以定义多个升级路径
     versions:
       - name: v17.8
         git: 
            revision: release-v17.8 
            buildCommand: "make bundle && make push-bundle-image && make push-operator-image" 
            casePath: "testing" 
         image:
            image: 152-231-registry.alauda.cn:60070/devops/gitlab-ce-operator-bundle:v17.8.0 
            casePath: "testing"
       - name: v17.11
         git: 
            revision: release-v17.11 
            buildCommand: "make bundle && make push-bundle-image && make push-operator-image" 
            casePath: "testing" 
         image:
            image: 152-231-registry.alauda.cn:60070/devops/gitlab-ce-operator-bundle:v17.11.0 
            casePath: "testing"
```

2. 根据配置文件运行，如果 runType 为 Git，着 clone 代码，并执行构建指令，构建需要测试的镜像。记录测试镜像和casePath 准备测试。

3. 如果 runType 是 image 无动作

4. 根据测试镜像部署 operator

4.1 创建 artifact

```yaml
apiVersion: app.alauda.io/v1alpha1
kind: Artifact
metadata:
  labels:
    cpaas.io/builtin: "true"
    cpaas.io/library: platform
    cpaas.io/present: "true"
    cpaas.io/type: bundle
  name: test-gitlab-ce-operator
  namespace: cpaas-system
spec:
  artifactVersionSelector:
    matchLabels:
      cpaas.io/artifact-version: test-gitlab-ce-operator
  description: gitlab-ce operator bundle image
  displayName: gitlab-ce operator
  imagePullSecrets: []
  present: true
  registry: 152-231-registry.alauda.cn:60070 # 根据测试镜像设置镜像 registry
  repository: devops/gitlab-ce-operator-bundle # 根据测试镜像设置 repository
  tagPatterns:
    - maxQuantity: 1
      name: stable
      regex: ^v(?P<Major>0|(?:[1-9]\d*))(?:\.(?P<Minor>0|(?:[1-9]\d*))(?:\.(?P<Patch>0|(?:[1-9]\d*))))$
    - maxQuantity: 1
      name: alpha
      regex: ^v(?P<Major>0|(?:[1-9]\d*))(?:\.(?P<Minor>0|(?:[1-9]\d*))(?:\.(?P<Patch>0|(?:[1-9]\d*)))?(?:\-(?P<PreRelease>[0-9A-z\.-]+))?(?:\+(?P<Meta>[0-9A-z\.-]+))?)$
  type: bundle
```

4.2 创建 artifactversion 

```yaml
apiVersion: app.alauda.io/v1alpha1
kind: ArtifactVersion
metadata:
  labels:
    cpaas.io/artifact-version: test-gitlab-ce-operator
    cpaas.io/library: platform
  name: test-gitlab-ce-operator.v17.11.0 # 根据 artifact 名称和测试镜像 tag生成
  namespace: cpaas-system
  ownerReferences:
    - apiVersion: app.alauda.io/v1alpha1
      kind: Artifact
      name: test-gitlab-ce-operator
      uid: 11473fe4-eea1-439d-8e19-c50736b2467a # 根据 artifact uid 填写
spec:
  present: true
  tag: v17.11.0 # 根据 测试镜像 tag 填写
```

4.3 等待 ArtifactVersion status 状态变为 Present。

```yaml
status:
  message: ""
  name: gitlab-ce-operator
  phase: Present
  reason: Success
  type: bundle
  version: test-gitlab-ce-operator.v17.11.0
```
