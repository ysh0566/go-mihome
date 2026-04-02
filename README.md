# go-mihome

`go-mihome` provides a root-level `package miot` for reusable Xiaomi MIoT primitives in Go.

The implementation is inspired by [XiaoMi/ha_xiaomi_home](https://github.com/XiaoMi/ha_xiaomi_home), the Xiaomi Home integration for Home Assistant.

The package currently covers:

- typed Xiaomi cloud OAuth, read/write APIs, and scene listing and triggering
- typed MIoT spec parsing with embedded rule assets
- platform-neutral device and entity descriptors
- network reachability and MIoT central-service discovery
- shared MIPS wire handling plus cloud and local MIPS clients
- direct LAN packet codec, request client, and keepalive runtime

## Package layout

- cloud: `NewOAuthClient`, `NewCloudClient`, scene listing and triggering
- spec: `NewSpecParser`
- entity: `NewEntityRegistry`
- network: `NewNetworkMonitor`, `NewMIPSDiscovery`
- mqtt/mips: `NewCloudMIPSClient`, `NewLocalMIPSClient`
- lan: `NewLANClient`, `NewLANDevice`, `BindNetworkMonitor`, `BindMIPSDiscovery`

## Runnable examples

The repository now ships real `main` programs under `example/`. Each example is directly runnable with `go run ./example/<name>` and defaults to read-only behavior.

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

Most examples read configuration from environment variables first and then fall back to small file-local constants. The most common variables are:

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

Example:

```bash
MIOT_ACCESS_TOKEN=... \
MIOT_DEVICE_DID=123456789 \
MIOT_PROPERTY_SIID=2 \
MIOT_PROPERTY_PIID=1 \
go run ./example/cloud_props
```

`cloud_profile`, `cloud_homes`, `cloud_props`, `spec_parse`, `entity_build`, and `oauth_token` are bounded request/response examples. `mdns_discovery` and `mips_cloud` run for a short default timeout or until `Ctrl+C`. `miot_client_runtime` is a long-running coordinator example that stays up until `Ctrl+C`, prints startup/snapshot/shutdown JSON, and bootstraps cloud push, local gateway routes, and LAN together. `lan_control` reads one property over the local MIoT LAN protocol and does not call `SetProp` or `InvokeAction` by default.

`miot_client_runtime` expects both `MIOT_ACCESS_TOKEN` and `MIOT_REFRESH_TOKEN` together on the first run so it can resolve the Xiaomi UID, persist bootstrap state, and store `auth_info` under `MIOT_STORAGE_DIR`. Later runs can omit both token variables only when the cached access token in that storage directory is still valid, because this example does not perform a pre-bootstrap refresh-token exchange. If you want to replace the stored credentials, export both token variables again for that launch.

`oauth_token` defaults to the Home Assistant Xiaomi OAuth app id `2882303761520251711` and redirect URL `https://mico.api.mijia.tech/login_redirect`. It also reuses the runtime bootstrap UUID stored under `MIOT_STORAGE_DIR`, so the token it exchanges is aligned with `miot_client_runtime` cloud push. Run it once without `MIOT_OAUTH_CODE` to print an authorization URL, then run it again with either the raw code or the full redirected URL in `MIOT_OAUTH_CODE` to exchange and print the token JSON.

## License

MIT. See [LICENSE](/Users/ysh0566/code/ysh0566/go-mihome/LICENSE).

## Cloud example

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

## Entity example

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

## MIPS and LAN transport

The cloud and local MIPS clients now ship with a default MQTT transport backed by Eclipse Paho. `NewCloudMIPSClient` builds its own MQTT connection automatically, and `RefreshAccessToken` updates live MQTT credentials when the transport supports hot refresh. `NewLocalMIPSClient` also builds one automatically when you provide either `TLSConfig` or PEM-encoded CA/client certificate material; you can still inject a custom `miot.MQTTConn` when you need different runtime behavior.

The direct LAN client still accepts a custom `miot.LANTransport`; if you do not inject one, it falls back to a small UDP transport that now supports synchronous request/reply calls plus inbound packet listening on the standard MIoT LAN port. The LAN runtime maintains a background probe loop for online/offline tracking, uses adaptive keepalive intervals while devices are healthy, falls back to fast retry probes before declaring a device offline, suppresses rapid online flapping after repeated short-window transitions, consumes inbound packets automatically when the transport supports listening, exposes `VoteForLANControl`, `SubscribeLANState`, and `SetSubscribeOption` for platform-neutral lifecycle control, and reports `Online` / `PushAvailable` / `WildcardSubscribe` in `GetDeviceList`.

The LAN layer includes typed `miIO.sub` / `miIO.unsub` negotiation driven by inbound probe packets, optional multi-interface broadcast scanning through `UpdateInterfaces`, and optional lifecycle bindings through `BindNetworkMonitor` and `BindMIPSDiscovery`. Known devices can be registered without a fixed route and learn their `IP` / `Interface` from broadcast probe replies. Unsolicited uplink packets are deduplicated in a short in-memory window and, when the transport supports it, automatically ACKed with a typed success reply. The current implementation covers typed packet encryption/decryption, request/reply calls, adaptive keepalive, interface-aware broadcast probing, network and central-gateway lifecycle integration, real push-subscription tracking, and unsolicited property/event dispatch.
