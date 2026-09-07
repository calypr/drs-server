# Plugin integration

Syfon can start an external authentication or authorization binary beside the server. The server communicates with that child process over HashiCorp `go-plugin` using NetRPC. A plugin is a server process extension. It is not an HTTP client plugin and it does not change the CLI.

The public Go contract lives in [`plugin/types.go`](https://github.com/calypr/syfon/blob/development/plugin/types.go):

- `AuthenticationPlugin.Authenticate` receives an `AuthenticationInput` and returns an `AuthenticationOutput`.
- `AuthorizationPlugin.Authorize` receives an `AuthorizationInput` and returns an `AuthorizationOutput`.

The same handshake configuration is used for both plugin types. Register the RPC plugin under `authn` or `authz`, matching the interface it implements.

## Configure a plugin

Set paths in the server config:

```yaml
auth:
  mode: gen3
  plugin_paths:
    authn: /opt/syfon/plugin/authn
    authz: /opt/syfon/plugin/authz
```

Syfon exports those values as `SYFON_AUTHN_PLUGIN_PATH` and `SYFON_AUTHZ_PLUGIN_PATH`. You can set the environment variables directly when you do not use a config file:

```bash
export SYFON_AUTHN_PLUGIN_PATH=/opt/syfon/plugin/authn
export SYFON_AUTHZ_PLUGIN_PATH=/opt/syfon/plugin/authz
bin/syfon serve --config config.yaml
```

Syfon starts each configured plugin during server startup. If an authentication plugin cannot start or complete its handshake, the server uses the built-in authentication for the selected mode. An authorization startup failure leaves the external authorizer unavailable. An authentication response with `Authenticated: false` or an authorization response with `Allow: false` denies the request. Plugin errors also deny the affected request.

## Implement the contracts

Import the public package from the repository module:

```go
import "github.com/calypr/syfon/plugin"
```

An authentication plugin receives the request ID, the raw `Authorization` header, and request metadata:

```go
type AuthenticationPlugin interface {
	Authenticate(context.Context, *AuthenticationInput) (*AuthenticationOutput, error)
}

type AuthenticationOutput struct {
	Authenticated bool
	Subject       string
	Claims        map[string]interface{}
	Reason        string
}
```

An authorization plugin receives the request ID, subject, action, resource, claims, and request metadata:

```go
type AuthorizationPlugin interface {
	Authorize(context.Context, *AuthorizationInput) (*AuthorizationOutput, error)
}

type AuthorizationOutput struct {
	Allow       bool
	Reason      string
	Obligations map[string]interface{}
}
```

The plugin binary must serve the RPC adapter expected by `go-plugin` and use the shared `plugin.Handshake` value. The examples under [`plugin/local_auth`](https://github.com/calypr/syfon/tree/development/plugin/local_auth) and [`plugin/gen3_auth`](https://github.com/calypr/syfon/tree/development/plugin/gen3_auth) show the registration shape.

## Build the bundled examples

The repository includes two authentication examples:

```bash
make build-local-auth-plugin
make build-gen3-auth-plugin
```

The binaries are written to `bin/local_auth_plugin` and `bin/gen3_auth_plugin`.

### Local authentication example

`plugin/local_auth` reads `DRS_BASIC_AUTH_USER` and `DRS_BASIC_AUTH_PASSWORD` when the plugin process starts. It checks a Basic Auth header when either value is configured. It accepts the request when both values are empty. The built-in server local-auth path also supports `auth.local_authz_csv`; the example plugin does not load that CSV.

### Gen3 authentication example

`plugin/gen3_auth` is a wiring example. Without mock mode, it accepts any non-empty `Authorization` header and does not validate a token or fetch privileges. With `DRS_AUTH_MOCK_ENABLED=true`, it accepts every request. Do not use this example as a production token validator.

For a deployed Gen3 environment, use a plugin that validates the token and returns the subject and claims required by the deployment. Syfon can then resolve Gen3 privileges and apply the authorization path configured for the server.

## Test and troubleshoot

Build the plugin and server from the same checkout. Confirm that the plugin binary is executable and that the path is absolute. Check the Syfon startup log when a plugin does not load. An invalid handshake, missing executable, or RPC failure leaves the built-in fallback in place when one exists.

For an authorization plugin, return `Allow: false` with a useful `Reason` when policy denies a request. For an authentication plugin, return `Authenticated: false` instead of treating a malformed header as a successful identity.
