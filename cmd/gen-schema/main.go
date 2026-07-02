package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"reflect"

	"github.com/invopop/jsonschema"
	"github.com/yousysadmin/whoosh/internal/deployfile/ast"
)

const commentsPath = "./internal/deployfile/ast"
const modulePath = "github.com/yousysadmin/whoosh"

func main() {
	out := flag.String("o", "deployfile.schema.json", "output file path")
	flag.Parse()

	r := &jsonschema.Reflector{
		// Use the yaml tags as property names, they are what the loader reads.
		FieldNameTag: "yaml",
		// Inline DeployFile at the root (nested types still go in $defs).
		ExpandedStruct: true,
		// StringList accepts a scalar or a list in YAML, reflection alone would emit only "array".
		// Map it here so the schema lib stays out of the ast package.
		Mapper: func(t reflect.Type) *jsonschema.Schema {
			if t == reflect.TypeOf(ast.StringList{}) {
				return &jsonschema.Schema{
					OneOf: []*jsonschema.Schema{
						{Type: "string"},
						{Type: "array", Items: &jsonschema.Schema{Type: "string"}},
					},
				}
			}
			return nil
		},
	}

	if err := r.AddGoComments(modulePath, commentsPath); err != nil {
		fail("read doc comments from %s: %v (run from the repo root)", commentsPath, err)
	}

	schema := r.Reflect(&ast.DeployFile{})
	schema.ID = jsonschema.ID("https://" + modulePath + "/deployfile.schema.json")
	schema.Title = "whoosh Deployfile"
	schema.Description = "Schema for whoosh Deployfile.yml and deploy/<stage>.yml. " +
		"Generated from internal/deployfile/ast by cmd/gen-schema - run `make schema` to regenerate."

	data, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		fail("marshal schema: %v", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(*out, data, 0o644); err != nil {
		fail("write %s: %v", *out, err)
	}
	fmt.Printf("wrote %s\n", *out)
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "gen-schema: "+format+"\n", args...)
	os.Exit(1)
}
