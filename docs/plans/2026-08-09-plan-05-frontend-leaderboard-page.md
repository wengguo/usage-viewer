# Module 5: Frontend Leaderboard Page

> Part of [`2026-08-09-tailwind-leaderboard-selflookup-plan.md`](2026-08-09-tailwind-leaderboard-selflookup-plan.md). Requires Module 3 (backend `/api/leaderboard` endpoint) and Module 4 (Tailwind vendoring, shared-nav pattern) complete.

**Verification scope, same honest limit as Module 4:** built the real binary, seeded a real Postgres with usage spanning multiple windows (one key with usage 5 days ago — visible in the 7d/30d columns only, not 1d/3d), started the server, and fetched `/leaderboard`, `/leaderboard.js`, and `POST /api/leaderboard` with `curl`. Confirmed the JSON response ranks correctly and windows filter correctly by natural-day boundary. Confirmed `node --check` passes on `leaderboard.js`. **Did not** render this in a browser — the visual card layout, hover states, and responsive grid collapse (4 columns → 2 → 1) need a human to actually look at before this is done.

---

### Task 1: `internal/web/leaderboard.html`

**Files:**
- Create: `internal/web/leaderboard.html`

**Step 1: Write the failing test**

Create `internal/web/leaderboard_test.go`:

```go
package web

import (
	"strings"
	"testing"
)

func TestLeaderboardPageIsCompleteAndSameOrigin(t *testing.T) {
	asset, err := Read("leaderboard.html")
	if err != nil {
		t.Fatalf("Read(leaderboard.html) error = %v", err)
	}
	if asset.ContentType != "text/html; charset=utf-8" {
		t.Fatalf("content type = %q", asset.ContentType)
	}
	content := string(asset.Content)
	for _, required := range []string{
		"消耗排行榜", "/leaderboard.js", "/vendor/tailwind.js", "/app.css",
		"limit-select", "leaderboard-grid", "leaderboard-status",
		`data-window="1d"`, `data-window="3d"`, `data-window="7d"`, `data-window="30d"`,
		"排行榜", "自助查询", "Key 查询",
	} {
		if !strings.Contains(content, required) {
			t.Errorf("leaderboard.html missing %q", required)
		}
	}
	for _, forbidden := range []string{"http://", "https://", "//cdn", "data:text/html"} {
		if strings.Contains(strings.ToLower(content), forbidden) {
			t.Errorf("leaderboard.html contains external or inline URL marker %q", forbidden)
		}
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/web/... -run TestLeaderboardPageIsCompleteAndSameOrigin -v`
Expected: `FAIL` — `ErrAssetNotFound` (file doesn't exist yet).

**Step 3: Create the file**

```html
<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>消耗排行榜 - 用量查询</title>
  <link rel="icon" type="image/svg+xml" href="/favicon.svg">
  <script src="/vendor/tailwind.js"></script>
  <link rel="stylesheet" href="/app.css">
</head>
<body class="min-h-screen bg-slate-50 text-slate-900">
  <nav class="border-b border-slate-200 bg-white">
    <div class="mx-auto flex h-14 max-w-6xl items-center gap-6 px-4">
      <span class="font-semibold">Sub2API 用量查询</span>
      <a href="/" class="text-sm font-medium text-slate-600 hover:text-teal-700">Key 查询</a>
      <a href="/leaderboard" aria-current="page" class="text-sm font-medium text-teal-700">排行榜</a>
      <a href="/self" class="text-sm font-medium text-slate-600 hover:text-teal-700">自助查询</a>
    </div>
  </nav>

  <main class="mx-auto max-w-6xl px-4 py-8">
    <div class="mb-6 flex flex-wrap items-center gap-3">
      <h1 class="text-xl font-semibold">消耗排行榜</h1>
      <label for="limit-select" class="ml-auto text-sm font-semibold text-slate-600">显示数量</label>
      <select id="limit-select" class="h-9 rounded-lg border border-slate-300 bg-white px-2 text-slate-900">
        <option value="10" selected>Top 10</option>
        <option value="20">Top 20</option>
        <option value="50">Top 50</option>
      </select>
    </div>

    <div class="status-region min-h-[3rem] py-3 text-sm text-slate-600" id="leaderboard-status" role="status" aria-live="polite"></div>

    <div class="grid grid-cols-1 gap-4 md:grid-cols-2 lg:grid-cols-4" id="leaderboard-grid" hidden>
      <section class="rounded-lg border border-slate-200 bg-white p-4" data-window="1d">
        <h2 class="mb-3 text-base font-semibold">今日榜</h2>
        <ol class="space-y-2" data-list></ol>
        <p class="hidden text-sm text-slate-500" data-empty>暂无数据</p>
      </section>
      <section class="rounded-lg border border-slate-200 bg-white p-4" data-window="3d">
        <h2 class="mb-3 text-base font-semibold">3 天榜</h2>
        <ol class="space-y-2" data-list></ol>
        <p class="hidden text-sm text-slate-500" data-empty>暂无数据</p>
      </section>
      <section class="rounded-lg border border-slate-200 bg-white p-4" data-window="7d">
        <h2 class="mb-3 text-base font-semibold">7 天榜</h2>
        <ol class="space-y-2" data-list></ol>
        <p class="hidden text-sm text-slate-500" data-empty>暂无数据</p>
      </section>
      <section class="rounded-lg border border-slate-200 bg-white p-4" data-window="30d">
        <h2 class="mb-3 text-base font-semibold">30 天榜</h2>
        <ol class="space-y-2" data-list></ol>
        <p class="hidden text-sm text-slate-500" data-empty>暂无数据</p>
      </section>
    </div>
  </main>

  <script src="/leaderboard.js" defer></script>
</body>
</html>
```

Design notes baked into this markup:
- The Top N `<select>` is the single shared control from the design doc (not four independent selects) — `leaderboard.js` reads its value once per load and applies it to all four windows via one API call.
- Each `<section data-window="...">` holds an `<ol data-list>` (populated by JS) and a `<p data-empty>` (shown/hidden depending on whether that window has any entries) — this is how the "某窗口无数据时该卡片显示'暂无数据'" design requirement gets implemented without a separate template per state.
- Grid responsiveness is `grid-cols-1` (mobile) → `md:grid-cols-2` → `lg:grid-cols-4`, matching the design doc's "四个卡片网格" spec.

**Step 4: Run test to verify it passes**

Requires Task 2 to also be done first, since `Read("leaderboard.html")` needs the embed wired up (Task 3). Come back to this after Task 3; running it now will still fail with `ErrAssetNotFound` even though the file exists on disk, because `internal/web/assets.go`'s `//go:embed` directive doesn't know about it yet.

**Step 5: Commit** (deferred — see Task 3's commit, which covers this file plus the embed wiring together, since neither compiles/tests meaningfully alone)

---

### Task 2: `internal/web/leaderboard.js`

**Files:**
- Create: `internal/web/leaderboard.js`

**Step 1: Write the failing test**

Append to `internal/web/leaderboard_test.go`:

```go
func TestLeaderboardScriptIsSafeAndFetchesTheRightEndpoint(t *testing.T) {
	asset, err := Read("leaderboard.js")
	if err != nil {
		t.Fatalf("Read(leaderboard.js) error = %v", err)
	}
	if asset.ContentType != "text/javascript; charset=utf-8" {
		t.Fatalf("content type = %q", asset.ContentType)
	}
	content := string(asset.Content)
	for _, required := range []string{
		"fetch('/api/leaderboard'", "keyMasked", "actualCost", "limit-select",
		"document.createElement", "appendChild",
	} {
		if !strings.Contains(content, required) {
			t.Errorf("leaderboard.js missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"inner" + "HTML", "insertAdjacent" + "HTML", "local" + "Storage", "session" + "Storage",
		"document." + "cookie", "console.",
	} {
		if strings.Contains(content, forbidden) {
			t.Errorf("leaderboard.js contains forbidden API %q", forbidden)
		}
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/web/... -run TestLeaderboardScriptIsSafeAndFetchesTheRightEndpoint -v`
Expected: `FAIL` — `ErrAssetNotFound`.

**Step 3: Create the file**

```javascript
(() => {
  'use strict';

  const byID = (id) => document.getElementById(id);
  const limitSelect = byID('limit-select');
  const statusRegion = byID('leaderboard-status');
  const grid = byID('leaderboard-grid');

  const windows = ['1d', '3d', '7d', '30d'];
  const sections = new Map(windows.map((window) => [window, document.querySelector(`[data-window="${window}"]`)]));

  const setStatus = (kind, title) => {
    statusRegion.className = 'status-region min-h-[3rem] py-3 text-sm text-slate-600';
    if (kind === 'error') {
      statusRegion.className += ' border-l-4 border-red-600 bg-red-50 px-4 text-red-700';
    }
    statusRegion.setAttribute('role', kind === 'error' ? 'alert' : 'status');
    statusRegion.textContent = title;
  };

  const hasExactKeys = (value, keys) => value && typeof value === 'object' && !Array.isArray(value) &&
    Object.keys(value).length === keys.length && keys.every((key) => Object.prototype.hasOwnProperty.call(value, key));
  const isCost = (value) => typeof value === 'string' && /^(0|[1-9]\d*)(\.\d+)?$/.test(value);
  const entryKeys = ['rank', 'keyMasked', 'name', 'groupName', 'actualCost'];
  const validEntry = (item) => hasExactKeys(item, entryKeys) &&
    typeof item.rank === 'number' && Number.isInteger(item.rank) && item.rank > 0 &&
    typeof item.keyMasked === 'string' && typeof item.name === 'string' &&
    typeof item.groupName === 'string' && isCost(item.actualCost);
  const validPayload = (payload) => hasExactKeys(payload, ['limit', 'windows']) &&
    typeof payload.limit === 'number' && Number.isInteger(payload.limit) &&
    payload.windows && typeof payload.windows === 'object' && !Array.isArray(payload.windows) &&
    windows.every((window) => Array.isArray(payload.windows[window]) && payload.windows[window].every(validEntry));

  const formatCost = (value) => {
    const num = +value;
    return Number.isFinite(num) ? num.toFixed(2) : value;
  };

  const renderWindow = (window, entries) => {
    const section = sections.get(window);
    const list = section.querySelector('[data-list]');
    const empty = section.querySelector('[data-empty]');
    while (list.firstChild) list.removeChild(list.firstChild);
    if (entries.length === 0) {
      empty.hidden = false;
      return;
    }
    empty.hidden = true;
    entries.forEach((entry) => {
      const item = document.createElement('li');
      item.className = 'flex items-center gap-3 rounded-md border border-slate-100 p-2';
      const rank = document.createElement('span');
      rank.className = 'flex h-6 w-6 flex-none items-center justify-center rounded-full bg-slate-100 text-xs font-semibold text-slate-600';
      rank.textContent = String(entry.rank);
      const detail = document.createElement('div');
      detail.className = 'min-w-0 flex-1';
      const name = document.createElement('div');
      name.className = 'truncate text-sm font-semibold';
      name.textContent = entry.name;
      const meta = document.createElement('div');
      meta.className = 'truncate text-xs text-slate-500';
      meta.textContent = `${entry.groupName || '无分组'} · ${entry.keyMasked}`;
      detail.appendChild(name);
      detail.appendChild(meta);
      const cost = document.createElement('span');
      cost.className = 'flex-none text-sm font-semibold tabular-nums text-teal-700';
      cost.textContent = `$${formatCost(entry.actualCost)}`;
      item.appendChild(rank);
      item.appendChild(detail);
      item.appendChild(cost);
      list.appendChild(item);
    });
  };

  let requestSequence = 0;

  const loadLeaderboard = async () => {
    const limit = parseInt(limitSelect.value, 10) || 10;
    const sequence = ++requestSequence;
    setStatus('loading', '正在加载...');
    grid.hidden = true;
    try {
      const response = await fetch('/api/leaderboard', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ limit }),
      });
      if (sequence !== requestSequence) return;
      if (!response.ok) throw new Error('leaderboard failed');
      const payload = await response.json();
      if (!validPayload(payload)) throw new Error('invalid response');
      windows.forEach((window) => renderWindow(window, payload.windows[window]));
      grid.hidden = false;
      setStatus('ready', '');
    } catch (_) {
      setStatus('error', '排行榜加载失败，请重试');
    }
  };

  limitSelect.addEventListener('change', loadLeaderboard);
  loadLeaderboard();
})();
```

Design notes:
- No pagination sequence-guard beyond `requestSequence` is needed here (unlike `app.js`'s search, there's no abort/cancel path — a leaderboard reload is cheap and rare, triggered only by the Top N select changing). `requestSequence` alone prevents a slow earlier response from clobbering a faster later one if the user rapid-fires the select.
- `validPayload`/`validEntry` mirror the strict "reject anything not exactly the expected shape" pattern already used in `app.js`'s `validSearchPayload`/`validKeyResult` — this is a deliberate consistency choice, not a new pattern.
- `entry.keyMasked` is rendered via `textContent`, never `innerHTML` — the masked key is still attacker-influenceable data in principle (it echoes characters from `api_keys.key`), so it gets the same XSS-safe treatment as every other rendered field.

**Step 4: Run test to verify it passes**

Requires Task 3 (the embed wiring) — see that task's Step 4 for the combined test run.

**Step 5: Commit** (deferred to Task 3)

---

### Task 3: Wire the embed and the `/leaderboard` routes

**Files:**
- Modify: `internal/web/assets.go`
- Modify: `internal/httpapi/handler.go`

**Step 1: Apply the embed change**

```diff
-//go:embed index.html app.css app.js credentials.html credentials.css credentials.js favicon.svg
+//go:embed index.html app.css app.js credentials.html credentials.css credentials.js favicon.svg vendor/tailwind.js leaderboard.html leaderboard.js
 var assets embed.FS
```

If Module 4's Task 1 already added `vendor/tailwind.js` to this line, your starting point already has it — just append `leaderboard.html leaderboard.js` to the existing directive rather than reintroducing `vendor/tailwind.js` a second time.

```diff
 	switch name {
-	case "index.html", "credentials.html":
+	case "index.html", "credentials.html", "leaderboard.html":
 		contentType = "text/html; charset=utf-8"
 	case "app.css", "credentials.css":
 		contentType = "text/css; charset=utf-8"
-	case "app.js", "credentials.js":
+	case "app.js", "credentials.js", "vendor/tailwind.js", "leaderboard.js":
 		contentType = "text/javascript; charset=utf-8"
```

Same caveat: if Module 4 already added `vendor/tailwind.js` to the `app.js` case line, just add `, "leaderboard.js"` to whatever that line currently looks like.

**Step 2: Apply the route change**

In `internal/httpapi/handler.go`'s `serveAsset` switch, add two cases right after wherever Module 4's `/vendor/tailwind.js` case landed:

```diff
 	case "/vendor/tailwind.js":
 		name = "vendor/tailwind.js"
+	case "/leaderboard":
+		name = "leaderboard.html"
+	case "/leaderboard.js":
+		name = "leaderboard.js"
 	default:
```

**Step 3: Run both new tests to verify they pass**

Run: `go test ./internal/web/... -run TestLeaderboard -v`
Expected: `PASS` for `TestLeaderboardPageIsCompleteAndSameOrigin` and `TestLeaderboardScriptIsSafeAndFetchesTheRightEndpoint`.

**Step 4: Full package checkpoint**

Run: `go build ./... && go vet ./... && node --check internal/web/leaderboard.js && go test ./... -count=1`
Expected: everything passes.

**Step 5: Commit**

```bash
git add internal/web/leaderboard.html internal/web/leaderboard.js internal/web/leaderboard_test.go internal/web/assets.go internal/httpapi/handler.go
git commit -m "feat: add the leaderboard page, script, and routes"
```

---

### Task 4: Manual end-to-end smoke test

This exact sequence was run while writing this plan (with a temporary `NewFullHandler`/`main.go` wiring, since Module 3's backend wiring and this module's frontend wiring were verified independently before either was committed to the working tree — if you're executing this plan in order, Module 3 is already committed and you don't need to re-wire anything, just build and run).

**Step 1: Seed data spanning multiple windows**

```bash
docker run -d --name mod05-smoke -e POSTGRES_USER=sub2api -e POSTGRES_PASSWORD=testpass -e POSTGRES_DB=sub2api -p 15986:5432 postgres:16-alpine
until docker exec mod05-smoke pg_isready -U sub2api >/dev/null 2>&1; do sleep 1; done
docker exec -i mod05-smoke psql -U sub2api -d sub2api <<'SQL'
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
  (1, 'sk-mod5-verify-alpha01', 'mod5-alpha', 1, 100, 0, 'active', now()),
  (2, 'sk-mod5-verify-beta002', 'mod5-beta', 1, 100, 0, 'active', now());
INSERT INTO public.usage_logs (id, api_key_id, actual_cost, created_at) VALUES
  (1, 1, 3.30, now()),
  (2, 2, 7.70, now() - interval '5 days');
SQL
go build -trimpath -o dist/sub2api-usage-viewer ./cmd/viewer
DATABASE_HOST=127.0.0.1 DATABASE_PORT=15986 DATABASE_USER=sub2api DATABASE_PASSWORD=testpass DATABASE_DBNAME=sub2api DATABASE_SSLMODE=disable \
  ./dist/sub2api-usage-viewer &
sleep 1.5
```

**Step 2: Check the API's window boundary is correct**

```bash
curl -s --noproxy '*' -X POST http://127.0.0.1:8081/api/leaderboard -H 'Content-Type: application/json' -H 'Origin: http://127.0.0.1:8081' --data-raw '{}'
```

Expected (verified while writing this plan): `mod5-beta`'s $7.70 (5 days old) appears in `"7d"` and `"30d"` only — not `"1d"` or `"3d"`. `mod5-alpha`'s $3.30 (today) appears in all four. Both keys' `keyMasked` fields show the `sk-***` masked form, never the raw key.

**Step 3: The part curl cannot do**

Open `http://127.0.0.1:8081/leaderboard` in a browser and confirm the four cards render, the Top N select actually changes what's shown, and the grid collapses to 2 columns then 1 as the window narrows. **Not performed while writing this plan** — no browser automation was available. Flag this to whoever executes the plan, or ask the user to check.

**Step 4: Tear down**

```bash
kill %1
docker rm -f mod05-smoke
git checkout -- dist/sub2api-usage-viewer
```

---

### Module 5 checkpoint

```bash
go build ./... && go vet ./...
node --check internal/web/leaderboard.js
go test ./... -count=1
```
Expected: all pass.

Next: [`2026-08-09-plan-06-frontend-self-page.md`](2026-08-09-plan-06-frontend-self-page.md)
