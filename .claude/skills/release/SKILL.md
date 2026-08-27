---
name: release
description: Tag and push a new gotruectl release so `go install .../cmd/gotruectl@latest` and `gotruectl self-update` pick it up. Use when asked to release, ship, tag, or publish a new version.
---

# release

`go install github.com/nepalsaurav/gotrue-builder/cmd/gotruectl@latest`
resolves `@latest` via the **highest existing semver git tag**, falling
back to the remote's reported default branch only when no tag exists at
all. This bit a real install once (see `PLAN.md`) — the default branch was
still `master`, frozen at a commit from before any real implementation,
and `go install ...@latest` silently resolved to that ancient pseudo-version
with no useful error pointing at the real cause. **Always release via a
tag, never rely on default-branch resolution.**

## Steps

1. **Confirm the working tree is clean and `main` is up to date.**

   ```sh
   git status
   git fetch origin
   git log --oneline main..origin/main   # should be empty
   ```

2. **Run the smoke test first** (see the `smoke-test` skill) — never tag a
   version that hasn't passed it. Fix and commit first if it fails.

3. **Pick the next version.** Bump patch by default unless the changes are
   clearly feature-level (minor) or breaking (major) — ask the user if
   genuinely unclear. Check the current version:

   ```sh
   git tag --sort=-v:refname | head -1
   ```

4. **Tag and push**, from `main`, at the commit you actually verified:

   ```sh
   git tag -a vX.Y.Z -m "<one-line summary of what's new>"
   git push origin vX.Y.Z
   ```

   Don't push a moving branch pointer as the "release" — the tag is the
   thing `go install` resolves against.

5. **Verify it actually resolves**, from a scratch `GOPATH`/`GOCACHE` so
   nothing local is cached:

   ```sh
   GOCACHE=$(mktemp -d) GOPATH=$(mktemp -d) \
     go install github.com/nepalsaurav/gotrue-builder/cmd/gotruectl@latest
   ```

   If this resolves to an *older* version than the tag you just pushed,
   `proxy.golang.org`'s cache hasn't caught up yet (this happened during
   initial setup and cleared within a few minutes) — retry shortly, or
   confirm the tag itself is reachable with
   `GOPROXY=direct go install .../cmd/gotruectl@vX.Y.Z` in the meantime,
   which bypasses the proxy cache entirely.

6. **Check the version string is right**: `gotruectl --version` reads Go's
   own embedded module version via `runtime/debug.ReadBuildInfo()` (see
   `root.go`) — no manual `-ldflags` needed, it should just show `vX.Y.Z`
   after a `go install .../cmd/gotruectl@vX.Y.Z`.

## If you also renamed a branch, or the default branch is wrong

`gh repo view <owner>/<repo> --json defaultBranchRef` shows what the remote
actually reports. Fix it with `gh repo edit <owner>/<repo>
--default-branch <name>` if it's stale — but do this in addition to
tagging, not instead of it. A tag makes `@latest` resolution correct
regardless of whatever the default branch is; don't depend on the default
branch being right as your only safety net.
