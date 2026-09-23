# s32ftp

[![CI](https://github.com/adfnekc/s32ftp/actions/workflows/ci.yml/badge.svg)](https://github.com/adfnekc/s32ftp/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/adfnekc/s32ftp)](https://github.com/adfnekc/s32ftp/releases)
[![Go Version](https://img.shields.io/github/go-mod/go-version/adfnekc/s32ftp)](go.mod)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Go Report Card](https://goreportcard.com/badge/github.com/adfnekc/s32ftp)](https://goreportcard.com/report/github.com/adfnekc/s32ftp)

**English** | [简体中文](README.zh-CN.md)

An FTP(S) gateway that speaks S3 on the backend. Legacy clients that only know
FTP get a stateless, horizontally scalable entry point to any S3-compatible
object store.

```
FTP client ──FTP/FTPS──▶ s32ftp ──S3 (SigV4)──▶ AWS S3 / MinIO / Ceph RGW / OSS ...
```

## Features

- **FTP(S) frontend** built on [ftpserverlib](https://github.com/fclairamb/ftpserverlib)
  - Configurable listen IP, port and banner
  - TLS modes: `off`, `explicit` (AUTH TLS), `required` (reject plaintext), `implicit` (port 990)
  - Passive port range, `public_host` for NAT/Docker, optional active-mode shutdown
  - Idle timeout, connection limit, per-user auth (plaintext or bcrypt), read-only accounts
- **S3 backend** built on AWS SDK for Go v2
  - Custom `endpoint_url`, `access_key_id`, `secret_access_key`, `session_token`, `bucket`, `region`
  - Path-style addressing (required by MinIO/Ceph), self-signed certificate handling, custom CA bundle
  - Streaming multipart uploads with bounded memory and configurable concurrency
- **Faithful FTP semantics over S3**
  - Directories emulated with prefixes and zero-byte `<dir>/` markers
  - `REST` upload/download resume, `APPE` append
  - `RNFR`/`RNTO` recursive rename via `CopyObject` + `DeleteObject`
- **Operations friendly**: structured logging (`log/slog`, text or JSON), graceful shutdown,
  multi-user bucket/prefix isolation

## Install

### go install (Go 1.26+)

```bash
go install github.com/adfnekc/s32ftp/cmd/s32ftp@latest
```

The binary is installed as `s32ftp` in `$(go env GOPATH)/bin`.

> Go 1.26 is required because a transitive dependency needs it. With the default
> `GOTOOLCHAIN=auto`, older toolchains download the right one automatically.

### Prebuilt binaries

Download the archive for your platform from
[Releases](https://github.com/adfnekc/s32ftp/releases) and put `s32ftp` on your `PATH`.

### Docker

```bash
docker build -t s32ftp .
docker run --rm -p 2121:2121 -p 50000-50100:50000-50100 \
  -v "$PWD/config.yaml:/etc/s32ftp/config.yaml:ro" s32ftp
```

## Quick start

### Docker Compose (ships a MinIO backend)

```bash
docker compose up --build -d
# FTP endpoint 127.0.0.1:2121, credentials admin / admin123
curl -T local.txt ftp://admin:admin123@127.0.0.1:2121/remote.txt
curl ftp://admin:admin123@127.0.0.1:2121/remote.txt
```

### From source

```bash
make build
cp configs/config.example.yaml config.yaml   # edit as needed
./bin/s32ftp -config config.yaml
```

### CLI flags

```
-config string        path to the YAML configuration file
-version              print version and exit
-hash-password string print a bcrypt hash for the given password and exit
```

## Configuration

Every option is documented in
[`configs/config.example.yaml`](configs/config.example.yaml). Core settings:

| Key | Description | Default |
| --- | --- | --- |
| `ftp.listen_ip` / `ftp.port` | Control channel bind address | `0.0.0.0` / `2121` |
| `ftp.banner` | Greeting sent after connect | `s32ftp FTP-to-S3 gateway ready` |
| `ftp.tls.mode` | `off` / `explicit` / `required` / `implicit` | `off` |
| `ftp.public_host` | IP advertised in PASV replies (NAT/Docker) | empty |
| `ftp.passive_port_range` | e.g. `50000-50100`; empty lets the OS choose | empty |
| `ftp.disable_active_mode` | Reject `PORT`/`EPRT` | `false` |
| `ftp.disable_ip_match` | Allow data connections from a different IP | `false` |
| `ftp.max_clients` | Max concurrent clients, 0 = unlimited | `0` |
| `ftp.users[].read_only` | Reject all mutating commands | `false` |
| `ftp.users[].bucket` / `root_prefix` | Per-account overrides | empty |
| `s3.endpoint_url` | S3 endpoint; empty means AWS | empty |
| `s3.access_key_id` / `secret_access_key` | Static credentials; empty uses the AWS default chain | empty |
| `s3.bucket` | Target bucket (required) | — |
| `s3.root_prefix` | Prefix all objects are stored under | empty |
| `s3.force_path_style` | Required by MinIO/Ceph | `true` |
| `s3.insecure_skip_verify` / `ca_bundle` | Self-signed endpoint handling | `false` / empty |
| `s3.part_size_mb` | Multipart part size (min 5) | `8` |
| `s3.upload_concurrency` | Parallel part uploads | `4` |
| `s3.create_bucket_if_missing` | Create the bucket at startup | `false` |
| `s3.dir_marker` | Write `<dir>/` markers so empty dirs persist | `true` |
| `logging.level` / `format` / `output` | `debug\|info\|warn\|error`, `text\|json`, `stdout\|stderr\|<path>` | `info` / `text` / `stdout` |

### Environment variable overrides

Useful for containers. The prefix is `S32FTP_`, nesting is joined with `_`:

```bash
S32FTP_FTP_PORT=2121
S32FTP_FTP_PUBLIC_HOST=203.0.113.10
S32FTP_FTP_PASSIVE_PORT_RANGE=50000-50100
S32FTP_S3_ENDPOINT_URL=https://s3.example.com
S32FTP_S3_ACCESS_KEY_ID=...
S32FTP_S3_SECRET_ACCESS_KEY=...
S32FTP_S3_BUCKET=ftp
S32FTP_S3_FORCE_PATH_STYLE=true
S32FTP_LOG_LEVEL=debug
```

### Passwords

```bash
s32ftp -hash-password 'my secret'   # prints a bcrypt hash
```

Put the result in `ftp.users[].password_hash` (mutually exclusive with `password`).

## Supported FTP commands

| FTP command | Maps to | Notes |
| --- | --- | --- |
| `USER` `PASS` `QUIT` `NOOP` `FEAT` `SYST` `OPTS` `CLNT` | — | Session handling |
| `AUTH` `PBSZ` `PROT` | — | FTPS |
| `PWD` `CWD` `CDUP` | Prefix navigation | |
| `LIST` `NLST` `MLSD` `MLST` | `ListObjectsV2` with `Delimiter=/` | Pagination handled |
| `SIZE` `MDTM` `STAT` | `HeadObject` | |
| `RETR` | `GetObject` | Streamed |
| `RETR` + `REST` | `GetObject` with `Range` | Download resume |
| `STOR` | Streaming multipart `UploadPart` | Bounded memory |
| `STOR` + `REST` | existing `[0,offset)` + new data | Upload resume |
| `APPE` | existing object + new data | Append |
| `DELE` | `DeleteObject` | |
| `MKD` `XMKD` | `PutObject("<dir>/")` | Directory marker |
| `RMD` `XRMD` | fails when non-empty; deletes the marker when empty | |
| `RNFR` `RNTO` | `CopyObject` + `DeleteObject`, recursive for prefixes | |
| `ALLO` | no-op | S3 needs no preallocation |
| `SITE CHMOD` | no-op (returns 200) | S3 has no permission bits |
| `MFMT` | disabled | S3 cannot set object mtime |
| `HASH` `AVBL` `SYMLINK` `COMB` | not implemented | Returns 502 |

## Design notes

### Path mapping

```
key = root_prefix + strings.TrimPrefix(path.Clean(ftpPath), "/")
```

- `root_prefix` is normalized to either `""` or `a/b/`
- A directory is the common prefix of object keys. `MKD` writes a zero-byte
  `<prefix>/` object so empty directories remain visible to `LIST`
- `Stat` first tries `HeadObject(key)`, then
  `ListObjectsV2(Prefix=key+"/", MaxKeys=1)`, so "virtual" directories that only
  have children (and no marker) are still recognized

### Transfers

- **Download**: the `GetObject` body is read directly. `Seek`/`REST` reopen the
  object with a ranged GET; `ReadAt` uses an independent range request.
- **Upload**: an `io.Pipe` feeds the FTP data connection into
  `manager.Uploader`, which performs a concurrent multipart upload. `Close`
  waits for completion, and `ABOR`/disconnects call `TransferError`, which
  aborts the multipart upload so no orphaned parts are left behind.
- `Content-Type` is inferred from the file extension.

### Error semantics

S3 `NoSuchKey`/`NotFound` become `os.ErrNotExist` and therefore FTP `550`;
mutations on a read-only account return `550`; S3 server-side errors return
`450` (retryable).

### Deployment notes

- Behind NAT or Docker, set `ftp.public_host` and publish
  `ftp.passive_port_range`, otherwise passive transfers will fail.
- Prefer `ca_bundle` over `insecure_skip_verify` when the S3 endpoint uses a
  private CA.
- `read_only: true` rejects `STOR`, `APPE`, `DELE`, `RNFR`, `MKD` and `RMD`.

## Development

```bash
make build        # build bin/s32ftp
make test         # unit + end-to-end tests
make test-race    # with the race detector
make vet
make docker-up    # s32ftp + MinIO via docker compose
```

The end-to-end suite (`internal/e2e`) starts an in-process
[gofakes3](https://github.com/johannesboyne/gofakes3) server and drives the full
stack with a real FTP client (`jlaffaye/ftp`), so it needs no Docker or network.
It covers upload/download/list/delete, directories and recursive rename, `APPE`,
`REST` resume, a 12 MiB multipart transfer, empty files, read-only accounts,
explicit TLS and `root_prefix` isolation.

## Limitations

- One bucket per instance; use multiple instances or per-account `bucket`
  overrides for more.
- S3 credentials are service-wide, not per FTP user. Per-user credentials can
  be added by extending `ftp.users[]` and building a dedicated backend in
  `AuthUser` (the architecture already allows it).
- No S3 ACLs, versioning or object lock (FTP has no equivalent).
- `SITE CHMOD` and `MFMT` are accepted but have no effect.
- `RMD` requires an empty directory.

## License

[MIT](LICENSE)
