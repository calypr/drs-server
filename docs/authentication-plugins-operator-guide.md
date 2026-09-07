# Syfon Authentication Plugin Operator Guide

## Enabling Authentication Plugins

1. Build or obtain a compatible authentication plugin binary.
2. Place the binary on the target system and set the environment variable `SYFON_AUTHN_PLUGIN_PATH` to the plugin binary path.
3. **Restart Syfon. Plugin integration occurs at server startup, not build time.** Authentication will now be delegated to the plugin.

## Fallback Behavior
- If no plugin is configured, Syfon uses built-in authentication.
- If the plugin returns `Authenticated: false` or an error, the request is denied.

## Troubleshooting
- Check Syfon logs for plugin errors.
- Ensure the plugin binary is executable and compatible with the Syfon interface.

## Example
```sh
export SYFON_AUTHN_PLUGIN_PATH=/opt/syfon-authn-plugin
syfon serve
```
