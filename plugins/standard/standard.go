// Package standard blank-imports the plugins whoosh ships with ("owned" plugins).
// Both the default binary (cmd/whoosh) and a custom `whoosh build` import this package, so a custom build gets the same
// built-ins as the official one.
//
// The only bundled built-in is print-hosts-table (default-on; a Deployfile disables it per stage with
// `plugins: [{name: print-hosts-table, enabled: false}]`).
package standard

import _ "github.com/yousysadmin/whoosh/plugins/standard/print_hosts_table"
