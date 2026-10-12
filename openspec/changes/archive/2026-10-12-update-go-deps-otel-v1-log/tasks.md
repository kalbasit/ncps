## 1. Update

- [x] 1.1 Bump the modules proposed by the Renovate PRs and run `go mod tidy`.
- [x] 1.2 Replace `log/global` with `otel.SetLoggerProvider`/`otel.GetLoggerProvider`.
- [x] 1.3 Refresh `vendorHash` and confirm `nix build .#ncps` succeeds.

## 2. Verify

- [x] 2.1 `task fmt`, `task lint`, `task test` all exit zero.
