// Package core blank-imports the plugins whoosh ships with ("owned" plugins).
// The default binary (cmd/whoosh), the core binary (cmd/whoosh-core), and a custom `whoosh build` all import this
// package, so a custom build gets the same built-ins as the official ones.
//
// The core built-ins are print-hosts-table and systemd (both default-on; a Deployfile disables one per stage with
// `plugins: [{name: print-hosts-table, enabled: false}]`).
package core

import (
	_ "github.com/yousysadmin/whoosh/plugins/core/print_hosts_table"
	_ "github.com/yousysadmin/whoosh/plugins/core/systemd"
)
