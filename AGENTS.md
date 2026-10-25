# AGENTS.md

Read this file first before changing Server Inventory Configuration Manager.

## Product baseline

- Product: Server Inventory Configuration Manager
- Primary source: Server-Inventory-Configuration-Manager.ps1
- Platform: Windows, portable PowerShell/WPF application
- Current release line: 3.1.x
- Public source and release repository: https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager
- Public updater feed: https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/releases/latest/download/update-manifest.json

The maintained release is a single readable PowerShell file. Do not replace it with an EXE bootstrapper, encoded PowerShell, downloaded runtime, or installer unless the project owner explicitly requests that architecture.

## Development rules

1. Preserve working inventory collectors, report generation, WPF layout, settings, and report viewer behavior unless a requested change requires otherwise.
2. Use semantic versions. Update both $script:AppVersion and the embedded collector $scriptVersion together.
3. Keep the stable launch filename Server-Inventory-Configuration-Manager.ps1.
4. Store settings, logs, updater cache, downloads, helper files, health state, and rollback data under %LOCALAPPDATA%\Server Inventory Configuration Manager, not beside the application.
5. Never commit credentials, GitHub tokens, signing private keys, passwords, or customer inventory output.
6. The updater must never automatically download, install, or restart.
7. A failed or offline update check must not be presented as current/up to date.
8. Update downloads must be HTTPS, host allowlisted, size checked, SHA-256 verified, and checked for expected application identity/version.
9. Keep only one rollback generation. Restore it if the updated application does not create a fresh healthy-start marker.
10. Do not add server setup, Codex CLI automation, unattended workers, payment setup, or API-account setup.

## Validation

Windows source validation:

~~~powershell
powershell.exe -NoProfile -File .\scripts\Test-Source.ps1
~~~

Release-asset dry run:

~~~powershell
powershell.exe -NoProfile -File .\scripts\Build-ReleaseManifest.ps1 -OutputDirectory .\dist
~~~

## Release procedure

1. Update source version and release-notes/<version>.md.
2. Verify CI.
3. Add/update release/VERSION only when publishing.
4. The release workflow validates source, creates stable PS1/checksum/manifest assets, publishes tag v<version>, and verifies the public feed.
5. Never overwrite an existing release or tag.
6. Update DEVELOPMENT_HANDOFF.md after release with exact tag/commit and validation status.
