# Contributing

Thanks for your interest in improving WSL Manager. This repository contains the DBX plugin source; the official store
catalog lives in [`t8y2/dbx-store`](https://github.com/t8y2/dbx-store) and the DBX host platform in
[`t8y2/dbx`](https://github.com/t8y2/dbx).

## Development environment

- Windows 11 with WSL (`wsl --version`) and optionally `wslc` (WSL 3.0+).
- DBX desktop `>= 0.5.68` for manual verification.
- Go 1.22+, Node.js 22+, and `@dbx-app/plugin-cli`:

  ```powershell
  npm install --no-audit --no-fund @dbx-app/plugin-cli
  ```

## Build and test

```powershell
# Build the plugin package (builds the Go sidecar)
node node_modules\@dbx-app\plugin-cli\bin\dbx-plugin.js package .

# Sidecar unit tests
cd backend
go vet ./...; go test ./...

# Manifest install-time rules (host word lists, entrypoints, menu references)
cd ..
node tools/manifest-check.mjs
```

`node ..\node_modules\@dbx-app\plugin-cli\bin\dbx-plugin.js dev --path . --port 5190` starts the browser development
host without DBX. If the development host is started from a directory carrying a Windows Low mandatory integrity label,
set `--data-dir` to a regular user directory (for example under `%LOCALAPPDATA%`) so the sidecar can access WSL.

## Pull requests

- Keep changes scoped; update `docs/rpc-contract.md` when the UI/sidecar interface changes.
- Run `go vet`, `go test` and `tools/manifest-check.mjs` before opening a PR.
- Update `README`/docs for user-visible changes.
- Follow the existing code style (`gofmt` for Go, no external UI dependencies).
- Do not commit `dist/`, `.dbx-dev/`, QA screenshots or generated artifacts; they are ignored by `.gitignore`.

## Release and store submission

Maintainers follow [docs/releasing.md](docs/releasing.md):

1. Bump the version in `manifest.json`, `backend/main.go`, `ui/index.html` and `docs/rpc-contract.md`.
2. Build the unsigned candidate and generate metadata with `tools/release-meta.mjs`.
3. Publish a version tag and GitHub Release with the `.dbxp`, `.artifact.json` and `release-candidates.json`.
4. Open a single pull request against `t8y2/dbx-store:main` with `publishers/<publisher>.json` and
   `candidates/<plugin-id>.json`.

Official store signing and catalog merge are performed by DBX Store maintainers; plugin authors do not hold the signing
key. Do not submit plugin source code to `t8y2/dbx-store`.
