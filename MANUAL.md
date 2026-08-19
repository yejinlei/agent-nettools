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
| `agent-nettools forward <listen> <dst>` | HTTPS→HTTP 劫持 |
| `agent-nettools proxy` | 仅启动 HTTP/SOCKS5 代理（独立运行） |
| `agent-nettools dns` | 仅启动本地 DNS 服务器（独立运行） |
| `agent-nettools web` | 仅启动 Web 仪表盘（独立运行） |
| `agent-nettools tun` | 仅启动 TUN 设备（独立运行） |
| `agent-nettools n2n` | 仅启动 n2n 虚拟局域网节点（独立运行） |
| `agent-nettools stunvpv` | 仅启动 STUN/TURN VPN 节点（独立运行） |
| `agent-nettools tui` | 启动 LLM Agent 交互模式（自然语言驱动） |

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
| `forward` | server, port | sni |

### 3.2 代理分组 (proxy-groups)

| 类型 | 行为 |
|------|------|
| `selector` | 手动选择 |
| `url-test` | 自动选最快 |
| `round-robin` | 轮询 |

### 3.3 规则 (rules)

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

你> exit
```

### 8.4 可用工具

| 工具 | 作用 |
|------|------|
| `get_config` | 读取当前完整配置 (YAML) |
| `update_config` | 用完整新 YAML 覆盖配置（写入前自动校验） |
| `ping_proxies` | 测试所有代理延迟 |
| `switch_group` | 切换 selector 分组到指定代理 |
| `add_rule` | 在规则列表开头插入一条路由规则 |
| `service` | 启动/停止单个子服务：`proxy`/`dns`/`web`/`tun`/`n2n`/`stunvpv`，或 `status` 查看运行状态。后台拉起与独立子命令同名的进程，互不干扰 |
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
- **App 不走代理**：现代 App 需要 TUN 模式（规划中）
- **tui 报 no api-key**：在 `config.yml` 的 `agent.api-key` 填 key，或 `export AGENT_API_KEY`
