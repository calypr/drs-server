package plugin

import (
	"context"
	hplugin "github.com/hashicorp/go-plugin"
)

// Handshake is the plugin handshake configuration for hashicorp/go-plugin
var Handshake = hplugin.HandshakeConfig{
	ProtocolVersion:  1,
	MagicCookieKey:   "SYFON_AUTHZ_PLUGIN",
	MagicCookieValue: "syfon_authz_plugin_v1",
}

// AuthorizationInput is the request sent to the plugin for an authz decision.
type AuthorizationInput struct {
	RequestID string
	Subject   string
	Action    string
	Resource  string
	Claims    map[string]interface{}
	Metadata  map[string]interface{}
}

// AuthorizationOutput is the plugin's response.
type AuthorizationOutput struct {
	Allow       bool
	Reason      string
	Obligations map[string]interface{}
}

// AuthorizationPlugin is the interface plugins must implement.
type AuthorizationPlugin interface {
	Authorize(ctx context.Context, in *AuthorizationInput) (*AuthorizationOutput, error)
}

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

// AuthenticationPlugin is the interface plugins must implement.
type AuthenticationPlugin interface {
	Authenticate(ctx context.Context, in *AuthenticationInput) (*AuthenticationOutput, error)
}
