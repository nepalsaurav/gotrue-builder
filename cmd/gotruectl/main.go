// Command gotruectl manages a local Docker-based GoTrue setup: a shared
// Postgres container plus one GoTrue container per tenant.
//
// Install/update with:
//
//	go install github.com/nepalsaurav/gotrue-builder/cmd/gotruectl@latest
package main

import "github.com/nepalsaurav/gotrue-builder/internal/gotruectl"

func main() {
	gotruectl.Execute()
}
