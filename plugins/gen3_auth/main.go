package main

import (
	"context"
	hplugin "github.com/hashicorp/go-plugin"
	"github.com/calypr/syfon/plugin"
	"net/rpc"
	"os"
)

type Gen3AuthPlugin struct {
	MockEnabled bool
}

func (p *Gen3AuthPlugin) Authenticate(_ context.Context, in *plugin.AuthenticationInput) (*plugin.AuthenticationOutput, error) {
	if p.MockEnabled {
		return &plugin.AuthenticationOutput{Authenticated: true}, nil
	}
	if in.AuthHeader == "" {
		return &plugin.AuthenticationOutput{Authenticated: false, Reason: "missing auth header"}, nil
	}
	// Token extraction and privilege fetching logic would go here
	return &plugin.AuthenticationOutput{Authenticated: true}, nil
}

type Gen3AuthPluginRPC struct {
	hplugin.Plugin
	Impl *Gen3AuthPlugin
}

func (p *Gen3AuthPluginRPC) Server(*hplugin.MuxBroker) (interface{}, error) {
	return p.Impl, nil
}

func (p *Gen3AuthPluginRPC) Client(b *hplugin.MuxBroker, c *rpc.Client) (interface{}, error) {
	return nil, nil // Not used in plugin binary
}

func main() {
	mock := os.Getenv("DRS_AUTH_MOCK_ENABLED") == "true"
	hplugin.Serve(&hplugin.ServeConfig{
		HandshakeConfig: plugin.Handshake,
		Plugins: map[string]hplugin.Plugin{
			"authn": &Gen3AuthPluginRPC{Impl: &Gen3AuthPlugin{MockEnabled: mock}},
		},
	})
}
