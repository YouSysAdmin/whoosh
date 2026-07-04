package plugintemplate

import (
	"context"
	"fmt"
)

// client is the placeholder for whatever your plugin talks to - an HTTP API, a cloud SDK, a service registry, a CLI.
// Configure builds it exactly once from the global params, and every feature and action shares it (the aws plugin
// does the same with its EC2/SSM clients).
//
// TODO: replace this whole file with your real client. Keep the shape: constructed from globalParams, methods take a
// ctx (honor ctx.Done() in long calls so Ctrl-C stays responsive), errors wrapped with the plugin/feature name.
type client struct {
	endpoint string
	token    string
}

// newClient builds the shared client. Fail here (offline) for config that can never work, defer network validation
// to first use so `whoosh validate` stays connection-free.
func newClient(p globalParams) (*client, error) {
	return &client{endpoint: p.Endpoint, token: p.Token}, nil
}

// requireEndpoint is the use-time guard for features that need the external system configured.
func (c *client) requireEndpoint(feature string) error {
	if c.endpoint == "" {
		return fmt.Errorf("%s: params.endpoint is not configured", feature)
	}
	return nil
}

// fetch retrieves one value from the external system (used by the context feature and the render action).
// TODO: replace the canned response with a real call.
func (c *client) fetch(_ context.Context, key string) (string, error) {
	return "TODO-value-of-" + key, nil
}

// discover lists host addresses from the external system (used by the inventory feature).
// TODO: replace the canned response with a real discovery call (cloud API, service registry, CMDB, ...).
func (c *client) discover(_ context.Context) ([]string, error) {
	return []string{"203.0.113.10", "203.0.113.11"}, nil
}
