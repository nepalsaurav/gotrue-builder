---
name: smoke-test
description: Run gotruectl's full live-Docker regression suite and report pass/fail per check. Use after any change to internal/gotruectl/*.go, or whenever asked to verify gotruectl still works.
---

# smoke-test

`gotruectl` has no Go unit tests by design — every feature is verified
against real Docker, Postgres, and GoTrue rather than mocked, because the
`docker`/`pg_dump`/`curl` interactions are the entire point of this tool.
`scripts/smoke-test.sh` is the regression suite: it builds the binary and
exercises every command end-to-end, then tears everything down whether it
passed or failed.

## Running it

```sh
cd <this repo>
./scripts/smoke-test.sh
```

Requires: Docker running, outbound network access (it pulls `postgres`,
`mailpit`, `redis`, and `supabase/auth` images), and ports `19999`, `19998`,
`8025`, `1025` free on the host.

Takes a few minutes — it really does create two tenants, provision a real
admin user, run three different `update` scenarios (success, rollback,
pull-failure), rotate a JWT secret, and check file permissions and
secret-echo behavior, not shortcuts of any of that.

## What "done" means

The script prints `PASS: ...` / `FAIL: ...` per check and ends with either
`ALL CHECKS PASSED` (exit 0) or `N CHECK(S) FAILED` (exit 1). Report the
exact pass/fail summary back — don't paraphrase "tests passed" without the
count, and never claim success from a truncated run.

If something fails:

1. Re-run once before concluding it's real — a couple of checks poll
   `/health` with a timeout and can be flaky under heavy back-to-back test
   runs (Docker/network contention), not the product. `wait_healthy` in the
   script already retries for ~15s per check; a failure that survives a
   clean re-run is real.
2. The script's own `cleanup()` runs on exit regardless of outcome (via
   `trap ... EXIT`) — if a failed run still leaves containers/volumes/
   `~/.gotrue-builder` behind, that's itself a bug worth fixing, not just
   working around.
3. Read `PLAN.md`'s "Facts already verified" and "Non-negotiable design
   decisions" in `CLAUDE.md` before changing the swap/rollback mechanism in
   `update.go` (`swapContainer`, `applyEnvChangesAndRestart`) — that logic
   is shared by `update run`, `update rotate-jwt-secret`, and
   `tenant config set`, so a bug there fails three different features at
   once.

## Extending it

New command or new tenant-mutating behavior → add a corresponding step to
`scripts/smoke-test.sh` in the same style: a `step "..."` header, the
command under test, then one or more `pass`/`fail`/`check` assertions on
its actual effect (a file's contents, a container's image, an HTTP status,
a permission mode) — not just "the command exited 0." The security
assertions added for secret-handling (file permissions, "does this ever
echo a secret") are the bar: assert the property that would actually catch
a regression, not just that something ran.
