package middleware

import (
	"context"

	"github.com/calypr/syfon/plugin"
)

// DummyPluginManager implements the public authorization plugin contract for testing.
type DummyPluginManager struct{}

func (d *DummyPluginManager) Authorize(ctx context.Context, in *plugin.AuthorizationInput) (*plugin.AuthorizationOutput, error) {
	return &plugin.AuthorizationOutput{Allow: true}, nil
}
