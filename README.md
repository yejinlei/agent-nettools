# agent-nettools — 一个轻量的网络代理客户端

## 一句话

agent-nettools 是网络代理领域的 `/etc/hosts`：一条命令，把任意 App 的网络流量重定向到你想要的目标。

支持 6 种远程代理协议、3 种代理分组、7 种规则类型，外加 forward 劫持、n2n P2P 虚拟局域网、STUN/TURN 标准协议 VPN、TUN 透明代理、MITM HTTPS 拦截、Web Dashboard，以及一个用自然语言驱动全部能力的 LLM Agent TUI。

## 架构

![net-redirect 架构](docs/architecture.svg)

```
                    ┌─────────────────────────────────────┐
                    │           net-redirect              │
                    │                                     │
  ┌──────────┐     │  ┌─────────┐  ┌──────────────────┐  │
  │  App     │────▶│  │ HTTP    │  │  Router (rules)   │  │
  │  系统代理 │     │  │ SOCKS5  │  │  DOMAIN/SUFFIX/  │  │
  │  TUN     │     │  │ LISTEN  │  │  CIDR/GEOIP/MATCH│  │
  └──────────┘     │  └─────────┘  └────────┬─────────┘  │
                    │                       │              │
                    │         ┌─────────────┼──────────┐   │
                    │         ▼             ▼          ▼   │
                    │   ┌──────────┐ ┌─────────┐ ┌──────┐ │
                    │   │  remote  │ │ group   │ │forw.│ │
                    │   │ proxy    │ │ selector│ │      │ │
                    │   │ HTTP/SS/ │ │ urltest │ │      │ │
                    │   │ Trojan/  │ │ rr      │ │      │ │
                    │   │ VMess    │ │         │ │      │ │
                    │   └──────────┘ └─────────┘ └──────┘ │
                    └─────────────────────────────────────┘
```

## 功能全景

| 类别 | 已实现 | 规划中 |
|------|--------|--------|
| 远程代理 | HTTP / HTTPS / SOCKS5 ✦UDP / SS / Trojan / VMess / **VLESS+Reality ★uTLS** | WireGuard |
| 代理分组 | selector / url-test / round-robin / **chain (代理链式)** | fallback / load-balance |
| 代理模式 | direct / global / rule | — |
| 规则类型 | DOMAIN / SUFFIX / KEYWORD / IP-CIDR / GEOIP / MATCH | REGEX / PORT-RANGE |
| 本地监听 | HTTP / SOCKS5 | TProxy |
| 端口转发 | forward: -L local / -R remote / -D dynamic / -U udp / tls | — |
| 系统代理 | sysproxy on/off/status (Win 注册表 + Linux gsettings) | — |
| 透明代理 | TUN (wintun / /dev/net/tun) + 自动桥接 n2n/stunvpv | — |
| 隧道 | n2n P2P / STUN/TURN VPN / **WireGuard** + tunnel.Peer 接缝 | — |
| 特殊能力 | forward 劫持 / MITM CA / 流量统计 | — |
| 运维 | ping / status / use / Web Dashboard / REST API / DNS(DoH/DoT) / LLM Agent TUI / gen_config / SCP | — |

★ = uTLS 指纹伪装   ✦ = 支持 UDP (PacketProxy)

完整分层速览与交互版全景图见 [MANUAL.md §2.1](MANUAL.md) 和 [docs/panorama.html](docs/panorama.html)。

## 快速开始

```powershell
cd agent-nettools

# 生成示例配置
go run main.go init

# 快速模式：一条命令启动
go run main.go start --proxy ss://aes-256-gcm:password@server:port
go run main.go start --proxy http://user:pass@server:port
go run main.go start --proxy trojan://password@server:port?sni=example.com

# 完整模式：用配置文件
go run main.go start -c config.yml
```

## 劫持场景：App 不走代理？

```
┌────────────────────────────────────────────────────────────────────────┐
│ 方案              │ 条件                  │ 需要 App 配合 │ 改 hosts  │
├───────────────────┼───────────────────────┼───────────────┼───────────┤
│ 系统代理 + rule   │ App 走 WinINet 代理   │ ❌ 不用       │ ❌ 不用   │
│ TUN 模式 (P0)     │ 任意 App              │ ❌ 完全不用   │ ❌ 不用   │
│ MITM CA (P0)      │ 任意 App + hosts      │ ❌ 完全不用   │ ✅ 需要   │
│ forward 命令      │ 任意 App + hosts      │ ❌ 完全不用   │ ✅ 需要   │
└────────────────────────────────────────────────────────────────────────┘
```

## 命令总览

```
agent-nettools [command]

命令：
  start        启动代理（-c 配置文件 / --proxy 快速模式）—— 一键全开所有启用项
  init         生成示例配置
  status       显示当前配置
  ping         测试代理延迟
  use          切换手动分组
  sysproxy     一键开关系统代理 (on/off/status)
  forward      SSH 风格端口转发 (-L / -R / -D / -U / tls)
  proxy        仅启动 HTTP/SOCKS5 代理（独立运行）
  dns          仅启动本地 DNS（独立运行）
  web          仅启动 Web 仪表盘（独立运行）
  tun          仅启动 TUN 设备（独立运行）
  n2n          仅启动 n2n 虚拟局域网节点（独立运行）
  stunvpv      仅启动 STUN/TURN VPN 节点（独立运行）
  tui          启动 LLM Agent 交互模式（自然语言驱动所有功能）
  scp          SSH 文件拷贝（记住 --alias）

全局选项：
  -c, --config  配置文件路径
```

