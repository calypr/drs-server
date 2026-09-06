# Syfon Authentication Plugin System

Syfon supports external authentication plugins using the same go-plugin architecture as authorization plugins. This allows operators to implement custom authentication logic (e.g., Basic Auth, JWT, OAuth2, Gen3, etc.) outside the main server binary. **Plugins are loaded at server startup (not build time) via the `SYFON_AUTHN_PLUGIN_PATH` environment variable.**

## Operator Guide

### Enabling an Authentication Plugin

1. Build or obtain a compatible authentication plugin binary implementing the Syfon AuthenticationPlugin interface.
2. Set the environment variable `SYFON_AUTHN_PLUGIN_PATH` to the path of the plugin binary.
3. Restart the Syfon server. **Plugin integration occurs at server startup, not build time.** All authentication requests will be delegated to the plugin.

### Fallback Behavior
- If no plugin is configured, Syfon uses built-in authentication (Basic Auth for local mode, JWT for Gen3 mode).
- If the plugin returns `Authenticated: false` or an error, the request is denied.

## Developer Guide

### Plugin Interface

Plugins must implement the following Go interface:

```go
package plugin

import "context"

// AuthenticationInput is the request sent to the plugin for authentication.
type AuthenticationInput struct {
	RequestID  string
	AuthHeader string
	Metadata   map[string]interface{}
}

// AuthenticationOutput is the plugin's response.
type AuthenticationOutput struct {
	Authenticated bool
	Subject       string
	Claims        map[string]interface{}
	Reason        string
}

type AuthenticationPlugin interface {
	Authenticate(ctx context.Context, in *AuthenticationInput) (*AuthenticationOutput, error)
}
```

### Plugin Registration
- The plugin must register itself with the go-plugin framework under the key `"authn"`.
- **Plugin integration occurs at server startup. No Syfon code changes or rebuilds are required to add or update a plugin.**
- See the Syfon source for an example of plugin RPC wiring.

### Example Plugin Skeleton

The `Server` method returns an RPC adapter. The adapter exposes the `net/rpc` method signature. It forwards each call to the context-aware plugin implementation with `context.Background()`.

```go
package main

import (
	"context"
	"errors"
	"net/rpc"

	"github.com/calypr/syfon/plugin"
	hplugin "github.com/hashicorp/go-plugin"
)

type MyAuthnPlugin struct{}

func (p *MyAuthnPlugin) Authenticate(ctx context.Context, in *plugin.AuthenticationInput) (*plugin.AuthenticationOutput, error) {
	// Implement your logic here
	return &plugin.AuthenticationOutput{Authenticated: true, Subject: "user"}, nil
}

type authnRPCServer struct {
	impl *MyAuthnPlugin
}

func (s *authnRPCServer) Authenticate(in *plugin.AuthenticationInput, reply *plugin.AuthenticationOutput) error {
	if reply == nil {
		return errors.New("authentication RPC reply is nil")
	}
	if s.impl == nil {
		return errors.New("authentication plugin implementation is nil")
	}
	output, err := s.impl.Authenticate(context.Background(), in)
	if err != nil {
		return err
	}
	if output == nil {
		return errors.New("authentication plugin returned nil output")
	}
	*reply = *output
	return nil
}

type authnPluginRPC struct {
	hplugin.Plugin
	Impl *MyAuthnPlugin
}

func (p *authnPluginRPC) Server(*hplugin.MuxBroker) (interface{}, error) {
	return &authnRPCServer{impl: p.Impl}, nil
}

func (p *authnPluginRPC) Client(*hplugin.MuxBroker, *rpc.Client) (interface{}, error) {
	return nil, nil
}

func main() {
	hplugin.Serve(&hplugin.ServeConfig{
		HandshakeConfig: plugin.Handshake,
		Plugins: map[string]hplugin.Plugin{
			"authn": &authnPluginRPC{Impl: &MyAuthnPlugin{}},
		},
	})
}
```

### Testing
- Use the Syfon test suite to verify plugin integration.
- You can inject a dummy plugin manager in tests for rapid iteration.

## Migration Notes
- Existing authentication logic is preserved as a fallback if no plugin is configured.
- For full migration, implement all required authentication flows in your plugin
