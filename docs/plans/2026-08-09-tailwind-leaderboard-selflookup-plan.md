# Tailwind UI Revamp + Leaderboard + Self-Lookup Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Rewrite the three-page frontend with Tailwind (vendored locally, no CDN dependency), and add two read-only features: a masked-key consumption leaderboard (1/3/7/30-day natural-day windows) and a free, no-login self-lookup page for a user to check their own key by exact key value or name.

**Architecture:** Pure additive backend work (new `search`/`postgres`/`httpapi` files, no changes to existing search/daily-usage SQL or handlers) plus a frontend rewrite (three static pages served from `internal/web`'s `embed.FS`, sharing a hand-copied nav partial, with Tailwind's Play CDN runtime vendored into the binary instead of loaded from a public CDN).

**Tech Stack:** Go 1.26.5, `pgx/v5`, Go `embed.FS`, vanilla JS (no framework), Tailwind CSS (vendored browser runtime `internal/web/vendor/tailwind.js`, already downloaded and committed).

**Design doc:** `docs/plans/2026-08-09-tailwind-leaderboard-selflookup-design.md` (read this first for the *why*; this plan is the *how*).

---

## Module map

This plan is split into ordered modules. Execute them **in order** — later modules depend on files created in earlier ones. Each module is its own file with bite-sized, TDD-style tasks.

| # | File | Covers |
|---|------|--------|
| 1 | [`2026-08-09-plan-01-backend-foundations.md`](2026-08-09-plan-01-backend-foundations.md) | `search.MaskKey`, leaderboard/self-lookup domain types and validators (pure Go, no DB) |
| 2 | [`2026-08-09-plan-02-backend-repositories.md`](2026-08-09-plan-02-backend-repositories.md) | `internal/postgres` leaderboard + self-lookup repositories, SQL contract tests, integration tests |
| 3 | [`2026-08-09-plan-03-backend-http-wiring.md`](2026-08-09-plan-03-backend-http-wiring.md) | New `httpapi` handlers (`/api/leaderboard`, `/api/self-lookup`), `main.go` dependency wiring |
| 4 | [`2026-08-09-plan-04-frontend-foundations.md`](2026-08-09-plan-04-frontend-foundations.md) | Vendor Tailwind into `embed.FS`, shared nav partial, Tailwind rewrite of `index.html`/`app.css`/`app.js` |
| 5 | [`2026-08-09-plan-05-frontend-leaderboard-page.md`](2026-08-09-plan-05-frontend-leaderboard-page.md) | `internal/web/leaderboard.html` + `leaderboard.js` |
| 6 | [`2026-08-09-plan-06-frontend-self-page.md`](2026-08-09-plan-06-frontend-self-page.md) | `internal/web/self.html` + `self.js` |
| 7 | [`2026-08-09-plan-07-regression-and-verification.md`](2026-08-09-plan-07-regression-and-verification.md) | Full test suite, manual browser verification against a seeded Postgres, final commit |

## Ground rules for every module

- **TDD**: write the failing test first, run it, watch it fail, then write the minimal code to pass, run it again, then commit. Every task below follows this shape explicitly.
- **Commit after every task**, not after every module. Small commits, `git add` only the files touched by that task.
- **Never modify** `internal/postgres/search.go`, `internal/postgres/daily.go`, `internal/httpapi/handler.go`'s existing `serveSearch`/`serveDailyUsage`, or their tests — these are out of scope and already have passing tests that must keep passing untouched.
- **Masking is mandatory** on every new response field that carries `api_keys.key` — call `search.MaskKey` before it leaves the repository→handler boundary (mask in the handler, not in SQL, so repository tests can assert on raw values and handler tests assert on masked values, mirroring how the existing code separates SQL concerns from HTTP concerns).
- Run `cd /Users/liutianping/Documents/projects/golang/usage-viewer` before every command below; all paths in this plan are relative to that directory unless stated otherwise.

## Verifying you're set up correctly before starting Module 1

```bash
cd /Users/liutianping/Documents/projects/golang/usage-viewer
go build ./... && go test ./... 2>&1 | tail -20
```
Expected: all packages `ok`, no failures. If this fails before you've changed anything, stop and investigate the pre-existing state first — do not build on top of a broken baseline.
