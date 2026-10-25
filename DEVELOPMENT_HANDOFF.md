# DEVELOPMENT_HANDOFF.md

## Current baseline

- Primary product: Windows portable executable Server-Inventory-Configuration-Manager.exe.
- User-confirmed functional/visual baseline: Server Inventory & Configuration Exporter GUI v3.0.10.
- Uploaded baseline EXE SHA-256: 572bf173917330a2fde4b4bf67df998f9f29522c8814d5350b50826caf38ee4e.
- The original v3.0.10 source ZIP was located. Its bundled EXE matches the uploaded baseline exactly; no EXE decompilation was used.
- Source provenance and original file hashes: baseline-verification.json.
- Existing published executable: v3.2.0 at commit 3c675de0e74a614c728a0ba0aa41d30a7aa45c99.
- Prepared version: v3.2.1. Publication and Windows results must be recorded after validation.
- Repository is public: https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager.

## Historical PS1 release and feed

v3.1.0 is the mistaken PS1-only application line. Its existing tag, release, and assets must remain unchanged. Its source is retained in legacy for history, not as executable build input.

The shared latest-release manifest now belongs exclusively to the executable line. The old PS1 updater cannot change application formats and should not be used. Its built-in latest-release URL can encounter executable metadata and reject it. Its version-specific v3.1.0 manifest remains historical. Users of v3.0.10 or the PS1 release must download the maintained EXE once; v3.0.10 has no updater to retrofit remotely.

Executable public feed: https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/releases/latest/download/update-manifest.json.

## Source and preserved behavior

The application uses the original Go Windows host and embedded HTML interface, original standalone Export-ServerInventory.ps1 and Run-Inventory.ps1, and original icon files. The collector differs from the original only in scriptVersion. scripts/verify_baseline.py enforces the original collector/wrapper/icon hashes, normalizing that one version value.

The legacy-derived collector build was removed from the active workflow. Historical generators are in legacy/build-tools. Build resources from original icon bytes with scripts/build_resources.py; Go links them with Windows FileVersion/ProductVersion and application identity metadata. No compiled EXE patching is used.

Inventory/report collection continues to use Windows' built-in PowerShell, preserving the v3.0.10 process-scoped invocation. Update checks, download, staging, native replacement, health, and rollback use Go/Win32 only.

## Updater architecture

- Authenticated first UI paint starts background checks; startup is never blocked on the network.
- A native 15-second wakeup checks the five-minute due time, avoiding gaps caused by intervening manual/focus checks. Focus requires two minutes. Manual cooldown is 30 seconds.
- All checks are single-flight. ETags are retained only for validated successful manifests. UTF-8 BOM feeds are accepted for backward compatibility; new feeds omit the BOM.
- Retry-After, shared-IP GitHub rate-reset headers, and exponential backoff apply to manual checks too. Errors/offline/cached same-version state cannot report up to date.
- Download and Install and restart remain separate explicit actions. Downloads have a 64 MiB ceiling, exact HTTPS repository/version URLs and redirect allowlists, size/hash checks, PE parsing, a mapped exact identity/version marker, and Windows file/product metadata validation.
- Installer and collection/report-dialog operations exclude each other. New collections are blocked once install begins.
- The new EXE runs its own update-helper mode, waits for the old host to exit, copies to the target volume, rechecks bytes, and atomically replaces the executable.
- One previous.exe rollback generation is retained. Health requires the expected token, exact new process PID/path/version, a rendered UI, and a visible window owned by that application's isolated browser process. It must remain alive during confirmation. No shipped headless-health bypass exists.
- Failed health restores and relaunches the previous version. Both target-volume temporary names are removed after use.
- The install target remains the current EXE path, keeping stable/custom/versioned shortcut targets valid. New downloads use the stable unversioned filename. No automatic legacy rename is performed.

## Per-user data

All updater data is under %LOCALAPPDATA%/Server Inventory Configuration Manager:

- Settings/settings.json
- Logs/Updates.log
- Updates/Cache/update-cache.json
- Updates/Downloads and Updates/Ready
- Rollback/previous.exe and previous.json
- Health/current.json and last-update.json
- Runtime/application-url.txt, application.ico, and per-session browser profiles

User-selected inventory reports remain in their selected output directory. Unknown files are not removed by update cleanup.

## Validation and release status

See VALIDATION.md. At preparation time, local Linux cross-compilation, Go vet for Windows, binary metadata parsing, source provenance, and JavaScript syntax checks pass. Hosted Windows tests and actual published previous-to-new update results are pending. Do not describe them as passed until the workflows complete.

## Approved scope

Continue inventory/configuration capture, reporting, server-role coverage, migration/recovery assistance, desktop UX, executable updater reliability, release automation, and handoff documentation. Do not add server setup, CLI automation, unattended workers, payment setup, API-account setup, or embedded credentials.
