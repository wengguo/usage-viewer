# 登录鉴权 + 默认落地页调整 — 设计

## 背景

当前三个页面（Key 查询 `/`、排行榜 `/leaderboard`、自助查询 `/self`）都无需登录即可访问。要求：

1. 「Key 查询」「排行榜」默认隐藏，只有登录成功才能查看（服务端真拦截，不是仅隐藏入口）
2. 登录账号密码硬编码在代码里
3. 默认首页改为「自助查询」，登录成功后展示另外两个入口

## 路由表

| 路径 | 登录要求 | 内容 |
|---|---|---|
| `/`、`/self` | 公开 | 自助查询页（同一内容，`/` 是主入口） |
| `/self.js` | 公开 | 自助查询脚本 |
| `/api/self-lookup` | 公开 | 自助查询接口 |
| `/login` | 公开 | 登录页 |
| `/api/login` | 公开 | 登录提交 |
| `/api/logout` | 公开 | 登出 |
| `/api/auth/status` | 公开 | 供前端查询登录态 |
| `/keys` | **需登录** | 原 `index.html`（Key 查询），路径从 `/` 迁移 |
| `/leaderboard` | **需登录** | 排行榜页 |
| `/api/search`、`/api/key-usage`、`/api/leaderboard` | **需登录** | 对应数据接口 |
| `/app.css`、`/app.js`、`/leaderboard.js`、`favicon.svg`、`/theme.js`、`/theme-init.js` | 公开 | 静态资源；内容不含业务数据，页面级已挡 |

未登录访问受保护页面 → `302` 到 `/login?next=<原路径>`。
未登录调用受保护 API → `401` JSON（`{"error":{"code":"UNAUTHENTICATED",...}}`）。

## 鉴权机制

- **凭据**：硬编码在 Go 代码常量里，账号 `admin`，密码 `usage-viewer-2026`（占位值，部署前自行改成真实密码并重新编译）。
- **登录**：`POST /api/login`，body `{"username","password"}`。用 `crypto/subtle.ConstantTimeCompare` 比较，防时序攻击。成功后：
  - 用 `crypto/rand` 生成 32 字节随机 token，base64url 编码
  - 写入进程内内存 session store（`map[token]expiry`，加锁），过期时间如 24h
  - `Set-Cookie`: `session=<token>; HttpOnly; SameSite=Strict; Path=/`（不加 `Secure`——本地 `http://127.0.0.1:8081` 直连场景无 TLS，Secure 会导致 cookie 存不下来；生产部署经 nginx/Caddy 反代 HTTPS 时是反代到浏览器一段用 HTTPS，cookie 本身仍工作）
- **登出**：`POST /api/logout`，删 session + 清 cookie。
- **中间件**：`requireAuth(next http.Handler) http.Handler`，检查 cookie → session store 是否命中且未过期；页面路由未命中时 302，API 路由未命中时 401。
- **会话存储无持久化**：进程重启后所有 session 失效，需重新登录——与本仓库现有"无本地持久化"惯例一致（唯一例外是前端 `localStorage['theme']`，鉴权 session 不复用该例外）。
- 不做失败次数限制/账号锁定（本工具默认仅 loopback 绑定，外部风险面小，符合"设置死一个账号密码即可"的从简诉求）。

## 前端改动

- 新增 `login.html` + `login.js`：与现有卡片式表单风格一致（复用 `self.html` 的排版模式），提交后带 `next` 参数跳转回原目标页；失败展示错误提示。
- 新增共享 `nav.js`（三个页面 `<head>` 或 body 底部引入）：页面加载后调用 `GET /api/auth/status`：
  - 未登录：隐藏侧边栏「排行榜」「Key 查询」链接（HTML 默认 `hidden`，避免闪烁），显示「登录」入口
  - 已登录：显示这两个链接，并把「登录」替换为「退出登录」按钮（调用 `/api/logout` 后跳转 `/`）
- 三个页面导航链接 href 更新：`/keys`、`/leaderboard`、`/`（自助查询用根路径）。
- `index.html`（Key 查询）内容不变，仅路由从 `/` 迁移到 `/keys`；对应地它的导航链接改为高亮 `/keys`。

## 不变的部分

- 排行榜、自助查询、Key 查询三个页面自身的业务逻辑、样式、图表渲染均不改动。
- CSP、安全响应头策略不变；登录相关接口同样过 `sameOrigin` 校验。
- Docker/环境变量部署方式不变（鉴权凭据是编译期常量，不走环境变量）。

## 受影响的现有测试

`internal/httpapi/handler_test.go`、`internal/web/*_test.go` 中依赖 `/`、`/api/search`、`/api/leaderboard` 路由行为的测试需要同步更新：

- 原本请求 `/` 期望拿到 Key 查询页的测试，改为请求 `/keys` 并带上登录 cookie
- 新增未登录访问 `/keys`、`/leaderboard`、对应 API 的 401/302 断言
- 新增登录成功/失败、登出、`/api/auth/status` 的测试

## 验证

- `go build ./...`、`go test ./...` 全部通过
- 手动验证：未登录访问 `/keys`、`/leaderboard` 被拦截并跳转登录页；登录后可访问；退出登录后再次被拦截；自助查询全程无需登录可用
