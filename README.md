# Sub2API Usage Viewer

独立 **Key 用量查询** 工具：只读访问已部署 Sub2API 的 PostgreSQL / Redis，不改 Sub2API 代码、配置、环境变量或 compose。

## 功能

| 功能 | 说明 |
|------|------|
| Key 列表与搜索 | 页面打开即请求 `POST /api/search` 的最新 Key；`targetType` 固定 `key`，可选 `query` 对 `api_keys.name` 与 `api_keys.key` 做 ILIKE 模糊匹配（对齐 Sub2API `search`），每页固定 20 条并返回总数 |
| 列表排序 | 默认按 `api_keys.id` 降序；可按今日用量或近 30 天用量升降序排序，费用按数值而非展示文本排序 |
| 列表字段 | 名称、分组、当前并发、今日用量、近 30 天用量、额度已用/总额度、上次使用、过期、状态、创建时间（响应不返回 key 明文） |
| 每日用量 | `POST /api/key-usage`；弹窗表格 + 折线图，可选 7 / 30 / 90 天 |
| 当前并发 | 读 Redis `concurrency:api_key:*` / `concurrency:live:api_key:*`；未配置或不可达时为 0 |
| 凭证发现 | 自动尝试 env / Sub2API `config.yaml` / `.env` / 已保存文件；失败可进入凭证表单 |
| 健康检查 | `GET /livez`、`GET /readyz` |

不支持账号 / 用户搜索。

## 前置条件

- Go 版本见 `go.mod`（本地编译）
- 能访问 Sub2API 的 PostgreSQL；Redis 可选
- 容器部署：Docker + Compose，且 Sub2API 栈已运行（共享网络时）

数据库可用共享 Sub2API 应用账号，或专用只读角色（见下方）。

## 快速开始（本地二进制）

```sh
mkdir -p dist
go build -trimpath -o dist/sub2api-usage-viewer ./cmd/viewer
./dist/sub2api-usage-viewer
```

成功启动输出 JSON，含 `"event":"ready"`。默认页面：

```text
http://127.0.0.1:8081/
```

默认首页「自助查询」无需登录；「Key 查询」「排行榜」需登录后查看，默认账号 `admin`，默认密码 `usage-viewer-2026`（内置占位值，仅供本地测试；可用环境变量 `SUB2API_USAGE_VIEWER_AUTH_USERNAME` / `SUB2API_USAGE_VIEWER_AUTH_PASSWORD` 覆盖，正式部署前必须显式设置为真实密码，见环境变量一节）。默认仅 loopback；非 loopback 绑定需显式确认（见环境变量）。

镜像：

```sh
docker build -t sub2api-usage-viewer:latest .
```

## 使用

1. 打开 `http://127.0.0.1:8081/`（或反代域名）。
2. 初始列表显示最新 20 个 Key；输入 Key 名称或 Key 值（2–100 个 Unicode 字符）后点「查找」，清空输入可回到无筛选列表。
3. 点「今日用量」或「近30天用量」表头切换排序；使用「上一页」「下一页」浏览匹配结果。
4. 点「每日用量」查看按天费用。

### 凭证发现顺序

未显式依赖手动表单时，按候选依次尝试直到连上：

1. `SUB2API_USAGE_VIEWER_DATABASE_URL`，或 `DATABASE_HOST` + `DATABASE_USER` + `DATABASE_PASSWORD` + `DATABASE_DBNAME`（可选 `DATABASE_PORT`、`DATABASE_SSLMODE`）
2. Sub2API `config.yaml`（相对 `SUB2API_USAGE_VIEWER_DATA_DIR` 与常见路径：`config.yaml`、`../data/config.yaml`、`data/config.yaml`、`/app/data/config.yaml` 等）
3. Sub2API `.env`（`POSTGRES_USER` / `POSTGRES_PASSWORD` / `POSTGRES_DB` 等）
4. 数据目录已保存凭证：`.usage-viewer-creds.json`（权限 0600）

主机名为 Docker 内部名（如 `postgres`）时，本机进程会额外尝试 `127.0.0.1` / `localhost`。全部失败则进入凭证表单（`/credentials`）。

共享账号模式：连通性预检。完整角色准入（角色名、只读默认、精确 schema/列权限）仅在显式提供 `SUB2API_USAGE_VIEWER_DATABASE_URL` 时启用。

## 部署（不改 Sub2API）

原则：viewer 加入 Sub2API 同一 Docker 网络，用内部主机名访问 `postgres` / `redis`。

### A. 一键脚本（已有 Sub2API 的服务器）

```sh
docker build -t sub2api-usage-viewer:latest .
docker save sub2api-usage-viewer:latest | gzip > sub2api-usage-viewer.tar.gz
# 传到服务器后 docker load，再执行：
chmod +x deploy/remote-install.sh
./deploy/remote-install.sh
```

脚本只读 `docker exec ... cat /app/data/config.yaml` 取库/Redis 配置，把 viewer 挂到 Sub2API 网络，绑定 `127.0.0.1:8081`。细节见 [deploy/DEPLOY_REMOTE.md](deploy/DEPLOY_REMOTE.md)。

### B. 本仓库 compose（同机开发）

前提：已有网络（默认 `sub2api_sub2api-network`）。

```sh
docker network ls | grep sub2api
docker compose --env-file /path/to/sub2api/.env up -d --build
```

compose 使用：

- `DATABASE_HOST=postgres`、`REDIS_HOST=redis`
- 外部网络名 `sub2api_sub2api-network`（项目名不同则改 `docker-compose.yml`）
- 端口 `127.0.0.1:8081:8081`
- 容器名 `sub2api-usage-viewer`

