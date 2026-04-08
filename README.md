# go-mihome

`go-mihome` 在根级 `package miot` 上提供可复用的小米 MIoT 原语，并额外提供可选的 [`camera`](camera/README.md) 子包，用于摄像头识别、会话解析、预览与快照。

实现思路参考 [XiaoMi/ha_xiaomi_home](https://github.com/XiaoMi/ha_xiaomi_home)，也就是 Home Assistant 的 Xiaomi Home 集成。

当前仓库主要覆盖以下能力：

- 类型化的小米云 OAuth、读写 API、场景列表与触发
- 类型化的 MIoT spec 解析，并内置规则资源
- 平台无关的设备与实体描述
- 网络可达性与 MIoT 中控服务发现
- 共享的 MIPS 协议处理，以及云端 / 本地 MIPS 客户端
- 直接 LAN 报文编解码、请求客户端与 keepalive 运行时
- 可选的 `camera` 子包：支持型号识别、摄像头目录、会话解析、FFmpeg/JPEG 预览链路与 HTTP 快照服务

## 包结构

- `cloud`：`NewOAuthClient`、`NewCloudClient`、场景查询与触发
- `spec`：`NewSpecParser`
- `entity`：`NewEntityRegistry`
- `network`：`NewNetworkMonitor`、`NewMIPSDiscovery`
- `mqtt` / `mips`：`NewCloudMIPSClient`、`NewLocalMIPSClient`
- `lan`：`NewLANClient`、`NewLANDevice`、`BindNetworkMonitor`、`BindMIPSDiscovery`
- `camera`：支持型号识别、设备筛选、流会话解析、预览运行时与 MJPEG/快照服务，详见 [`camera/README.md`](camera/README.md)

## 可运行示例

仓库在 `example/` 下提供了可直接运行的 `main` 程序。大多数示例都可以直接通过 `go run ./example/<name>` 启动，默认尽量保持只读。

- `go run ./example/cloud_profile`
- `go run ./example/cloud_homes`
- `go run ./example/cloud_props`
- `go run ./example/spec_parse`
- `go run ./example/entity_build`
- `go run ./example/mdns_discovery`
- `go run ./example/mips_cloud`
- `go run ./example/miot_client_runtime`
- `go run ./example/oauth_token`
- `go run ./example/lan_control`
- `go run ./example/camera_preview`

大部分示例都会优先从环境变量读取配置，其次才回落到示例里的默认常量。最常见的环境变量包括：

- `MIOT_CLIENT_ID`
- `MIOT_CLOUD_SERVER`
- `MIOT_ACCESS_TOKEN`
- `MIOT_STORAGE_DIR`
- `MIOT_SPEC_URN`
- `MIOT_DEVICE_DID`
- `MIOT_PROPERTY_SIID`
- `MIOT_PROPERTY_PIID`
- `MIOT_LAN_DID`
- `MIOT_LAN_TOKEN`
- `MIOT_LAN_IP`
- `MIOT_LAN_IFACE`
- `MIOT_MIPS_GROUP_ID`
- `MIOT_REFRESH_TOKEN`
- `MIOT_RUNTIME_SNAPSHOT_INTERVAL`
- `MIOT_MDNS_BOOTSTRAP_TIMEOUT`
- `MIOT_OAUTH_REDIRECT_URL`
- `MIOT_OAUTH_UUID`
- `MIOT_OAUTH_CODE`

查询云端属性的最小示例：

```bash
MIOT_ACCESS_TOKEN=... \
MIOT_DEVICE_DID=123456789 \
MIOT_PROPERTY_SIID=2 \
MIOT_PROPERTY_PIID=1 \
go run ./example/cloud_props
```

`cloud_profile`、`cloud_homes`、`cloud_props`、`spec_parse`、`entity_build` 和 `oauth_token` 都是有限时的请求/响应型示例。`mdns_discovery` 与 `mips_cloud` 会运行一小段默认时间，或持续到你按下 `Ctrl+C`。`miot_client_runtime` 是长期运行的协调器示例，会持续运行到 `Ctrl+C`，并输出启动 / 快照 / 关闭 JSON，同时拉起云端推送、本地网关路由和 LAN。`lan_control` 会通过本地 MIoT LAN 协议读取一个属性，默认不会调用 `SetProp` 或 `InvokeAction`。

`miot_client_runtime` 第一次运行时需要同时提供 `MIOT_ACCESS_TOKEN` 和 `MIOT_REFRESH_TOKEN`，这样它才能解析 Xiaomi UID、持久化 bootstrap 状态，并把 `auth_info` 存到 `MIOT_STORAGE_DIR`。后续运行只有在该目录下缓存的 access token 仍然有效时，才可以省略这两个变量；因为这个示例在 bootstrap 之前不会主动执行 refresh-token 交换。如果你要替换已缓存的凭证，重新导出这两个变量再运行一次即可。

`oauth_token` 默认使用 Home Assistant Xiaomi OAuth app id `2882303761520251711`，重定向地址默认是 `https://mico.api.mijia.tech/login_redirect`。它还会复用 `MIOT_STORAGE_DIR` 里的 runtime bootstrap UUID，这样换出来的 token 能和 `miot_client_runtime` 的云端推送对齐。第一次运行时不要设置 `MIOT_OAUTH_CODE`，程序会先打印授权 URL；拿到回调后，再把原始 code 或完整重定向 URL 填进 `MIOT_OAUTH_CODE` 重新运行，即可输出 token JSON。

## 摄像头示例

`example/camera_preview` 展示了 `camera` 子包最完整的一条预览链路：

1. 解析目标摄像头
2. 通过 resolver 拿到会话
3. 读取视频流并解码 JPEG
4. 通过 HTTP 输出 `/healthz`、`/snapshot.jpg`、`/stream.mjpeg`

常见环境变量包括：

- `MIOT_CAMERA_ID`
- `MIOT_CAMERA_MODEL`
- `MIOT_CAMERA_PINCODE`
- `MIOT_CAMERA_SESSION_RESOLVER_URL`
- `MIOT_CAMERA_STREAM_URL`
- `MIOT_CAMERA_TRANSPORT`
- `MIOT_CAMERA_CODEC`
- `MIOT_CAMERA_LISTEN_ADDR`

如果你已经有静态 RTSP 地址，可以这样跑：

```bash
MIOT_CAMERA_ID=123456789 \
MIOT_CAMERA_MODEL=xiaomi.camera.v1 \
MIOT_CAMERA_STREAM_URL=rtsp://127.0.0.1:8554/live \
MIOT_CAMERA_TRANSPORT=rtsp \
MIOT_CAMERA_CODEC=h264 \
go run ./example/camera_preview
```

如果你有一个单独的 session resolver 服务，则只需要把：

```bash
MIOT_CAMERA_SESSION_RESOLVER_URL=http://127.0.0.1:18082/resolve
```

指向该服务即可。更完整的结构说明和链路分解见 [`camera/README.md`](camera/README.md)。

## 云端调用示例

```go
client, err := miot.NewCloudClient(
	miot.CloudConfig{
		ClientID:    "2882303761520251711",
		CloudServer: "cn",
	},
	miot.WithCloudTokenProvider(myTokenProvider),
)
if err != nil {
	return err
}

homes, err := client.GetHomeInfos(ctx)
if err != nil {
	return err
}
_ = homes

scenes, err := client.GetScenes(ctx, nil)
if err != nil {
	return err
}
_ = scenes
```

## 实体构建示例

```go
registry := miot.NewEntityRegistry(myBackend)
device, err := registry.Build(deviceInfo, specInstance)
if err != nil {
	return err
}

if device.Descriptor.Category == "climate" {
	fmt.Println(device.Descriptor.SemanticType)
}

entity := device.EntityByKey("p:2:1")
if entity != nil {
	result, err := entity.Get(ctx)
	if err != nil {
		return err
	}
	_ = result
}
```

## MIPS 与 LAN 传输

云端和本地 MIPS 客户端现在都内置了基于 Eclipse Paho 的默认 MQTT 传输。`NewCloudMIPSClient` 会自动建立自己的 MQTT 连接，并且在底层传输支持热更新时，`RefreshAccessToken` 会同步刷新在线 MQTT 凭证。`NewLocalMIPSClient` 也会在你提供 `TLSConfig` 或 PEM 编码的 CA / 客户端证书材料时自动构建连接；如果你需要不同的运行时行为，仍然可以注入自定义 `miot.MQTTConn`。

直接 LAN 客户端仍然支持自定义 `miot.LANTransport`；如果没有注入，它会退回到一个小型 UDP 传输实现。这个默认实现现在支持同步 request/reply、监听标准 MIoT LAN 端口上的入站报文，并在运行时维护后台探测循环做在线 / 离线跟踪：设备健康时使用自适应 keepalive；在宣告离线前使用更快的重试探测；在短时间反复抖动时抑制过快的在线切换；底层传输支持监听时自动消费入站报文；同时暴露 `VoteForLANControl`、`SubscribeLANState`、`SetSubscribeOption` 以实现平台无关的生命周期控制，并在 `GetDeviceList` 里报告 `Online` / `PushAvailable` / `WildcardSubscribe`。

LAN 层还包含类型化的 `miIO.sub` / `miIO.unsub` 协商，它由入站探测包驱动；支持通过 `UpdateInterfaces` 做多网卡广播扫描；并且可以通过 `BindNetworkMonitor` 与 `BindMIPSDiscovery` 绑定外部生命周期。已知设备即使没有固定路由，也可以从广播探测响应中学习自己的 `IP` / `Interface`。对于主动上行的非请求报文，运行时会在一个短时内存窗口里去重；当底层传输支持时，还会自动回 ACK 成功响应。当前实现已经覆盖了：类型化报文加解密、request/reply、自适应 keepalive、感知网卡的广播探测、网络与中控网关生命周期集成、真实推送订阅跟踪，以及非请求属性 / 事件分发。

## 许可证

MIT。详见 [LICENSE](LICENSE)。
