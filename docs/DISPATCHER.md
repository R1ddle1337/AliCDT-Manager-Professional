# 固定前门 Dispatcher

## 目标架构

固定入口域名解析到两台或更多“非 CDT”网关，每台网关运行同一个无状态
`cdt-dispatcher`：

```text
客户端固定域名/IP
        │ TCP/UDP 443
        ▼
两台以上独立高带宽 L4 Dispatcher
        │ 按健康状态、剩余额度和速率选择
        ▼
香港 CDT Relay（每条连接/UDP 会话固定一个后端）
        │
        ▼
全球落地节点
```

Dispatcher 不解密 SS2022、VLESS、REALITY、TLS 或其他协议，只搬运字节。
控制器通过专用 `CDT_DISPATCH_TOKEN` 提供不含凭证的 Relay 快照；快照连续
失败超过 `CDT_DISPATCH_STALE_AFTER` 后，网关清空后端并保持监听，避免继续
使用可能已经耗尽额度的旧成员。

## 网关要求

- 至少两台独立计费、非 CDT 的高带宽主机；每台有独立公网 IPv4（需要 IPv6
  时同时准备 AAAA）。不要把稀缺 CDT Relay 或面板主机当 Dispatcher。
- 防火墙放行客户端到网关的 TCP/UDP 443；放行网关到 Relay 的 TCP/UDP
  Relay 端口，以及到控制器的 HTTPS。健康端口只绑定 `127.0.0.1`。
- 网关出口带宽应覆盖所有 Relay 汇总流量；Dispatcher 不会减少实际流量，
  只把入口固定并让额度使用更均衡。

面板主机若已有 Caddy/Nginx 占用公网 443，不能通过“再开一个容器”充当前门；
必须先准备独立网关并完成灰度。否则只部署 Dispatcher 二进制不会改变现有
入口，也不应提前切换 DNS。

## 部署

在控制器的私有环境文件中生成并保存一个随机专用令牌（不要复用管理员令牌）：

```bash
printf 'CDT_DISPATCH_TOKEN=%s\n' "$(openssl rand -hex 32)"
```

将同一个值写入控制器和每台网关的环境文件，权限设为 `0600`。控制器重启后，
从 **入口池** 页面复制池 ID；池必须已有在线、已应用配置且正在监听的 Relay。

### Docker（推荐）

在每台网关主机准备 `.env`（只保存在本机）：

```dotenv
CDT_DISPATCH_CONTROLLER_URL=https://panel.example.com
CDT_DISPATCH_POOL_ID=pool_xxxxxxxxx
CDT_DISPATCH_TOKEN=<same-random-token>
CDT_DISPATCH_PUBLIC_PORT=443
CDT_DISPATCH_NETWORK=tcp+udp
```

然后执行：

```bash
docker compose -f deploy/docker-compose.dispatcher.yml up -d --build
curl -fsS http://127.0.0.1:9091/readyz
```

生产环境可改用 `ghcr.io/r1ddle1337/alicdt-dispatcher:latest`，并固定到
经过验证的版本标签；不要在命令行参数中传令牌。

代码更新后，在每台网关仓库目录执行
`deploy/cdt-dispatcher-update.sh`；脚本会检查分支、构建新镜像、等待就绪，
失败时恢复上一个本地镜像。两台网关应分别更新并逐台确认 `/readyz`。

### systemd / Alpine OpenRC

Linux 主机可以使用控制器提供的 `dispatcher/install.sh`（或仓库中的同名脚本）
下载并校验 Release 中的 SHA-256 二进制：

```bash
curl -fsSL https://panel.example.com/dispatcher/install.sh -o /tmp/install-cdt-dispatcher.sh
chmod 700 /tmp/install-cdt-dispatcher.sh
chmod 600 /etc/cdt-dispatcher.env
set -a; . /etc/cdt-dispatcher.env; set +a
bash /tmp/install-cdt-dispatcher.sh \
  --controller "$CDT_DISPATCH_CONTROLLER_URL" --pool-id "$CDT_DISPATCH_POOL_ID" \
  --source controller --listen :8443
```

