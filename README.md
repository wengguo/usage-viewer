# Sub2API Usage Viewer

独立的 **Key 用量查询工具**，只读查询已部署 Sub2API 的 PostgreSQL / Redis，不修改 Sub2API 代码、配置、环境变量或 docker-compose。

The viewer is one Go process. Its frontend is embedded in the binary. It can run directly on a host or as a container on the same Docker network as Sub2API. It stores no application state beyond optional saved database credentials, and it needs no production Node.js runtime, Redis, Docker, migration, or change to existing Sub2API source. It reads PostgreSQL for all key fields; current concurrency is the exception — it reads the Sub2API Redis instance when `REDIS_HOST` is configured and reports 0 when Redis is unavailable.

## 这是做什么的

面向已有 Sub2API 实例的运维/查询场景：

- **按 Key 名称或 Key 值搜索**（对齐 Sub2API `/api/v1/api-keys?search=`，不支持账号/用户）
- **列表字段**对齐 Sub2API `/api/v1/keys` 页面逻辑：名称、分组、当前并发、今日用量、近 30 天用量、额度已用/总额度、上次使用时间、过期时间、状态、创建时间
- **每日用量**：点击「每日用量」弹窗，展示表格 + 折线图（天数可选 7/30/90）
- **当前并发**：从 Sub2API 网关使用的 Redis 键 `concurrency:api_key:*` / `concurrency:live:api_key:*` 读取；Redis 不可用时显示 0
- **只读**：只发 SELECT；默认复用 Sub2API 数据库账号

本仓库可与 `sub2api` **平级独立管理**（单独 git 仓库），不需要嵌在 Sub2API 源码树中。

## 功能一览

| 功能 | 说明 |
|------|------|
| Key 搜索 | 按名称或 Key 值 ILIKE 模糊匹配，最多 20 条 |
| 列表 | 名称 / 分组 / 并发 / 今日 / 近 30 天 / 额度 / 上次使用 / 过期 / 状态 / 创建时间 |
| 每日用量 | 弹窗 + 折线图 hover 显示日期与金额 |
| 凭证发现 | 自动从 env / config.yaml / .env / 已保存文件发现数据库配置 |
| 健康检查 | `/livez`、`/readyz` |

## 前置条件

- Go 1.26.5 或 `go.mod` 声明的兼容版本（本地编译）
- Docker + Compose 插件（容器部署）
- 能访问 Sub2API 的 PostgreSQL（以及可选 Redis）
- 部署到已有 Sub2API 服务器时：Sub2API 容器已在运行

专用只读角色可选；共享账号模式下直接使用 Sub2API 应用库账号。

## 快速开始（本地二进制）

```sh
mkdir -p dist
go build -trimpath -o dist/sub2api-usage-viewer ./cmd/viewer
```

结果是完整服务端 + 内嵌前端。交叉编译时设置 `GOOS` / `GOARCH`；不要在不同 OS/架构之间直接拷贝二进制。

启动：

```sh
./dist/sub2api-usage-viewer
```

成功启动会输出 JSON 日志，包含 `"event":"ready"`。默认监听：

```text
http://127.0.0.1:8081/
```

无登录。请保持 loopback，或放在受信任的内部网络边界之后。

容器镜像构建：

```sh
docker build -t sub2api-usage-viewer:latest .
```

## 使用方式

1. 打开页面（默认 `http://127.0.0.1:8081/`）。
2. 在输入框输入 Key 名称或 Key 值（至少 2 个字符），点「查找」。
3. 列表展示该 Key 的用量与状态字段。
4. 点「每日用量」查看按天费用表格与折线图；可切换 7 / 30 / 90 天。

### 凭证如何获取

未设置 `SUB2API_USAGE_VIEWER_DATABASE_URL` 时，按优先级尝试，直到连上：

1. 环境变量：`DATABASE_HOST` + `DATABASE_USER` + `DATABASE_PASSWORD` + `DATABASE_DBNAME`（可选 `DATABASE_PORT`、`DATABASE_SSLMODE`）
2. Sub2API `config.yaml`（查找路径：`./config.yaml`、`../data/config.yaml`、`data/config.yaml`、`/app/data/config.yaml`）
3. Sub2API `.env`（`./.env`、`../.env`、`/app/.env`），读取 `POSTGRES_USER`、`POSTGRES_PASSWORD`、`POSTGRES_DB`
4. 数据目录已保存凭证（`.usage-viewer-creds.json`，权限 0600）

若均为 Docker 内部主机名（如 `postgres`），会自动尝试 `127.0.0.1` / `localhost` 回退。全部失败则进入凭证表单模式（页面可填连接信息）。

