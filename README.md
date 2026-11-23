# ContainerHTTPChecker

A small HTTP/HTTPS healthcheck built as a static Go binary with no external
Go dependencies. Runs in a `scratch` container without a shell.

The check performs exactly one `GET` request. HTTP status **200–299** indicates
a healthy service. Redirects are not followed; 3xx, 4xx, 5xx, network errors,
timeouts, and TLS errors all cause the check to fail. The response body is not
evaluated.

## Configuration

| Environment variable | Default | Description |
| --- | --- | --- |
| `HEALTHCHECK_URL` | required | Absolute URL, e.g. `http://127.0.0.1:8080/health` |
| `HEALTHCHECK_TIMEOUT` | `5s` | Positive timeout for the entire request, e.g. `500ms` or `3s` |
| `HEALTHCHECK_CA_FILE` | unset | Path to a PEM file containing additional trusted CA certificates |

Successful checks produce no output. Errors are written to stderr.

Exit codes: `0` = healthy, `1` = failed check or invalid configuration.

## Build locally

```sh
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o healthcheck .
HEALTHCHECK_URL=http://127.0.0.1:8080/health ./healthcheck
go test ./...
```

To build for a Linux container on macOS:

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o dist/healthcheck .
```

For ARM64, set `GOARCH=arm64` instead.

## Add to an existing application image

The healthcheck must run **inside the application's container**. A separate
checker container does not set the application container's health status.
Example final stage of a multi-stage Dockerfile:

```dockerfile
# The "app-build" and "checker" stages must be defined earlier.
# "checker" provides /healthcheck and the CA bundle, for example using:
# FROM container-http-checker AS checker
FROM scratch
COPY --from=app-build /out/app /app
COPY --from=checker /healthcheck /healthcheck
# Required only for HTTPS:
COPY --from=checker /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt

ENV HEALTHCHECK_URL=http://127.0.0.1:8080/health
ENV HEALTHCHECK_TIMEOUT=5s
HEALTHCHECK --interval=30s --timeout=6s --start-period=5s --retries=3 CMD ["/healthcheck"]
ENTRYPOINT ["/app"]
```

The healthcheck's JSON/exec form does not require a shell. The binary reads the
environment variables itself. Set Docker's timeout slightly higher than
`HEALTHCHECK_TIMEOUT`; adjust both values if a longer timeout is needed.

Alternatively, use Docker Compose with an application image containing the binary:

```yaml
services:
  app:
    image: my-app:latest
    environment:
      HEALTHCHECK_URL: http://127.0.0.1:8080/health
      HEALTHCHECK_TIMEOUT: 5s
    healthcheck:
      test: ["CMD", "/healthcheck"]
      interval: 30s
      timeout: 6s
      start_period: 5s
      retries: 3
```

## Build a standalone checker image

```sh
docker build -t container-http-checker .
docker run --rm -e HEALTHCHECK_URL=https://example.com container-http-checker
```

The included Dockerfile builds a scratch image containing the static binary
and a CA bundle, running as an unprivileged user. A normal invocation performs
one check and exits; the image is not a long-running service. Its `HEALTHCHECK`
instruction is primarily intended as a template for the application image.

## HTTPS and custom certificates

Go automatically verifies the certificate chain, validity period, and hostname.
The host operating system does **not** provide trusted CAs to a scratch
container. The Dockerfile therefore copies a CA bundle into the image.
For plain HTTP, the binary alone is sufficient.

For an internal CA, copy its public CA certificate into the container as a PEM
file or mount it read-only, then set
`HEALTHCHECK_CA_FILE=/certs/internal-ca.pem`. The file must be readable by the
container user. The CA is trusted in addition to the system CAs; this also
works without a system CA bundle. Private keys are not required.

The hostname in `HEALTHCHECK_URL` must match the certificate. For
`https://127.0.0.1`, the certificate needs a matching IP SAN.
There is deliberately no option to disable certificate verification.

## GitHub checks and updates

The workflows are based on
[CupNotifyer](https://github.com/KevinCFechtel/CupNotifyer):

| Check | Benefit for this repository |
| --- | --- |
| `gofmt`, tests with race detector, `go vet`, build | Check formatting, behavior, and common Go mistakes |
| `govulncheck` | Detect reachable vulnerabilities, including in the Go standard library |
| Dependency Review on pull requests | Review new dependencies and action updates for potential issues |
| CodeQL | Static security analysis of the Go code |
| Scratch image build, HTTP smoke test, Trivy | Verify the container runs and detect HIGH/CRITICAL vulnerabilities in the final image |
| Dependabot | Weekly updates for Go modules, GitHub Actions, and Docker base images |

CI runs on pushes to `main`, pull requests, manual dispatch, and a weekly schedule.
Scheduled runs detect new vulnerabilities even without code changes.
Go checks and release builds use the current stable Go version;
`go.mod` continues to specify the minimum supported version.
The Go base image in the Dockerfile is updated separately through Dependabot.
All external actions and the Trivy image are pinned to commit SHAs or an image
digest. Workflows have read-only permissions by default; write permissions
are granted only for CodeQL results and release publishing.

CupNotifyer's container publishing, registry login, and QEMU setup are not needed
here: releases publish only cross-compiled binaries.

Additional recommended GitHub repository settings:

- Enable the Dependency Graph and Dependabot Security Updates to support
  dependency review and automatic security updates.
- Keep Secret Scanning and Push Protection enabled.
- Protect `main` with a ruleset or branch protection rule and require CI checks
  before merging.
- When using this CodeQL workflow, use Advanced Setup rather than running
  CodeQL Default Setup in parallel.
- Protect release tags matching `v*` with a tag ruleset against unauthorized
  creation, modification, and deletion.

## Releases

A tag matching `vX.Y.Z` triggers the release workflow:

```sh
git tag v0.1.0
git push origin v0.1.0
```

Only after all CI and security checks pass are two static Linux binaries
and a checksum file published as a GitHub Release:

- `healthcheck-linux-amd64`
- `healthcheck-linux-arm64`
- `SHA256SUMS`

The workflow requires no additional secrets; it uses the short-lived
`GITHUB_TOKEN`. Pre-release tags are deliberately not supported.
The workflow does not create commits or tags itself.

After downloading the appropriate binary and checksum file, verify the checksum
and make the binary executable. For AMD64, for example:

```sh
grep ' healthcheck-linux-amd64$' SHA256SUMS | sha256sum --check -
chmod +x healthcheck-linux-amd64
```

SHA256 checksums detect corrupted downloads but do not replace a cryptographic
signature or provenance attestation.
