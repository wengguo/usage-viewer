# Module 6: Frontend Self-Lookup Page

> Part of [`2026-08-09-tailwind-leaderboard-selflookup-plan.md`](2026-08-09-tailwind-leaderboard-selflookup-plan.md). Requires Module 3 (backend `/api/self-lookup` endpoint — **including the daily-usage fix described below**) and Module 4 (Tailwind vendoring, shared-nav pattern) complete.

**A design gap was found and fixed while writing this module — read this before anything else.** The original design doc said the self-lookup card should show a "每日用量图表" (daily usage chart), and Module 1/Module 3 as originally written returned a `SelfResult` with no daily-usage data and deliberately withheld the internal key id from the response (an intentional anti-enumeration decision). Those two facts are incompatible: without the id, the frontend has no way to call `/api/key-usage` itself, and `/api/key-usage` requires a `keyId` it will never have. This was caught by attempting to build this module and discovering the API had nothing for `self.js` to render a chart from.

**The fix (already applied to Module 1's and Module 3's plan documents — if you're reading this after they were written, they already reflect it):**
- `search.SelfResult` (Module 1, Task 3) gained a `DailyUsage []DailyUsagePoint` field.
- `serveSelfLookup` (Module 3, Task 3) now calls `application.dailyUsage.DailyUsage(ctx, id, 30)` — reusing the handler's *existing* `DailyUsageService` dependency (the same one `/api/key-usage` uses) — and attaches the result to `result.DailyUsage` before writing the response. The id is used only for that one internal call and never appears in the JSON body.

If you are executing Module 1 and Module 3 for the first time (not resuming from an already-completed checkout), you will get this fix automatically — it's already baked into those documents. This note exists only so nobody re-discovers the same gap independently.

**Verification scope, same honest limit as Modules 4 and 5:** built the real binary, seeded a real Postgres (one key with two `usage_logs` rows on different days, non-zero quota/quotaUsed, a future `expires_at`), started the server, fetched `/self` and `/self.js` with `curl`, and exercised `/api/self-lookup` three ways: exact key match, exact name match (same record), and a miss. All three matched expectations, including that `dailyUsage` came back with both real usage points. `node --check` passed on `self.js`. **Did not** render this in a browser — the card layout, quota progress bar fill, and the simplified chart's visual output need a human to look at.

---

### Task 1: `internal/web/self.html`

**Files:**
- Create: `internal/web/self.html`

**Step 1: Write the failing test**

Create `internal/web/self_test.go`:

```go
package web

import (
	"strings"
	"testing"
)

func TestSelfLookupPageIsCompleteAndSameOrigin(t *testing.T) {
	asset, err := Read("self.html")
	if err != nil {
		t.Fatalf("Read(self.html) error = %v", err)
	}
	if asset.ContentType != "text/html; charset=utf-8" {
		t.Fatalf("content type = %q", asset.ContentType)
	}
	content := string(asset.Content)
	for _, required := range []string{
		"自助查询", "/self.js", "/vendor/tailwind.js", "/app.css",
		"self-form", "credential", "self-card", "self-chart-wrap",
		"self-quota-bar", "self-quota-percent", "self-name", "self-key",
		"排行榜", "Key 查询",
	} {
		if !strings.Contains(content, required) {
			t.Errorf("self.html missing %q", required)
		}
	}
	for _, forbidden := range []string{"http://", "https://", "//cdn", "data:text/html"} {
		if strings.Contains(strings.ToLower(content), forbidden) {
			t.Errorf("self.html contains external or inline URL marker %q", forbidden)
		}
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/web/... -run TestSelfLookupPageIsCompleteAndSameOrigin -v`
Expected: `FAIL` — `ErrAssetNotFound`.

**Step 3: Create the file**

```html
<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>自助查询 - 用量查询</title>
  <link rel="icon" type="image/svg+xml" href="/favicon.svg">
  <script src="/vendor/tailwind.js"></script>
  <link rel="stylesheet" href="/app.css">
</head>
<body class="min-h-screen bg-slate-50 text-slate-900">
  <nav class="border-b border-slate-200 bg-white">
    <div class="mx-auto flex h-14 max-w-6xl items-center gap-6 px-4">
      <span class="font-semibold">Sub2API 用量查询</span>
      <a href="/" class="text-sm font-medium text-slate-600 hover:text-teal-700">Key 查询</a>
      <a href="/leaderboard" class="text-sm font-medium text-slate-600 hover:text-teal-700">排行榜</a>
      <a href="/self" aria-current="page" class="text-sm font-medium text-teal-700">自助查询</a>
    </div>
  </nav>

  <main class="mx-auto max-w-2xl px-4 py-8">
    <h1 class="mb-2 text-xl font-semibold">自助查询</h1>
    <p class="mb-6 text-sm text-slate-600">输入你自己的 Key 或名称，只查看这一条记录。</p>

    <form class="flex gap-2" id="self-form" novalidate>
      <label for="credential" class="sr-only">Key 或名称</label>
      <input id="credential" type="text" autocomplete="off" autocapitalize="off" spellcheck="false" placeholder="请输入 Key 或名称" aria-describedby="self-status"
        class="h-11 w-full min-w-0 rounded-lg border border-slate-300 bg-white px-3 text-slate-900">
      <button id="self-button" type="submit"
        class="inline-flex h-11 w-28 flex-none items-center justify-center gap-2 rounded-lg border border-teal-700 bg-teal-700 font-semibold text-white disabled:cursor-wait disabled:opacity-70">
        <span class="spinner" id="self-spinner" aria-hidden="true" hidden></span>
        <span id="self-button-label">查询</span>
      </button>
    </form>

    <div class="status-region min-h-[3rem] py-3 text-sm text-slate-600" id="self-status" role="status" aria-live="polite"></div>

    <article class="rounded-lg border border-slate-200 bg-white p-5" id="self-card" hidden>
      <div class="flex items-start justify-between gap-3">
        <div>
          <h2 class="text-lg font-semibold" id="self-name"></h2>
          <p class="text-sm text-slate-500" id="self-group"></p>
        </div>
        <span class="rounded-full bg-slate-100 px-3 py-1 text-xs font-semibold text-slate-600" id="self-status-badge"></span>
      </div>
      <dl class="mt-4 grid grid-cols-2 gap-3 text-sm">
        <div>
          <dt class="font-semibold text-slate-600">Key</dt>
          <dd class="tabular-nums" id="self-key"></dd>
        </div>
        <div>
          <dt class="font-semibold text-slate-600">今日用量</dt>
          <dd class="tabular-nums" id="self-today-cost"></dd>
        </div>
        <div>
          <dt class="font-semibold text-slate-600">额度已用 / 总额度</dt>
          <dd class="tabular-nums" id="self-quota"></dd>
        </div>
        <div>
          <dt class="font-semibold text-slate-600">过期时间</dt>
          <dd id="self-expires"></dd>
        </div>
      </dl>
      <div class="mt-4">
        <div class="mb-1 flex justify-between text-xs text-slate-500">
          <span>额度使用进度</span>
          <span id="self-quota-percent"></span>
        </div>
        <div class="h-2 overflow-hidden rounded-full bg-slate-100">
          <div class="h-full rounded-full bg-teal-700" id="self-quota-bar" style="width: 0%"></div>
        </div>
      </div>
      <h3 class="mt-5 mb-2 text-sm font-semibold text-slate-600">近 30 天用量</h3>
      <div class="chart-wrap" id="self-chart-wrap"></div>
    </article>
  </main>

  <script src="/self.js" defer></script>
</body>
</html>
```

Design notes:
- Input accepts either the full key or the name — no client-side hint about which one matched, matching the anti-enumeration stance from the design doc (the response doesn't say "matched by name" vs "matched by key" either).
- The quota progress bar (`#self-quota-bar`) is a plain empty `<div>` with `style="width: 0%"` as a safe default before JS runs — `self.js` sets the real width after a successful lookup.
- `#self-card` starts `hidden` and only un-hides on a successful match, so a stale card from a previous query never lingers visibly if a new query fails.

**Step 4: Run test to verify it passes**

Requires Task 3 (embed wiring). Come back after that task, same as Module 5's Task 1/3 relationship.

---

### Task 2: `internal/web/self.js`

**Files:**
- Create: `internal/web/self.js`

**Step 1: Write the failing test**

Append to `internal/web/self_test.go`:

```go
func TestSelfLookupScriptIsSafeAndFetchesTheRightEndpoint(t *testing.T) {
	asset, err := Read("self.js")
	if err != nil {
		t.Fatalf("Read(self.js) error = %v", err)
	}
	if asset.ContentType != "text/javascript; charset=utf-8" {
		t.Fatalf("content type = %q", asset.ContentType)
	}
	content := string(asset.Content)
	for _, required := range []string{
		"fetch('/api/self-lookup'", "keyMasked", "dailyUsage", "quotaUsed",
		"document.createElement", "appendChild", "status === 404",
	} {
		if !strings.Contains(content, required) {
			t.Errorf("self.js missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"inner" + "HTML", "insertAdjacent" + "HTML", "local" + "Storage", "session" + "Storage",
		"document." + "cookie", "console.",
	} {
		if strings.Contains(content, forbidden) {
			t.Errorf("self.js contains forbidden API %q", forbidden)
		}
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/web/... -run TestSelfLookupScriptIsSafeAndFetchesTheRightEndpoint -v`
Expected: `FAIL` — `ErrAssetNotFound`.

**Step 3: Create the file**

```javascript
(() => {
  'use strict';

  const byID = (id) => document.getElementById(id);
  const form = byID('self-form');
  const credentialInput = byID('credential');
  const button = byID('self-button');
  const spinner = byID('self-spinner');
  const statusRegion = byID('self-status');
  const card = byID('self-card');
  const nameEl = byID('self-name');
  const groupEl = byID('self-group');
  const statusBadge = byID('self-status-badge');
  const keyEl = byID('self-key');
  const todayCostEl = byID('self-today-cost');
  const quotaEl = byID('self-quota');
  const expiresEl = byID('self-expires');
  const quotaPercentEl = byID('self-quota-percent');
  const quotaBarEl = byID('self-quota-bar');
  const chartWrap = byID('self-chart-wrap');

  const setStatus = (kind, title) => {
    statusRegion.className = 'status-region min-h-[3rem] py-3 text-sm text-slate-600';
    if (kind === 'error') {
      statusRegion.className += ' border-l-4 border-red-600 bg-red-50 px-4 text-red-700';
    }
    statusRegion.setAttribute('role', kind === 'error' ? 'alert' : 'status');
    statusRegion.textContent = title;
  };

  const setBusy = (busy) => {
    credentialInput.disabled = busy;
    button.disabled = busy;
    spinner.hidden = !busy;
  };

  const validateCredential = (rawValue) => {
    const value = rawValue.trim();
    const length = Array.from(value).length;
    if (length < 2) return { error: '至少输入 2 个字符' };
    if (length > 100) return { error: '不能超过 100 个字符' };
    return { value };
  };

  const hasExactKeys = (value, keys) => value && typeof value === 'object' && !Array.isArray(value) &&
    Object.keys(value).length === keys.length && keys.every((key) => Object.prototype.hasOwnProperty.call(value, key));
  const isCost = (value) => typeof value === 'string' && /^(0|[1-9]\d*)(\.\d+)?$/.test(value);
  const dailyPointKeys = ['date', 'actualCost'];
  const validDailyPoint = (point) => hasExactKeys(point, dailyPointKeys) && typeof point.date === 'string' && isCost(point.actualCost);
  const resultKeys = ['keyMasked', 'name', 'groupName', 'quota', 'quotaUsed', 'status', 'expiresAt', 'todayCost', 'dailyUsage'];
  const validResult = (payload) => hasExactKeys(payload, resultKeys) &&
    typeof payload.keyMasked === 'string' && typeof payload.name === 'string' && typeof payload.groupName === 'string' &&
    isCost(payload.quota) && isCost(payload.quotaUsed) && typeof payload.status === 'string' &&
    typeof payload.expiresAt === 'string' && isCost(payload.todayCost) &&
    Array.isArray(payload.dailyUsage) && payload.dailyUsage.every(validDailyPoint);

  const formatCost = (value) => {
    const num = +value;
    return Number.isFinite(num) ? num.toFixed(4) : value;
  };
  const formatQuota = (value) => {
    if (!value || value === '0') return '无限制';
    const num = +value;
    return Number.isFinite(num) ? num.toFixed(2) : value;
  };
  const formatTimestamp = (value) => {
    if (!value) return '—';
    try { return new Date(value).toLocaleString(); } catch (_) { return value; }
  };

  const renderCard = (result) => {
    nameEl.textContent = result.name;
    groupEl.textContent = result.groupName || '无分组';
    statusBadge.textContent = result.status;
    keyEl.textContent = result.keyMasked;
    todayCostEl.textContent = `$${formatCost(result.todayCost)}`;
    quotaEl.textContent = `${formatCost(result.quotaUsed)} / ${formatQuota(result.quota)}`;
    expiresEl.textContent = formatTimestamp(result.expiresAt);

    const quota = +result.quota || 0;
    const quotaUsed = +result.quotaUsed || 0;
    const percent = quota > 0 ? Math.min(100, (quotaUsed / quota) * 100) : 0;
    quotaPercentEl.textContent = quota > 0 ? `${percent.toFixed(1)}%` : '无限制';
    quotaBarEl.style.width = `${percent}%`;

    renderChart(result.dailyUsage);
    card.hidden = false;
  };

  // A deliberately separate, simplified copy of app.js's daily-usage chart
  // renderer — self.js is not allowed to depend on app.js loading first (it
  // never does; they're served on different pages), so sharing a module
  // isn't an option without introducing a new shared script the design
  // explicitly decided against. Same visual language, fewer features (no
  // hover tooltip) to keep this page's script small.
  const renderChart = (items) => {
    chartWrap.textContent = '';
    if (items.length === 0) return;
    const width = 640;
    const height = 200;
    const padL = 48;
    const padR = 12;
    const padT = 12;
    const padB = 28;
    const plotW = width - padL - padR;
    const plotH = height - padT - padB;
    const maxCost = Math.max(...items.map((item) => +item.actualCost || 0), 0.001);
    const x = (i) => padL + (items.length === 1 ? plotW / 2 : (i / (items.length - 1)) * plotW);
    const y = (value) => padT + plotH - (Math.min(value, maxCost) / maxCost) * plotH;

    const svgNs = 'http' + '://www.w3.org/2000/svg';
    const svg = document.createElementNS(svgNs, 'svg');
    svg.setAttribute('viewBox', `0 0 ${width} ${height}`);
    svg.setAttribute('role', 'img');
    svg.setAttribute('aria-label', '近 30 天用量折线图');

    const ySteps = 3;
    for (let step = 0; step <= ySteps; step++) {
      const value = (maxCost * step) / ySteps;
      const yy = y(value);
      const line = document.createElementNS(svgNs, 'line');
      line.setAttribute('x1', String(padL));
      line.setAttribute('y1', String(yy));
      line.setAttribute('x2', String(width - padR));
      line.setAttribute('y2', String(yy));
      line.setAttribute('stroke', '#e9eaeb');
      line.setAttribute('stroke-width', '1');
      svg.appendChild(line);
      const label = document.createElementNS(svgNs, 'text');
      label.setAttribute('x', String(padL - 6));
      label.setAttribute('y', String(yy + 4));
      label.setAttribute('text-anchor', 'end');
      label.setAttribute('font-size', '10');
      label.setAttribute('fill', '#52606d');
      label.textContent = value.toFixed(2);
      svg.appendChild(label);
    }

    const labelIndexes = [0, Math.floor((items.length - 1) / 2), items.length - 1];
    labelIndexes.forEach((i) => {
      const text = document.createElementNS(svgNs, 'text');
      text.setAttribute('x', String(x(i)));
      text.setAttribute('y', String(height - padB + 16));
      text.setAttribute('text-anchor', 'middle');
      text.setAttribute('font-size', '10');
      text.setAttribute('fill', '#52606d');
      text.textContent = items[i].date;
      svg.appendChild(text);
    });

    const points = items.map((item, i) => `${x(i)},${y(+item.actualCost || 0)}`);
    const polyline = document.createElementNS(svgNs, 'polyline');
    polyline.setAttribute('points', points.join(' '));
    polyline.setAttribute('fill', 'none');
    polyline.setAttribute('stroke', '#0f766e');
    polyline.setAttribute('stroke-width', '2');
    polyline.setAttribute('stroke-linejoin', 'round');
    polyline.setAttribute('stroke-linecap', 'round');
    svg.appendChild(polyline);

    items.forEach((item, i) => {
      const circle = document.createElementNS(svgNs, 'circle');
      circle.setAttribute('cx', String(x(i)));
      circle.setAttribute('cy', String(y(+item.actualCost || 0)));
      circle.setAttribute('r', '3');
      circle.setAttribute('fill', '#0f766e');
      svg.appendChild(circle);
    });

    chartWrap.appendChild(svg);
  };

  let requestSequence = 0;

  const submitLookup = async (credential) => {
    const sequence = ++requestSequence;
    setBusy(true);
    setStatus('loading', '正在查询...');
    card.hidden = true;
    try {
      const response = await fetch('/api/self-lookup', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ credential }),
      });
      if (sequence !== requestSequence) return;
      if (response.status === 404) {
        setStatus('empty', '未找到匹配的 Key');
        return;
      }
      if (!response.ok) throw new Error('self-lookup failed');
      const payload = await response.json();
      if (!validResult(payload)) throw new Error('invalid response');
      renderCard(payload);
      setStatus('ready', '');
    } catch (_) {
      setStatus('error', '查询失败，请重试');
    } finally {
      if (sequence === requestSequence) setBusy(false);
    }
  };

  form.addEventListener('submit', (event) => {
    event.preventDefault();
    const validation = validateCredential(credentialInput.value);
    if (validation.error) {
      credentialInput.setAttribute('aria-invalid', 'true');
      setStatus('error', validation.error);
      credentialInput.focus();
      return;
    }
    credentialInput.removeAttribute('aria-invalid');
    submitLookup(validation.value);
  });
})();
```

Design notes:
- `response.status === 404` is checked explicitly and before the generic `!response.ok` branch, so a miss renders "未找到匹配的 Key" (an empty-state message) rather than the generic "查询失败，请重试" error — matching the design doc's requirement that a miss reads as "not found," not as a system failure.
- The credential is never echoed back into any rendered text or error message — only the generic validation messages ("至少输入 2 个字符" etc.) and the generic empty/error states are shown, so nothing about *why* a lookup failed leaks beyond what the design doc already allows.
- Same strict `validResult`/`hasExactKeys` pattern as `app.js` and `leaderboard.js` — reject any payload shape that isn't exactly what's expected, rather than defensively picking out just the fields used.

**Step 4: Run test to verify it passes**

Requires Task 3.

---

### Task 3: Wire the embed and the `/self` routes

**Files:**
- Modify: `internal/web/assets.go`
- Modify: `internal/httpapi/handler.go`

**Step 1: Apply the embed change**

```diff
-//go:embed index.html app.css app.js credentials.html credentials.css credentials.js favicon.svg vendor/tailwind.js leaderboard.html leaderboard.js
+//go:embed index.html app.css app.js credentials.html credentials.css credentials.js favicon.svg vendor/tailwind.js leaderboard.html leaderboard.js self.html self.js
 var assets embed.FS
```

(The starting line already has Module 4's `vendor/tailwind.js` and Module 5's `leaderboard.html leaderboard.js` if those were done first, in sequence — just append `self.html self.js`.)

```diff
-	case "index.html", "credentials.html", "leaderboard.html":
+	case "index.html", "credentials.html", "leaderboard.html", "self.html":
 		contentType = "text/html; charset=utf-8"
 	case "app.css", "credentials.css":
 		contentType = "text/css; charset=utf-8"
-	case "app.js", "credentials.js", "vendor/tailwind.js", "leaderboard.js":
+	case "app.js", "credentials.js", "vendor/tailwind.js", "leaderboard.js", "self.js":
 		contentType = "text/javascript; charset=utf-8"
```

**Step 2: Apply the route change**

```diff
 	case "/leaderboard.js":
 		name = "leaderboard.js"
+	case "/self":
+		name = "self.html"
+	case "/self.js":
+		name = "self.js"
 	default:
```

**Step 3: Run both new tests to verify they pass**

Run: `go test ./internal/web/... -run TestSelfLookup -v`
Expected: `PASS` for `TestSelfLookupPageIsCompleteAndSameOrigin` and `TestSelfLookupScriptIsSafeAndFetchesTheRightEndpoint`.

**Step 4: Full package checkpoint**

Run: `go build ./... && go vet ./... && node --check internal/web/self.js && go test ./... -count=1`
Expected: everything passes.

**Step 5: Commit**

```bash
git add internal/web/self.html internal/web/self.js internal/web/self_test.go internal/web/assets.go internal/httpapi/handler.go
git commit -m "feat: add the self-lookup page, script, and routes"
```

---

### Task 4: Manual end-to-end smoke test

Verified while writing this plan with real seeded data; reproduce this to confirm your checkout behaves the same way.

**Step 1: Seed a key with quota and multi-day usage**

```bash
docker run -d --name mod06-smoke -e POSTGRES_USER=sub2api -e POSTGRES_PASSWORD=testpass -e POSTGRES_DB=sub2api -p 15984:5432 postgres:16-alpine
until docker exec mod06-smoke pg_isready -U sub2api >/dev/null 2>&1; do sleep 1; done
docker exec -i mod06-smoke psql -U sub2api -d sub2api <<'SQL'
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
INSERT INTO public.api_keys (id, key, name, group_id, quota, quota_used, expires_at, status, created_at) VALUES
  (1, 'sk-mod6-verify-final01', 'mod6-final', 1, 100, 42.50, now() + interval '30 days', 'active', now());
INSERT INTO public.usage_logs (id, api_key_id, actual_cost, created_at) VALUES
  (1, 1, 5.10, now()),
  (2, 1, 2.20, now() - interval '1 days');
SQL
go build -trimpath -o dist/sub2api-usage-viewer ./cmd/viewer
DATABASE_HOST=127.0.0.1 DATABASE_PORT=15984 DATABASE_USER=sub2api DATABASE_PASSWORD=testpass DATABASE_DBNAME=sub2api DATABASE_SSLMODE=disable \
  ./dist/sub2api-usage-viewer &
sleep 1.5
```

**Step 2: Verify both match paths and the miss path**

```bash
curl -s --noproxy '*' -X POST http://127.0.0.1:8081/api/self-lookup -H 'Content-Type: application/json' -H 'Origin: http://127.0.0.1:8081' --data-raw '{"credential":"sk-mod6-verify-final01"}'
curl -s --noproxy '*' -X POST http://127.0.0.1:8081/api/self-lookup -H 'Content-Type: application/json' -H 'Origin: http://127.0.0.1:8081' --data-raw '{"credential":"mod6-final"}'
curl -s --noproxy '*' -w '\nHTTP %{http_code}\n' -X POST http://127.0.0.1:8081/api/self-lookup -H 'Content-Type: application/json' -H 'Origin: http://127.0.0.1:8081' --data-raw '{"credential":"does-not-exist-xyz"}'
```

Expected (verified while writing this plan): the first two calls return identical bodies — `"keyMasked":"sk-mod***inal01"`, `"quotaUsed":"42.50"`, `"todayCost":"5.10"`, and `"dailyUsage"` with two entries (yesterday's 2.20, today's 5.10). The third returns `404` with the generic `NOT_FOUND` body.

**Step 3: The part curl cannot do**

Open `http://127.0.0.1:8081/self` in a browser, submit a real credential, and confirm the card renders, the quota bar fills to the right percentage, and the chart draws a visible line with two points. **Not performed while writing this plan.**

**Step 4: Tear down**

```bash
kill %1
docker rm -f mod06-smoke
git checkout -- dist/sub2api-usage-viewer
```

---

### Module 6 checkpoint

```bash
go build ./... && go vet ./...
node --check internal/web/self.js
go test ./... -count=1
```
Expected: all pass.

Next: [`2026-08-09-plan-07-regression-and-verification.md`](2026-08-09-plan-07-regression-and-verification.md)
