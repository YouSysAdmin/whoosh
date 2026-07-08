package main

import (
	"github.com/yousysadmin/whoosh/entrypoint"
	_ "github.com/yousysadmin/whoosh/plugins/core"
)

func main() {
	entrypoint.Main()
}
