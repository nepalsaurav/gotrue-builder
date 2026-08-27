// Command gotruectl (built as gotrue-builder) manages a local Docker-based
// GoTrue setup: a shared Postgres container plus one GoTrue container per
// tenant. It also keeps the original `build` command, which clones and
// builds supabase/auth from source for the (optional) self-built-image path.
package main

import (
	"fmt"
	"os"
)

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  gotruectl postgres up|down
  gotruectl tenant create --name NAME --port PORT [--signup] [--site-url URL] [--external-url URL] [--jwt-secret SECRET] [--jwt-aud AUD]
  gotruectl tenant list
  gotruectl tenant logs --name NAME [--follow]
  gotruectl tenant start --name NAME
  gotruectl tenant stop --name NAME
  gotruectl tenant delete --name NAME [--keep-data]
  gotruectl build [-version v2.196.0] [-dest /opt/gotrue] [-workdir DIR]`)
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	var err error
	switch os.Args[1] {
	case "postgres":
		err = runPostgresCmd(os.Args[2:])
	case "tenant":
		err = runTenantCmd(os.Args[2:])
	case "build":
		err = runBuildCmd(os.Args[2:])
	case "-h", "--help", "help":
		usage()
		return
	default:
		usage()
		os.Exit(1)
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
