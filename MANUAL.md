# agent-nettools 用户手册

---

## 1. 安装

```powershell
cd agent-nettools
go mod tidy
go build -o net-redirect.exe .
```

---

## 2. 命令速查

| 命令 | 作用 |
|------|------|
| `agent-nettools init` | 生成示例 config.yml |
| `agent-nettools start -c config.yml` | 完整模式启动（所有启用的子服务一起跑） |
| `agent-nettools start --proxy ss://...` | 快速模式启动 |
| `agent-nettools status` | 显示当前配置 |
| `agent-nettools ping` | 测试所有代理延迟 |
| `agent-nettools use <分组> <代理>` | 切换手动分组 |
| `agent-nettools sysproxy on/off/status` | 一键开关系统代理 |
| `agent-nettools forward <模式> ...` | SSH 风格端口转发 (-L/-R/-D/-U/tls) |
| `agent-nettools proxy` | 仅启动 HTTP/SOCKS5 代理（独立运行） |
| `agent-nettools dns` | 仅启动本地 DNS 服务器（独立运行） |
| `agent-nettools web` | 仅启动 Web 仪表盘（独立运行） |
| `agent-nettools tun` | 仅启动 TUN 设备（独立运行） |
| `agent-nettools n2n` | 仅启动 n2n 虚拟局域网节点（独立运行） |
| `agent-nettools stunvpv` | 仅启动 STUN/TURN VPN 节点（独立运行） |
| `agent-nettools tui` | 启动 LLM Agent 交互模式（自然语言驱动） |

---

## 2.1 全景速览（分层 × 功能）

工具按它作用的 **TCP/IP 层** 和 **功能分组** 两轴分布，同类命令聚在一起：

```
┌────────────────────────────────────────────────────────────────────────┐
│ L7 应用层     │ MITM HTTPS 拦截 · gen_config · TUI Agent · Web 面板     │
├────────────────────────────────────────────────────────────────────────┤
│ L4 传输层     │ forward(-L/-R/-D/-U/tls) · 代理监听 · 代理链 chain       │
├────────────────────────────────────────────────────────────────────────┤
│ L3 网络层     │ TUN 透明代理 · n2n P2P · STUN/TURN VPN                  │
├────────────────────────────────────────────────────────────────────────┤
│ 代理协议层    │ http · https · socks5 ✦UDP · ss · trojan · vmess         │
│             │ vless+Reality ★uTLS 指纹 · chain                         │
├────────────────────────────────────────────────────────────────────────┤
│ 控制面        │ 规则路由 · 分组(selector/url-test/rr) · 流量统计          │
├────────────────────────────────────────────────────────────────────────┤
│ 运维面        │ sysproxy · ping · status · use · init · scp · DNS · Web  │
└────────────────────────────────────────────────────────────────────────┘
   ★ = uTLS 指纹伪装   ✦ = 支持 UDP (PacketProxy)
```

LLM + Agent 是横贯所有层的能力：一句话驱动上面任意层。完整交互版图表见 `docs/panorama.html`。速记口诀：

> **L7 改应用，L4 转端口，L3 接网卡，协议可换，路由决定走谁，运维看面板。**

详细分类见 `docs/FEATURES.md`。

---

## 3. 配置详解

### 3.1 代理 (proxies)

| 类型 | 必选字段 | 可选字段 |
|------|---------|---------|
| `http` | server, port | username, password |
| `https` | server, port | sni, alpn |
| `socks5` | server, port | username, password |
| `ss` | server, port, cipher, password | — |
| `trojan` | server, port, password | sni, alpn |
| `vmess` | server, port, uuid | alterId, method |
| `vless` | server, port, uuid | sni, alpn, public-key, short-id, fingerprint |
| `forward` | server, port | sni |

### 3.2 可插拔协议一览

所有代理统一实现 `proxy.Proxy`（`Connect/Latency/Close`）；支持 UDP 的额外实现 `proxy.PacketProxy`（`ConnectUDP`）。

