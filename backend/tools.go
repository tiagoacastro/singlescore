//go:build tools
// +build tools

// This file ensures gqlgen is pinned as a tool dependency so
// `go run github.com/99designs/gqlgen` works deterministically.
package tools

import (
	_ "github.com/99designs/gqlgen"
	_ "github.com/99designs/gqlgen/graphql"
	_ "github.com/99designs/gqlgen/graphql/handler"
	_ "github.com/99designs/gqlgen/graphql/playground"
)