共享账号模式只做连通性预检；完整角色准入（角色名、只读默认、精确 schema、权限）仅在显式提供 `SUB2API_USAGE_VIEWER_DATABASE_URL` 时启用。

## 与已部署 Sub2API 共享 Docker 网络

核心原则：**不改 Sub2API**，只让 usage-viewer 容器加入同一 Docker 网络，用内部主机名访问 `postgres` / `redis`。

### 方式 A：一键脚本（推荐，公网服务器）

适用于 Sub2API 已在 Docker 中运行的机器：

```sh
# 1) 准备镜像（本机构建后传到服务器）
docker build -t sub2api-usage-viewer:latest .
docker save sub2api-usage-viewer:latest | gzip > sub2api-usage-viewer.tar.gz
scp sub2api-usage-viewer.tar.gz user@server:/tmp/
# 服务器上：
gunzip -c /tmp/sub2api-usage-viewer.tar.gz | docker load

# 2) 上传 deploy/ 目录后执行
chmod +x deploy/remote-install.sh
./deploy/remote-install.sh
```

脚本会：

1. 找到运行中的 Sub2API 容器
2. **只读** `docker exec ... cat /app/data/config.yaml`，提取 database/redis
3. 把 usage-viewer 挂到 Sub2API 所在网络
4. 绑定 `127.0.0.1:8081` 启动（供反代使用）

可选参数：

```sh
./deploy/remote-install.sh \
  --image sub2api-usage-viewer:latest \
  --host-port 127.0.0.1:8081 \
  --data-volume usage-viewer-data
```

### 方式 B：本仓库 docker-compose.yml（开发/同机）

前提：Sub2API 主栈已创建网络（默认名 `sub2api_sub2api-network`，由 compose 项目名 + 网络名组成）。

```sh
# 确认网络名
docker network ls | grep sub2api

# 用 Sub2API 的 .env 提供 POSTGRES_*（路径按实际位置调整）
docker compose --env-file /path/to/sub2api/.env up -d --build
```

compose 中写死：

- `DATABASE_HOST=postgres`、`REDIS_HOST=redis`
- 外部网络：`sub2api_sub2api-network`
- 端口：`127.0.0.1:8081:8081`

若服务器上 compose 项目名不是 `sub2api`，请修改 `docker-compose.yml` 的 `networks.sub2api-network.name` 为实际网络名。

### 方式 C：手动 docker run

```sh
# 假设已从 config.yaml 得到密码与网络名
docker run -d --name usage-viewer --restart unless-stopped \
  --network sub2api_sub2api-network \
  -p 127.0.0.1:8081:8081 \
  -e DATABASE_HOST=postgres \
  -e DATABASE_PORT=5432 \
  -e DATABASE_USER=sub2api \
  -e DATABASE_PASSWORD='真实密码' \
  -e DATABASE_DBNAME=sub2api \
  -e DATABASE_SSLMODE=disable \
  -e REDIS_HOST=redis \
  -e REDIS_PORT=6379 \
  -e SUB2API_USAGE_VIEWER_DATA_DIR=/app/data \
  -e SUB2API_USAGE_VIEWER_LISTEN_ADDR=0.0.0.0:8081 \
  -e SUB2API_USAGE_VIEWER_ACKNOWLEDGE_NON_LOOPBACK=true \
  -v usage-viewer-data:/app/data \
  sub2api-usage-viewer:latest
```

### 反向代理（nginx）

容器只监听本机 `8081`，对外用 nginx/caddy：

```sh
# 参考 deploy/nginx-usage-viewer.conf，替换域名与证书路径
sudo cp deploy/nginx-usage-viewer.conf /etc/nginx/conf.d/usage-viewer.conf
sudo nginx -t && sudo systemctl reload nginx
```

**必须** `proxy_set_header Host $host;`。viewer 的 API 校验 Origin 与 Host 一致；反代后若 Host 不对，会返回跨源拒绝。

Caddy 示例：

```text
usage.example.com {
  reverse_proxy 127.0.0.1:8081
}
```

### 网络共享示意

```text
┌──────────────────── Docker 网络: sub2api_sub2api-network ────────────────────┐
│  sub2api  ──►  postgres:5432                                                 │
│     │              ▲                                                         │
│     │              │ 只读 SELECT                                             │
│     ▼              │                                                         │
│  redis:6379  ◄── usage-viewer (并发 ZCOUNT)                                  │
└──────────────────────────────────────────────────────────────────────────────┘
                              ▲
                              │ 127.0.0.1:8081
                         nginx / caddy
                              │
                         公网 HTTPS
```