每个 `proxy`/`dns`/`web`/`tun`/`n2n`/`stunvpv` 子命令都只跑一块，前台运行、`Ctrl-C` 退出——排障或按需起服务很方便。`start` 则会把所有 `enable: true` 的子服务一起拉起。

详细用法见 [MANUAL.md](MANUAL.md)。

### 自然语言驱动：`tui`

复杂命令、配置记不住？用 `tui` 启动 LLM Agent，直接用中文描述需求：

```powershell
go run main.go tui
```

```
你> 把 google 走 ss-1
  ⚙️ 调用工具 switch_group(group=Auto, proxy=ss-1)
     ↳ 已把分组 Auto 切换到 ss-1
AI> 已把 Auto 分组切换到 ss-1，google 相关流量现在走 ss-1。

你> 测一下所有代理延迟
  ⚙️ 调用工具 ping_proxies()
     ↳ ss-1   152ms  trojan-1  208ms
AI> ...
```

Agent 走 OpenAI 兼容 API（任意兼容端点均可，本地/自建/官方），在 `config.yml` 的 `agent:` 段配置 `base-url`、`api-key`、`model` 即可。

## 配置示例

```yaml
listen:
  http: 7890
  socks5: 7891

mode: rule

proxies:
  - name: ss-1
    type: ss
    server: 1.2.3.4
    port: 8388
    cipher: aes-256-gcm
    password: my-secret

  - name: trojan-1
    type: trojan
    server: 5.6.7.8
    port: 443
    password: trojan-pass
    sni: example.com

  - name: forward-multica
    type: forward
    server: 192.168.0.77
    port: 8080

proxy-groups:
  - name: Auto
    type: url-test
    proxies: [ss-1, trojan-1]
    url: https://www.gstatic.com/generate_204
    interval: 300

  - name: Manual
    type: selector
    proxies: [ss-1, trojan-1, DIRECT]
    default: ss-1

rules:
  - DOMAIN,api.multica.ai,forward-multica
  - DOMAIN-SUFFIX,.google.com,Auto
  - IP-CIDR,8.8.8.8/32,DIRECT
  - GEOIP,CN,DIRECT
  - MATCH,DIRECT
```

## 深度功能挖掘

对比 Clash / FRP / n2n / WebRTC 等工具，还有哪些功能值得做：

| 优先级 | 功能 | 说明 | 状态 |
|--------|------|------|------|
| **P0** | TUN 模式 | 内核级网络接管，所有 App 无感走代理 | ✅ |
| **P0** | 系统代理一键管理 | sysproxy on/off/status (Win/Linux) | ✅ |
| **P0** | MITM + 自签 CA 自动安装 | HTTPS→HTTP 劫持，自动装证书 | ✅ |
| **P1** | 端口转发 -L/-R/-D/-U/tls | SSH 风格 5 模式 + --proxy | ✅ |
| **P1** | 代理链式 | proxyA → proxyB → 目标 | ✅ |
| **P1** | 流量统计 + 日志 | statsConn 自动计量上下行字节 + 活跃连接 | ✅ |
| **P2** | Web UI 仪表盘 + REST API | 可视化统计、切分组、看日志 | ✅ |
| **P2** | DNS 代理 | 本地 DNS 解析，DoH/DoT + FakeDNS | ✅ |
| **P2** | VLESS / Reality | uTLS 指纹伪装 + X25519 + ShortID | ✅ |
| **P3** | P2P 隧道 | n2n P2P / STUN/TURN 跨 NAT 组网 | ✅ |
| **P3** | WebRTC / UDP 代理 | SOCKS5 UDP ASSOCIATE + forward -U | ✅ |
| P3 | WireGuard 隧道 | tunnel.Peer 接缝已就绪，实现即插 | ✅ |
| P3 | HTTP/3 (QUIC) | 下一代 HTTP 代理 | ✅ |

## 依赖

| 包 | 用途 |
|----|------|
| github.com/spf13/cobra | CLI 框架 |
| gopkg.in/yaml.v3 | 配置解析 |
| `github.com/quic-go/quic-go` | HTTP/3 (QUIC) 代理 (`type: http3`) |
| `github.com/quic-go/quic-go` | HTTP/3 (QUIC) 代理 (`type: http3`) |
| golang.org/x/crypto | SS 加密 (ChaCha20-Poly1305) |

## 路线图

- [x] 6 种远程代理
- [x] 3 种分组
- [x] 规则路由
- [x] forward 劫持
- [x] TUN 模式 (P0)
- [x] MITM + 自签 CA 自动安装 (P0)
- [x] Web Dashboard + REST API (P2)
- [x] DNS 代理 + DoH/DoT + FakeDNS (P2)
- [x] P2P 隧道 — n2n 虚拟局域网 (P3)
- [x] STUN/TURN 标准协议 VPN (P3)
- [x] LLM Agent + TUI 自然语言驱动 (P3)
- [x] 系统代理一键管理 (sysproxy, P0)
- [x] 端口转发 -L/-R/-D/-U/tls (P1)
- [x] 流量统计 + 日志 (statsConn, P1)
- [x] 代理链式 chain (P1)
- [x] VLESS / Reality (uTLS 指纹, P2)
- [x] P2P 隧道 (n2n / STUN/TURN, P3)
- [x] WebRTC / UDP 代理 (SOCKS5 UDP ASSOCIATE + forward -U, P3)
- [x] WireGuard 隧道
- [x] HTTP/3 (QUIC) 代理
