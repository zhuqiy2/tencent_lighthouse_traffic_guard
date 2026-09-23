# Tencent Lighthouse Traffic Guard

轻量的腾讯云轻量应用服务器流量监控器。它调用 Lighthouse API 查询实例流量包用量，在达到阈值时可提交关机请求。

## 特性

- Go 单文件 CLI，适合 cron 或常驻轮询。
- 默认阈值为 95%，达到阈值即触发判断。
- 默认 dry-run，不会关机；只有增加 `--execute` 才会调用 `StopInstances`。
- 已关机实例不会重复提交关机请求。
- 使用腾讯云官方 Go SDK。

## 编译

```bash
go mod tidy
go test ./...
go build -o tencent-lighthouse-traffic-guard .
```

## 凭证与权限

程序支持环境变量，也支持通过 `--env-file` 加载 `KEY=VALUE` 配置。复制 [.env.example](.env.example) 后填写真实值，不要提交真实密钥：

```bash
cp .env.example /etc/tencent-lighthouse-traffic-guard.env
chmod 600 /etc/tencent-lighthouse-traffic-guard.env
```

也可以直接使用环境变量：

```bash
export TENCENTCLOUD_SECRET_ID="你的 SecretId"
export TENCENTCLOUD_SECRET_KEY="你的 SecretKey"
export TENCENTCLOUD_REGION="ap-guangzhou"
export TENCENTCLOUD_INSTANCE_ID="lhins-xxxxxxxx,lhins-yyyyyyyy"
```

CAM 用户的最小权限如下，策略模板见 [cam-policy-minimal.json](cam-policy-minimal.json)：

- `lighthouse:DescribeInstances`
- `lighthouse:DescribeInstancesTrafficPackages`
- 实际执行关机时增加 `lighthouse:StopInstances`

建议先只授予前两个权限进行观察，确认日志和阈值无误后，再把 `lighthouse:StopInstances` 加到同一策略。

如果控制台支持按资源限制，建议把资源限制为目标实例；如果该 API/地域不支持资源级授权，则只能使用 `*`，并通过单独 CAM 子用户、密钥轮换和主机文件权限降低风险。

## 单次检查

默认是 dry-run：

```bash
./tencent-lighthouse-traffic-guard
```

使用配置文件：

```bash
./tencent-lighthouse-traffic-guard --env-file /etc/tencent-lighthouse-traffic-guard.env
```

指定参数：

```bash
./tencent-lighthouse-traffic-guard \
  --region ap-guangzhou \
  --instance-id lhins-xxxxxxxx,lhins-yyyyyyyy \
  --threshold 95
```

`--instance-id` 和 `TENCENTCLOUD_INSTANCE_ID` 都支持逗号分隔多个实例。程序会逐台查询；达到阈值时只关机对应实例，重复 ID 会自动去重。

确认行为后开启真实关机：

```bash
./tencent-lighthouse-traffic-guard --execute
```

## 常驻轮询

每 5 分钟检查一次：

```bash
./tencent-lighthouse-traffic-guard --interval 5m --execute
```

## cron

例如每 5 分钟执行一次。将环境变量放入只有运行用户可读的文件中：

```cron
*/5 * * * * /usr/local/bin/tencent-lighthouse-traffic-guard --env-file /etc/tencent-lighthouse-traffic-guard.env --execute >> /var/log/tencent-lighthouse-traffic-guard.log 2>&1
```

环境文件权限建议为 `0600`。如果不确定 API 返回的套餐含义或实例状态，先不使用 `--execute` 观察日志。

## 注意事项

- 本程序只处理查询结果中的第一个流量包；部署前应确认目标实例只有一个当前有效套餐。
- 关机请求是异步提交的，程序记录“request submitted”不代表实例已经完成关机。
- 达到阈值后，程序不会自动开机，也不会自动购买或续费流量包。
- 请在测试实例上先验证区域、实例 ID、权限和流量统计结果。
