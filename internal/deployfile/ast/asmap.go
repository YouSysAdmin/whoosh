package ast

import "gopkg.in/yaml.v3"

// AsMap renders the resolved config as a generic map keyed by its YAML field names.
// It is exposed to templates as {{.config}} so scripts can build flexible logic from the whole Deployfile (servers,
// roles, app, vars, ...).
func (c *DeployFile) AsMap() (map[string]any, error) {
	data, err := yaml.Marshal(c)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return m, nil
}
