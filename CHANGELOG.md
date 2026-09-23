# Changelog

All notable changes to this project are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.0] - 2026-09-21

Initial release.

### Added

- FTP(S) frontend backed by any S3-compatible object store.
  - TLS modes: `off`, `explicit`, `required`, `implicit`.
  - Configurable listen IP/port, banner, idle timeout, connection limit.
  - Passive port range, NAT `public_host`, active-mode toggle, data-connection
    IP matching toggle.
  - Multi-user authentication with plaintext or bcrypt passwords, per-user
    `bucket`/`root_prefix` overrides and read-only accounts.
- S3 backend on AWS SDK for Go v2.
  - Custom `endpoint_url`, `region`, static credentials or the AWS default
    credential chain, session tokens.
  - Path-style addressing, `insecure_skip_verify`, custom `ca_bundle`.
  - Streaming multipart uploads with configurable part size and concurrency.
- FTP command support: `LIST`/`NLST`/`MLSD`/`MLST`, `STAT`, `SIZE`, `MDTM`,
  `RETR`, `STOR`, `APPE`, `DELE`, `MKD`, `RMD`, `RNFR`/`RNTO`, `REST`, `ALLO`,
  `SITE`.
  - Directory emulation via prefixes and zero-byte `<dir>/` markers.
  - Upload/download resume (`REST`) and append (`APPE`).
  - Recursive directory rename.
- Configuration via YAML with `S32FTP_*` environment variable overrides,
  validation, and bcrypt password hashing (`-hash-password`).
- Structured logging (`log/slog`, text or JSON) and graceful shutdown.
- Dockerfile, `docker compose` stack with MinIO, and a `Makefile`.
- Unit tests and an in-process end-to-end suite driven by a real FTP client.
