// Command server is the API entrypoint. P0-01 provides the minimal boot so the
// module builds; P0-03 wires Echo, pgx, slog and graceful shutdown.
package main

import (
	"fmt"
	"os"
)

func main() {
	// Placeholder: replaced by the Echo server in P0-03.
	fmt.Fprintln(os.Stderr, "smm-api: skeleton (see P0-03)")
}
