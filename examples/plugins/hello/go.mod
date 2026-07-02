module github.com/yousysadmin/whoosh-example-hello

go 1.26.4

require github.com/yousysadmin/whoosh v0.0.0-00010101000000-000000000000

require (
	github.com/Masterminds/semver/v3 v3.5.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

// This example lives inside the whoosh repo, build against the checkout next to
// it. A real out-of-tree plugins would `require` a tagged whoosh version instead.
replace github.com/yousysadmin/whoosh => ./../../..
