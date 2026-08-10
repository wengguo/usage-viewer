# Module 7: Regression and Full Verification

> Part of [`2026-08-09-tailwind-leaderboard-selflookup-plan.md`](2026-08-09-tailwind-leaderboard-selflookup-plan.md). Requires Modules 1-6 complete and committed. This module adds no new features — it exists to prove the six modules compose correctly as a whole, not just individually, and to hand off a clean, fully-tested state.

Each of Modules 1-6 was verified in isolation (own unit tests, own integration tests where applicable, own curl smoke test against a throwaway Postgres). That is necessary but not sufficient — it does not prove that Module 5's leaderboard page and Module 6's self-lookup page both work *at the same time* against the *same* running server, using the *same* seeded database, the way a real deployment would. This module runs that combined check once, for real, and is where regressions from module-to-module interaction (not module-internal bugs) would surface.

**Verification scope, consistent with every prior module:** everything network/API/data-shape related was verified for real. Actual visual rendering in a browser was **not** verified anywhere in this plan — no headless browser tool was available. Task 3 below is the single point where that gap must finally be closed, either by the person executing this plan or by explicitly telling the user it wasn't done.

---

### Task 1: Full automated test suite, run clean

**Step 1: Confirm the tree is in the state Modules 1-6 left it in**

```bash
git log --oneline -10
git status --short
```

Expected: a clean working tree (no uncommitted changes), with the six modules' commits visible in the log (each module's Task-level commits — there will be more than six commits total, since most modules had 3-5 commits each).

**Step 2: Full build and static checks**

```bash
go build ./...
go vet ./...
gofmt -l $(git ls-files '*.go')
```