| 协议 | 类型字段 | UDP | 特色 |
|------|---------|-----|------|
| HTTP | `http` | — | 明文 CONNECT |
| HTTPS | `https` | — | TLS CONNECT |
| SOCKS5 | `socks5` | ✅ | CONNECT + UDP ASSOCIATE |
| Shadowsocks | `ss` | — | ChaCha20-Poly1305 等 |
| Trojan | `trojan` | — | TLS 伪装 HTTPS |
| VMess | `vmess` | — | UUID + AEAD |
| **VLESS + Reality** | `vless` | — | uTLS 指纹伪装 + X25519 + ShortID |
| Chain（链式） | `chain` | 继承 | 多跳串联 |

### 3.2.1 VLESS + Reality

Reality 让 TLS 握手看起来像真实浏览器（Chrome/Firefox/iOS/Edge/Random 的 ClientHello），绕过 SNI / JA3 指纹封锁：

```yaml
- name: vless-reality
  type: vless
  server: example.com
  port: 443
  uuid: 你的-uuid
  public-key: 服务器curve25519公钥(base64url,43字符)
  short-id: 8位hex
  fingerprint: chrome      # chrome/firefox/ios/edge/random
  sni: example.com
```

`fingerprint` 为空 → 走普通 crypto/tls；非空 → 走 uTLS 指纹路径，配合 `public-key`/`short-id` 进行 X25519 密钥交换认证。

### 3.2.2 代理链式 (Chain)

```yaml
- name: hop
  type: chain
  proxies: [ss-1, trojan-1]   # ss-1 → trojan-1 → 目标
```

`Connect` 逐段拨号：经 p[0] 连到 p[1] 的服务器，……最后连到目标。链本身是一个 Proxy，可被规则 / 转发 / 分组像普通代理一样引用。

### 3.2.3 TUN 透明代理

让任意 App 无感走代理（内核级接管，虚拟网卡读写真实 IP 包）：

```yaml
tun:
  enable: true
  device: "net-redirect"
  mtu: 1500
  gateway: "198.18.0.1"
  cidr: "198.18.0.0/16"
  dns: "198.18.0.2"
```

在 `n2n` 或 `stunvpv` 的 edge/client 模式下把 `tun.enable: true`，TUN 会自动桥接到隧道（tunnel.Peer 接缝）。Windows 需先下载 `wintun.dll` 到程序目录。

### 3.3 代理分组 (proxy-groups)

| 类型 | 行为 |
|------|------|
| `selector` | 手动选择 |
| `url-test` | 自动选最快 |
| `round-robin` | 轮询 |

### 3.4 规则 (rules)

| 类型 | 示例 |
|------|------|
| `DOMAIN` | `DOMAIN,google.com,Auto` |
| `DOMAIN-SUFFIX` | `DOMAIN-SUFFIX,.google.com,Auto` |
| `DOMAIN-KEYWORD` | `DOMAIN-KEYWORD,youtube,Auto` |
| `IP-CIDR` | `IP-CIDR,8.8.8.8/32,DIRECT` |
| `GEOIP` | `GEOIP,CN,DIRECT` |
| `MATCH` | `MATCH,DIRECT` |

---

## 4. 场景实战

### 4.1 劫持 App 流量

```yaml
mode: rule
proxies:
  - name: forward-multica
    type: forward
    server: 192.168.0.77
    port: 8080
rules:
  - DOMAIN,api.multica.ai,forward-multica
  - MATCH,DIRECT
```

### 4.2 全局代理

```yaml
mode: global
proxies:
  - name: ss-1
    type: ss
    server: 1.2.3.4
    port: 8388
    cipher: aes-256-gcm
    password: secret
```

### 4.3 多代理自动选最快

```yaml
mode: global
proxies:
  - name: ss-1
    type: ss
    server: 1.2.3.4
    port: 8388
    cipher: aes-256-gcm
    password: pass1
  - name: ss-2
    type: ss
    server: 5.6.7.8
    port: 8388
    cipher: aes-256-gcm
    password: pass2
proxy-groups:
  - name: Auto
    type: url-test
    proxies: [ss-1, ss-2]
    url: https://www.gstatic.com/generate_204
    interval: 300
rules:
  - MATCH,Auto
```

---

## 5. 快速模式

