# camera 模块

`camera` 子包提供一套可复用的小米摄像头能力，覆盖以下几层：

- 支持型号识别与设备归一化
- 摄像头目录整理与目标选择
- 高层 `Library` facade，用于统一初始化、token 更新和 camera instance 生命周期管理
- 会话解析与流入口抽象
- RTP / HTTP Annex-B / Direct Annex-B 码流读取
- 基于 FFmpeg 的 JPEG 预览解码
- 运行时状态回调与帧分发
- 快照与 MJPEG 的 HTTP 暴露
- 基于本地米家会话的原生摄像头预览桥接

如果你想先看一条完整链路，建议从 [example/camera_preview](../example/camera_preview) 开始。

## 模块能做什么

`camera` 不是一个“只给 RTSP URL 就完事”的薄封装，而是把摄像头链路拆成了几层可替换组件：

1. 先从设备快照里筛出“这是一个支持的摄像头”
2. 再为该型号选择合适的解析 profile
3. 然后拿到一个 `Session`
4. 再把 `Session` 变成连续的 `AccessUnit`
5. 最后把码流交给运行时、JPEG 解码器，或者导出到 HTTP 预览服务

这样做的好处是：你可以只替换其中一层，而不必重写整条链路。

## 获取摄像头流的推荐流程

如果你需要一层更接近 camera-lite dylib 的高层入口，而不是手工拼装 `RuntimeOptions`，可以直接使用 `NewLibrary`。它会负责：

- 管理共享 access token
- 按需创建并复用 camera instance
- 默认按 `本地 Mi Home 会话 -> micoapi token backend` 做 session fallback
- 在 `Close()` 时统一停止已创建的 camera

### 流程总览

1. 用 `Catalog` 从 `miot.DeviceSnapshot` 中筛出支持的摄像头，并拿到 `Target`
2. 用 `MatchProfile(target.Model)` 选出型号对应的 `Profile`
3. 选择一种会话来源，得到 `Session`
4. 用 `ProbeStreamClient` 把 `Session` 变成 `AccessUnit`
5. 如需 JPEG 预览，用 `FFmpegFrameDecoder` 把 `AccessUnit` 解成 `JPEGFrame`
6. 如需统一生命周期管理，用 `Runtime` / `Instance` 注册回调并启动
7. 如需对外暴露快照或 MJPEG，用 `FrameStore` + `HTTPServer`

### 会话来源有哪几种

- 静态会话：你已经有 RTSP / HTTP 流地址，可以自己实现一个很小的 `Resolver`
- HTTP 解析器：你有一个外部会话服务，使用 `HTTPResolver`
- 进程内直连解析：你能直接访问本地米家会话，使用 `NewMiHomeCameraSessionResolverBackend` + `DirectResolver`
- 本地 resolver 服务：你希望把“解析会话”和“消费会话”拆成两个进程，使用 `CameraSessionResolverApp` 或 `SessionResolverHTTPServer`

### 最常见的接入链路

#### 方式一：程序内直接获取流并解码预览

这条链路最接近 `example/camera_preview` 的实际做法：

```go
backend := camera.NewMiHomeCameraSessionResolverBackend(nil)

runtime := camera.NewRuntime(camera.RuntimeOptions{
	Factory: camera.NewProbeDriverFactory(camera.ProbeDriverFactoryOptions{
		Resolvers: []camera.Resolver{
			camera.NewDirectResolver(camera.DirectResolverOptions{
				Name:    "mihome-local",
				Backend: backend,
			}),
		},
		Streamer: camera.NewProbeStreamClient(camera.ProbeStreamClientOptions{
			Backend: backend,
		}),
		Decoder: camera.NewFFmpegFrameDecoder(""),
	}),
})

instance, err := runtime.Create(camera.Info{
	CameraID:     target.CameraID,
	Model:        target.Model,
	ChannelCount: 1,
})
if err != nil {
	return err
}

frameStore := camera.NewFrameStore()
instance.RegisterJPEG(0, func(_ string, frame camera.JPEGFrame) {
	frameStore.Store(frame)
})

if err := instance.Start(ctx, camera.StartOptions{PinCode: pinCode}); err != nil {
	return err
}

handler := camera.NewHTTPServer(camera.HTTPServerOptions{
	FrameStore: frameStore,
}).Handler()
_ = handler
```

这条链路里各层的职责是：

- `DirectResolver` 负责拿到会话元数据
- `ProbeStreamClient` 负责把会话变成连续 Annex-B 访问单元
- `FFmpegFrameDecoder` 负责把访问单元解成 JPEG
- `Runtime` / `Instance` 负责统一启动、停止和回调分发
- `FrameStore` / `HTTPServer` 负责把最新画面暴露给外部

#### 方式二：先启动本地 resolver 服务，再在消费端通过 HTTP 获取流

