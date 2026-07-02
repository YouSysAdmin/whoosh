package deployfile

import (
	_ "embed"
	"strings"
)

//go:embed templates/deployfile.yaml
var deployfileTemplate string

//go:embed templates/stage.yaml
var stageTemplate string

//go:embed templates/example.sh
var exampleScript string

// DeployfileScaffold returns the contents for a starter shared Deployfile.yml.
func DeployfileScaffold() []byte {
	return []byte(deployfileTemplate)
}

// StageScaffold returns the contents for a starter deploy/<stage>.yml, with the stage name substituted for the
// {{stage}} placeholder.
func StageScaffold(stage string) []byte {
	return []byte(strings.ReplaceAll(stageTemplate, "{{stage}}", stage))
}

// ExampleScript returns a sample script for deploy/scripts/.
func ExampleScript() []byte {
	return []byte(exampleScript)
}
