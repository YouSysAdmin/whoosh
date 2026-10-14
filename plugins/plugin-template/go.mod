module github.com/yousysadmin/whoosh/plugins/plugin-template

go 1.27.0

require github.com/yousysadmin/whoosh v1.8.1

require (
	github.com/Masterminds/semver/v3 v3.5.0 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
)

// This template lives inside the whoosh repo, so it builds against the checkout two levels up.
// After copying it out, delete this replace and `require` a tagged whoosh version instead.
replace github.com/yousysadmin/whoosh => ../..