```powershell
net-redirect start --proxy ss://aes-256-gcm:password@server:8388
net-redirect start --proxy http://user:pass@server:443
net-redirect start --proxy trojan://password@server:443?sni=example.com
net-redirect start --proxy socks5://user:pass@server:1080
```

---

## 6. n2n 虚拟局域网 (P2P VPN)

### 6.1 架构

```
 Supernode (公网服务器)
   ┌─────────────────┐
   │  IP 分配        │
   │  节点发现        │
   │  NAT 打洞协调    │
   └──────┬──────────┘
          │
    ┌─────┴─────┐
    ▼           ▼
 ┌──────┐   ┌──────┐
 │ Edge │◄──│ Edge │  ← P2P 直连 (打洞成功后)
 │ A    │   │ B    │
 └──────┘   └──────┘
```

n2n 模块实现了一个轻量级 P2P 虚拟局域网：
- **Supernode（超级节点）**：运行在公网服务器上，负责节点注册、IP 分配、P2P 打洞协调
- **Edge（边缘节点）**：运行在各客户端上，通过Supernode 发现对端，尝试 NAT 穿透后直接 P2P 通信
- 通信使用 UDP 协议，支持 AES-256-GCM 加密

### 6.2 配置

```yaml
n2n:
  enable: true
  mode: "supernode"          # "supernode" 或 "edge"
  listen: ":7654"            # UDP 监听端口
  supernode: "1.2.3.4:7654"  # Edge 模式下指定 Supernode 地址
  community: "net-redirect"  # 社区名称，同一社区才能互连
  password: "my-secret"      # 加密密码（可选）
  virtual-cidr: "10.200.0.0/16"  # 虚拟网段
  mtu: 1400
  interval: 30               # 心跳间隔（秒）
```

### 6.3 启动 Supernode

在公网服务器上运行：

```yaml
n2n:
  enable: true
  mode: "supernode"
  listen: ":7654"
  community: "my-team"
  password: "team-secret"
  virtual-cidr: "10.200.0.0/16"
```

```powershell
net-redirect start -c config.yml
```

### 6.4 启动 Edge 节点

在客户端上运行：

```yaml
n2n:
  enable: true
  mode: "edge"
  listen: ":7654"
  supernode: "1.2.3.4:7654"   # 替换为 Supernode 的公网 IP
  community: "my-team"
  password: "team-secret"
```

Edge 启动后会自动注册到 Supernode，获取虚拟 IP，并尝试与其他 Edge 节点建立 P2P 直连。如果 NAT 穿透失败，数据会通过 Supernode 中转。

### 6.5 原理

1. **注册**：Edge 向 Supernode 发送 Register 包，Supernode 分配虚拟 IP 并返回已有节点列表
2. **心跳**：Edge 定期发送 Heartbeat 保持连接，Supernode 回复最新的节点列表
3. **P2P 打洞**：当 Edge A 需要与 Edge B 通信时，Supernode 协调双方尝试 UDP 打洞
4. **数据转发**：打洞成功后数据 P2P 直连，失败则通过 Supernode 中转
5. **清理**：Supernode 每 60 秒检查心跳，5 分钟超时的节点自动移除

---

## 7. STUN/TURN 虚拟局域网 (标准协议 VPN)

### 7.1 架构

```
 TURN Server (公网服务器)
   ┌─────────────────────────┐
   │  STUN Binding Request   │ ← NAT 地址发现
   │  TURN Allocate          │ ← 中继资源分配
   │  Virtual IP 分配管理     │
   │  Peer 注册与发现         │
   └──────┬──────────┬───────┘
          │          │
    ┌─────┘          └─────┐
    ▼                      ▼
 ┌──────────┐         ┌──────────┐
 │ Client A │◄───relay──│ Client B │ ← 数据经 TURN 中继
 │ 10.201.0.10│         │ 10.201.0.11│
 └──────────┘         └──────────┘
```

与 n2n 不同，STUN/TURN VPN 使用**标准协议**（RFC 5389/5766/5769），兼容任何标准 STUN/TURN 客户端。核心能力：

