module github.com/yousysadmin/whoosh-examples

go 1.26.4

require github.com/yousysadmin/whoosh v0.0.0-00010101000000-000000000000

require (
	github.com/Masterminds/semver/v3 v3.5.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

// These examples live inside the whoosh repo, build against the checkout. A real
// out-of-tree plugins would `require` a tagged whoosh version instead.
replace github.com/yousysadmin/whoosh => ../..
