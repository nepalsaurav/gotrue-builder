# gotrue-builder (gotruectl)

A standalone Go CLI, developed and used independently of any other
repository. It manages a local Docker-based [GoTrue](https://github.com/supabase/auth)
deployment: one shared Postgres container, plus one GoTrue container per
tenant. Nothing here assumes another project is checked out alongside it —
treat this repo as the whole world for any task inside it.

`abc_project_app` (a separate, unrelated repo on this machine) happens to be
one *consumer* of GoTrue instances this tool provisions — its backend
verifies GoTrue-issued JWTs, and `admin create-user`/`admin list-users`
here exist specifically because that project's own admin-provisioning
endpoint has a gap (creates a local DB row, never the matching GoTrue
identity). That's context for *why* a couple of features exist, not a
dependency: never read, modify, or assume the presence of `abc_project_app`
while working in this repo.

## Orientation

- **`README.md`** — the user-facing command reference. Treat it as the
  source of truth for "what does command X do" over anything below.
- **`PLAN.md`** — the design-decision log, in the order decisions were
  made, including ones that were considered and explicitly rejected (Docker
  Go SDK vs. shelling out to the `docker` CLI, a background daemon vs.
  on-demand commands, auto version-checking, etc.). **Read it before
  proposing an architectural change** — it's very likely already been
  raised and settled with a stated reason.
- This file — quick map for making a change, not a design rationale doc.

## Layout

```
cmd/gotruectl/main.go        — the entire entrypoint: parses nothing itself,
                                just calls gotruectl.Execute()
internal/gotruectl/          — all real code, single package, flat files:
  root.go                      Cobra root command + subcommand wiring
  config.go, configcmd.go      Viper layered config; `config show`/`set-smtp`
  postgres.go                  shared postgres container lifecycle
  tenant.go                    tenant create/list/logs/start/stop/delete;
                                also owns GOTRUE_RATE_LIMIT_* flags + the
                                generic --set KEY=VALUE escape hatch, both
                                on tenant create and (in tenantconfig.go)
                                tenant config set
  tenantconfig.go              `tenant config` (single tenant, vertical,
                                every setting) / `tenant config set`
  update.go                    blue/green swap+rollback (`update run`,
                                `update rotate-jwt-secret`), shared by both
  backup.go                    pg_dump-based per-tenant backup, plus
                                `backup restore` — see the non-negotiable-
                                decisions entry below before touching it
  status.go                    cross-cutting "every GoTrue container" view
  doctor.go                    active health probe (docker/postgres/tenant
                                /health/backup freshness), non-zero exit on
                                failure — status/tenant config show state,
                                doctor actually checks it. gatherDoctorChecks()
                                is the pure data-gathering step, shared with:
  dashboard.go                 same checks as doctor.go, live in a bubbletea
                                TUI (5s auto-refresh, q to quit) — renders via
                                ui.go's renderTable, NOT bubbles/table (see
                                below)
  caddyfile.go                 generates a security-hardened Caddy reverse-
                                proxy config: allowlists GoTrue's public
                                routes only, default-denies everything else
                                including /admin/*
  key.go, admin.go             service_role JWT minting + Admin API calls
  build.go                     phase-2: build supabase/auth from source
  selfupdate.go                `go install` wrapper
  ui.go                        lipgloss styling — tables + success/warn/
                                error messages; NEVER used on key's token
                                or admin's JSON output (must stay pipeable)
  docker.go, paths.go,
  prompt.go, util.go           low-level helpers (exec wrappers, env file
                                parsing, path resolution, interactive prompts)
scripts/smoke-test.sh          live end-to-end test against real Docker
.claude/skills/                see below
```

Everything is one flat package (`gotruectl`) by design — this is a thin CLI
wrapper, not a layered application; splitting it into more packages would
be the kind of premature structure `PLAN.md` already argues against for
this project's actual size.

## Non-negotiable design decisions (don't re-litigate without reading PLAN.md)

- **Shell out to the `docker` CLI via `os/exec`, not the Docker Go SDK.**
  Raised and explicitly declined mid-session: this tool is a thin wrapper
  around a handful of docker invocations, and `docker pull`/`logs -f`
  stream live progress to the terminal for free via `os/exec` — the SDK
  would need manual event-stream handling to match that.
- **No background daemon or scheduler**, anywhere. `status`, `backup`, and
  `update` are all on-demand commands, explicitly chosen over a daemon when
  asked. Wire your own cron/systemd timer around them if you want automatic
  backups — that's out of scope for this tool.
- **gotruectl never runs or manages a reverse proxy/TLS itself** —
  `caddyfile` generates a *config file* (allowlist-only, default-deny,
  security headers) but never touches a running Caddy install, reloads it,
  or manages certificates. `update run`'s "a few seconds of downtime during
  the swap" limitation is a direct, accepted consequence of staying out of
  that layer (Docker can't bind two containers to one host port at once).
- **No automatic "check for latest GoTrue version."** `update run --version`
  is always explicit; no network call to GitHub releases.
- **`backup restore` exists** (no longer a non-goal — explicitly requested)
  but is deliberately the most defensive command in the codebase: it always
  takes a fresh safety backup of current state first (so a restore against
  the wrong file is itself undoable), asks for confirmation unless `--yes`,
  stops the container during the restore, and runs `psql -v
  ON_ERROR_STOP=1`. A real bug was caught while testing it: the restore
  preamble issued its own `CREATE SCHEMA auth AUTHORIZATION <role>` before
  replaying the dump — but `pg_dump --schema=auth` already emits its own
  unqualified `CREATE SCHEMA auth;` near the top of the dump, so the two
  collided (`ERROR: schema "auth" already exists`), aborting before any
  data was replayed. Only found by actually restoring real data and
  checking it came back — a schema-shape check wouldn't have caught it.
  Fixed by dropping the preamble's `CREATE SCHEMA` entirely: running the
  restore `-U <tenant role>` means the dump's own `CREATE SCHEMA auth;`
  already creates it correctly owned, no explicit `AUTHORIZATION` needed.
- **`bubbles/table` doesn't handle ANSI-styled cell content correctly** —
  found by actually running `dashboard` in a `tmux` pane (see the
  `smoke-test` skill): embedding `lipgloss`-colored status text into
  `bubbles/table` cells silently mis-truncated both that cell and ate
  characters from the next column. `dashboard.go` renders via `ui.go`'s
  plain `renderTable` (lipgloss/table, already verified to handle ANSI
  correctly) inside `View()` instead — `bubbles` isn't a dependency at all.
  Don't reach for `bubbles/table` for colored cells without re-verifying
  this upstream, and don't add it as a dependency for a view that (like
  `dashboard`) doesn't actually need selection/scrolling.
- **Never guess a `GOTRUE_*` env var's name, unit, or semantics from
  memory or a web search summary — read the actual upstream source.** Web
  search results and even `WebFetch`-summarized pages disagreed on the
  rate-limit variable names before this was checked properly; the
  authoritative source is `internal/conf/configuration.go` (struct fields,
  `split_words:"true"` tags) cross-checked against
  `internal/api/apilimiter/apilimiter.go` (which documents the *exact* env
  var next to each field in a comment, and shows the real time window each
  one applies over — `RATE_LIMIT_OTP` and `RATE_LIMIT_VERIFY` are per-5-
  minutes, `RATE_LIMIT_EMAIL_SENT`/`RATE_LIMIT_SMS_SENT` are per-hour via a
  custom `Rate` type in `internal/conf/rate.go` that also accepts an
  `N/duration` form, `MFA.RateLimitChallengeAndVerify` is per-*minute* and
  nested under a `GOTRUE_MFA_` prefix) — `curl` the raw file, don't trust a
  paraphrase. Getting this wrong is worse than not having the feature: a
  silently-ignored or misnamed rate limit is a false sense of security.

## Working here

- **Build**: `go build -o gotruectl ./cmd/gotruectl`
- **Static checks**: `gofmt -l .` (must be empty) and `go vet ./...`
- **There are no Go unit tests.** Every feature so far has been verified by
  actually running it against real Docker rather than mocking it — the
  `docker`/`pg_dump`/`curl` interactions are the entire point, and mocking
  them would mostly test the mocks. `scripts/smoke-test.sh` is the
  regression suite: it builds the binary, exercises every command
  (postgres, tenant + SMTP + config, status managed/unmanaged, backup
  content, key/admin round-trip, update success + rollback + pull-failure,
  JWT rotation, `tenant config set`), and tears everything down whether it
  passed or failed. Run it (or the `smoke-test` skill) after any change
  that touches `internal/gotruectl/*.go` — don't consider a change done
  without running it.
- **New tenant-mutating command that recreates the container?** Reuse
  `applyEnvChangesAndRestart`/`swapContainer` in `update.go` rather than
  writing another rename/stop/run/health-check/rollback sequence — that's
  the one place this pattern is allowed to exist.
- **Module path is `github.com/nepalsaurav/gotrue-builder`**; the installed
  binary path is `.../cmd/gotruectl` specifically (not the module root) —
  `go install` needs the exact package path. Releasing a new version needs
  an actual git tag, not just a push to `main`: `go install ...@latest`
  resolves via the highest semver tag when one exists, and falls back to
  the remote's reported default branch otherwise, which is fragile (this
  bit a real install once — see the `release` skill and `PLAN.md`).

## Skills

- **`smoke-test`** — runs `scripts/smoke-test.sh` and reports pass/fail
  per check. Use after any code change.
- **`release`** — bumps the version, tags, and pushes, so `go install
  .../cmd/gotruectl@latest` and `gotruectl self-update` pick it up.
