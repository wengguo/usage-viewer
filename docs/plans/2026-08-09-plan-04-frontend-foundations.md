# Module 4: Frontend Foundations — Vendor Tailwind, Shared Nav, Main Page Rewrite

> Part of [`2026-08-09-tailwind-leaderboard-selflookup-plan.md`](2026-08-09-tailwind-leaderboard-selflookup-plan.md). No dependency on Modules 1-3 — this module is pure frontend and can, in principle, be done in parallel with them. It's sequenced after because the design doc groups backend-first, and Module 5/6's pages need this module's shared-nav pattern established first.

**Verification scope and its honest limit:** every change below was applied to the real files, built, `go vet`'d, unit-tested, and smoke-tested by starting the real binary against a real Postgres container and fetching the pages/assets with `curl` — confirming correct routes, content types, and that the served HTML/CSS/JS is exactly what's written here. `node --check` confirmed `app.js` has no syntax errors. **What this verification does NOT cover: actual visual rendering in a browser.** No headless browser or screenshot tool was available in this environment. The dual-DOM mobile/desktop split, Tailwind class correctness, and general visual polish need a human to open the page in an actual browser before this is considered done — flag this explicitly when executing this module, don't claim "it looks right" from curl output alone.

---

### Task 1: Embed and route `vendor/tailwind.js`

**Files:**
- Modify: `internal/web/assets.go`
- Modify: `internal/httpapi/handler.go`

**Step 1: Write the failing test**

Append to `internal/web/assets_test.go`:

