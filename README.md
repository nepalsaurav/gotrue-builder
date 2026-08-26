# gotrue-builder

Builds [supabase/auth](https://github.com/supabase/auth) (GoTrue) from source via GitHub Actions and publishes the resulting binaries as GitHub Releases — so you can install GoTrue on a bare-metal VM without needing a Go toolchain there.

## Usage

1. Go to **Actions -> Build supabase/auth and publish release -> Run workflow**.
2. Enter the supabase/auth tag to build (e.g. `v2.196.0`).
3. When the run finishes, grab the binary for your architecture from this repo's **Releases** page:
   - `gotrue-auth-linux-amd64`
   - `gotrue-auth-linux-arm64`
   - `SHA256SUMS` — verify with `sha256sum -c SHA256SUMS`

Binaries are statically linked (`CGO_ENABLED=0`) and built directly from the tagged upstream source — this repo doesn't modify GoTrue's code, it only builds and republishes it.
