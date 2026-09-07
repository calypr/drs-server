# Gen3 Auth Plugin

This plugin implements the Syfon AuthenticationPlugin interface for Gen3/Fence-style authentication.

## Usage

Build the plugin binary:

```
go build -o gen3_auth_plugin .
```

Set the following environment variables as needed:
- `DRS_AUTH_MOCK_ENABLED=true` to enable mock mode (all requests authenticated)

Configure Syfon to use the plugin:

```
export SYFON_AUTHN_PLUGIN_PATH=/path/to/gen3_auth_plugin
syfon serve
```

