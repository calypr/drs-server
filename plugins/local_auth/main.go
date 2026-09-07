package main

import (
	"context"
	hplugin "github.com/hashicorp/go-plugin"
	"github.com/calypr/syfon/plugin"
	"net/rpc"
	"os"
)

type LocalAuthPlugin struct {
	BasicUser string
	BasicPass string
}

func (p *LocalAuthPlugin) Authenticate(ctx context.Context, in *plugin.AuthenticationInput) (*plugin.AuthenticationOutput, error) {
	if p.BasicUser != "" || p.BasicPass != "" {
		err := ValidateBasicAuth(in.AuthHeader, p.BasicUser, p.BasicPass)
		if err != nil {
			return &plugin.AuthenticationOutput{Authenticated: false, Reason: err.Error()}, nil
		}
	}
	return &plugin.AuthenticationOutput{Authenticated: true}, nil
}

type LocalAuthPluginRPC struct {
	hplugin.Plugin
	Impl *LocalAuthPlugin
}

func (p *LocalAuthPluginRPC) Server(*hplugin.MuxBroker) (interface{}, error) {
	return p.Impl, nil
}

func (p *LocalAuthPluginRPC) Client(b *hplugin.MuxBroker, c *rpc.Client) (interface{}, error) {
	return nil, nil // Not used in plugin binary
}

func main() {
	user := os.Getenv("DRS_BASIC_AUTH_USER")
	pass := os.Getenv("DRS_BASIC_AUTH_PASSWORD")
	hplugin.Serve(&hplugin.ServeConfig{
		HandshakeConfig: plugin.Handshake,
		Plugins: map[string]hplugin.Plugin{
			"authn": &LocalAuthPluginRPC{Impl: &LocalAuthPlugin{BasicUser: user, BasicPass: pass}},
		},
	})
}
