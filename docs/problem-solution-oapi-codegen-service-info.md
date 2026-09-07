# Problem and Solution: GA4GH Service Info Codegen Type Errors

## Problem

The GA4GH DRS `/service-info` response is composed from multiple schemas, and `oapi-codegen` currently emits broken response-scoped enum references for some nested `drs` fields.

The failure looks like this:

```text
undefined: N200ServiceInfoDrsControlledAccessClaimFormat
undefined: N200ServiceInfoDrsControlledAccessDefault
undefined: N200ServiceInfoDrsSupportedUploadMethodTypes
```

This is a code generation problem, not a runtime DRS behavior problem.

## Why Build Fails

`oapi-codegen` emits the real enum types under the named `DrsService` schema path:

```go
type DrsServiceDrsControlledAccessClaimFormat string
type DrsServiceDrsControlledAccessDefault string
type DrsServiceDrsSupportedUploadMethodTypes string
type DrsServiceTypeArtifact string
```

But the generated `/service-info` response structs reference names that are never defined:

```go
ControlledAccessClaimFormat *N200ServiceInfoDrsControlledAccessClaimFormat
ControlledAccessDefault *N200ServiceInfoDrsControlledAccessDefault
SupportedUploadMethodTypes *[]N200ServiceInfoDrsSupportedUploadMethodTypes
```

The same issue appears in strict server JSON response structs as `N200ServiceInfoJSONResponse...`.

## Current Fix

Syfon fixes this with handwritten compatibility aliases:

- `apigen/client/drs/compat.go`
- `apigen/server/drs/compat.go`

Those files define the missing response-scoped names as aliases to the real generated enum types:

```go
type N200ServiceInfoDrsControlledAccessClaimFormat = DrsServiceDrsControlledAccessClaimFormat
type N200ServiceInfoDrsControlledAccessDefault = DrsServiceDrsControlledAccessDefault
type N200ServiceInfoDrsSupportedUploadMethodTypes = DrsServiceDrsSupportedUploadMethodTypes
type N200ServiceInfoTypeArtifact = DrsServiceTypeArtifact
```

The server package also needs the strict JSON response aliases:

```go
type N200ServiceInfoJSONResponseDrsControlledAccessClaimFormat = DrsServiceDrsControlledAccessClaimFormat
type N200ServiceInfoJSONResponseDrsControlledAccessDefault = DrsServiceDrsControlledAccessDefault
type N200ServiceInfoJSONResponseDrsSupportedUploadMethodTypes = DrsServiceDrsSupportedUploadMethodTypes
type N200ServiceInfoJSONResponseTypeArtifact = DrsServiceTypeArtifact
```

## Why This Fix Works

The aliases leave the wire API untouched and only repair the generated Go type graph at compile time.

JSON still uses the same fields:

```json
{
  "drs": {
    "supportedUploadMethodTypes": ["s3", "https"],
    "controlledAccessClaimFormat": "ga4gh-passport-url-claim",
    "controlledAccessDefault": "open-access-read"
  }
}
```

## Tradeoffs

Pros:

- No GA4GH schema changes.
- Small, obvious workaround in normal Go source.
- Keeps committed generated output compiling cleanly.

Cons:

- It still depends on current `oapi-codegen` naming behavior.
- Future inline enum additions under `/service-info` may require more aliases.