| 需求 | 做法 |
|------|------|
| 数据库凭证 | 从 Sub2API 容器 `config.yaml` 只读提取，或 compose `--env-file` 注入；不写回 Sub2API |
| 网络 | usage-viewer 加入 Sub2API 同一 bridge 网络，用 `postgres` / `redis` 主机名 |
| 不改 Sub2API | 不修改其 compose、代码、环境变量、卷数据 |

## 环境变量

### 必选（严格只读角色模式）

通过进程管理器或密钥系统注入。viewer 不加载 `.env` 文件本身（凭证发现会主动读磁盘上的 `.env` 作为候选源）：

```sh
export SUB2API_USAGE_VIEWER_DATABASE_URL='postgresql://sub2api_usage_viewer:<password>@127.0.0.1:5432/sub2api?sslmode=disable'
export SUB2API_USAGE_VIEWER_DATABASE_ROLE='sub2api_usage_viewer'
export SUB2API_USAGE_VIEWER_DATA_DIR='./data'
```

示例中的密码为占位符。远程 TCP 库必须使用 `sslmode=verify-full` 且非空 `sslrootcert`；可选 `sslcert` / `sslkey`。loopback / Unix socket 可用本地合适的 SSL 模式。不接受其它 URL query 参数。

共享账号模式可不设上述变量，走凭证发现。

### 共享账号常用变量（Docker 部署）

| 变量 | 说明 |
|------|------|
| `DATABASE_HOST` | 如 `postgres` |
| `DATABASE_PORT` | 默认 5432 |
| `DATABASE_USER` / `DATABASE_PASSWORD` / `DATABASE_DBNAME` | 与 Sub2API 一致 |
| `DATABASE_SSLMODE` | 容器内常用 `disable` |
| `REDIS_HOST` / `REDIS_PORT` / `REDIS_PASSWORD` / `REDIS_DB` | 当前并发；可选 |

### 可选配置

未设置时使用下列默认值。

| Variable | Default and accepted range |
|----------|----------------------------|
| `SUB2API_USAGE_VIEWER_LISTEN_ADDR` | `127.0.0.1:8081`; numeric IP and port only |
| `SUB2API_USAGE_VIEWER_ACKNOWLEDGE_NON_LOOPBACK` | `false`; exactly `true` or `false` |
| `SUB2API_USAGE_VIEWER_DB_CONNECT_TIMEOUT` | `5s`; positive Go duration |
| `SUB2API_USAGE_VIEWER_DB_ACQUIRE_TIMEOUT` | `2s`; positive Go duration |
| `SUB2API_USAGE_VIEWER_DB_QUERY_TIMEOUT` | `5s`; positive Go duration |
| `SUB2API_USAGE_VIEWER_DB_POOL_MAX_CONNS` | `4`; integer from 1 through 4 |
| `SUB2API_USAGE_VIEWER_DB_POOL_MIN_CONNS` | `0`; integer from 0 through the configured maximum |
| `SUB2API_USAGE_VIEWER_DB_MAX_CONN_LIFETIME` | `30m`; positive Go duration |
| `SUB2API_USAGE_VIEWER_DB_MAX_CONN_IDLE_TIME` | `5m`; positive Go duration |
| `SUB2API_USAGE_VIEWER_DB_HEALTH_CHECK_PERIOD` | `1m`; positive Go duration |
| `SUB2API_USAGE_VIEWER_MAX_QUERY_RANGE` | `720h`; from `1h` through `720h` |
| `SUB2API_USAGE_VIEWER_HTTP_READ_HEADER_TIMEOUT` | `5s`; positive Go duration |
| `SUB2API_USAGE_VIEWER_HTTP_READ_TIMEOUT` | `10s`; positive Go duration |
| `SUB2API_USAGE_VIEWER_HTTP_WRITE_TIMEOUT` | `15s`; positive Go duration |
| `SUB2API_USAGE_VIEWER_HTTP_IDLE_TIMEOUT` | `60s`; positive Go duration |
| `SUB2API_USAGE_VIEWER_SHUTDOWN_TIMEOUT` | `10s`; positive Go duration |

Keep the default loopback listener unless access is protected by a trusted internal network boundary. A non-loopback bind requires both an explicit `SUB2API_USAGE_VIEWER_LISTEN_ADDR` and `SUB2API_USAGE_VIEWER_ACKNOWLEDGE_NON_LOOPBACK=true`; acknowledgement does not add authentication or TLS.

成功启动的 ready 日志中，`address_class` 为 `"address_class":"loopback"` 或 `"address_class":"acknowledged_non_loopback"`。

