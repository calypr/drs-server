# Syfon Developer Guide: Authoring Authorization Plugins

This guide explains how to implement a custom authorization plugin for Syfon using the HashiCorp go-plugin system.

## Plugin Contract

Your plugin must implement the following Go interface:

```go
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

```go
package main

import (
    "context"
    "net/rpc"
    hplugin "github.com/hashicorp/go-plugin"
    "github.com/calypr/syfon/plugin"
)

type MyAuthzPlugin struct{}

func (p *MyAuthzPlugin) Authorize(ctx context.Context, in *plugin.AuthorizationInput) (*plugin.AuthorizationOutput, error) {
    // Implement your policy logic here
    allow := (in.Subject == "admin")
    return &plugin.AuthorizationOutput{
        Allow: allow,
        Reason: "example policy",
        Obligations: map[string]interface{}{
            "resources": []interface{}{in.Resource},
            "privileges": map[string]interface{}{in.Resource: []interface{}{in.Action}},
        },
    }, nil
}

type authzPluginRPC struct {
    hplugin.Plugin
    Impl *MyAuthzPlugin
}

func (p *authzPluginRPC) Server(*hplugin.MuxBroker) (interface{}, error) {
    return p.Impl, nil
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
