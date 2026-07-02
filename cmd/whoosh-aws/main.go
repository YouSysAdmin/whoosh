// Build whoosh with the AWS plugin via Goreleaser
package main

import (
	"github.com/yousysadmin/whoosh/entrypoint"

	_ "github.com/yousysadmin/whoosh/plugins/aws"
	_ "github.com/yousysadmin/whoosh/plugins/standard"
)

func main() {
	entrypoint.Main()
}
