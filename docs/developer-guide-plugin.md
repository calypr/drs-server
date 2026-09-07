# Syfon Developer Guide: Authoring Authorization Plugins

This guide explains how to implement a custom authorization plugin for Syfon using the HashiCorp go-plugin system.

## Plugin Contract

Your plugin must implement the following Go interface:

```go
package plugin

import "context"

type AuthorizationPlugin interface {
	Authorize(ctx context.Context, in *AuthorizationInput) (*AuthorizationOutput, error)
}

// Input struct
type AuthorizationInput struct {
	RequestID string
	Subject   string
	Action    string
	Resource  string
	Claims    map[string]interface{}
	Metadata  map[string]interface{}
}

// Output struct
type AuthorizationOutput struct {
	Allow       bool
	Reason      string
	Obligations map[string]interface{}
}
```

- The plugin receives all relevant request context, JWT claims, and metadata.
- It must return `Allow=true` to permit, or `Allow=false` to deny, with an optional reason and obligations.

## Handshake and Communication
- Use `github.com/hashicorp/go-plugin` for plugin RPC and handshake.
- The handshake config must match Syfon’s expectations (see `plugin/types.go`).

## Example Plugin Skeleton

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

type MyAuthzPlugin struct{}

func (p *MyAuthzPlugin) Authorize(ctx context.Context, in *plugin.AuthorizationInput) (*plugin.AuthorizationOutput, error) {
	allow := (in.Subject == "admin")
	return &plugin.AuthorizationOutput{
		Allow:  allow,
		Reason: "example policy",
		Obligations: map[string]interface{}{
			"resources":  []interface{}{in.Resource},
			"privileges": map[string]interface{}{in.Resource: []interface{}{in.Action}},
		},
	}, nil
}

type authzRPCServer struct {
	impl *MyAuthzPlugin
}

func (s *authzRPCServer) Authorize(in *plugin.AuthorizationInput, reply *plugin.AuthorizationOutput) error {
	if reply == nil {
		return errors.New("authorization RPC reply is nil")
	}
	if s.impl == nil {
		return errors.New("authorization plugin implementation is nil")
	}
	output, err := s.impl.Authorize(context.Background(), in)
	if err != nil {
		return err
	}
	if output == nil {
		return errors.New("authorization plugin returned nil output")
	}
	*reply = *output
	return nil
}

type authzPluginRPC struct {
	hplugin.Plugin
	Impl *MyAuthzPlugin
}

func (p *authzPluginRPC) Server(*hplugin.MuxBroker) (interface{}, error) {
	return &authzRPCServer{impl: p.Impl}, nil
}

func (p *authzPluginRPC) Client(*hplugin.MuxBroker, *rpc.Client) (interface{}, error) {
	return nil, nil
}

func main() {
	hplugin.Serve(&hplugin.ServeConfig{
		HandshakeConfig: plugin.Handshake,
		Plugins: map[string]hplugin.Plugin{
			"authz": &authzPluginRPC{Impl: &MyAuthzPlugin{}},
		},
	})
}
```

## Testing Your Plugin
- Write unit tests for your plugin logic.
- You can run your plugin standalone for debugging, but it must be launched by Syfon in production.

## Versioning and Compatibility
- Maintain compatibility with the Syfon plugin contract.
- If the contract changes, update both Syfon and your plugin accordingly.

## Resources
- [HashiCorp go-plugin documentation](https://github.com/hashicorp/go-plugin)
- See `plugin/types.go` for the public contract and follow the local server-wrapper pattern above for RPC registration.
