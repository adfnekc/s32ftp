# Security Policy

## Supported versions

The latest release is supported with security fixes. Please reproduce issues on
the most recent tag before reporting.

## Reporting a vulnerability

Please **do not** open a public issue for security problems.

Use GitHub's private vulnerability reporting
("Security" tab → "Report a vulnerability") on
<https://github.com/adfnekc/s32ftp/security/advisories/new>, or email the
maintainer listed in the repository profile.

Include, when possible:

- affected version or commit,
- a description of the issue and its impact,
- configuration and TLS/S3 setup involved,
- reproduction steps or a proof of concept.

You can expect an initial acknowledgement within a few days.

## Deployment hardening

s32ftp is a network service that holds S3 credentials. When deploying:

- Put the FTP control and passive data ports behind a firewall, and prefer a
  private network or VPN over the public internet.
- Use `ftp.tls.mode: required` or `implicit` whenever credentials cross an
  untrusted network. Plaintext FTP sends the password in the clear.
- Set `ftp.public_host` and `ftp.passive_port_range` explicitly rather than
  exposing a wide port range.
- Grant the S3 credentials only the permissions the gateway needs on the target
  bucket.
- Prefer `s3.ca_bundle` over `s3.insecure_skip_verify: true`, which disables
  certificate verification for the S3 endpoint.
- Keep `ftp.max_clients` set to a sensible limit to reduce resource exhaustion
  risk.
