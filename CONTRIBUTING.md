# Contributing

Thanks for taking the time to contribute!

## Getting started

```bash
git clone https://github.com/adfnekc/s32ftp.git
cd s32ftp
make build
make test
```

Requirements: Go 1.26+ and GNU Make. Docker is optional (only for the
`docker compose` demo stack).

## Development workflow

1. Fork the repository and create a topic branch (`feat/...`, `fix/...`, `docs/...`).
2. Keep changes focused; add or update tests for behaviour changes.
3. Make sure the following pass locally before opening a pull request:

   ```bash
   go build ./...
   go vet ./...
   go test -race -count=1 ./...
   ```

4. Open a pull request against `main` and describe the motivation and the
   behaviour change.

## Commit messages

This project uses [Conventional Commits](https://www.conventionalcommits.org/):

```
feat: add per-user S3 credentials
fix: abort multipart upload when the data connection drops
docs: clarify passive mode behind NAT
test: cover recursive directory rename
```

## Code style

- Follow standard Go style; run `go fmt ./...` (the `make fmt` target does this).
- Prefer small, well-named functions and explicit error handling.
- Keep the S3 filesystem layer (`internal/s3fs`) free of FTP-specific types; the
  bridge lives in `internal/ftpdriver`.
- Add a comment when a decision is non-obvious (S3 has no directories, no
  append, no POSIX metadata, etc.).

## Tests

- Unit tests live next to the code (`internal/config`, `internal/s3fs`).
- End-to-end tests live in `internal/e2e` and run a real FTP client against the
  server with an in-process fake S3 backend, so they need no network access.
- New FTP behaviour should be covered by an end-to-end test whenever possible.

## Reporting issues

Please use the GitHub issue templates and include:

- the s32ftp version (`s32ftp -version` or the container tag),
- the relevant configuration with secrets redacted,
- the FTP client used,
- logs at `logging.level: debug`.

## License

By contributing you agree that your contributions are licensed under the
[MIT License](LICENSE).
