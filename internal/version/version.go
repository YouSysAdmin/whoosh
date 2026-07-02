package version

// Version is overridable at build time with
// -ldflags "-s -w -X github.com/yousysadmin/whoosh/internal/version.Version=1.2.3"
var Version = "dev"
