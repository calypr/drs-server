# Syfon Authorization Plugin Developer Guide

This guide describes how to implement, test, and integrate custom authorization plugins for Syfon.

## Plugin Interface

Your plugin must implement the following Go interface:

```go
type AuthorizationPlugin interface {
    Authorize(ctx context.Context, in *AuthorizationInput) (*AuthorizationOutput, error)
}
```

### AuthorizationInput Fields
- `RequestID` (string): Unique request identifier
- `Subject` (string): User or entity subject
- `Action` (string): HTTP method (e.g., GET, POST)
- `Resource` (string): Request path
- `Claims` (map[string]interface{}): JWT claims
- `Metadata` (map[string]interface{}): Request metadata (IP, user agent, etc.)

### AuthorizationOutput Fields
- `Allow` (bool): Whether to allow the request
- `Reason` (string): Reason for the decision
- `Obligations` (map[string]interface{}): Additional data, e.g.,
  - `resources`: []string
  - `privileges`: map[string][]string

## Input/Output Mapping
- Syfon extracts request and context data to populate `AuthorizationInput`.
- Your plugin should return obligations in the expected format for Syfon to map to context.

## Example Plugin
See the `plugins/` directory for a sample plugin implementation.

## Testing
- Unit test your plugin logic.
- Use Syfon's integration tests to verify end-to-end behavior.

## Extension Points
- Add custom fields to `Obligations` as needed, but document them for operators.

## Troubleshooting
- Ensure your plugin binary is executable and matches the handshake config.
- Log errors and reasons for denied requests for easier debugging.