systemd 单元带 `CAP_NET_BIND_SERVICE`，可直接监听 443。Alpine OpenRC 建议
监听 8443，再用防火墙 DNAT 443 到 8443；服务脚本使用
`supervise-daemon`，不会要求 systemd 目录存在。

## DNS 与灰度切换

在入口池中选择“固定 Dispatcher”公网入口模式（API 字段
`front_door_mode=dispatcher`）。该模式会自动禁止并清理 Relay DNS 托管，避免
控制器把 CDT Relay IP 写回固定域名。在 DNS Provider 页面使用**独立托管记录**
声明 Dispatcher 的公网 IP：同一固定域名创建两条或更多 DNS-only（不经过
Cloudflare 代理）的 A/AAAA 记录；Cloudflare 可选择自动 TTL，需要最快切换时
选择 60 秒。不要把 CDT Relay IP 和 Dispatcher IP 混在同一个 RRset。先用独立
灰度域名验证：

如果希望由控制器定期校验 DNS，可在“DNS 托管 → 添加记录”中为每个网关各建
一条**手动** A/AAAA 记录；不要选择 Relay Agent 来源，也不要把这些记录绑定到
入口池。这样同步器只维护声明的网关记录，不会触碰区域里的其他记录。

已有入口池也可以通过管理 API 更新（请求中保留原有成员和目标）：

```json
{"front_door_mode":"dispatcher","dns_provider_id":""}
```

```bash
dig +short gray-entry.example.com A
curl -fsS http://127.0.0.1:9091/readyz
curl -fsS http://127.0.0.1:9091/stats
```

分别从 TCP 和 UDP 客户端完成一次真实协议握手，再观察两个网关的计数器和
Relay 端的 Agent 计数器。确认配额保护触发时，控制器快照撤下对应 Relay，
Dispatcher 新连接会自动落到其他健康成员。

正式切换前降低旧入口 TTL，保留旧 DNS/Relay 至少一个 TTL 周期；出现异常时
把 DNS RRset 恢复为旧入口即可。DNS 不能迁移已经建立的 TCP 字节流，旧连接
会自然结束或由客户端重连。

## 运行语义与限制

- 默认 `quota_weighted`：优先剩余安全时间（剩余 GB ÷ 近期 GB/分钟），再乘
  Relay 成员权重；未知流量的成员保持可用但不会被错误地当作已耗尽。
- 当控制器明确报告所有账户都已耗尽时，Dispatcher 对新会话 fail-closed，
  等待下一次账期或健康成员恢复；已建立的 TCP 流不主动中断。
- TCP 连接和 UDP 客户端会话固定后端；后端拨号失败会进入短暂熔断并尝试
  下一个成员。已建立 TCP 流不能无损迁移。
- 控制器快照是账户级 CDT 流量，不是单 ECS 独立额度；同账户 Relay 会一起
  排空。Dispatcher 只能按控制器提供的账户快照分流。
- 多 A DNS 仍受递归缓存和客户端选择策略影响，不能保证每个用户按比例命中
  每台网关；需要严格入口故障转移时，应在网关层配合云负载均衡或 Anycast。

## 观测与令牌轮换

- `GET /healthz`：进程存活。
- `GET /readyz`：有新鲜控制器快照和至少一个健康 Relay 时返回 200。
- `GET /stats`：连接、字节、后端失败和轮询状态（不含地址/令牌）。
- `GET /metrics`：Prometheus 文本指标，可只绑定本机并由监控 Agent 抓取。

轮换令牌时先在控制器和两台网关部署新值，再逐台重启并确认 `/readyz`，最后
废弃旧值；不要把令牌提交到 Git、Issue、日志或节点分享链接中。