- **STUN**：客户端通过 Binding Request 发现自己的公网地址，用于 NAT 穿透判断
- **TURN**：客户端通过 Allocate 请求获取中继地址，所有数据经 TURN 服务器中转
- **控制协议**：在 TURN 数据通道之上，自定义轻量控制协议处理虚拟 IP 注册、节点发现

### 7.2 配置

```yaml
stunvpv:
  enable: true
  mode: "supernode"          # "supernode" 或 "client"
  listen: ":3478"            # STUN/TURN 服务器监听端口
  turn-server: "1.2.3.4:3478"  # Client 模式下 TURN 服务器地址
  realm: "net-redirect"      # TURN 认证域
  username: "vpn-user"       # TURN 认证用户名
  password: "vpn-pass"       # TURN 认证密码
  virtual-cidr: "10.201.0.0/16"  # 虚拟网段
  mtu: 1400
```

### 7.3 启动 Supernode (TURN 服务器)

在公网服务器上运行：

```yaml
stunvpv:
  enable: true
  mode: "supernode"
  listen: ":3478"
  realm: "net-redirect"
  username: "vpn-user"
  password: "vpn-pass"
  virtual-cidr: "10.201.0.0/16"
```

```powershell
net-redirect start -c config.yml
```

TURN 服务器启动后：
- 监听 UDP 3478 端口，响应 STUN Binding Request
- 客户端通过控制协议注册，获取虚拟 IP
- 维护节点列表，广播给所有在线节点
- 中继转发节点间的 IP 数据包

### 7.4 启动 Client 节点

在客户端上运行：

```yaml
stunvpv:
  enable: true
  mode: "client"
  listen: ":3478"
  turn-server: "1.2.3.4:3478"   # 替换为 TURN 服务器的公网 IP
  realm: "net-redirect"
  username: "vpn-user"
  password: "vpn-pass"
```

Client 启动流程：
1. 发送 STUN Binding Request 到服务器，发现公网地址
2. 发送注册请求，服务器分配虚拟 IP（如 `10.201.0.10`）
3. 接收服务器返回的在线节点列表
4. 定期心跳保持连接
5. 需要发送数据时，通过控制协议将 IP 包经 TURN 服务器中继转发

### 7.5 协议细节

**STUN 消息**：服务器标准响应 STUN Binding Request，返回 XOR-MAPPED-ADDRESS。

**控制协议**（自定义，在 UDP 数据包中传输）：

```
Byte 0:   消息类型 (1=Register, 2=RegisterAck, 3=PeerList, 4=Data)
Byte 1:   IP 地址长度 (4 或 16)
Byte 2-5: 虚拟 IP (IPv4)
Byte 6-7: 载荷长度 (uint16 big-endian)
Byte 8+:  载荷数据
```

### 7.6 与 n2n 的对比

| 特性 | n2n | STUN/TURN VPN |
|------|-----|---------------|
| 协议标准 | 自定义二进制 | RFC 5389/5766 (标准) |
| NAT 穿透 | P2P 打洞 | TURN 中继 (总是可靠) |
| 加密 | AES-256-GCM | 可配合 DTLS |
| 兼容性 | 仅本工具 | 任何标准 TURN 客户端 |
| 适用场景 | 低延迟 P2P | 高可靠性中继 |

---

## 7.7 端口转发（SSH 风格，5 种模式）

`forward` 子命令提供 SSH 同款的多模式端口转发，每个模式对应 `forward` 包里的一个函数，扩展新模式只需加一个 case。

```
agent-nettools forward <模式> [参数...] [--proxy <name>]
```

**统一选项**：`--proxy <name>` 让"目标拨号"走配置文件里的某个代理（SS / Trojan / SOCKS5 等）；不指定则直连。UDP 模式下该代理必须支持 UDP（目前仅 SOCKS5 通过 `PacketProxy` 支持）。

### 7.7.1 本地转发 (-L)

```
agent-nettools forward local <listen> <dst>
```

本地监听 → 固定目标。等价于 `ssh -L`。

```
agent-nettools forward local 127.0.0.1:3306 db.internal:3306 --proxy prod-ss
```

