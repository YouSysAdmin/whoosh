package ast

import (
	"fmt"

	"github.com/Masterminds/semver/v3"

	"github.com/yousysadmin/whoosh/internal/errors"
)

// SchemaVersion is the Deployfile schema version this build implements, and MinSchemaVersion is the oldest it still accepts.
// A Deployfiles version: must fall within [MinSchemaVersion, SchemaVersion].
// Bump these as the schema evolves: raise SchemaVersion when adding compatible fields, raise MinSchemaVersion when
// dropping support for an old shape.
var (
	SchemaVersion    = semver.MustParse("1.0.0")
	MinSchemaVersion = semver.MustParse("1.0.0")
)

// checkVersion enforces that a Deployfile's declared version is one this build understands.
// An empty version is treated as compatible (back-compat for files written before the field existed).
// "1", "1.0" and "1.0.0" all parse equal.
func checkVersion(raw string) error {
	if raw == "" {
		return nil
	}
	v, err := semver.NewVersion(raw)
	if err != nil {
		return &errors.ConfigError{Msg: fmt.Sprintf("invalid version %q", raw), Err: err}
	}
	if v.LessThan(MinSchemaVersion) {
		return &errors.VersionError{Have: v.String(), Min: MinSchemaVersion.String(), TooOld: true}
	}
	if v.GreaterThan(SchemaVersion) {
		return &errors.VersionError{Have: v.String(), Max: SchemaVersion.String(), TooNew: true}
	}
	return nil
}
