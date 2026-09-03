# AliCDT Manager Professional

这是基于 AliCDT-Manager 的 Go 控制器与 Relay Agent 重构版本，提供浅色专业控制台、云资源自动化和 CDT 入口到落地节点的透明转发。

**阿里云 CDT 流量监控与自动化管理控制台**

[![Docker](https://img.shields.io/badge/Docker-local%20build-blue?logo=docker)](https://docs.docker.com/compose/)
[![GitHub](https://img.shields.io/badge/GitHub-R1ddle1337-black?logo=github)](https://github.com/R1ddle1337/AliCDT-Manager-Professional)

</div>

---

## 功能
- 支持 AMD64 / ARM64 架构
- 多账户聚合监控，CDT 流量实时展示
- 流量熔断：超阈值自动停机
- 余额待还熔断：到设置的待还余额值自动停机
- 抢占型实例类型库存不足提醒
- 停机模式有节省停机和普通停机，默认节省，可自选择
- 抢占式实例保活：被回收自动拉起
- 定时开关机计划
- Telegram 告警通知
- 账单统计（待还款金额，国际站准确）
- 每1~2分钟自动同步数据，有特殊情况可点击立即同步
- 添加账户先保存后同步，避免等待阿里云接口返回
- 账户卡片直接生成 root SSH Agent 安装命令，注册码一次性使用
- Agent 自动升级：HTTPS 下载、SHA-256 校验、原子替换、失败自动回滚
- Agent 默认每天北京时间 04:00 检查控制器内置的已校验版本；需要 GitHub Release 时设置 `CDT_AGENT_RELEASE_SOURCE=github`，GitHub 暂时不可用时不影响现有转发
- Agent 自动管理现有 UFW、firewalld、nftables 或 iptables 的入口规则；入口服务/池停用后只删除 Agent 自己创建的规则，不会自行启用或重置防火墙
- Alpine/小磁盘 Agent 自动安装 sing-box `access.log` 守护任务：每分钟检查，超过 50 MiB 原地清空（可配置）
- TCP/UDP/TCP+UDP 透明转发，支持主备、轮询、加权和源 IP Hash
- TCP 长连接与 UDP 会话固定落地目标，健康异常时新连接自动切换
- 落地节点支持粘贴完整分享链接，自动生成仅替换中转 IP/端口的节点配置
- 控制台采用浅色专业界面，去除 Emoji 图标和文案
- 国内站暂不启用余额与账单功能，国际站账单功能保持可用
- DNS 入口托管抽象层：支持阿里云 DNS 与 Cloudflare，多 A 记录自动同步与健康排空
- 逻辑入口池：一份用户节点绑定多台 CDT Relay，成员上线/排空状态自动同步到 DNS
- 入口池支持 `front_door_mode=dispatcher`：控制器只下发后端快照，不会把 Relay IP 写回固定前门域名
- 统一入口池默认启用流量自动排空：绑定账户达到 CDT 阈值后先撤 DNS，再停止新连接，恢复后自动加入
- 配额预测保护：根据连续账户快照计算近期消耗速率，预计在控制与 DNS 生效窗口内达到阈值时提前排空
- 固定前门 Dispatcher：使用两台以上非 CDT L4 网关承载稳定域名，按 Relay 健康度和账户剩余额度无感分流 TCP/UDP
- 多用户控制台：管理员可创建、编辑、启用/禁用和删除用户，分配云账户并设置用户月流量额度；用户只能查看自己的账户级用量

注意：阿里云 `ListCdtInternetTraffic` 按账户统计公网出向流量，接口不提供 ECS 级明细。
同一账户绑定多台 Relay 时会共享该账户的额度和保护状态；若需要独立额度，请使用独立的阿里云计费账户。
用户用量按管理员分配的阿里云账户汇总；控制台不会把账户级 CDT 快照伪装成单个终端的精确用量。用户额度用于管理和展示，账户的自动排空/停机保护仍由云账户保护阈值执行。
预测窗口默认 `4m`，可通过 `CDT_TRAFFIC_SAFETY_WINDOW` 调整或设为 `0s` 关闭。入口池成员权重不影响普通多 A DNS 的选择比例。

Agent 更新说明：生产控制器通过 `CDT_AGENT_RELEASE_SOURCE=github` 从
`CDT_AGENT_RELEASE_REPO` 的 GitHub Release 获取并校验 AMD64/ARM64 二进制，缓存目录默认为
`/app/data/agent-releases`。GitHub 暂时不可用时继续提供最近一次校验成功的缓存版本，首次无缓存时使用镜像内置版本。
Agent 安装脚本默认设置 `CDT_AGENT_UPDATE_TIME=04:00` 和 `CDT_AGENT_UPDATE_LOCATION=Asia/Shanghai`，每天只检查一次；如需兼容旧配置，可显式设置 `CDT_AGENT_UPDATE_INTERVAL`。
Agent 安装支持 systemd 和 Alpine Linux 的 OpenRC；在没有这两种服务管理器的容器中，请使用容器编排器运行 Agent。

为避免 1G 磁盘被 sing-box 访问日志占满，安装/升级 Agent 时会创建
`cdt-sing-box-log-cleanup` 定时任务。默认每分钟检查
`/var/log/sing-box/access.log`，超过 `50 MiB` 就原地截断，不重启 sing-box，也不删除配置。
可在 `/etc/cdt-relay/sing-box-log-cleanup.env` 调整：

```sh
CDT_SINGBOX_ACCESS_LOG=/var/log/sing-box/access.log
CDT_SINGBOX_ACCESS_LOG_MAX_MB=50
```

Alpine 使用 root 的 BusyBox `crond`，systemd 主机使用同名 `.timer`；没有对应调度器的容器需要由容器编排器自行运行该检查脚本。

固定入口 Dispatcher 的部署、DNS-only 记录、灰度切换和回滚步骤见
[`docs/DISPATCHER.md`](docs/DISPATCHER.md)。Dispatcher 必须部署在至少两台
独立的非 CDT 网关上，不能占用面板主机的 443，也不能把管理员令牌下发给网关。

## 所需 RAM 权限
https://ram.console.alibabacloud.com/users
```bash
AliyunECSFullAccess
```
```bash
AliyunCDTFullAccess
```
```bash
AliyunBSSFullAccess
```

## Go 控制器部署

仓库现在只保留 Go 控制器、Relay Agent 和 Dispatcher 运行栈。旧的
FastAPI/Python 镜像、根目录旧 Compose 文件和旧安装脚本已移除。

开发环境：

```bash
export CDT_ADMIN_TOKEN="replace-with-random-admin-token"
export CDT_BOOTSTRAP_ENROLL_TOKEN="replace-with-one-time-enrollment-token"
docker compose -f deploy/docker-compose.go.yml up --build
```

生产环境请使用 `deploy/docker-compose.go.production.yml`，将
`CDT_ADMIN_TOKEN` 放在仓库外的权限为 `0600` 的环境文件中，并把
`/app/alicdt-manager/data` 作为唯一数据库目录。生产更新可通过面板的“一键更新”
或 `deploy/alicdt-manager-update.sh` 执行；脚本会先备份数据库，再重建并等待健康检查。

## Nginx/Cloudflare 配置示例

请手动填写 #端口 #域名 #Pem证书路径 #Key证书路径

```bash
server {
    listen #端口 ssl;
    server_name #域名;

    ssl_certificate     #Pem证书路径;
    ssl_certificate_key #Key证书路径;

    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_ciphers HIGH:!aNULL:!MD5;

    # 禁止访问数据目录
    location ^~ /data/ {
        deny all;
        return 403;
    }

    # 禁止访问 .env 等敏感文件
    location ~ /\. {
        deny all;
        return 403;
    }

    location / {
        proxy_pass http://127.0.0.1:18000;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_read_timeout 3600s;
    }
}

```

---

## Tech Stack

- Backend: Go controller + Go Relay Agent + SQLite
- Frontend: Vue + TailwindCSS


## Nodeseek
https://www.nodeseek.com/post-737919-1