应用场景：把远程数据库端口暴露到本机；`--proxy prod-ss` 让出去的那一段经代理链式，常用于从本地访问经代理才能到达的内网服务。

### 7.7.2 远程转发 (-R)

```
agent-nettools forward remote <sshAlias> <remoteListen> <localDst>
```

通过 SSH 隧道在远端主机上开监听，把远端的请求转回本机目标。复用 `scp` 已记住的主机 alias（`scp --alias prod ...`）。

```
agent-nettools forward remote prod :9090 127.0.0.1:8080
```

应用场景：从公网机器回连内网开发机、暴露本地调试服务。

### 7.7.3 动态转发 (-D)

```
agent-nettools forward dynamic <listen>
```

本地 SOCKS5 监听，拨号任意目标。等价于 `ssh -D`。

```
agent-nettools forward dynamic 1080
```

应用场景：作为临时 SOCKS5 代理供应用使用。

### 7.7.4 UDP 转发 (-U)

```
agent-nettools forward udp <listen> <dst> [--proxy <name>]
```

本地 UDP 监听 → 固定 UDP 目标（DNS / QUIC 等）。

```
agent-nettools forward udp 127.0.0.1:1053 1.1.1.1:53 --proxy prod-socks5
```

`--proxy` 指定 SOCKS5 代理 → 走该代理的 UDP ASSOCIATE 出去；不指定则本机直连 UDP。应用场景：把本机 DNS 经代理出去；走 QUIC 的服务做 UDP 隧道。

### 7.7.5 TLS 终止 (tls)

```
agent-nettools forward tls <listen> <dst> [sni]
```

HTTPS 监听 → 明文 HTTP 后端（MITM / 流量观察）。

```
agent-nettools forward tls 0.0.0.0:443 127.0.0.1:80
```

配合 `mitm` 段的自签 CA 可拦截并记录 App 明文流量（详见 4.1 场景）。

---

## 7.8 系统代理一键开关 (sysproxy)

```
agent-nettools sysproxy on          [http://127.0.0.1:7890] [--no-proxy host,host]
agent-nettools sysproxy off
agent-nettools sysproxy status
```

| 平台 | 实际写入 |
|------|---------|
| Windows | 注册表 `HKCU\...\Internet Settings` (ProxyEnable/ProxyServer) + `netsh winhttp` |
| Linux | `gsettings org.gnome.system.proxy` + 生成 `~/.proxy.env` 供 `source` |

`on` 不带地址时默认取 `config.yml` 的 HTTP 监听端口。`--no-proxy` 指定代理排除主机（如 `localhost,127.0.0.1`）。

应用场景：一键让本机所有 App 走 agent-nettools 的代理（浏览器也走）；配合 `forward dynamic` 或 `start` 使用。

---

## 7.9 TUN 透明代理 + tunnel.Peer 接缝

让"任意 App 无感走代理"的根：在内核开一个虚拟网卡，读写真实 IP 包。

### 7.9.1 分层

```
App ──► 内核 TUN (wintun / /dev/net/tun)
          │  Read: parsePacket → 拿到 dstIP
          ▼
     tunnel.Peer 接缝
   ┌───────┴────────┐
   ▼                ▼
 n2n.Edge       stunvpv.Client    (未来: WireGuard …)
   │                │
   ▼                ▼
P2P / TURN 中继 → 对端 TUN → 对端 App
```

`tunnel.Peer` 是一个最小接缝接口（`OnData`/`SendTo`/`VirtualIP`），位于 `tun/peer.go`。它让 TUN 不依赖任何具体隧道包——**新增 WireGuard 等只需实现 Peer**。这是"设计好架构、方便后续添加功能"的关键点。

### 7.9.2 自动桥接

在 `n2n` / `stunvpv` 的 edge/client 模式下，把 `tun.enable: true`，启动时会自动：

1. 创建 TUN 设备 + 设置 MTU + 添加路由（把 overlay CIDR 指向虚拟网关）
2. `TunDevice.SetPeer(edge|client)`：把隧道接上接缝
3. `edge.OnData(func(src, data){ tun.WritePacket(data) })`：对端包 → 写回 TUN
4. TUN 读循环：收到真实 IP 包 → 解析 dstIP → `peer.SendTo(dstIP, data)` 发往对端

