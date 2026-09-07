# Syfon Operator Guide: Authorization Plugin Configuration

This guide explains how to configure Syfon to use an external authorization plugin for all authorization decisions, using the HashiCorp go-plugin system.

## Prerequisites
- A compiled authorization plugin binary implementing the Syfon plugin contract.
- Syfon server built with plugin support (default).

## Configuration Steps

### 1. Place the Plugin Binary
Copy your plugin binary to a secure location on the server running Syfon. Example:

```
/opt/syfon-plugins/authz-plugin
```

### 2. Set the Plugin Path Environment Variable
Set the `SYFON_AUTHZ_PLUGIN_PATH` environment variable to the absolute path of your plugin binary:

```
export SYFON_AUTHZ_PLUGIN_PATH=/opt/syfon-plugins/authz-plugin
```

You can add this to your systemd unit, Dockerfile, or shell profile as appropriate.

### 3. (Optional) Configure the Fence Issuer
Set `DRS_FENCE_URL` to the HTTPS origin that is allowed to issue JWTs:

```
export DRS_FENCE_URL="https://fence.example.com"
```

### 4. Start Syfon
Start the Syfon server as usual. On startup, Syfon will attempt to launch and handshake with the plugin. If the plugin is unavailable or fails, startup continues with the configured built-in behavior.

### 5. Monitoring and Troubleshooting
- Check Syfon logs for plugin startup, handshake, and authorization errors.
- If the plugin process crashes or is killed, Syfon will deny all requests until the plugin is available again.

## Security Notes
- Only use plugins from trusted sources.
- Run plugins with least privilege and consider using a dedicated user.
- Keep plugin binaries up to date and monitor for vulnerabilities.

## Example Systemd Snippet
```
[Service]
Environment=SYFON_AUTHZ_PLUGIN_PATH=/opt/syfon-plugins/authz-plugin
ExecStart=/usr/local/bin/syfon-server
```
