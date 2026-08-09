# 设计文档：Tailwind UI 改版 + 排行榜 + 自助查询

日期：2026-08-09

## 背景与目标

现有 `usage-viewer` 前端是手写 HTML/CSS/JS，通过 Go `embed.FS` 直接内嵌为静态文件，视觉风格朴素。本次改动：

1. 用 Tailwind 重写全部页面视觉，提升可用性与美观度。
2. 新增「消耗排行榜」：按 1 / 3 / 7 / 30 天自然日榜单，展示 Top N 消耗最高的 Key。
3. 新增「自助查询」：免登录，输入自己的 Key 值或名称精确查询，只看到这一条 Key 的详情与每日用量，不暴露其他 Key。

项目约束（继承自现有 README）：只读访问 Sub2API 数据库，不改 Sub2API 代码/配置；无登录、无用户体系；`api_keys` 表无 owner/user 字段。

## 范围确认

- **UI 改版**：`/`（主列表）、`/leaderboard`（新）、`/self`（新）三页全部用 Tailwind 重写，共享一个顶部导航栏。
- **Tailwind 接入方式**：不用 CDN 运行时（避免部署环境无公网出口导致页面无样式退化），改为下载 Tailwind Play CDN 的浏览器端脚本到本地，内嵌进 `embed.FS`，页面本地引用 `/vendor/tailwind.js`。已下载至 `internal/web/vendor/tailwind.js`（约 407KB）。
- **排行榜**：
  - 4 个自然日榜（今天 00:00 至今、近 3/7/30 个日历日，含今天），按 `usage_logs.actual_cost` 求和排序。
  - 四榜平铺同页（4 列/4 块布局），共用一个 Top N 选择器（10/20/50，默认 10）。
  - 无鉴权（与项目现有免登录模型一致），但返回的 `key` 字段必须掩码：长度 >12 保留前 6 + `***` + 后 6；长度 ≤12 全部替换为 `***`。
- **自助查询**：
  - 独立页面 `/self`，输入框接受 Key 值或名称，**精确匹配**（不用 ILIKE 模糊）。
  - 命中后展示卡片式详情（掩码 key、名称、分组、额度、状态、今日用量 + 每日用量图表），不是表格形式，不暴露其他 Key。
  - 名称重复时取 `id` 最小（最早创建）的一条，保证结果稳定。
  - 未命中统一返回「未找到匹配的 Key」，不区分是 key 错误还是名称错误，避免枚举探测。

## 后端设计

### 新增：`internal/search/mask.go`

```go
func MaskKey(key string) string
```
- `len(key) <= 12`：整串替换为 `"***"`。
- `len(key) > 12`：`key[:6] + "***" + key[len(key)-6:]`。

### 新增：`internal/search/leaderboard.go`

```go
type LeaderboardWindow string
const (
    Window1Day  LeaderboardWindow = "1d"
    Window3Day  LeaderboardWindow = "3d"
    Window7Day  LeaderboardWindow = "7d"
    Window30Day LeaderboardWindow = "30d"
)

type LeaderboardEntry struct {
    Rank       int    `json:"rank"`
    KeyMasked  string `json:"keyMasked"`
    Name       string `json:"name"`
    GroupName  string `json:"groupName"`
    ActualCost string `json:"actualCost"`
}

func ValidateLimit(limit int) (int, error) // 仅允许 10/20/50，0 或未传时默认 10
```

### 新增：`internal/postgres/leaderboard.go`

- `LeaderboardRepository.Top(ctx, limit int) (map[search.LeaderboardWindow][]search.LeaderboardEntry, error)`
- 内部对 4 个窗口分别执行同一条参数化 SQL（窗口起点不同），每条：
  ```sql
  SELECT api_key.id, api_key.key, api_key.name, COALESCE(grp.name,''),
         SUM(ul.actual_cost)::text
  FROM public.usage_logs ul
  JOIN public.api_keys api_key ON api_key.id = ul.api_key_id
  LEFT JOIN public.groups grp ON grp.id = api_key.group_id
  WHERE ul.created_at >= $1 AND api_key.deleted_at IS NULL
  GROUP BY api_key.id, api_key.key, api_key.name, grp.name
  ORDER BY SUM(ul.actual_cost) DESC
  LIMIT $2
  ```
- 复用现有 `withReadOnlyTx` helper。
- Repository 层返回后，httpapi 层调用 `search.MaskKey` 做掩码（保持 SQL 层不处理展示逻辑）。

### 新增：`internal/search/selflookup.go`

```go
type SelfResult struct {
    KeyMasked string `json:"keyMasked"`
    Name      string `json:"name"`
    GroupName string `json:"groupName"`
    Quota     string `json:"quota"`
    QuotaUsed string `json:"quotaUsed"`
    Status    string `json:"status"`
    ExpiresAt string `json:"expiresAt"`
    TodayCost string `json:"todayCost"`
}

func ValidateCredential(s string) (string, error) // trim、非空、长度上限（复用 MaximumTextRunes 风格）
```

### 新增：`internal/postgres/selflookup.go`