```go
func TestVendoredTailwindScriptIsEmbeddedAndServedAsJavaScript(t *testing.T) {
	asset, err := Read("vendor/tailwind.js")
	if err != nil {
		t.Fatalf("Read(vendor/tailwind.js) error = %v", err)
	}
	if asset.ContentType != "text/javascript; charset=utf-8" {
		t.Fatalf("content type = %q", asset.ContentType)
	}
	if len(asset.Content) < 1000 {
		t.Fatalf("vendored script suspiciously small: %d bytes", len(asset.Content))
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/web/... -run TestVendoredTailwindScript -v`
Expected: `FAIL` — `ErrAssetNotFound` (the embed doesn't include `vendor/tailwind.js` yet).

**Step 3: Write minimal implementation**

In `internal/web/assets.go`:

```diff
-//go:embed index.html app.css app.js credentials.html credentials.css credentials.js favicon.svg
+//go:embed index.html app.css app.js credentials.html credentials.css credentials.js favicon.svg vendor/tailwind.js
 var assets embed.FS
```

```diff
-	case "app.js", "credentials.js":
+	case "app.js", "credentials.js", "vendor/tailwind.js":
 		contentType = "text/javascript; charset=utf-8"
```

In `internal/httpapi/handler.go`'s `serveAsset` method, add a case to the route switch (right before `default:`):

```diff
 	case "/favicon.svg":
 		name = "favicon.svg"
+	case "/vendor/tailwind.js":
+		name = "vendor/tailwind.js"
 	default:
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/web/... -run TestVendoredTailwindScript -v`
Expected: `PASS`.

Then a route-level check:
Run: `go test ./internal/httpapi/... -v`
Expected: all existing tests still pass (this change is additive to the route switch, nothing existing moves).

**Step 5: Commit**

```bash
git add internal/web/assets.go internal/web/assets_test.go internal/httpapi/handler.go
git commit -m "feat: embed and serve the vendored Tailwind runtime"
```

---

### Task 2: Rewrite `index.html` with Tailwind utilities and shared nav

**Files:**
- Modify: `internal/web/index.html` (full rewrite)

**Step 1: Write the failing test**

The existing `internal/web/assets_test.go`'s `TestEmbeddedAssetsAreCompleteAndSameOrigin` already asserts `index.html` contains specific strings (`"用量查询"`, `"Key 名称或 Key"`, `"/app.css"`, `"/app.js"`, all ten table headers, etc.) and forbids `http://`/`https://`/`//cdn`/`data:text/html` substrings anywhere in the file. This test doesn't need a new assertion — it needs to keep passing on the *new* HTML. Treat it as the acceptance test for this task.

Run (before rewriting): `go test ./internal/web/... -run TestEmbeddedAssetsAreCompleteAndSameOrigin -v`
Expected: `PASS` on the current file — this establishes the baseline you must not regress.

**Step 2: Rewrite the file**

Replace `internal/web/index.html` in full with:

```html
<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>用量查询</title>
  <link rel="icon" type="image/svg+xml" href="/favicon.svg">
  <script src="/vendor/tailwind.js"></script>
  <link rel="stylesheet" href="/app.css">
</head>
<body class="min-h-screen bg-slate-50 text-slate-900">
  <nav class="border-b border-slate-200 bg-white">
    <div class="mx-auto flex h-14 max-w-6xl items-center gap-6 px-4">
      <span class="font-semibold">Sub2API 用量查询</span>
      <a href="/" aria-current="page" class="text-sm font-medium text-teal-700">Key 查询</a>
      <a href="/leaderboard" class="text-sm font-medium text-slate-600 hover:text-teal-700">排行榜</a>
      <a href="/self" class="text-sm font-medium text-slate-600 hover:text-teal-700">自助查询</a>
      <a class="settings-link ml-auto text-sm font-medium text-teal-700 hover:underline" href="/credentials" title="数据库连接设置" hidden>设置</a>
    </div>
  </nav>

  <main class="mx-auto max-w-6xl px-4 py-8">
    <div class="search-workspace">
      <form class="search-form" id="search-form" action="/api/search" method="post" novalidate>
        <label id="query-label" for="query" class="mb-2 block text-sm font-semibold text-slate-600">Key 名称或 Key</label>
        <div class="search-row flex gap-2">
          <input id="query" type="text" autocomplete="off" autocapitalize="off" spellcheck="false" placeholder="请输入 Key 名称或 Key 值" aria-describedby="search-status"
            class="h-11 w-full max-w-2xl min-w-0 rounded-lg border border-slate-300 bg-white px-3 text-slate-900">
          <button id="search-button" type="submit"
            class="inline-flex h-11 w-32 flex-none items-center justify-center gap-2 rounded-lg border border-teal-700 bg-teal-700 font-semibold text-white disabled:cursor-wait disabled:opacity-70">
            <span class="spinner" id="search-spinner" aria-hidden="true" hidden></span>
            <span id="search-button-label">查找</span>
          </button>
        </div>
      </form>

      <div class="status-region min-h-[3rem] py-3 text-sm text-slate-600" id="search-status" role="status" aria-live="polite"></div>

      <section class="results mt-4" id="results" aria-busy="false" hidden>
        <h2 id="result-count" class="mb-4 text-xl font-semibold"></h2>

        <div class="hidden overflow-x-auto rounded-lg border border-slate-200 bg-white md:block" id="table-scroll">
          <table class="w-full min-w-[1200px] border-collapse">
            <thead><tr>
              <th scope="col" class="border-b border-slate-200 p-3 text-left text-sm font-semibold text-slate-600">名称</th>
              <th scope="col" class="border-b border-slate-200 p-3 text-left text-sm font-semibold text-slate-600">分组</th>
              <th scope="col" class="border-b border-slate-200 p-3 text-left text-sm font-semibold text-slate-600">当前并发</th>
              <th id="today-cost-header" scope="col" aria-sort="none" class="border-b border-slate-200 p-3 text-left">
                <button class="sort-button inline-flex items-center gap-1.5 text-sm font-semibold text-slate-600 hover:text-teal-700" id="sort-today-cost" type="button" data-label="今日用量">今日用量 <span class="sort-direction text-xs font-medium text-slate-400">默认</span></button>
              </th>
              <th id="total-30d-cost-header" scope="col" aria-sort="none" class="border-b border-slate-200 p-3 text-left">
                <button class="sort-button inline-flex items-center gap-1.5 text-sm font-semibold text-slate-600 hover:text-teal-700" id="sort-total-30d-cost" type="button" data-label="近30天用量">近30天用量 <span class="sort-direction text-xs font-medium text-slate-400">默认</span></button>
              </th>
              <th scope="col" class="border-b border-slate-200 p-3 text-left text-sm font-semibold text-slate-600">额度已用 / 总额度</th>
              <th scope="col" class="border-b border-slate-200 p-3 text-left text-sm font-semibold text-slate-600">上次使用时间</th>
              <th scope="col" class="border-b border-slate-200 p-3 text-left text-sm font-semibold text-slate-600">过期时间</th>
              <th scope="col" class="border-b border-slate-200 p-3 text-left text-sm font-semibold text-slate-600">状态</th>
              <th scope="col" class="border-b border-slate-200 p-3 text-left text-sm font-semibold text-slate-600">创建时间</th>
              <th scope="col" class="border-b border-slate-200 p-3 text-left text-sm font-semibold text-slate-600">每日用量</th>
            </tr></thead>
            <tbody id="key-body"></tbody>
          </table>
        </div>

        <div class="block space-y-3 md:hidden" id="key-cards"></div>

        <nav class="pagination mt-4 flex items-center justify-between gap-3 text-sm text-slate-600 md:justify-end" aria-label="Key 列表分页">
          <button class="pagination-button h-9 min-w-[76px] rounded-md border border-teal-700 px-3 font-semibold text-teal-700 disabled:cursor-wait disabled:opacity-70" id="previous-page" type="button">上一页</button>
          <span id="page-status" aria-live="polite"></span>
          <button class="pagination-button h-9 min-w-[76px] rounded-md border border-teal-700 px-3 font-semibold text-teal-700 disabled:cursor-wait disabled:opacity-70" id="next-page" type="button">下一页</button>
        </nav>
      </section>
    </div>
  </main>

  <dialog class="usage-dialog w-[min(820px,calc(100%-2rem))] max-h-[80vh] overflow-y-auto rounded-xl border border-slate-200 bg-white p-0" id="usage-dialog">
    <div class="flex items-center justify-between border-b border-slate-200 px-5 py-4">
      <h2 id="dialog-title" class="text-lg font-semibold"></h2>
      <button class="dialog-close flex h-9 w-9 items-center justify-center rounded-lg text-xl text-slate-600 hover:bg-slate-100" id="dialog-close" type="button" aria-label="关闭">×</button>
    </div>
    <div class="flex items-center gap-3 px-5 pt-4 text-sm font-semibold text-slate-600">
      <label for="dialog-days">天数</label>
      <select id="dialog-days" class="h-9 rounded-lg border border-slate-300 bg-white px-2 text-slate-900">
        <option value="7">7 天</option>
        <option value="30" selected>30 天</option>
        <option value="90">90 天</option>
      </select>
    </div>
    <div class="chart-wrap px-5 pt-4" id="chart-wrap"></div>
    <div class="dialog-table-wrap mx-5 my-2 max-h-[40vh] overflow-y-auto border-y border-slate-200" id="dialog-table-wrap">
      <table class="w-full">
        <thead><tr>
          <th scope="col" class="p-3 text-left text-sm font-semibold text-slate-600">日期</th>
          <th scope="col" class="p-3 text-left text-sm font-semibold text-slate-600">消耗费用 (USD)</th>
        </tr></thead>
        <tbody id="daily-body"></tbody>
      </table>
    </div>
    <div class="dialog-status min-h-[2rem] px-5 pb-4 text-sm text-slate-600" id="dialog-status" role="status" aria-live="polite"></div>
  </dialog>

  <script src="/app.js" defer></script>
</body>
</html>
```

Every `id` from the original file is preserved exactly (`search-form`, `query`, `search-button`, `search-spinner`, `search-status`, `results`, `result-count`, `table-scroll`, `key-body`, `sort-today-cost`, `sort-total-30d-cost`, `today-cost-header`, `total-30d-cost-header`, `previous-page`, `next-page`, `page-status`, `usage-dialog`, `dialog-title`, `dialog-close`, `dialog-days`, `chart-wrap`, `dialog-table-wrap`, `daily-body`, `dialog-status`) — `app.js` doesn't need to change how it looks anything up. One new element was added: `id="key-cards"` — a mobile-only card container that didn't exist before (see Task 4).

**Step 3: Run test to verify it passes**

Run: `go test ./internal/web/... -run TestEmbeddedAssetsAreCompleteAndSameOrigin -v`
Expected: `PASS`. All ten required strings from the table headers, plus `"用量查询"`, `"Key 名称或 Key"`, `"请输入 Key 名称或 Key 值"`, `"查找"`, `"/app.css"`, `"/app.js"`, `"sort-today-cost"` survive the rewrite — they were verified present in the file above.

Run: `go test ./internal/httpapi/... -run TestEmbeddedApplicationAssetsUseExactGetOnlyRoutes -v`
Expected: `PASS` — the `contains: "用量查询"` assertion for `GET /` still matches.

**Step 4: Commit**

```bash
git add internal/web/index.html
git commit -m "feat: rewrite index.html with Tailwind utilities and shared nav"
```

---

### Task 3: Trim `app.css` to only what Tailwind can't express

**Files:**
- Modify: `internal/web/app.css` (full rewrite)
- Modify: `internal/web/assets_test.go`
- Modify: `internal/httpapi/handler_test.go`

**Step 1: Write the failing test**

This is a case where you fix the test *as* the task, because the old test encoded assumptions (specific hex colors, a `639px` breakpoint) that the redesign deliberately removes. Change the assertion in `internal/web/assets_test.go`:

```diff
-		{name: "app.css", contentType: "text/css; charset=utf-8", required: []string{"#f7f8fa", "#0f766e", "@media (max-width: 639px)", "@media (prefers-reduced-motion: reduce)"}},
+		{name: "app.css", contentType: "text/css; charset=utf-8", required: []string{".spinner", "@keyframes spin", "usage-dialog::backdrop", "@media (prefers-reduced-motion: reduce)"}},
```

And in `internal/httpapi/handler_test.go`:

```diff
-		{method: http.MethodGet, path: "/app.css", wantStatus: http.StatusOK, contentType: "text/css; charset=utf-8", contains: ".workspace"},
+		{method: http.MethodGet, path: "/app.css", wantStatus: http.StatusOK, contentType: "text/css; charset=utf-8", contains: ".spinner"},
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/web/... ./internal/httpapi/... -v 2>&1 | grep -A2 "app.css\|app\\.css"`
Expected: `FAIL` — the new assertions look for strings that don't exist in the *old* `app.css` yet (this is the inverted TDD direction: the test now describes the target file, and it fails until Step 3 rewrites the file to match).

**Step 3: Rewrite `app.css`**

Replace `internal/web/app.css` in full with:

```css
/* Utility classes handle layout, color, and spacing (see index.html). This
   file only holds behavior Tailwind utilities can't express: the spinner
   keyframe animation, the native <dialog> backdrop, and the
   reduced-motion override. */

[hidden] { display: none !important; }

.spinner { animation: spin 0.8s linear infinite; }
@keyframes spin { to { transform: rotate(360deg); } }

.usage-dialog::backdrop { background: rgb(23 32 42 / 45%); }

@media (prefers-reduced-motion: reduce) { .spinner { animation: none; } }
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/web/... ./internal/httpapi/... -v`
Expected: `PASS` for every test in both packages.

**Step 5: Commit**

```bash
git add internal/web/app.css internal/web/assets_test.go internal/httpapi/handler_test.go
git commit -m "feat: trim app.css to only what Tailwind utilities can't express"
```

---

### Task 4: Dual-DOM mobile cards in `app.js`

**Files:**
- Modify: `internal/web/app.js`

Recall the design decision from the design doc: desktop shows the `<table>` (`hidden md:block` on its wrapper, now present in `index.html`), mobile shows a separate card list (`block md:hidden`, the new `#key-cards` div). Both are populated from the same `state.results` — this task wires that up.

**Step 1: Write the failing test**

The existing `internal/web/assets_test.go` tests (`TestBrowserScriptAvoidsPersistenceAndUnsafeHTMLSinks`, `TestKeySurfaceUsesExplicitApprovedFields`) already scan `app.js` for forbidden APIs and required safe-rendering primitives (`document.createElement`, `appendChild`, etc.) and required key-surface fields (`currentConcurrency`, `todayCost`, etc.). These must keep passing — that's the regression guard for this task. There's no new assertion to add; the acceptance bar is "these two tests still pass, and manually reading the diff confirms the mobile card path reuses the same `state.results` and the same formatting helpers as the table path" (no separate data-fetching or formatting logic — that would be the actual bug this design guards against: two render paths drifting apart).

Run (before editing): `go test ./internal/web/... -run "TestBrowserScriptAvoidsPersistenceAndUnsafeHTMLSinks|TestKeySurfaceUsesExplicitApprovedFields" -v`
Expected: `PASS` on the current file — baseline.

**Step 2: Apply the edits**

Four changes to `internal/web/app.js`, in order:

**2a.** Add the `key-cards` element reference right after `keyBody`:

```diff
   const keyBody = byID('key-body');
+  const keyCards = byID('key-cards');
```

**2b.** Make `clearRows` clear both containers:

```diff
   const clearRows = () => {
     while (keyBody.firstChild) keyBody.removeChild(keyBody.firstChild);
+    while (keyCards.firstChild) keyCards.removeChild(keyCards.firstChild);
   };
```

**2c.** Switch `addCell`'s class-name scheme from the removed `.numeric`/`.breakable` CSS classes to inline Tailwind utility classes (these classes used to be defined in the old `app.css`; that file no longer defines them after Task 3, so this must change or the cells silently lose their styling):

```diff
-  const addCell = (row, label, value, className = '') => {
+  const addCell = (row, label, value, kind = '') => {
     const cell = document.createElement('td');
     cell.dataset.label = label;
     cell.textContent = value;
-    if (className) cell.className = className;
+    cell.className = 'border-b border-slate-200 p-3 align-top' +
+      (kind === 'numeric' ? ' whitespace-nowrap tabular-nums' : kind === 'breakable' ? ' max-w-[240px] break-words' : '');
     row.appendChild(cell);
   };
```

Every existing call site (`addCell(row, '名称', item.name, 'breakable')` etc., in both `renderResults` and `openUsageDialog`'s daily-usage table renderer) keeps working unchanged — the third argument's two string values (`'numeric'`, `'breakable'`) are the same, only what `addCell` does with them changes.

**2d.** In `renderResults`, give the desktop "每日用量" button real Tailwind classes (it used to rely on the now-removed `.row-action` CSS rule), call the new `renderKeyCards()` at the end, and add `renderKeyCards` plus its `addCardRow` helper right after `renderResults`:

```diff
       const button = document.createElement('button');
       button.type = 'button';
-      button.className = 'row-action';
+      button.className = 'row-action inline-flex h-8 min-w-[76px] items-center justify-center rounded-md border border-teal-700 px-3 text-sm font-semibold text-teal-700 hover:bg-teal-700 hover:text-white';
       button.textContent = '每日用量';
       button.addEventListener('click', () => openUsageDialog(item.id, item.name));
       actionCell.appendChild(button);
       row.appendChild(actionCell);
       keyBody.appendChild(row);
     });
+    renderKeyCards();
     resultsRegion.hidden = false;
   };
+
+  const addCardRow = (card, label, value) => {
+    const row = document.createElement('div');
+    row.className = 'grid grid-cols-[minmax(96px,40%)_1fr] gap-3 py-1 text-sm';
+    const labelCell = document.createElement('div');
+    labelCell.className = 'font-semibold text-slate-600';
+    labelCell.textContent = label;
+    const valueCell = document.createElement('div');
+    valueCell.className = 'break-words';
+    valueCell.textContent = value;
+    row.appendChild(labelCell);
+    row.appendChild(valueCell);
+    card.appendChild(row);
+  };
+
+  const renderKeyCards = () => {
+    while (keyCards.firstChild) keyCards.removeChild(keyCards.firstChild);
+    state.results.forEach((item) => {
+      const card = document.createElement('article');
+      card.className = 'rounded-lg border border-slate-200 bg-white p-4';
+      addCardRow(card, '名称', item.name);
+      addCardRow(card, '分组', item.groupName || '无分组');
+      addCardRow(card, '当前并发', String(item.currentConcurrency));
+      addCardRow(card, '今日用量', `$${formatCost(item.todayCost)}`);
+      addCardRow(card, '近30天用量', `$${formatCost(item.total30dCost)}`);
+      addCardRow(card, '额度已用 / 总额度', `${formatCost(item.quotaUsed)} / ${formatQuota(item.quota)}`);
+      addCardRow(card, '上次使用时间', formatTimestamp(item.lastUsedAt));
+      addCardRow(card, '过期时间', formatTimestamp(item.expiresAt));
+      addCardRow(card, '状态', item.status);
+      addCardRow(card, '创建时间', formatTimestamp(item.createdAt));
+      const button = document.createElement('button');
+      button.type = 'button';
+      button.className = 'row-action mt-3 inline-flex h-8 w-full items-center justify-center rounded-md border border-teal-700 text-sm font-semibold text-teal-700 hover:bg-teal-700 hover:text-white';
+      button.textContent = '每日用量';
+      button.addEventListener('click', () => openUsageDialog(item.id, item.name));
+      card.appendChild(button);
+      keyCards.appendChild(card);
+    });
+  };
```

Note `renderKeyCards` calls `clearRows`'s card-clearing logic again at its own start (`while (keyCards.firstChild)...`) — this is intentionally redundant with `clearRows` (called earlier in `loadSearch`, before `renderResults` runs) so that `renderKeyCards` is safe to call standalone if it's ever reused elsewhere, at the cost of one harmless extra empty loop per render. Not worth removing for a two-line loop.

**Step 3: Run test to verify it passes**

Run: `node --check internal/web/app.js`
Expected: no output (exit 0) — confirms no JS syntax errors before running Go tests.

Run: `go test ./internal/web/... -v`
Expected: `PASS` for every test, including the two named in Step 1.

**Step 4: Full package checkpoint**

Run: `go build ./... && go vet ./... && go test ./... -count=1`
Expected: everything passes.

**Step 5: Commit**

```bash
git add internal/web/app.js
git commit -m "feat: render mobile key cards alongside the desktop table"
```

---

### Task 5: Manual end-to-end smoke test (same shape as Module 3's Task 5)

**Step 1: Stand up a seeded Postgres and start the real binary**

```bash
docker run -d --name mod04-smoke -e POSTGRES_USER=sub2api -e POSTGRES_PASSWORD=testpass -e POSTGRES_DB=sub2api -p 15987:5432 postgres:16-alpine
until docker exec mod04-smoke pg_isready -U sub2api >/dev/null 2>&1; do sleep 1; done
docker exec -i mod04-smoke psql -U sub2api -d sub2api <<'SQL'
CREATE TABLE public.groups (id bigint PRIMARY KEY, name varchar NOT NULL);
CREATE TABLE public.api_keys (
  id bigint PRIMARY KEY, key varchar NOT NULL, name varchar NOT NULL,
  group_id bigint, quota numeric NOT NULL, quota_used numeric NOT NULL,
  last_used_at timestamptz, expires_at timestamptz, status varchar NOT NULL,
  created_at timestamptz NOT NULL, deleted_at timestamptz
);
CREATE TABLE public.usage_logs (
  id bigint PRIMARY KEY, api_key_id bigint, actual_cost numeric NOT NULL, created_at timestamptz NOT NULL
);
INSERT INTO public.groups VALUES (1, 'demo-group');
INSERT INTO public.api_keys (id, key, name, group_id, quota, quota_used, status, created_at) VALUES
  (1, 'sk-mod4-verify-alpha01', 'mod4-alpha', 1, 100, 0, 'active', now());
SQL

go build -trimpath -o dist/sub2api-usage-viewer ./cmd/viewer
DATABASE_HOST=127.0.0.1 DATABASE_PORT=15987 DATABASE_USER=sub2api DATABASE_PASSWORD=testpass DATABASE_DBNAME=sub2api DATABASE_SSLMODE=disable \
  ./dist/sub2api-usage-viewer &
sleep 1.5
```

Before this, check for a stale process on port 8081 (`lsof -iTCP:8081 -sTCP:LISTEN -n -P`) — this exact issue tripped up verification twice already in Modules 3 and 4. Kill it first if found.

**Step 2: Check routes and content over curl**

```bash
curl -s --noproxy '*' http://127.0.0.1:8081/ | head -20                     # nav + Tailwind script tag present
curl -s --noproxy '*' -o /dev/null -w '%{http_code} %{content_type}\n' http://127.0.0.1:8081/vendor/tailwind.js   # 200, text/javascript
curl -s --noproxy '*' http://127.0.0.1:8081/app.css                          # only the four trimmed rules
curl -s --noproxy '*' -X POST http://127.0.0.1:8081/api/search -H 'Content-Type: application/json' -H 'Origin: http://127.0.0.1:8081' --data-raw '{"targetType":"key"}'
```

Expected: 200s throughout, `/api/search` still returns `mod4-alpha`'s row — the frontend rewrite touched no backend contract.

**Step 3: The part curl cannot do — flag it, don't skip it**

Open `http://127.0.0.1:8081/` in an actual browser (not curl) and confirm:
- Tailwind classes actually apply (page has visible styling, not raw unstyled HTML)
- The nav row shows three links with the current page highlighted
- Resizing the window below Tailwind's `md` breakpoint (768px) swaps the table for stacked cards, not both or neither
- The search flow, sort buttons, pagination, and daily-usage dialog all still function

This step was **not performed** while writing this plan — there was no browser automation tool available. Whoever executes this plan should either do this manually or explicitly ask the user to.

**Step 4: Tear down**

```bash
kill %1
docker rm -f mod04-smoke
git checkout -- dist/sub2api-usage-viewer
```

---

### Module 4 checkpoint

```bash
go build ./... && go vet ./...
gofmt -l internal/httpapi/*.go   # only pre-existing unrelated files (if any) should print
node --check internal/web/app.js
go test ./... -count=1
```
Expected: everything passes, no gofmt issues on files this module touched, no JS syntax errors.

Next: [`2026-08-09-plan-05-frontend-leaderboard-page.md`](2026-08-09-plan-05-frontend-leaderboard-page.md)
