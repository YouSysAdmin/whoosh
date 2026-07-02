//go:build !noplugins

// The bundled ("standard") plugins are linked in by default.
// Build with `-tags noplugins` (make build-minimal) to drop them - plugins register via init(),
// so omitting this import omits them from the binary.
package main

import _ "github.com/yousysadmin/whoosh/plugins/standard"
