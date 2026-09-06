package authentication

import (
	"context"
	"net/rpc"
	"os"
	"os/exec"
	"sync"

	hplugin "github.com/hashicorp/go-plugin"

	"github.com/calypr/syfon/plugin"
)

// AuthenticationPluginManager manages the plugin process and calls Authenticate.
type AuthenticationPluginManager struct {
	client *pluginClient
}

// NewAuthenticationPluginManager loads the plugin binary and returns a manager.
func NewAuthenticationPluginManager(pluginPath string) (*AuthenticationPluginManager, error) {
	client := hplugin.NewClient(&hplugin.ClientConfig{
		HandshakeConfig: plugin.Handshake,
		Plugins: map[string]hplugin.Plugin{
			"authn": &authnPluginRPC{},
		},
		Cmd:              exec.Command(pluginPath),
		AllowedProtocols: []hplugin.Protocol{hplugin.ProtocolNetRPC},
	})

	rpcClient, err := client.Client()
	if err != nil {
		return nil, err
	}

	raw, err := rpcClient.Dispense("authn")
	if err != nil {
		return nil, err
	}

	return &AuthenticationPluginManager{client: &pluginClient{client: client, raw: raw}}, nil
}

// Authenticate delegates to the plugin.
func (pm *AuthenticationPluginManager) Authenticate(ctx context.Context, in *plugin.AuthenticationInput) (*plugin.AuthenticationOutput, error) {
	pm.client.mu.Lock()
	defer pm.client.mu.Unlock()
	pluginImpl, ok := pm.client.raw.(plugin.AuthenticationPlugin)
	if !ok {
		return nil, os.ErrInvalid
	}
	return pluginImpl.Authenticate(ctx, in)
}

type authnPluginRPC struct{ hplugin.Plugin }

func (p *authnPluginRPC) Server(*hplugin.MuxBroker) (interface{}, error) {
	return nil, nil // Not used in client
}
func (p *authnPluginRPC) Client(b *hplugin.MuxBroker, c *rpc.Client) (interface{}, error) {
	return &authnRPC{client: c}, nil
}

type authnRPC struct {
	client *rpc.Client
}

func (a *authnRPC) Authenticate(ctx context.Context, in *plugin.AuthenticationInput) (*plugin.AuthenticationOutput, error) {
	var out plugin.AuthenticationOutput
	err := a.client.Call("Plugin.Authenticate", in, &out)
	return &out, err
}

type pluginClient struct {
	client *hplugin.Client
	raw    interface{}
	mu     sync.Mutex
}

// AuthorizationPluginManager manages the plugin process and calls Authorize.
type AuthorizationPluginManager struct {
	client *pluginClient
}

// NewAuthorizationPluginManager loads the plugin binary and returns a manager.
func NewAuthorizationPluginManager(pluginPath string) (*AuthorizationPluginManager, error) {
	client := hplugin.NewClient(&hplugin.ClientConfig{
		HandshakeConfig: plugin.Handshake,
		Plugins: map[string]hplugin.Plugin{
			"authz": &authzPluginRPC{},
		},
		Cmd:              exec.Command(pluginPath),
		AllowedProtocols: []hplugin.Protocol{hplugin.ProtocolNetRPC},
	})

	rpcClient, err := client.Client()
	if err != nil {
		return nil, err
	}

	raw, err := rpcClient.Dispense("authz")
	if err != nil {
		return nil, err
	}

	return &AuthorizationPluginManager{client: &pluginClient{client: client, raw: raw}}, nil
}

// Authorize delegates to the plugin.
func (pm *AuthorizationPluginManager) Authorize(ctx context.Context, in *plugin.AuthorizationInput) (*plugin.AuthorizationOutput, error) {
	pm.client.mu.Lock()
	defer pm.client.mu.Unlock()
	pluginImpl, ok := pm.client.raw.(plugin.AuthorizationPlugin)
	if !ok {
		return nil, os.ErrInvalid
	}
	return pluginImpl.Authorize(ctx, in)
}

type authzPluginRPC struct{ hplugin.Plugin }

func (p *authzPluginRPC) Server(*hplugin.MuxBroker) (interface{}, error) {
	return nil, nil // Not used in client
}
func (p *authzPluginRPC) Client(b *hplugin.MuxBroker, c *rpc.Client) (interface{}, error) {
	return &authzRPC{client: c}, nil
}

type authzRPC struct {
	client *rpc.Client
}

func (a *authzRPC) Authorize(ctx context.Context, in *plugin.AuthorizationInput) (*plugin.AuthorizationOutput, error) {
	var out plugin.AuthorizationOutput
	err := a.client.Call("Plugin.Authorize", in, &out)
	return &out, err
}

var _ plugin.AuthorizationPlugin = (*AuthorizationPluginManager)(nil)
var _ plugin.AuthenticationPlugin = (*AuthenticationPluginManager)(nil)