- `SelfLookupRepository.Lookup(ctx, credential string) (search.SelfResult, int64, bool, error)`（返回内部 id 供查每日用量，不下发给前端）
- SQL：`WHERE deleted_at IS NULL AND (key = $1 OR name = $1) ORDER BY id ASC LIMIT 1`

### `internal/httpapi` 新增两个 handler

- `POST /api/leaderboard`：body `{"limit": 10}`，复用现有 `sameOrigin` / `Content-Type` / `DisallowUnknownFields` 校验模式。返回：
  ```json
  {"limit":10,"windows":{"1d":[...],"3d":[...],"7d":[...],"30d":[...]}}
  ```
- `POST /api/self-lookup`：body `{"credential":"..."}`。
  - 未命中：`404 {"error":{"code":"NOT_FOUND","message":"未找到匹配的 Key"}}`
  - 命中：`200` 返回 `SelfResult` + 复用现有 `DailyUsageRepository`（用查到的内部 id，默认 30 天）拼出 `dailyUsage` 字段。

`cmd/viewer/main.go` 的 `app.Dependencies.NewHandler` 接入两个新 repository。

### 数据库权限

新接口复用现有 `docs/least-privilege-role.sql` 已授权的列（`api_keys.key/name/quota/...`、`usage_logs.actual_cost`），**不需要修改角色权限**。

## 前端设计

### 静态资源改动

`internal/web/assets.go` 的 `//go:embed` 列表与 `Read()` 的 `switch` 新增：
- `leaderboard.html`、`leaderboard.js`
- `self.html`、`self.js`
- `vendor/tailwind.js`（`text/javascript`）

`internal/httpapi/handler.go` 的 `serveAsset` 路由新增：
```go
case "/leaderboard": name = "leaderboard.html"
case "/self":         name = "self.html"
```

### 共享导航

三个页面各自复制一份导航 HTML（无模板引擎，手动同步）：
```html
<nav class="border-b border-slate-200 bg-white">
  <div class="mx-auto flex h-14 max-w-6xl items-center gap-6 px-4">
    <span class="font-semibold">Sub2API 用量查询</span>
    <a href="/" class="...">Key 查询</a>
    <a href="/leaderboard" class="...">排行榜</a>
    <a href="/self" class="...">自助查询</a>
  </div>
</nav>
```
当前页链接手写 `aria-current="page"` 高亮样式，不用 JS 判断。

### `internal/web/index.html` / `app.js` 改造

- 保留现有表单/表格/弹窗结构与 `id`，`app.js` 的状态管理与请求逻辑基本不变，仅将无 class 元素替换为 Tailwind utility class。
- `<head>` 引入本地 `<script src="/vendor/tailwind.js"></script>`。
- 保留一个精简版 `app.css`，只放 Tailwind utility 覆盖不了的规则（`.spinner` 动画、`dialog::backdrop`、`prefers-reduced-motion`）。
- 移动端列表：双 DOM 方案——桌面表格 `hidden md:table` + 移动卡片列表 `block md:hidden`，同一份数据渲染两套 DOM。

### `internal/web/leaderboard.html` + `leaderboard.js`（新建）

- 顶部 Top N 下拉（10/20/50，默认 10），四榜共用。
- 四个卡片网格 `grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-4`，卡片标题「今日榜 / 3天榜 / 7天榜 / 30天榜」，内部为排名列表（排名徽标 + 名称 + 掩码 key + 金额）。
- `loadLeaderboard()`：一次 `POST /api/leaderboard` 填充四卡；切换 Top N 重新请求。
- 某窗口无数据时该卡片显示「暂无数据」。

### `internal/web/self.html` + `self.js`（新建）

- 居中输入框 + 按钮，接受 Key 或名称。
- 命中：卡片展示掩码 key、名称、分组、额度进度条、状态徽标、今日用量 + 每日用量图表（复制一份精简版折线图渲染函数到 `self.js`，不与 `app.js` 共享模块，避免引入脚本加载顺序依赖）。
- 未命中：统一提示「未找到匹配的 Key」。
- 空输入：前端本地校验拦截，不发请求。

## 测试计划

- `internal/search/mask_test.go`：掩码边界（空串、恰好12位、13位、超长）。
- `internal/search/leaderboard_test.go`：`ValidateLimit` 合法值/非法值。
- `internal/postgres` 新增 repository 测试：复用现有 `test_harness_integration_test.go` 的 testcontainers 基建，验证排序、窗口边界、空数据。
- 手动浏览器验证：本地起临时 Postgres + 假数据，打开三个页面，验证排序/翻页/Top N 切换/自助查询命中与未命中/移动端双 DOM 切换。
- 回归：确认 `/api/search`、`/api/key-usage` 现有行为不受影响（未改动其 Go 逻辑，仅改 HTML/CSS 结构）。

## 已知取舍

- Tailwind 走本地内嵌脚本而非官方构建流程，包体积增加约 400KB，但换来部署环境零网络依赖。
- 4 个榜单窗口在一次请求内串行/并行查 4 次 SQL，数据量小时无感知，大数据量下是潜在优化点，本次不做提前优化。
- 移动端双 DOM 会略微增加 HTML 体积与维护成本，换取更好的移动端体验。
