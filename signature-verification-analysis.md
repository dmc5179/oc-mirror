# oc-mirror v2: Signature Verification for Unsigned Certified Operators

## Problem Statement

oc-mirror v2 enforces container image signature verification by default. When mirroring
the certified operator index, images published by ISVs (Independent Software Vendors)
into `registry.connect.redhat.com` are not required to be signed. oc-mirror cannot
distinguish between an image that was never signed (and therefore has no signature to
retrieve) and an image whose signature exists but failed to download. Both cases produce
the same fatal error, causing the entire operator bundle to fail mirroring.

## Observed Behavior

Running:

```
oc-mirror --v2 --config ./imageset-config.yaml file:///path/to/mirror
```

Against the certified operator index (`registry.redhat.io/redhat/certified-operator-index:v4.19`)
produces errors like:

```
error mirroring image docker://registry.connect.redhat.com/netapp/trident-autosupport@sha256:566667b5...
  error: reading signatures: reading manifest sha256-566667b5e321b6b3b067e704b8a84f6bfce0425866ccec69674e45a6d4c2b197.sig
  in registry.connect.redhat.com/netapp/trident-autosupport: name unknown: Image not found
```

7 of 11 images succeed (all from `registry.redhat.io`, Red Hat's own signed images).
4 fail (all from `registry.connect.redhat.com`, NetApp ISV images that are unsigned).
The operator bundle itself is then skipped because its related images failed.

## Root Cause Analysis

### The Error Chain

1. **registries.d config generation** (`internal/pkg/registriesd/registriesd.go`):
   `PrepareRegistrydCustomDir()` creates YAML config files with `use-sigstore-attachments: true`
   for every source registry. This is called from `RunMirrorToDisk()`, `RunMirrorToMirror()`,
   and `RunDiskToMirror()` in `executor.go`.

2. **SystemContext configuration** (`internal/pkg/mirror/mirror.go:113-114`):
   When `RemoveSignatures` is false, `sourceCtx.RegistriesDirPath` is set to the custom
   registries.d directory. This tells the upstream `go.podman.io/image/v5` library to look
   for sigstore attachments.

3. **Upstream library behavior** (`go.podman.io/image/v5/copy.Image()`):
   The library reads the registries.d config, sees `use-sigstore-attachments: true`, and
   constructs a `.sig` tag reference by converting `sha256:DIGEST` to `sha256-DIGEST.sig`.
   It then attempts to read the manifest for this tag from the source registry.

4. **Registry response**: For unsigned images, the `.sig` tag does not exist. The registry
   returns HTTP "name unknown: Image not found". The library wraps this as a fatal error:
   `reading signatures: reading manifest sha256-DIGEST.sig in REGISTRY/REPO: name unknown`.

5. **Error propagation**: The error propagates through the batch worker
   (`internal/pkg/batch/concurrent_chan_worker.go`), which records the failure and cascades
   it to skip the operator bundle image.

### Why the Current Design Is Flawed

The signature lookup path is deterministic: for a given image digest, there is exactly one
expected `.sig` tag location. When oc-mirror queries that location and gets "not found",
two interpretations are possible:

- **The image was never signed**: The `.sig` tag was never created. This is expected for
  certified operator images from ISVs who are not required to sign.
- **The signature exists but retrieval failed**: A transient or permanent error prevented
  fetching an existing signature.

oc-mirror treats both cases identically as errors. The registry's error response provides
enough information to distinguish them: "name unknown" / "manifest unknown" (HTTP 404)
means the signature was never published, while network errors, auth failures, or server
errors (HTTP 5xx) indicate a retrieval problem with a potentially existing signature.

### Existing Escape Hatch

The `--remove-signatures` flag prevents setting `RegistriesDirPath` entirely AND sets
`RemoveSignatures: true` on copy options. This is too blunt: it throws away valid
signatures from Red Hat images just to avoid errors on unsigned ISV images.

## Implemented Solution

### New CLI Flag

```
--signature-verification string   Signature verification mode:
                                  "strict" requires all images to have signatures;
                                  "best-effort" skips missing signatures for unsigned images
                                  (default "strict")
```

### Behavior Matrix

| `--remove-signatures` | `--signature-verification` | Behavior |
|---|---|---|
| false | `strict` | **Current default**: copy signatures, fail on missing |
| false | `best-effort` | **New**: copy signatures when available, skip gracefully when absent |
| true | (either) | `--remove-signatures` wins: no signatures read or copied |

### Code Changes

#### 1. `internal/pkg/mirror/const.go` — New constants

Added `SignatureVerificationStrict` and `SignatureVerificationBestEffort` constants.

#### 2. `internal/pkg/mirror/options.go` — New field on `GlobalOptions`

Added `SignatureVerification string` field to `GlobalOptions`.

#### 3. `internal/pkg/mirror/mirror.go` — Retry logic and error classifier

Added `IsSignatureNotFoundError()` which detects the specific "signature doesn't exist"
error pattern: the error message must contain both `"reading signatures"` and either
`"name unknown"` or `"manifest unknown"`. This distinguishes unsigned images from real
retrieval failures.

In `copy()`, when `copy.Image()` fails and the mode is `best-effort`:
- Check if the error matches the "signature not found" pattern
- If so, retry the copy with `RemoveSignatures: true` and `RegistriesDirPath: ""`
  for that specific image only
- If the retry also fails, the error is returned normally

This approach means:
- Signed images (e.g., from `registry.redhat.io`) get their signatures copied on the
  first attempt with no retry needed
- Unsigned images (e.g., from `registry.connect.redhat.com`) fail on the first attempt,
  are detected as unsigned, and succeed on the retry without signatures
- Real failures (network, auth, server errors) are not caught by the pattern and remain
  as errors

#### 4. `internal/pkg/cli/executor.go` — Flag registration and validation

Registered the `--signature-verification` flag with default `"strict"`. Added validation
in `Validate()` to reject values other than `"strict"` and `"best-effort"`.

#### 5. `internal/pkg/archive/image-blob-gatherer.go` — Archive blob gathering tolerance

In `imageSignatureBlobs()`, when the signature manifest fetch fails and the mode is
`best-effort`, the function checks if the error indicates the signature doesn't exist.
If so, it returns `nil, nil` (no blobs, no error) instead of a `SignatureBlobGathererError`.
This prevents the archive phase from failing on unsigned images.

#### 6. `internal/pkg/cli/executor_test.go` — Test updates

Updated `TestExecutorValidate` to set `SignatureVerification: mirror.SignatureVerificationStrict`
on `GlobalOptions` so existing validation tests pass with the new validation check.

### Usage

To mirror the certified operator index including unsigned ISV operators:

```
oc-mirror --v2 --config ./imageset-config.yaml \
  --signature-verification best-effort \
  file:///path/to/mirror
```

To enforce that all images must be signed (current default behavior):

```
oc-mirror --v2 --config ./imageset-config.yaml \
  --signature-verification strict \
  file:///path/to/mirror
```

### Design Decisions

**Why retry instead of pre-checking?** Pre-checking whether a `.sig` tag exists would
add an extra registry API call for every image. The retry approach only adds overhead
for unsigned images (typically the minority), and avoids false negatives from race
conditions or registry API inconsistencies.

**Why handle at the mirror.copy() level?** The `copy.Image()` call is the innermost
point where the error originates. Handling it here keeps the retry logic localized and
avoids spreading signature-awareness through the batch worker or executor layers.

**Why string values instead of a boolean?** A string flag (`strict` / `best-effort`)
is more extensible than a boolean and makes the CLI self-documenting. Future modes
(e.g., `warn-only`) could be added without breaking the flag interface.

## Files Modified

| File | Change |
|------|--------|
| `internal/pkg/mirror/const.go` | Added signature verification mode constants |
| `internal/pkg/mirror/options.go` | Added `SignatureVerification` field to `GlobalOptions` |
| `internal/pkg/mirror/mirror.go` | Added `IsSignatureNotFoundError()` and retry logic in `copy()` |
| `internal/pkg/cli/executor.go` | Registered `--signature-verification` flag, added validation |
| `internal/pkg/archive/image-blob-gatherer.go` | Tolerant signature blob gathering in best-effort mode |
| `internal/pkg/cli/executor_test.go` | Set default `SignatureVerification` in test fixtures |
