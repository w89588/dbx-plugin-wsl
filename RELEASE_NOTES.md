# WSL Manager v0.1.0

Initial public release.

## Highlights

- **WSL distributions (`wsl`)**: list with live state/disk usage, start/stop, default distro, version switching, default user, sparse/compact/resize, export/import, unregister, distro info (OS release, kernel, disk, IP, `/etc/wsl.conf`), open terminal/Explorer, run commands inside a distro.
- **WSL containers (`wslc`)**: container list/lifecycle, run, logs (follow), exec, live stats, prune; image list/pull/build/tag/save/load/remove/prune; volumes and networks management; live event stream.
- **Registry credentials**: sign in/out to container registries (`docker login` equivalent) so private images and Docker Hub rate limits are handled; pull/build/run failures are classified and the UI offers a one-click "go to sign in" action with the registry prefilled.
- **Command console**: run arbitrary `wsl` / `wslc` commands as argv (no shell), with stdout/stderr/exit code.
- **Settings**: visual editors for `.wslconfig` and wslc `settings.yaml` with automatic backups.
- UI: overview dashboard, dark/light theme, Chinese/English localization, task panel with live logs and cancellation.

## Requirements

- Windows 11 with WSL (`wsl --version` works; verified with WSL 3.0.3/3.0.4).
- DBX desktop `>= 0.5.68` (verified on 0.6.49).
- `wslc` is optional; without it the distribution pages still work.

## Permissions

`host.workbench`, `host.events`, `host.storage` — no network, filesystem, clipboard, data or AI permissions are requested. The native sidecar runs with the current user and only invokes `wsl.exe` / `wslc.exe`.

## Install

1. Download `com.dbx.wsl-0.1.0-windows-x64.dbxp` from this release.
2. Open DBX → Plugin Center → install the local package (enable unsigned development packages if the store build is not signed yet).
3. Open **WSL Manager** from the plugin shortcut / command palette.

## Notes and limitations

- Windows-only (`windows-x64`).
- Registry credentials are stored by `wslc` on the local machine; the plugin never persists passwords and passes them to `wslc` over stdin.
- `wslc` 3.0.3/3.0.4 has no command to list signed-in registries, so sign-in state is confirmed by pull results.
- Export/import/build path fields require a Windows path (no native file picker yet).
