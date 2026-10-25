# DEVELOPMENT_HANDOFF.md

## Current baseline

- Application: Server Inventory Configuration Manager
- Current published version: 3.2.0
- Baseline: user-confirmed executable v3.0.10 source package
- Stable artifact: `Server-Inventory-Configuration-Manager.exe`
- Repository: https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager
- Visibility: public
- Default branch: main
- Update feed: https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/releases/latest/download/update-manifest.json
- Target: Windows x64 portable desktop application, Windows Server 2012 and newer as broadly as practical
- Toolchain target: Go 1.20.x

## Published release

- Release: v3.2.0
- Release URL: https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/releases/tag/v3.2.0
- Release source commit: 3c675de0e74a614c728a0ba0aa41d30a7aa45c99
- Stable release asset: Server-Inventory-Configuration-Manager.exe
- Release asset SHA-256: 194ead5dd42391bfcf231ee15708537834aa771dbe79aeb983852ce90f251c9e
- Release asset size: 6321664 bytes
- Update manifest asset: update-manifest.json
- Windows CI run: https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/actions/runs/40167636433
- Publish and public-feed verification run: https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/actions/runs/40167636428
- Both workflows completed successfully.
- Windows CI built with Go 1.20.14, validated the PE/identity/version contract, executed the real helper-mode install path, confirmed healthy-start handling, and ran a tampered-update negative control.
- The publish workflow verified the public latest-release manifest and release assets after publication.
- The older v3.1.0 PS1 release remains historical and is not the maintained executable baseline.

## Architecture

The application is a Go Windows GUI host that embeds:

- `index.html` for the local browser-style interface
- `Export-ServerInventory.ps1` for inventory/report collection
- `Run-Inventory.ps1` for collector execution validation
- application icon assets

The GUI binds only to `127.0.0.1` and requires a per-run authorization token.

Per-user application data is below:

`%LOCALAPPDATA%\Server Inventory Configuration Manager`

Important paths:

- `Settings\settings.json`
- `Logs\Updates.log`
- `Updates\Cache\update-cache.json`
- `Updates\Downloads`
- `Updates\Ready\Server-Inventory-Configuration-Manager.exe`
- `Rollback\previous.exe`
- `Rollback\previous.json`
- `Health\current.json`

## Updater

- Startup check starts after the interface is launched.
- Background checks run every five minutes.
- Returning focus requests a check when the previous attempt is at least two minutes old.
- Requests are single-flight and use ETag/If-None-Match caching.
- Retry-After and exponential failure backoff are honored.
- Manual checks have a cooldown.
- Offline/failure states are explicit.
- Download and Install and restart are separate explicit actions.
- Downloads are HTTPS/host allowlisted, size checked, SHA-256 verified, PE/AMD64/GUI validated, and checked for application identity/version.
- Installation uses the downloaded new EXE itself in helper mode.
- The helper copies the staged file to the target filesystem before atomic replacement.
- One rollback executable is retained.
- The new app must write a matching healthy-start marker and confirm a visible application window before the helper accepts the update; otherwise it restores the previous EXE.
- The updater preserves the current executable path, including custom filenames, so existing shortcuts continue to work.

A matching SHA-256 validates bytes against the trusted manifest. It is not proof of publisher signing.

## Validation limitation

Local Linux build validation can cross-compile and inspect the Windows PE but cannot interactively exercise the Windows browser window, UAC, focus events, or a visible post-update window. Hosted Windows CI runs Go tests, source/binary validation, and the helper-mode install/relaunch path with a headless health probe. A real interactive Windows v3.2.0 to later-release update should still be field-tested.

## Approved scope

Continue inventory/configuration capture, report output, server-role coverage, migration/recovery assistance, desktop UX, executable updater behavior, release automation, and documentation. Do not add server setup, Codex CLI automation, unattended workers, payment setup, or API-account setup.