```yaml
# 示例：两台不同 NAT 的机器互访内网
tun:
  enable: true
n2n:
  enable: true
  mode: "edge"
  supernode: "1.2.3.4:7654"
  community: "my-team"
```

启动后 `ping <对端虚拟IP>` 即通。可用 `--no-tun` 关闭桥接，单独跑 relay。Windows 需先放置 `wintun.dll`。

---

## 7.10 流量统计

`listener` 的 relay 路径自动生效：`Listener.Options.Stats` 接一个 `web.StatsTracker`，每个连接在 relay 时通过 `statsConn` 双向计数（读=下载，写=上传），同时跟踪活跃连接数。业务代码无需改动。

统计可通过 Web 面板 / REST 端点读取（`/api/stats`）。

```
┌──────────────┐       ┌──────────────┐
│   client     │◄─────►│   remote     │
│    (statsConn)       (statsConn)    │
└──────┬───────┘       └──────┬───────┘
       │                       │
       └───────── StatsTracker ─┘
                  │
               web /api/stats
```

---

## 7.11 LLM Agent 的 gen_config 工具

Agent 内可用的 `gen_config` 工具允许用户用自然语言描述想要的配置，由模型把描述转成结构化 `spec`，工具负责拼装 + 校验 + 落盘——**不是手写 YAML**。

| 工具 | 区别 |
|------|------|
| `get_config` | 读取当前完整配置 |
| `update_config` | 用完整新 YAML 覆写（需要完整 YAML） |
| `gen_config` | 从结构化 spec 生成一份完整可用配置 |

示例对话：

```
你> 给我配一个 8080 端口的 ss 代理，自动选最快，google 走它
  ⚙️ 调用工具 gen_config(spec={
       "listen":{"http":8080},
       "mode":"rule",
       "proxies":[{"name":"ss1","type":"ss","server":"a.com","port":8388,
                    "cipher":"aes-256-gcm","password":"pw"}],
       "groups":[{"name":"Auto","type":"url-test","proxies":["ss1"],
                  "url":"http://www.gstatic.com/generate_204","interval":300}],
       "rules":["GEOIP,CN,DIRECT","DOMAIN-SUFFIX,.google.com,Auto","MATCH,Auto"]
     }, path="config.yml")
     ↳ 配置已写入 config.yml（校验通过）
AI> 已生成，可 `start -c config.yml` 启动。
```

---

## 7.12 VLESS + Reality 实战场景

VLESS+Reality 让代理流量伪装成普通 HTTPS 访问真实网站（Chrome/Firefox/Edge/iOS 的 ClientHello），绕过 SNI / JA3 指纹封锁。

```yaml
proxies:
  - name: vless-r
    type: vless
    server: example.com
    port: 443
    uuid: 你的-uuid
    public-key: <base64url, 43字符>
    short-id: 12345678
    fingerprint: chrome
    sni: example.com
rules:
  - MATCH, vless-r
```

`fingerprint` 取值：`chrome` / `firefox` / `ios` / `edge` / `random`。配合 `--proxy vless-r` 用于 `forward` 让出站流量走 Reality。

---

## 7.13 全景图

`docs/panorama.html` 是一个交互版的工具全景图（archify 生成，暗色/亮色主题可切换），纵轴是 TCP/IP 层，横轴是功能分组，同类命令聚在一起，LLM+Agent 用独立的 tall 列横贯所有层。双击可打开查看。配套的静态分层文档在 `docs/FEATURES.md`。

---

## 8. LLM Agent + TUI（自然语言驱动）

复杂命令、配置记不住？`tui` 子命令启动一个 LLM Agent 交互模式，直接用自然语言描述需求，Agent 自动调用底层工具完成。

### 8.1 启动

```powershell
go run main.go tui
# 或编译后：agent-nettools tui
```

### 8.2 配置

在 `config.yml` 的 `agent:` 段配置 OpenAI 兼容端点：

