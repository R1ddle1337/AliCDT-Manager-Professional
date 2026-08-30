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
- TCP/UDP/TCP+UDP 透明转发，支持主备、轮询、加权和源 IP Hash
- TCP 长连接与 UDP 会话固定落地目标，健康异常时新连接自动切换
- 控制台采用浅色专业界面，去除 Emoji 图标和文案
- 国内站暂不启用余额与账单功能，国际站账单功能保持可用

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

## 一键安装

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/R1ddle1337/AliCDT-Manager-Professional/main/install.sh)
```

docker-compose.yml 默认端口为
ports:
     - "127.0.0.1:8000:8000"
在安装完成需要配置 Nginx 反代通过域名访问


## 手动部署

```bash
mkdir -p /app/alicdt-manager/data && cd /app/alicdt-manager
```
```bash
echo "SECRET_KEY=$(cat /dev/urandom | tr -dc 'a-zA-Z0-9' | head -c 48)" > .env
```
```bash
curl -fsSL https://raw.githubusercontent.com/R1ddle1337/AliCDT-Manager-Professional/main/docker-compose.yml -o docker-compose.yml
```
```bash
docker compose build
docker compose up -d --no-build
```

## Nginx Cloudflare 配置示例

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
        proxy_pass http://127.0.0.1:8000;
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

## 常用命令

```bash
# 重启服务
cd /app/alicdt-manager && docker compose restart

# 重新构建并更新
cd /app/alicdt-manager && docker compose build && docker compose up -d --no-build

# 停止服务/卸载（保留数据）
cd /app/alicdt-manager && docker compose down

# 彻底卸载
cd /app/alicdt-manager && docker compose down && rm -rf /app/alicdt-manager
```


## Tech Stack

- Backend: Go controller + Go Relay Agent + SQLite
- Frontend: Vue + TailwindCSS


## Nodeseek
https://www.nodeseek.com/post-737919-1