### C. 手动 `docker run`

```sh
docker run -d --name sub2api-usage-viewer --restart unless-stopped \
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

### 反向代理

容器只绑本机 `8081` 时，用 nginx/caddy 对外：

```sh
sudo cp deploy/nginx-usage-viewer.conf /etc/nginx/conf.d/usage-viewer.conf
# 改域名与证书后：
sudo nginx -t && sudo systemctl reload nginx
```

**必须** `proxy_set_header Host $host;`。API 校验 Origin 与 Host 一致；Host 不对会 `ORIGIN_REJECTED`。

Caddy：

```text
usage.example.com {
  reverse_proxy 127.0.0.1:8081
}
```

```text
Docker 网络 sub2api_sub2api-network
  sub2api ──► postgres:5432
     │              ▲
     ▼              │ SELECT only
  redis:6379 ◄── sub2api-usage-viewer
                    ▲
                    │ 127.0.0.1:8081
                 nginx / caddy
```

## 环境变量

### 严格只读角色模式（可选）

```sh
export SUB2API_USAGE_VIEWER_DATABASE_URL='postgresql://sub2api_usage_viewer:<password>@127.0.0.1:5432/sub2api?sslmode=disable'
export SUB2API_USAGE_VIEWER_DATABASE_ROLE='sub2api_usage_viewer'
export SUB2API_USAGE_VIEWER_DATA_DIR='./data'
```

示例密码为占位符。远程 TCP 须 `sslmode=verify-full` 且非空 `sslrootcert`；可选 `sslcert` / `sslkey`。不接受其它 URL query。loopback / Unix socket 可用本地合适 SSL 模式。

共享账号可不设上述 URL/ROLE，走凭证发现。

### 登录账号密码（可选）

默认账号：`admin`；默认密码：`usage-viewer-2026`（内置占位值，仅供本地测试）。

```sh
export SUB2API_USAGE_VIEWER_AUTH_USERNAME='admin'
export SUB2API_USAGE_VIEWER_AUTH_PASSWORD='请改成真实密码'
```

`SUB2API_USAGE_VIEWER_AUTH_USERNAME` / `SUB2API_USAGE_VIEWER_AUTH_PASSWORD` 覆盖「Key 查询」「排行榜」的登录账号密码；正式部署前必须显式设置为真实密码。「自助查询」页面本身始终无需登录。

### 共享账号常用变量

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

成功启动 ready 日志中 `address_class` 为 `"address_class":"loopback"` 或 `"address_class":"acknowledged_non_loopback"`。

## 健康检查

After the `ready` event, both same-origin health routes return `{"status":"ok"}`:

```sh
curl --fail --silent http://127.0.0.1:8081/livez
curl --fail --silent http://127.0.0.1:8081/readyz
```

仅 `GET`。套接字在启动准入完成前不会打开。

## 关闭

发送 `SIGINT`（Ctrl-C）或 `SIGTERM`。进程输出 `stopping`，在 `SUB2API_USAGE_VIEWER_SHUTDOWN_TIMEOUT` 内停 HTTP、关监听与连接池。

## 诊断码

失败只输出一条脱敏 JSON。不记录 URL、凭据、角色名、SQL 参数、搜索词、业务数据。

| Exit | Code | Meaning |
|------|------|---------|
| 2 | `UV-CFG-001` | Runtime configuration is invalid |
| 2 | `UV-BIND-001` | Listen address is not safely acknowledged |
| 3 | `UV-DB-001` | Database connection could not be established |
| 4 | `UV-ROLE-001` | Database role is not admitted |
| 5 | `UV-RO-001` | Database read-only verification failed |
| 6 | `UV-SCHEMA-001` | Database schema is not compatible |
| 7 | `UV-SERVER-001` | Server could not start safely |

共享账号模式下连接失败常回退凭证表单而非直接退出。细节查 DBA 侧 PostgreSQL 日志。

## 专用只读角色（可选）

模板：[docs/least-privilege-role.sql](docs/least-privilege-role.sql)。

- 仅 CONNECT、schema USAGE、所需列 SELECT（含搜索用的 `api_keys.key`）
- 不授予表级全读、写、序列、CREATE、TEMP、函数执行、grant option

使用前：替换 `<sub2api_database>`；交互式设密码；审查 `PUBLIC` 继承权限。不要让 viewer 角色成为其它角色 owner/member。

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
The viewer has no production Node.js runtime, Redis, Docker dependency.

With a working Docker daemon, run the disposable PostgreSQL and real-process suite separately:

```sh
go test -tags=integration ./... -count=1 -timeout=120s
```

If Docker is unavailable, these tests report explicit skips outside CI. A skipped database or browser check is not a pass.

## 卸载

```sh
docker stop sub2api-usage-viewer && docker rm sub2api-usage-viewer
docker volume rm usage-viewer-data   # 可选
```

删除 nginx 配置并 reload。不影响 Sub2API。

## 仓库结构

```text
usage-viewer/
├── cmd/viewer/
├── internal/
├── deploy/                 # remote-install、nginx 示例、DEPLOY_REMOTE.md
├── docs/                   # least-privilege-role.sql
├── tests/
├── docker-compose.yml
├── Dockerfile
└── README.md
```

与 Sub2API 平级示例：

```text
/opt/
├── sub2api/          # 已有部署（勿改）
└── usage-viewer/     # 本仓库
```

## 更多文档

- [deploy/DEPLOY_REMOTE.md](deploy/DEPLOY_REMOTE.md)
- [docs/least-privilege-role.sql](docs/least-privilege-role.sql)
