# gotrue-builder

A small Go CLI that clones a pinned tag of [supabase/auth](https://github.com/supabase/auth) (GoTrue), builds it, and copies the resulting binary into place — run directly on the target machine, no CI involved.

## Usage

```sh
go build -o gotrue-builder .
sudo ./gotrue-builder build                      # builds v2.196.0, installs to /opt/gotrue/gotrue-auth
./gotrue-builder build -version v2.197.0 -dest /opt/gotrue
```

Flags:
- `-version` — supabase/auth tag to build (default `v2.196.0`)
- `-dest` — directory the binary is copied into (default `/opt/gotrue`)
- `-workdir` — where the source gets cloned (default `.gotrue-builder-src`); re-running with the same version reuses the existing clone instead of re-cloning

The binary at `<dest>/gotrue-auth` is what you point your systemd unit(s) at. This repo doesn't modify GoTrue's source — it only clones the tagged upstream code and builds it as-is.
