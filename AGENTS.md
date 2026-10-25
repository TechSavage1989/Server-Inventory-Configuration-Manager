# AGENTS.md

Read this file first before changing Server Inventory Configuration Manager.

## Product baseline

- Product: Server Inventory Configuration Manager
- Primary source: Go Windows host plus embedded `index.html`, `Export-ServerInventory.ps1`, and `Run-Inventory.ps1`
- Stable portable artifact: `Server-Inventory-Configuration-Manager.exe`
- Platform: Windows x64 portable desktop application
- Compatibility target: Windows Server 2012 and newer as broadly as practical
- Go toolchain target: Go 1.20.x
- Current release line: 3.2.x
- Public source and release repository: https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager
- Public updater feed: https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/releases/latest/download/update-manifest.json

The v3.1.0 PS1 release is historical and was published against the wrong product format. The maintained product from v3.2.0 forward is the executable application.

## Development rules

1. Preserve working collectors, report generation, browser-hosted GUI layout, settings, and report viewer behavior unless a requested change requires otherwise.
2. Use semantic versions. Update `appVersion`, `collectorVersion`, and `$scriptVersion` together.
3. Keep the release filename `Server-Inventory-Configuration-Manager.exe` stable and without a version suffix.
4. Store settings, logs, updater cache, downloads, ready updates, health state, and rollback data under `%LOCALAPPDATA%\Server Inventory Configuration Manager`.
5. Never commit credentials, GitHub tokens, signing private keys, passwords, or customer inventory output.
6. The updater must never automatically download, install, or restart.
7. A failed or offline update check must never be presented as current/up to date.
8. Update downloads must be HTTPS/host allowlisted, size checked, SHA-256 verified, PE validated, and checked for expected application identity/version.
9. Stage replacement on the target filesystem, retain one rollback generation, and restore it if the new application does not confirm a healthy visible startup.
10. Do not add server setup, Codex CLI automation, unattended workers, payment setup, or API-account setup.

## Validation

```powershell
.\scripts\Test-Source.ps1

go test ./...
go vet ./...

# After building the icon-bearing EXE
.\scripts\Test-Binary.ps1 -ExecutablePath .\build\Server-Inventory-Configuration-Manager.exe -ExpectedVersion <version>
.\scripts\Test-UpdateInstall.ps1 -ExecutablePath .\build\Server-Inventory-Configuration-Manager.exe -ExpectedVersion <version>
.\scripts\Build-ReleaseManifest.ps1 -ExecutablePath .\build\Server-Inventory-Configuration-Manager.exe -OutputDirectory .\dist -ExpectedVersion <version>
```

## Release procedure

1. Update source version and `release-notes/<version>.md`.
2. Verify Windows CI.
3. Update `release/VERSION` only when publishing.
4. The release workflow builds with Go 1.20.x, injects the application icon, validates the executable, runs the helper-mode update integration test, creates stable EXE/checksum/manifest assets, publishes tag `v<version>`, and verifies the public feed.
5. Never overwrite an existing release or tag.
6. Update `DEVELOPMENT_HANDOFF.md` after release with exact tag/commit and validation status.