如果你想把“解析摄像头会话”单独做成一个进程，可以先启动：

```go
app := camera.NewCameraSessionResolverApp(camera.CameraSessionResolverAppOptions{})
if err := app.Serve(ctx, "127.0.0.1:18082"); err != nil {
	return err
}
```

随后在消费端使用：

- `MIOT_CAMERA_SESSION_RESOLVER_URL=http://127.0.0.1:18082/resolve`
- 或者在代码里手工构造 `HTTPResolver`

这样消费端不必直接持有底层米家媒体连接逻辑，只要能访问 resolver HTTP 服务即可。

#### 方式三：你已经有静态 RTSP 地址

如果上层已经给出 RTSP 地址、协议和 codec，那么不一定需要米家 backend。只要实现一个返回固定 `Session` 的 `Resolver` 即可。`example/camera_preview` 里的 `staticResolver` 就是最小例子。

## 运行要求

### FFmpeg

JPEG 预览链路依赖 FFmpeg：

- 默认从 `PATH` 中寻找 `ffmpeg`
- 也可以通过 `FFMPEG_PATH` 指定绝对路径

如果没有 FFmpeg，`FFmpegFrameDecoder` 和基于它的预览链路无法工作。

### 本地米家会话

如果你使用 `NewMiHomeCameraSessionResolverBackend` 或 `NewCameraSessionResolverApp` 的默认 backend，需要本地可用的米家登录态。当前加载顺序是：

1. 先读环境变量
2. 环境变量不完整时，再尝试读取本机米家 plist

相关环境变量包括：

- `MIBOT_MIHOME_USER_ID`
- `MIBOT_MIHOME_CUSER_ID`
- `MIBOT_MIHOME_PASS_TOKEN`
- `MIBOT_MIHOME_SERVICE_TOKEN`
- `MIBOT_MIHOME_SSECURITY`
- `MIBOT_MIHOME_DEVICE_ID`
- `MIBOT_MIHOME_REGION`
- `MIBOT_MIHOME_SID`
- `MIBOT_MIHOME_PLIST_PATH`

其中一部分组合满足即可：

- `user_id + service_token + ssecurity + device_id`
- 或 `user_id + pass_token + device_id`

## 结构体说明

下面只覆盖 `camera` 包的公开结构体。`compat.go` 里的兼容别名不再重复展开。

### 1. 设备识别与目录层

| 结构体 | 作用 |
| --- | --- |
| `SupportInfo` | 某个摄像头型号的支持信息，主要补充通道数、展示名称和厂商。来源于内嵌的 `metadata.yaml`。 |
| `Target` | 归一化后的摄像头目标对象。它把设备 DID、型号、房间、在线状态等信息整理成解析器和运行时都能直接消费的格式。 |
| `Profile` | 型号匹配结果。不同型号族会映射到不同 profile，供 resolver/backend 选择不同处理路径。 |
| `Catalog` | 把根包的 `miot.DeviceSnapshot` 转成 `[]Target` 的入口。适合做“列出支持摄像头”“按 DID 选摄像头”。 |

### 2. 会话解析层

| 结构体 | 作用 |
| --- | --- |
| `Session` | 一次成功解析后的摄像头会话。包含 `transport`、`stream_url`、`codec`、`session_id`、`token` 等关键信息，是后续拉流的统一入口。 |
| `ResolverChain` | 把多个 `Resolver` 串成一个 fallback 链。前一个失败时自动尝试下一个。 |
| `HTTPResolverOptions` | `HTTPResolver` 的配置对象。你需要提供请求构造逻辑和响应解析逻辑。 |
| `HTTPResolver` | 通过 HTTP 请求获取摄像头会话的通用 resolver。适合接对外 session 服务。 |
| `SessionResolverProbe` | backend 的“预探测结果”，先告诉上层 camera/model/codec。 |
| `SessionResolverStreamRequest` | backend 打开实际流时的请求参数，包含 camera/model/profile/codec。 |
| `DirectResolverOptions` | `DirectResolver` 的配置，核心是注入 `SessionResolverBackend`。 |
| `DirectResolver` | 不经过 RTSP，而是直接基于 backend 产出 `direct-annexb` 会话。适合本地直连媒体 backend。 |
| `SessionResolverHTTPServerOptions` | `SessionResolverHTTPServer` 的配置，包括 backend 和 session TTL。 |
| `SessionResolverHTTPServer` | 把 backend 包装成 HTTP 服务，提供 `/resolve` 和 `/stream/...` 两类端点。 |
| `CameraSessionResolverAppOptions` | 本地 resolver app 的启动参数，支持自定义输出、backend、TTL 和 logger。 |
| `CameraSessionResolverApp` | 一个更高层的本地 resolver 应用封装，默认会在运行时可用时接入 Mi Home backend。 |