```yaml
agent:
  enable: false
  base-url: "https://api.openai.com/v1"   # 任意 OpenAI 兼容端点
  api-key: ""                             # API key（或 export AGENT_API_KEY）
  model: "gpt-4o-mini"                    # 如 gpt-4o-mini / glm-5.2 / deepseek-v4-flash
```

也支持 `export AGENT_API_KEY=xxx` 环境变量注入 key。

### 8.3 示例

```
你> 有哪些命令可以用？
  ⚙️ 调用工具 list_commands()
AI> init / start / status / ping / use / forward / tui ...

你> 测一下所有代理延迟
  ⚙️ 调用工具 ping_proxies()
     ↳ ss-1   152ms  trojan-1  208ms
AI> ...

你> 把 google 走 ss-1
  ⚙️ 调用工具 switch_group(group=Auto, proxy=ss-1)
AI> 已切换。

你> 给我配一个 8080 端口的 ss 代理，自动选最快
  ⚙️ 调用工具 gen_config(spec={...})
     ↳ 配置已写入 config.yml

你> exit
```

### 8.4 可用工具

| 工具 | 作用 |
|------|------|
| `get_config` | 读取当前完整配置 (YAML) |
| `update_config` | 用完整新 YAML 覆盖配置（写入前自动校验） |
| `gen_config` | 从结构化 spec 生成一份完整可用配置（不是手写 YAML） |
| `ping_proxies` | 测试所有代理延迟 |
| `switch_group` | 切换 selector 分组到指定代理 |
| `add_rule` | 在规则列表开头插入一条路由规则 |
| `service` | 启动/停止单个子服务：`proxy`/`dns`/`web`/`tun`/`n2n`/`stunvpv`，或 `status` 查看运行状态 |
| `list_commands` | 列出所有 CLI 子命令 |

Agent 最多自动调用 8 轮工具；`exit`/`quit`/`q` 或 Ctrl-D 退出。

### 8.5 service 工具示例

```
你> 启动 dns 服务
  ⚙️ 调用工具 service(action=start, name=dns)
     ↳ dns 已启动 (pid=12345，配置 config.yml)

你> 现在开了哪些服务？
  ⚙️ 调用工具 service(action=status)
     ↳ 运行中的子服务 (1):
         dns       pid=12345

你> 把 dns 停了
  ⚙️ 调用工具 service(action=stop, name=dns)
     ↳ dns 已停止 (pid=12345)
```

每个子服务都是独立进程（与独立子命令 `agent-nettools dns` 等同源），互不影响；`start` 模式下重复启动会提示已在运行。

---

## 9. 独立运行模式（非 TUI）

每个网络子服务都能脱离整体单独跑，方便排障或只起需要的那一块。命令与 `tui` 里 `service` 工具拉起的进程同源：

```powershell
# 仅代理监听
agent-nettools proxy -c config.yml

# 仅 DNS
agent-nettools dns -c config.yml

# 仅 Web 仪表盘
agent-nettools web -c config.yml

# 仅 TUN 设备（需管理员权限）
agent-nettools tun -c config.yml

# 仅 n2n 节点
agent-nettools n2n -c config.yml

# 仅 STUN/TURN VPN 节点
agent-nettools stunvpv -c config.yml
```

每个命令都从同一个 `config.yml` 读取自己那段的配置（端口/地址/模式），在前台运行，`Ctrl-C` 优雅退出。

如果只想一键全开，用 `agent-nettools start -c config.yml` 即可——它会把所有 `enable: true` 的子服务一起拉起。

---

## 10. 常见问题

- **端口占用**：`taskkill //F //FI "PID eq <pid>"`
- **规则不生效**：从上到下顺序匹配，第一条命中生效
- **App 不走代理**：开 `tun.enable: true` + 启动 n2n/stunvpv，TUN 会接管；或 `sysproxy on`
- **tui 报 no api-key**：在 `config.yml` 的 `agent.api-key` 填 key，或 `export AGENT_API_KEY`
- **Windows TUN 启动失败**：下载 `wintun.dll` 放到程序目录
- **UDP 转发要经过代理**：用 `forward udp <listen> <dst> --proxy <socks5代理名>`，SOCKS5 的 UDP ASSOCIATE 才会接管