Expected: `go build`/`go vet` produce no output (success). `gofmt -l` will print exactly two pre-existing paths that predate this plan and were never touched by any of its seven modules — `internal/concurrency/concurrency.go` and `internal/httpapi/daily_test.go` (both from the repository's initial commit, confirmed via `git log --oneline -1 -- <path>` while writing this plan). **Any other path printed here is new** and means a module's own checkpoint missed a formatting issue — run `gofmt -w <path>` and investigate why before proceeding. Do not "fix" the two pre-existing files as part of this plan — that's unrelated scope creep; leave them exactly as they are unless the user separately asks for a repo-wide formatting cleanup.

**Step 3: Full unit and non-integration test suite**

```bash
go test ./... -count=1 -v 2>&1 | tee /tmp/full-test-output.log
grep -c '^--- PASS' /tmp/full-test-output.log
grep -c '^--- FAIL' /tmp/full-test-output.log
```

Expected: the FAIL count is `0`. The PASS count should be substantially higher than it was before this plan started (Modules 1-6 added roughly 40-50 new test functions/subtests across `internal/search`, `internal/postgres`, `internal/httpapi`, and `internal/web` — the exact number depends on how subtests are counted, so don't hardcode an expected total; just confirm zero failures and a visibly larger count than the pre-plan baseline).

**Step 4: Full integration test suite, run twice for determinism**

```bash
go test -tags integration ./internal/postgres/... -timeout 180s -count=1
go test -tags integration ./internal/postgres/... -timeout 180s -count=1
```

Expected: `ok` both times. If either run fails, do not proceed — a flaky integration suite at this point most likely means a real race or ordering dependency between Module 2's leaderboard/self-lookup fixtures and the pre-existing `preflight_integration_test.go`/`readonly_integration_test.go` fixtures (they all share one `sharedHarness` per the existing `test_harness_integration_test.go` design — verify each new integration test's `t.Cleanup` actually removes everything it inserted, the same way the existing tests do).

**Step 5: JS syntax check on every shipped script**

```bash
for f in internal/web/app.js internal/web/leaderboard.js internal/web/self.js internal/web/credentials.js; do
  echo "checking $f"
  node --check "$f" || echo "FAILED: $f"
done
```

Expected: no `FAILED` lines. `credentials.js` is included even though this plan never touched it — it's a cheap way to confirm the check command itself works correctly (a script you know is fine should also print no failure).

---

### Task 2: Combined end-to-end smoke test — all three pages, one server, one database

This is the check that Modules 1-6's individual smoke tests could not perform: hitting `/`, `/leaderboard`, and `/self` against the *same* running process and the *same* seeded rows, back to back, confirming nothing about wiring one page's route broke another's.

**Step 1: Seed a richer dataset covering every feature's edge cases at once**

```bash
docker run -d --name mod07-smoke -e POSTGRES_USER=sub2api -e POSTGRES_PASSWORD=testpass -e POSTGRES_DB=sub2api -p 15983:5432 postgres:16-alpine
until docker exec mod07-smoke pg_isready -U sub2api >/dev/null 2>&1; do sleep 1; done
docker exec -i mod07-smoke psql -U sub2api -d sub2api <<'SQL'
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
INSERT INTO public.groups (id, name) VALUES (1, 'production'), (2, 'staging');
INSERT INTO public.api_keys (id, key, name, group_id, quota, quota_used, expires_at, status, created_at, deleted_at) VALUES
  (1, 'sk-mod7-alpha-000000001', 'alpha-heavy-user', 1, 500, 310.25, now() + interval '60 days', 'active', now() - interval '20 days', NULL),
  (2, 'sk-mod7-beta--000000002', 'beta-light-user', 1, 50, 2.00, now() + interval '10 days', 'active', now() - interval '5 days', NULL),
  (3, 'sk-mod7-gamma-000000003', 'gamma-no-group', NULL, 0, 0, NULL, 'active', now() - interval '1 days', NULL),
  (4, 'sk-mod7-deleted0000004', 'deleted-should-never-appear', 2, 100, 100, NULL, 'active', now() - interval '90 days', now());
INSERT INTO public.usage_logs (id, api_key_id, actual_cost, created_at) VALUES
  (1, 1, 45.00, now()),
  (2, 1, 30.00, now() - interval '2 days'),
  (3, 1, 20.00, now() - interval '10 days'),
  (4, 2, 2.00, now()),
  (5, 4, 999.99, now());
SQL

go build -trimpath -o dist/sub2api-usage-viewer ./cmd/viewer
lsof -iTCP:8081 -sTCP:LISTEN -n -P 2>/dev/null | grep sub2api && pkill -f "dist/sub2api-usage-viewer" && sleep 0.5
DATABASE_HOST=127.0.0.1 DATABASE_PORT=15983 DATABASE_USER=sub2api DATABASE_PASSWORD=testpass DATABASE_DBNAME=sub2api DATABASE_SSLMODE=disable \
  ./dist/sub2api-usage-viewer &
sleep 1.5
```

This dataset deliberately includes: a heavy user (alpha, for leaderboard ranking and a full quota bar), a light user (beta, near-empty quota), a key with no group (gamma, exercises the `COALESCE(grp.name,'')` / "无分组" fallback everywhere), and a **deleted** key with the largest cost of all (999.99) that must never appear in search results, the leaderboard, or self-lookup.

**Step 2: Health and page routes**

```bash
for path in /livez /readyz / /leaderboard /self /vendor/tailwind.js /app.css /app.js /leaderboard.js /self.js; do
  code=$(curl -s --noproxy '*' -o /dev/null -w '%{http_code}' "http://127.0.0.1:8081$path")
  echo "$path -> $code"
done
```

Expected: every path returns `200`.

**Step 3: Main search page — confirm the deleted key never surfaces**

```bash
curl -s --noproxy '*' -X POST http://127.0.0.1:8081/api/search -H 'Content-Type: application/json' -H 'Origin: http://127.0.0.1:8081' --data-raw '{"targetType":"key"}' | python3 -m json.tool
```

Expected: `total` is `3` (alpha, beta, gamma — not the deleted key), and no result's `name` is `"deleted-should-never-appear"`.

**Step 4: Leaderboard — confirm ranking, window boundaries, and masking, together**

```bash
curl -s --noproxy '*' -X POST http://127.0.0.1:8081/api/leaderboard -H 'Content-Type: application/json' -H 'Origin: http://127.0.0.1:8081' --data-raw '{"limit":10}' | python3 -m json.tool
```

Expected:
- `1d` window: alpha ($45) ranked above beta ($2); gamma absent (no usage logged); deleted key absent.
- `3d` window: alpha's total climbs to $75 (45+30), still ranked first.
- `30d` window: alpha's total is $95 (45+30+20), still first; beta still $2; deleted key **absent even though its $999.99 dwarfs everyone** — this is the one assertion in this whole plan that most directly protects against the "read-only role can see deleted rows" class of bug, so don't skip checking it.
- Every entry's `keyMasked` starts with `sk-mod7` truncated to 6 characters followed by `***` — never the full raw key.

**Step 5: Self-lookup — confirm the deleted key is unreachable even by exact credential**

```bash
curl -s --noproxy '*' -X POST http://127.0.0.1:8081/api/self-lookup -H 'Content-Type: application/json' -H 'Origin: http://127.0.0.1:8081' --data-raw '{"credential":"sk-mod7-alpha-000000001"}' | python3 -m json.tool
curl -s --noproxy '*' -w '\nHTTP %{http_code}\n' -X POST http://127.0.0.1:8081/api/self-lookup -H 'Content-Type: application/json' -H 'Origin: http://127.0.0.1:8081' --data-raw '{"credential":"sk-mod7-deleted0000004"}'
```

Expected: the first call returns alpha's detail with `dailyUsage` showing three points (today $45, 2 days ago $30, 10 days ago $20) and `todayCost` of `"45.00"`. **The second call — looking up the deleted key by its exact, correct credential — must return `404 NOT_FOUND`, not the deleted key's data.** This is the single most important security assertion in this entire plan: a deleted key's owner (or anyone who obtained its value) must not be able to use self-lookup to see it's still "active" somewhere.

**Step 6: Tear down**

```bash
kill %1
docker rm -f mod07-smoke
git checkout -- dist/sub2api-usage-viewer
```

---

### Task 3: The browser gap — close it or hand it off explicitly

Every module from 4 through 6 flagged the same limitation: no headless browser was available while writing this plan, so nothing about actual visual rendering, responsive layout collapse, hover states, or interactive JS behavior (as opposed to the JSON it fetches) was ever confirmed by looking at a screen.

**Do one of the following before considering this plan complete:**

**Option A — if a browser or Playwright/Puppeteer is available in the execution environment:** open each of `/`, `/leaderboard`, `/self` against the Task 2 dataset and confirm, at minimum:
- Tailwind classes are actually applying (pages have visible color/spacing, not raw unstyled HTML — the single most likely failure mode if `/vendor/tailwind.js` silently failed to load).
- The main page's table/card split responds to viewport width (resize below ~768px, confirm the table hides and cards appear, and vice versa).
- The leaderboard's four cards render side-by-side on a wide viewport and stack on narrow.
- The self-lookup form actually submits on Enter and on button click, and the quota bar visually fills to a sane width for alpha's ~62% usage.

**Option B — if no browser tooling is available (the situation this plan was written under):** do not claim this was checked. State plainly, to whoever reads this plan's completion report or to the user directly: *"Every API contract, route, and data shape was verified against real HTTP and real Postgres. No visual rendering was checked in an actual browser — please open `/`, `/leaderboard`, and `/self` yourself before treating this as done."* This is not a formality — the difference between "the JSON is correct" and "the page looks right" is exactly the gap a Tailwind class typo could hide, and nothing in Tasks 1-2 would catch that class of bug.

---

### Task 4: Final commit and handoff

**Step 1: Confirm nothing is left uncommitted**

```bash
git status --short
```

Expected: clean (everything from Modules 1-6 should already be committed at their own Task boundaries; this module added no new source files, only ran checks).

**Step 2: If Task 1-2 surfaced any fix, commit it now, atomically, with a message describing what cross-module interaction it fixed** — not "fix bug," but e.g. "fix: self-lookup must exclude deleted keys even on exact credential match" if Task 2 Step 5's second assertion had failed and required a fix. (It did not fail when this plan was written — Module 2's `selfLookupSQL` already has `WHERE api_key.deleted_at IS NULL` — but if you're executing this on a modified checkout, verify it's still there.)

**Step 3: Report completion honestly**

State clearly what was and was not verified:
- ✅ All unit tests, integration tests (against real Postgres via testcontainers), and HTTP-level smoke tests (against a real built binary and real Postgres) pass.
- ✅ Cross-page/cross-feature interactions (deleted-key exclusion across all three read paths, masking consistency, natural-day window boundaries) were checked together, not just per-module.
- ⚠️ Visual rendering, responsive behavior, and browser-side interactivity were **not** confirmed in an actual browser at any point in this plan's execution, unless Task 3's Option A was completed. Say so explicitly rather than letting silence imply it was checked.

---

### Plan complete

At this point, if Tasks 1-2 pass and Task 3 has been resolved one way or the other, the full feature set from the design doc (`docs/plans/2026-08-09-tailwind-leaderboard-selflookup-design.md`) is implemented, tested, and ready:

- Tailwind-based UI across `/`, `/leaderboard`, `/self` with a shared nav, vendored locally (no runtime network dependency).
- A masked-key consumption leaderboard across four natural-day windows with a shared Top N control.
- A free, exact-match self-lookup page returning masked key detail plus a 30-day usage chart, with no route by which a deleted key or another user's data can be reached.