### 3. 拉流与解码层

| 结构体 | 作用 |
| --- | --- |
| `AccessUnit` | 一个已经整理好的媒体访问单元，通常是 Annex-B 格式的视频 payload。它是流读取层和解码层之间的通用数据单位。 |
| `ProbeStreamClientOptions` | `ProbeStreamClient` 的配置。可以注入自定义 RTP 包源，或注入 direct backend。 |
| `ProbeStreamClient` | 把 `Session` 变成连续 `AccessUnit` 的默认实现。支持 RTSP、HTTP Annex-B、Direct Annex-B 三种入口。 |
| `ProbeDecoderConfig` | 解码器启动参数，目前核心是声明输入 codec。 |
| `FFmpegFrameDecoder` | 基于 FFmpeg 的 `ProbeFrameDecoder` 实现，把 `AccessUnit` 解成 `JPEGFrame`。 |

### 4. 运行时与回调层

| 结构体 | 作用 |
| --- | --- |
| `Info` | 创建摄像头实例时使用的基础配置，包括 camera id、model、通道数和是否要求 pin code。 |
| `StartOptions` | 运行时启动参数，包括清晰度、PinCode、音频开关和重连开关。 |
| `RuntimeOptions` | `Runtime` 的全局配置。最关键的是 `Factory`，它决定实例最终如何启动底层 driver。 |
| `Frame` | 运行时分发的原始音视频帧。适合需要直接处理 H264/H265/音频原始数据的调用方。 |
| `DecodedFrame` | 运行时分发的已解码数据帧载体，目前用于统一承载 JPEG/PCM 等解码结果。 |
| `Runtime` | 摄像头运行时容器，负责创建、缓存、查询和销毁多个 `Instance`。 |
| `Instance` | 单个摄像头的运行实例。对外提供 `Start` / `Stop`，并允许注册状态、原始帧、JPEG、PCM 等回调。 |
| `JPEGFrame` | 解码后的 JPEG 快照，包含通道、尺寸、采集时间和 JPEG payload。 |

### 5. HTTP 预览层

| 结构体 | 作用 |
| --- | --- |
| `FrameStore` | 缓存最新一帧 `JPEGFrame`，并在有新帧时唤醒等待者。它是预览 HTTP 服务和运行时回调之间的桥梁。 |
| `HTTPServerOptions` | `HTTPServer` 的配置，主要是 `FrameStore` 和 multipart boundary。 |
| `HTTPServer` | 基于 `FrameStore` 提供 `/healthz`、`/snapshot.jpg`、`/stream.mjpeg` 三个端点。 |

### 6. 原生快照桥接层

| 结构体 | 作用 |
| --- | --- |
| `CameraStreamDescriptor` | `CameraStreamManager` 管理某个摄像头 worker 时使用的描述对象，携带 camera/model/region/access token/channel count 等上下文。 |
| `CameraStreamManagerOptions` | `CameraStreamManager` 的配置，包括 FFmpeg 路径、logger 和自定义 pipeline factory。 |
| `CameraStreamManager` | 每个摄像头复用一条 JPEG 生产管线，按需返回缓存快照；适合“我只想拿最新一张图”的场景。 |

## 相关枚举与接口

除了上面的结构体，使用时最常见的还有这些类型：

- 枚举：`VideoQuality`、`Status`、`Codec`、`FrameType`
- 目录接口：`Loader`
- 会话接口：`Resolver`
- backend 接口：`SessionResolverBackend`、`SessionResolverStream`
- 流接口：`ProbeStreamer`、`ProbeStreamSession`
- 解码接口：`ProbeFrameDecoder`、`ProbeDecoderSession`
- 运行时接口：`DriverFactory`、`Driver`、`EventSink`

如果你只替换实现层而不想改整体调用链，通常就是围绕这些接口注入自己的实现。

## 与 example/camera_preview 的关系

`example/camera_preview` 展示的是模块最完整也最容易复用的一条链路：

1. 通过云端设备列表或显式配置拿到 `Target`
2. 组装 resolver
3. 构造 `Runtime`
4. 注册 JPEG 回调，把图像存进 `FrameStore`
5. 通过 `HTTPServer` 对外输出

示例启动后会提供：

- `GET /healthz`
- `GET /snapshot.jpg`
- `GET /stream.mjpeg`

如果你只是想验证链路是否通了，优先跑这个 example。

## 补充说明

- 当前 `example/camera_preview` 只支持 `channel=0`，但运行时 API 本身保留了多通道结构。
- `CameraStreamManager` 更偏“快照桥接”；如果你需要完整的原始视频回调，优先走 `Runtime + ProbeDriverFactory`。
- `Session` 只是会话描述，不是正在运行的连接；真正的拉流发生在 `ProbeStreamClient.Start` 或 runtime driver 启动之后。
