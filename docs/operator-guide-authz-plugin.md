# Syfon Authorization Plugin Operator Guide

This guide explains how to configure, deploy, and troubleshoot authorization plugins for Syfon.

## Plugin Configuration

- Set the environment variable `SYFON_AUTHZ_PLUGIN_PATH` to the absolute path of your plugin binary.
- Ensure the plugin binary is executable and compatible with the Syfon plugin interface.

## Deployment

1. Place the plugin binary on the same host as the Syfon server.
2. Set `SYFON_AUTHZ_PLUGIN_PATH` in the environment before starting Syfon.
3. Restart the Syfon service to load the plugin.

## Troubleshooting

- Check Syfon logs for plugin startup or communication errors.
- Ensure the plugin binary matches the expected handshake and interface.
- If authorization fails, verify the plugin is running and accessible.

## Example

```sh
export SYFON_AUTHZ_PLUGIN_PATH=/opt/syfon/plugins/my-authz-plugin
systemctl restart syfon
```

For more details, see the developer guide.

