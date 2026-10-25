# DEVELOPMENT_HANDOFF.md

## Current baseline

- Application: Server Inventory Configuration Manager
- Version: 3.1.0
- Source: Server-Inventory-Configuration-Manager.ps1
- Repository: https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager
- Visibility: public
- Default branch: main
- Update feed: https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/releases/latest/download/update-manifest.json
- Target: Windows portable PowerShell/WPF, Windows Server 2012 and newer as broadly as practical
- Starting source: latest user-confirmed single-file v3.0.25 baseline from the development chat

## Architecture

The application is one readable PS1 containing the WPF interface, collectors, report generation, report viewer, icon/shortcut support, and updater.

Per-user application data is below:

%LOCALAPPDATA%\Server Inventory Configuration Manager

Important paths:

- Settings\settings.json
- Logs\Updates.log
- Updates\Cache\update-cache.json
- Updates\Downloads
- Updates\Ready
- Updates\Helper\Install-Update.ps1
- Rollback\previous.ps1
- Rollback\previous.json
- Health\current.json

## Updater

- Startup check begins after the WPF window renders.
- Automatic checks run every five minutes.
- Returning focus checks when at least two minutes have passed.
- Checks are single-flight and use ETag/If-None-Match caching.
- Retry-After and exponential failure backoff are honored.
- Manual checks have a cooldown.
- Offline/failure states are explicit.
- Download and Install and restart are separate explicit actions.
- Downloads are HTTPS/host allowlisted, size checked, SHA-256 verified, and checked for application identity/version.
- Installation stages a temporary copy on the target filesystem before replacement.
- One rollback copy is retained.
- The new app must write a fresh healthy-start marker after WPF render or the helper restores/relaunches the previous version.
- Legacy versioned filenames and shortcuts are migrated where practical.

A matching SHA-256 validates bytes against the trusted manifest. It is not proof of publisher signing.

## Validation

~~~powershell
powershell.exe -NoProfile -File .\scripts\Test-Source.ps1
powershell.exe -NoProfile -File .\scripts\Build-ReleaseManifest.ps1 -OutputDirectory .\dist
~~~

Hosted CI uses windows-latest and Windows PowerShell.

## Known validation limitation

This environment cannot interactively exercise WPF, UAC, native focus/timer behavior, or a real install/restart/rollback cycle. Hosted Windows CI validates parsing and release/download contracts but is non-interactive. Any native Windows behavior not exercised on an interactive desktop must be disclosed in the release handoff.

## Approved scope

Continue inventory/configuration capture, report output, server-role coverage, migration/recovery assistance, desktop UX, reliable updater behavior, release automation, and documentation. Do not add server setup, Codex CLI automation, unattended workers, payment setup, or API-account setup.
