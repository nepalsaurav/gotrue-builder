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
  tenant.go                    tenant create/list/logs/start/stop/delete
  tenantconfig.go              `tenant config` (single tenant, vertical,
                                every setting) / `tenant config set`
  update.go                    blue/green swap+rollback (`update run`,
                                `update rotate-jwt-secret`), shared by both
  backup.go                    pg_dump-based per-tenant backup
  status.go                    cross-cutting "every GoTrue container" view
  doctor.go                    active health probe (docker/postgres/tenant
                                /health/backup freshness), non-zero exit on
                                failure — status/tenant config show state,
                                doctor actually checks it
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
- **No reverse proxy / TLS** — out of scope. `update run`'s "a few seconds
  of downtime during the swap" limitation is a direct, accepted consequence
  of this (Docker can't bind two containers to one host port at once).
- **No automatic "check for latest GoTrue version."** `update run --version`
  is always explicit; no network call to GitHub releases.
- **No backup restore command** — restoring is materially riskier than
  backing up and wasn't asked for. Manual restore: `gunzip |
  docker exec -i postgres psql -U postgres -d gotrue_<name>`.

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
