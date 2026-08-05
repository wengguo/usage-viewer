# 公网服务器部署指南（不改动现有 Sub2API）

目标：在已运行 Sub2API 的公网服务器上部署 usage-viewer，**不修改** Sub2API 的容器、代码、配置、环境变量、docker-compose 文件。usage-viewer 通过 nginx 反向代理对外访问。

## 前提

- 服务器已运行 Sub2API（Docker 容器），postgres/redis 在容器内、同一 Docker 网络。
- 服务器已安装 Docker。
- 有 usage-viewer 镜像（见下方「镜像获取」）。
- 服务器已有 nginx（或用 caddy 等替代）。

## 镜像获取（三选一）

```sh
# 方式 A：从镜像仓库拉取（若你推送了镜像）
docker pull your-registry.example.com/sub2api-usage-viewer:latest

# 方式 B：从本机构建并传输
# 在本地（能访问 Docker）执行：
cd usage-viewer
docker build -t sub2api-usage-viewer:latest .
docker save sub2api-usage-viewer:latest | gzip > sub2api-usage-viewer.tar.gz
scp sub2api-usage-viewer.tar.gz user@server:/tmp/
# 在服务器执行：
gunzip -c /tmp/sub2api-usage-viewer.tar.gz | docker load

# 方式 C：在服务器直接构建（需要 go 工具链 + 网络可拉依赖）
cd usage-viewer
docker build -t sub2api-usage-viewer:latest .
```

## 安装

把 `remote-install.sh` 上传到服务器，执行：

```sh
chmod +x remote-install.sh
./remote-install.sh
```

脚本会自动：
1. 找到运行中的 sub2api 容器。
2. 从容器内 `/app/data/config.yaml` 读取数据库和 Redis 配置（只读，不改动）。
3. 把 usage-viewer 容器挂到 sub2api 所在的 Docker 网络。
4. 以 `127.0.0.1:8081`（仅本机）启动 usage-viewer。

可选参数：

```sh
./remote-install.sh --image sub2api-usage-viewer:latest \
                    --host-port 127.0.0.1:8081 \
                    --data-volume usage-viewer-data
```

## 验证（本机）

```sh
curl -s http://127.0.0.1:8081/readyz
# => {"status":"ok"}

curl -s -X POST http://127.0.0.1:8081/api/search \
  -H 'Content-Type: application/json' \
  -d '{"targetType":"key","query":"你的key名称或key值"}'
# => {"targetType":"key","results":[...]}
```

## 反向代理（nginx）

1. 把 `nginx-usage-viewer.conf` 放到 `/etc/nginx/conf.d/`，把 `usage.example.com` 换成你的域名。
2. 申请证书（certbot）或替换证书路径。
3. 重载 nginx：

```sh
sudo nginx -t && sudo systemctl reload nginx
```

4. 浏览器访问 `https://usage.example.com/`。

### 反向代理注意事项

- **必须保持 `proxy_set_header Host $host;`**。usage-viewer 的 API 校验 `Origin` 与请求 Host 一致，反代后 Origin 来自浏览器域名，Host 需保持同名，否则 API 会被判跨源拒绝。
- 端口 `8081` 只绑定 `127.0.0.1`，不要直接暴露到公网（依赖 nginx 的 TLS/鉴权）。
- 若用 caddy：`usage.example.com { reverse_proxy 127.0.0.1:8081 }`，自动 HTTPS。

## 工作原理（为什么不改 Sub2API）

| 需求 | 做法 |
|------|------|
| 数据库凭证 | 脚本从 sub2api 容器内 `config.yaml` **读取**（`docker exec ... cat`），提取后以环境变量传给 usage-viewer 容器。不写回、不改文件。 |
| 网络连通 | usage-viewer 容器加入 sub2api 所在 Docker 网络，直接用 `config.yaml` 里的数据库 host（如 `postgres`）。 |
| Redis 并发 | 同样从 `config.yaml` 读 redis host，usage-viewer 容器在同网络可连。 |
| 只读 | usage-viewer 只发 SELECT 查询；数据库账号是 sub2api 的应用账号（有只读权限即可）。 |

## 故障排查

| 现象 | 检查 |
|------|------|
| `ready` 未出现，日志报 `UV-DB-001` | `docker logs usage-viewer`；确认网络连通：`docker exec usage-viewer sh -c 'getent hosts postgres'` |
| 页面打开但搜索报「搜索失败」 | 确认数据库有数据；`curl` 直接测 `/api/search` 看返回 |
| 页面 API 报 ORIGIN_REJECTED | nginx 未透传 Host 或域名与浏览器地址不一致 |
| 当前并发恒为 0 | Redis 未配置或不可达；`config.yaml` 无 redis 段时脚本会告警 |
| 登录页/凭证表单出现 | 凭证发现失败；检查 `docker logs usage-viewer`，或手动用 `--env-file` 传 `DATABASE_*` |

## 回滚 / 卸载

```sh
docker stop usage-viewer && docker rm usage-viewer
docker volume rm usage-viewer-data   # 可选：删除保存的凭证等
```
删除 nginx 配置并重载即可。不影响 Sub2API。
