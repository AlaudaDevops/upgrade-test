# Generated config 自检清单

每次 skill 生成完一个 `configs/<file>.yaml`，**必须**按顺序跑下面 4 项；任一失败 → 立即停止，把失败原因报告给用户，**不要**擅自改 yaml 蒙混。

## 1. YAML 语法

```bash
python3 -c "import yaml,sys; yaml.safe_load(open(sys.argv[1])); print('✅ yaml.safe_load ok')" configs/<file>.yaml
```

期望：打印 `✅ yaml.safe_load ok`，非零 exit 直接报错。

## 2. 必填字段静态检查

```bash
file=configs/<file>.yaml

# operatorhub 类型必填
grep -q "type: operatorhub" "$file" && {
  grep -q "packagePrefix:" "$file"   || echo "❌ 缺 violet.packagePrefix"
  grep -q "platformAddress:" "$file" || echo "❌ 缺 violet.platformAddress"
}

# 每个版本都得有 channel（operatorhub 强制）
versions=$(grep -c "^      - name:" "$file")
channels=$(grep -c "^        channel:" "$file")
[ "$versions" = "$channels" ] || echo "❌ versions 数 ($versions) ≠ channels 数 ($channels)"

# packagePrefix 不能是占位符
grep -E "packagePrefix:\s*(\{\{|<|TODO|FIXME)" "$file" && echo "❌ packagePrefix 还是占位符"

# platformAddress 必须 https://
grep -E "platformAddress:\s*[^h]" "$file" | grep -v "^\s*#" && echo "❌ platformAddress 必须以 https:// 开头"
```

## 3. 凭证安全扫描

```bash
file=configs/<file>.yaml

# 非注释行里出现非空 platformPassword/platformUsername → 警告（不强制阻断）
grep -E "^[^#]*platform(Username|Password):\s+\S+" "$file" && {
  echo "⚠️  yaml 里有明文凭证。如果该文件会进 git，强烈建议改用 env：
       export VIOLET_PLATFORM_USERNAME=...
       export VIOLET_PLATFORM_PASSWORD=..."
}

# 显然的密码模式（如 'password' 后跟 6+ 字符）
grep -iE "(password|passwd|secret).*:\s*['\"]?[A-Za-z0-9!@#\$%^&*]{6,}" "$file" | grep -v "^\s*#" && {
  echo "❌ 检测到可能的明文密码，请确认是否需要"
}
```

## 4. Go 业务校验（最强保障）

upgrade CLI 没有独立 `--validate` 子命令，但 `pkg/config.LoadConfig` 在加载时会跑 `validateConfig`。最薄的调用方式：

```bash
cd /Users/alauda/Projects/DevOps/AlaudaDevops/upgrade-test

# 直接尝试 ./upgrade --config <file> --kubeconfig /dev/null
# 配置错会在 LoadConfig 阶段就报错并退出，不会真去连集群
# 业务校验失败 → exit 1，错误信息含 "channel is required" 等
./upgrade --config configs/<file>.yaml --kubeconfig /dev/null 2>&1 | head -30 &
pid=$!
sleep 3
kill -0 $pid 2>/dev/null && {
  # 进程还活着 = 配置加载成功，进了升级流程。立刻杀掉，配置 OK
  kill $pid 2>/dev/null
  echo "✅ LoadConfig + validateConfig 通过"
} || {
  wait $pid
  rc=$?
  [ $rc -ne 0 ] && echo "❌ 配置校验失败 (exit $rc)"
}
```

⚠️ **注意**：步骤 4 会真的启动一次进程并读 kubeconfig；用 `/dev/null` 当 kubeconfig 大概率在 connect 阶段失败，但那是**配置加载之后**的失败，足以证明 yaml 加载通过。

## 失败处理

- 步骤 1 失败 → YAML 语法错。打开 `configs/<file>.yaml` 让用户看到错误位置，**不要**自动修。
- 步骤 2 失败 → 缺必填字段或占位符未替换。回到 SKILL.md 阶段 2/3 重新问对应字段。
- 步骤 3 警告 → 提示用户但不阻断；询问"确认要把凭证留在 yaml 里吗？(y/N)"。
- 步骤 4 失败 → 把 Go 端报错原文给用户看（通常是 "channel is required for operatorhub type"），定位到具体版本后回到阶段 3 补字段。
