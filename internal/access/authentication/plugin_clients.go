package authentication

import (
	"context"
	"net/rpc"
	"os"
	"os/exec"
	"strings"
	"sync"

	hplugin "github.com/hashicorp/go-plugin"

	"github.com/calypr/syfon/internal/config"
	"github.com/calypr/syfon/plugin"
)

// authenticationPluginManager manages the plugin process and calls Authenticate.
type authenticationPluginManager struct {
	client *pluginClient
}

// newAuthenticationPluginManager loads the plugin binary and returns a manager.
func newAuthenticationPluginManager(pluginPath string, childEnv []string) (*authenticationPluginManager, error) {
	cmd := exec.Command(pluginPath)
	cmd.Env = append([]string(nil), childEnv...)
	client := hplugin.NewClient(&hplugin.ClientConfig{
		HandshakeConfig: plugin.Handshake,
		Plugins: map[string]hplugin.Plugin{
			"authn": &authnPluginRPC{},
		},
		Cmd:              cmd,
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

	return &authenticationPluginManager{client: &pluginClient{client: client, raw: raw}}, nil
}

// Authenticate delegates to the plugin.
func (pm *authenticationPluginManager) Authenticate(ctx context.Context, in *plugin.AuthenticationInput) (*plugin.AuthenticationOutput, error) {
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

// authorizationPluginManager manages the plugin process and calls Authorize.
type authorizationPluginManager struct {
	client *pluginClient
}

// newAuthorizationPluginManager loads the plugin binary and returns a manager.
func newAuthorizationPluginManager(pluginPath string, childEnv []string) (*authorizationPluginManager, error) {
	cmd := exec.Command(pluginPath)
	cmd.Env = append([]string(nil), childEnv...)
	client := hplugin.NewClient(&hplugin.ClientConfig{
		HandshakeConfig: plugin.Handshake,
		Plugins: map[string]hplugin.Plugin{
			"authz": &authzPluginRPC{},
		},
		Cmd:              cmd,
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

	return &authorizationPluginManager{client: &pluginClient{client: client, raw: raw}}, nil
}

// Authorize delegates to the plugin.
func (pm *authorizationPluginManager) Authorize(ctx context.Context, in *plugin.AuthorizationInput) (*plugin.AuthorizationOutput, error) {
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

var _ plugin.AuthorizationPlugin = (*authorizationPluginManager)(nil)
var _ plugin.AuthenticationPlugin = (*authenticationPluginManager)(nil)

var documentedAuthEnvironmentKeys = []string{
	"DRS_AUTH_MOCK_ENABLED",
	"DRS_AUTH_MOCK_REQUIRE_AUTH_HEADER",
	"DRS_AUTH_MOCK_RESOURCES",
	"DRS_AUTH_MOCK_METHODS",
	"DRS_LOCAL_AUTHZ_CSV",
	"SYFON_AUTHZ_PLUGIN_PATH",
	"SYFON_AUTHN_PLUGIN_PATH",
	"DRS_FENCE_URL",
}

func pluginEnvironment(auth config.AuthConfig) []string {
	values := make(map[string]string, len(documentedAuthEnvironmentKeys))
	if auth.Mock.Enabled {
		values["DRS_AUTH_MOCK_ENABLED"] = "true"
	} else if value, ok := os.LookupEnv("DRS_AUTH_MOCK_ENABLED"); ok {
		values["DRS_AUTH_MOCK_ENABLED"] = value
	}
	if auth.Mock.RequireAuthHeader {
		values["DRS_AUTH_MOCK_REQUIRE_AUTH_HEADER"] = "true"
	} else if value, ok := os.LookupEnv("DRS_AUTH_MOCK_REQUIRE_AUTH_HEADER"); ok {
		values["DRS_AUTH_MOCK_REQUIRE_AUTH_HEADER"] = value
	}
	if len(auth.Mock.Resources) > 0 {
		values["DRS_AUTH_MOCK_RESOURCES"] = strings.Join(auth.Mock.Resources, ",")
	} else if value, ok := os.LookupEnv("DRS_AUTH_MOCK_RESOURCES"); ok {
		values["DRS_AUTH_MOCK_RESOURCES"] = value
	}
	if len(auth.Mock.Methods) > 0 {
		values["DRS_AUTH_MOCK_METHODS"] = strings.Join(auth.Mock.Methods, ",")
	} else if value, ok := os.LookupEnv("DRS_AUTH_MOCK_METHODS"); ok {
		values["DRS_AUTH_MOCK_METHODS"] = value
	}
	if auth.LocalAuthzCSV != "" {
		values["DRS_LOCAL_AUTHZ_CSV"] = auth.LocalAuthzCSV
	} else if value, ok := os.LookupEnv("DRS_LOCAL_AUTHZ_CSV"); ok {
		values["DRS_LOCAL_AUTHZ_CSV"] = value
	}
	if auth.PluginPaths.Authz != "" {
		values["SYFON_AUTHZ_PLUGIN_PATH"] = auth.PluginPaths.Authz
	} else if value, ok := os.LookupEnv("SYFON_AUTHZ_PLUGIN_PATH"); ok {
		values["SYFON_AUTHZ_PLUGIN_PATH"] = value
	}
	if auth.PluginPaths.Authn != "" {
		values["SYFON_AUTHN_PLUGIN_PATH"] = auth.PluginPaths.Authn
	} else if value, ok := os.LookupEnv("SYFON_AUTHN_PLUGIN_PATH"); ok {
		values["SYFON_AUTHN_PLUGIN_PATH"] = value
	}
	if auth.FenceURL != "" {
		values["DRS_FENCE_URL"] = auth.FenceURL
	} else if value, ok := os.LookupEnv("DRS_FENCE_URL"); ok {
		values["DRS_FENCE_URL"] = value
	}

	keySet := make(map[string]struct{}, len(documentedAuthEnvironmentKeys))
	for _, key := range documentedAuthEnvironmentKeys {
		keySet[key] = struct{}{}
	}
	env := make([]string, 0, len(os.Environ())+len(values))
	seen := make(map[string]struct{}, len(values))
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if _, documented := keySet[key]; documented {
			if _, alreadySeen := seen[key]; alreadySeen {
				continue
			}
			seen[key] = struct{}{}
			continue
		}
		env = append(env, entry)
	}
	for _, key := range documentedAuthEnvironmentKeys {
		if value, ok := values[key]; ok {
			env = append(env, key+"="+value)
		}
	}
	return env
}
