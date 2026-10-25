# DEVELOPMENT_HANDOFF.md

## Current baseline

- Primary product: Windows portable executable Server-Inventory-Configuration-Manager.exe.
- User-confirmed functional/visual baseline: Server Inventory & Configuration Exporter GUI v3.0.10.
- Uploaded baseline EXE SHA-256: 572bf173917330a2fde4b4bf67df998f9f29522c8814d5350b50826caf38ee4e.
- The original v3.0.10 source ZIP was located. Its bundled EXE matches the uploaded baseline exactly; no EXE decompilation was used.
- Source provenance and original file hashes: baseline-verification.json.
- Latest published and automatically validated executable: v3.2.3, source commit 6ee8048539ae429d05f7938e06c2baabdd34fb92.
- Earlier v3.2.2 remains unchanged at commit ab7fbb783ce4032f951bb717d745f4471ae4d889.
- Earlier v3.2.1 remains unchanged at commit 54d627d736f11335c8cf6ce4c89649f1c6351289.
- User confirmation still applies to the v3.0.10 functional baseline. v3.2.3 has hosted Windows validation, not a claim of user field confirmation.
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

- Current release: https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/releases/tag/v3.2.3.
- Exact tag/commit: v3.2.3 / 6ee8048539ae429d05f7938e06c2baabdd34fb92.
- Change: a small Portable EXE badge beside the existing header badges, plus consistent version metadata. Collector/report logic, updater behavior, settings behavior, and icon bytes are unchanged.
- Stable EXE: Server-Inventory-Configuration-Manager.exe, 6491648 bytes.
- SHA-256: 8cd753daefddc1072ee2cc69a48c0269c4a9ea5b646f7d21215c29a5575e29b5.
- Exact-release Windows source CI passed: https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/actions/runs/40171462464.
- Publication, public feed/assets, and real v3.2.2-to-v3.2.3 update passed: https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/actions/runs/40171462463.
- All 24 published-upgrade checks passed, including detection, explicit download/install, new visible healthy window, saved settings, stable shortcut relaunch, real two/five-minute thresholds, cleanup, and forced missing-health rollback/relaunch.
- Target was on D: with staging on C:. Native helper CI also passed same-volume replacement and the locked-target regression control.
- Independent public download, checksum file/manifest, Windows PE/version metadata, embedded Git revision, embedded new badge, and real conditional HTTP 304 were verified.
- Structured evidence: release/3.2.3-verification.json. Prior v3.2.2 evidence remains in release/3.2.2-verification.json.
- Earlier tags and assets, including v3.2.2 and historical v3.1.0 PS1, were preserved. The release workflow rejects overwrites. GitHub repository-level immutable locking remains disabled.

Testing limits are unchanged: hosted Windows Server 2025/Edge automation, no human desktop/UAC test, synthetic focus event after a real wait, deterministic native network-failure/security cases, no live inventory/report workflows, and no legacy Windows Server or other-browser validation. The original collector/wrapper/icon provenance checks still pass. SHA-256 validates bytes, not publisher signing.

## Approved scope

Continue inventory/configuration capture, reporting, server-role coverage, migration/recovery assistance, desktop UX, executable updater reliability, release automation, and handoff documentation. Do not add server setup, CLI automation, unattended workers, payment setup, API-account setup, or embedded credentials.

## Locked-target cleanup regression

The Windows test runs the exact published v3.2.1 EXE against an exclusively locked installed target. It reproduces one orphan target-volume update file. The v3.2.2 helper refuses the same unsafe installation, preserves the installed file bytes, logs the cause, and leaves zero target-volume temporary files. The previous published binary is checksum-pinned in the test. Source CI for this fix: https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/actions/runs/40169661939, source commit 61fb5ef25ffde8764a229d64cf2c3cb5f0bec5f6.

## Published updater test release 3.2.3

Version 3.2.3 adds a Portable EXE badge beside the existing header badges. The icon, layout structure, collectors, report code, settings behavior, and updater implementation are otherwise unchanged. Application, collector, script, and Windows resource versions advance together. The published-upgrade workflow uses the exact published v3.2.2 EXE, SHA-256 68a816ae191fab47526893bca06deb0466a9193a543f2b2410955bd7f6e8d9cc, as its previous-version test input. Source CI passed at commit b9ba2f934d4f039ac27d47284b7d043771d6c483: https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/actions/runs/40171315650. Release v3.2.3 is published and its real v3.2.2-to-v3.2.3 upgrade passed all 24 checks. The v3.2.2 verification file remains historical evidence.
