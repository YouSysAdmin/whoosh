// The default whoosh binary with all bundled plugins
package main

import (
	"github.com/yousysadmin/whoosh/entrypoint"

	_ "github.com/yousysadmin/whoosh/plugins/aws"
	_ "github.com/yousysadmin/whoosh/plugins/core"
	_ "github.com/yousysadmin/whoosh/plugins/rbenv"
	_ "github.com/yousysadmin/whoosh/plugins/slack"
)

func main() {
	entrypoint.Main()
}
