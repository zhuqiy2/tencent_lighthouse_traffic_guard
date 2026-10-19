# Tencent Lighthouse Traffic Guard

轻量的腾讯云轻量应用服务器流量监控器。它调用 Lighthouse API 查询实例流量包用量，在达到阈值时可提交关机请求。

## 特性

- Go 单文件 CLI，适合 cron 或常驻轮询。
- 默认阈值为 95%，达到阈值即触发判断。
- 默认 dry-run，不会关机；只有增加 `--execute` 才会调用 `StopInstances`。
- 已关机实例不会重复提交关机请求。
- 支持多实例、多地域，以及按地域自动发现全部实例。
- 可选 `--auto-start`：已关机且流量低于阈值时自动开机。
- 本地记录每台实例的流量变化（保留 7 天），按 7 天内的累计增量计算平均小时速率；实际用量到阈值就关机，否则在数据未刷新时按预测量到阈值关机。
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
- `lighthouse:DescribeRegions`
- `lighthouse:DescribeInstancesTrafficPackages`
- 实际执行关机时增加 `lighthouse:StopInstances`
- 启用 `--auto-start` 时增加 `lighthouse:StartInstances`

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

多个地域使用 `TENCENTCLOUD_TARGETS`，格式为 `地域=实例 ID[,实例 ID];地域=实例 ID`：

```dotenv
TENCENTCLOUD_TARGETS=ap-seoul=lhins-seoul-1,lhins-seoul-2;ap-guangzhou=lhins-gz-1
```

设置 `TENCENTCLOUD_TARGETS` 后，它会优先于 `TENCENTCLOUD_REGION` 和 `TENCENTCLOUD_INSTANCE_ID`。每个地域使用独立 API 客户端。

### 自动发现地域内所有实例

把实例 ID 写成 `all`，程序会分页列出该地域的全部实例，并只处理存在有效流量包的实例：

```dotenv
TENCENTCLOUD_TARGETS=ap-seoul=all;ap-guangzhou=all
```

单地域也可以直接写：

```dotenv
TENCENTCLOUD_REGION=ap-seoul
TENCENTCLOUD_INSTANCE_ID=all
```

`all` 不能与具体实例 ID 混用。自动发现模式的影响范围是整个地域，建议先不加 `--execute` 观察日志中的实例数量与流量数据。没有流量包的实例会被跳过，不会导致整次运行失败。

### 自动覆盖全部地域

用 `*`（或 `all`）作为地域，可自动枚举所有可用的 Lighthouse 地域，并在每个地域内自动发现实例：

```dotenv
TENCENTCLOUD_TARGETS=*=all
```

也可以写成：

```dotenv
TENCENTCLOUD_REGION=*
TENCENTCLOUD_INSTANCE_ID=all
```

或直接：

```dotenv
TENCENTCLOUD_REGION=all
TENCENTCLOUD_INSTANCE_ID=all
```

`*` / `all` 只能搭配 `all`。如果同时显式配置了某个地域，该地域不会被重复处理。全地域模式会产生较多 API 调用，请先用 dry-run 确认范围与耗时。

确认行为后开启真实关机：

```bash
./tencent-lighthouse-traffic-guard --execute
```

## 流量预测

腾讯云流量包用量大约每小时刷新一次，因此高频轮询往往会拿到同一个值。程序会把这些观测结果保存到本地历史，只记录用量真正发生变化的点，保留最近 7 天，并据此计算平均小时速率：

```text
速率 = 最近一次的用量 - 7 天内最早一次的用量
       ----------------------------------------
                两次变化的时间间隔
```

判断顺序：

1. 实际接口返回的用量达到阈值，直接关机；
2. 否则，若 API 返回的用量已经连续 `PREDICT_STALE_AFTER`（默认 `75m`）没有变化，且窗口内至少有两个变化点，则用 `当前用量 + 速率 × 未变化时长` 作为预测用量；
3. 预测用量达到阈值时同样关机。

预测结果会写进日志：

```text
instance=lhins-xxx history points=42 rate=312.50MB/h trend=91.20%
instance=lhins-xxx traffic data unchanged for 2h0m0s, predicted=1000.00GB (95.80%)
stop request submitted for instance=lhins-xxx
```

相关参数：

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `--predict` | `true` | 关闭后只使用 API 返回值判断 |
| `--stale-after` | `75m` | 用量多久未变化视为数据陈旧 |
| `--state-file` | 与 env 文件同目录 | 历史文件路径 |

对应环境变量为 `PREDICT_ENABLED`、`PREDICT_STALE_AFTER`、`STATE_FILE`。历史固定保留 7 天。

历史文件位于 env 文件所在目录（默认 `lighthouse-traffic-state.json`），以原子写入方式更新，已加入 `.gitignore`。预测只依赖本地历史，建议用同一个文件路径持续运行；多个进程同时写同一文件会互相覆盖。

如果数据长期没有变化，预测时长最多外推 24 小时，避免长时间停机后产生严重偏大的估算。预测只是保守估计，不是腾讯云的官方计费数据。

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
- 自动发现模式（`all`）会覆盖地域内全部实例，请先用 dry-run 确认范围。
- 关机请求是异步提交的，程序记录“request submitted”不代表实例已经完成关机。
- 启用 `--auto-start` 时，已关机且流量低于阈值的实例会被自动启动。
- 程序不会自动购买或续费流量包。
- 请在测试实例上先验证区域、实例 ID、权限和流量统计结果。
