//go:build tools

// Pins the code generator in go.mod so `go run github.com/Khan/genqlient` works after `go mod tidy`.
package rearm

import _ "github.com/Khan/genqlient/generate"