## 健康检查

After the `ready` event, both same-origin health routes return `{"status":"ok"}`:

```sh
curl --fail --silent http://127.0.0.1:8081/livez
curl --fail --silent http://127.0.0.1:8081/readyz
```

仅支持 `GET`。套接字在启动准入完成前不会打开。

## 关闭

发送 `SIGINT`（Ctrl-C）或 `SIGTERM`。进程输出 `stopping`，在 `SUB2API_USAGE_VIEWER_SHUTDOWN_TIMEOUT` 内停止 HTTP，关闭监听与 PostgreSQL 连接池。

## 诊断码

失败只输出一条脱敏 JSON。不会记录 URL、凭据、角色名、SQL 参数、搜索词、库内业务数据。

| Exit | Code | Meaning |
|------|------|---------|
| 2 | `UV-CFG-001` | Runtime configuration is invalid |
| 2 | `UV-BIND-001` | Listen address is not safely acknowledged |
| 3 | `UV-DB-001` | Database connection could not be established |
| 4 | `UV-ROLE-001` | Database role is not admitted |
| 5 | `UV-RO-001` | Database read-only verification failed |
| 6 | `UV-SCHEMA-001` | Database schema is not compatible |
| 7 | `UV-SERVER-001` | Server could not start safely |

共享账号模式下连接失败通常回退到凭证表单而不是直接退出。敏感细节请查 DBA 侧 PostgreSQL 日志，不要放宽 viewer 诊断输出。

## 专用只读角色（可选严格模式）

DBA 模板见 [docs/least-privilege-role.sql](docs/least-privilege-role.sql)：仅 CONNECT、public USAGE，以及对所需列的 SELECT；不授予表级全读、写、序列、所有权、CREATE、TEMP、函数执行或 grant option。

使用前：

1. 将 `<sub2api_database>` 换成真实库名
2. 用交互式 DBA 工具设密码，不要写进 SQL 文件
3. 审查 `PUBLIC` 继承权限

模板只改角色与 ACL，不改 Sub2API 表/索引/策略/数据。不要把 viewer 角色设为其它角色 owner 或 member。

## 验证

Docker-independent release checks:

```sh
node --check internal/web/app.js
go test ./... -count=1
go test -race ./... -count=1
go vet ./...
go build -trimpath -o dist/sub2api-usage-viewer ./cmd/viewer
```

Node.js in the first command is a development-time syntax check only; it is not a production runtime dependency.

With a working Docker daemon, run the disposable PostgreSQL and real-process suite separately:

```sh
go test -tags=integration ./... -count=1 -timeout=120s
```

If Docker is unavailable, these tests report explicit skips outside CI. A skipped database or browser check is not a pass.

## 故障排查

| 现象 | 处理 |
|------|------|
| 启动无 `ready`，日志 `UV-DB-001` | `docker logs usage-viewer`；检查网络：`docker exec usage-viewer getent hosts postgres` |
| 搜索失败 | 直接 curl `/api/search`；确认库内有 key 数据 |
| `ORIGIN_REJECTED` | 反代未透传 Host，或域名与浏览器地址不一致 |
| 当前并发恒 0 | Redis 未配或不可达 |
| 出现凭证表单 | 凭证发现失败；检查 config.yaml 是否可读，或手动注入 `DATABASE_*` |
| compose 找不到网络 | `docker network ls` 改 `docker-compose.yml` 中 external 网络名 |

## 卸载

```sh
docker stop usage-viewer && docker rm usage-viewer
docker volume rm usage-viewer-data   # 可选
```

删除 nginx 配置并 reload。不影响 Sub2API。

## 仓库结构（独立仓库）

```text
usage-viewer/
├── cmd/viewer/          # 入口
├── internal/            # 业务代码
├── deploy/              # 公网部署脚本与 nginx 示例
│   ├── remote-install.sh
│   ├── nginx-usage-viewer.conf
│   └── DEPLOY_REMOTE.md
├── docs/                # 最小权限角色 SQL
├── docker-compose.yml   # 与 Sub2API 共享网络部署
├── Dockerfile
└── README.md
```

与 `sub2api` 平级克隆示例：

```text
/opt/
├── sub2api/             # 已有 Sub2API 部署（不要改）
└── usage-viewer/        # 本仓库
```

## 更多文档

- [deploy/DEPLOY_REMOTE.md](deploy/DEPLOY_REMOTE.md) — 公网服务器详细步骤
- [docs/least-privilege-role.sql](docs/least-privilege-role.sql) — 专用只读角色模板
