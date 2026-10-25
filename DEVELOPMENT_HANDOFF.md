# DEVELOPMENT_HANDOFF.md

## Current baseline

- Primary product: Windows portable executable Server-Inventory-Configuration-Manager.exe.
- User-confirmed functional/visual baseline: Server Inventory & Configuration Exporter GUI v3.0.10.
- Uploaded baseline EXE SHA-256: 572bf173917330a2fde4b4bf67df998f9f29522c8814d5350b50826caf38ee4e.
- The original v3.0.10 source ZIP was located. Its bundled EXE matches the uploaded baseline exactly; no EXE decompilation was used.
- Source provenance and original file hashes: baseline-verification.json.
- Latest published and automatically validated executable: v3.2.2, source commit ab7fbb783ce4032f951bb717d745f4471ae4d889.
- Earlier v3.2.1 remains unchanged at commit 54d627d736f11335c8cf6ce4c89649f1c6351289.
- User confirmation still applies to the v3.0.10 functional baseline. v3.2.2 has hosted Windows validation, not a claim of user field confirmation.
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

- Release: https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/releases/tag/v3.2.2.
- Exact release tag/commit: v3.2.2 / ab7fbb783ce4032f951bb717d745f4471ae4d889.
- Stable EXE size: 6491648 bytes.
- EXE SHA-256: 68a816ae191fab47526893bca06deb0466a9193a543f2b2410955bd7f6e8d9cc.
- Published assets: stable EXE, .exe.sha256.txt, update-manifest.json, and go-test-results.txt.
- Exact-release source CI passed: https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/actions/runs/40169800117.
- Publication, public feed/download verification, and the real published v3.2.1-to-v3.2.2 update passed: https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/actions/runs/40169800158.
- The prior real v3.2.0-to-v3.2.1 update also passed: https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/actions/runs/40169175169.
- Actual Windows automation verified public detection, explicit Download and Install and restart, native replacement, visible rendered health, byte-identical settings, exactly one previous rollback EXE, shortcut preservation/relaunch, real two/five-minute thresholds, cleanup, forced failed-health rollback and previous-version relaunch.
- Target EXE was on D: and staging was on C:, exercising cross-volume staging. The separate native helper test covers same-volume installation.
- Independent downloads matched both checksums, Windows PE/application/version metadata, and the embedded Git revision. The public feed returned a real ETag conditional HTTP 304.
- A Windows negative control runs the actual published v3.2.1 EXE with an exclusive target lock: one orphan file before the fix, zero with v3.2.2, installed bytes unchanged.
- Full structured evidence: release/3.2.2-verification.json. GUI test JSON/logs/screenshots are workflow artifacts.
- Earlier releases/tags/assets, including the v3.1.0 PS1 release, were not overwritten. The workflow refuses existing versions. GitHub's repository-level immutable-release lock is not enabled; the API immutable flag is false.

See VALIDATION.md for explicit limits. Hosted tests ran on Windows Server 2025 with Edge. UAC consent, physical desktop interaction/focus return, other browser fallback modes, older Windows Server versions, and live inventory/report workflows were not tested. Offline/error/rate-limit/ETag/security cases use deterministic native test transport, with an additional real public ETag check. SHA-256 is not publisher signing.

## Approved scope

Continue inventory/configuration capture, reporting, server-role coverage, migration/recovery assistance, desktop UX, executable updater reliability, release automation, and handoff documentation. Do not add server setup, CLI automation, unattended workers, payment setup, API-account setup, or embedded credentials.

## Locked-target cleanup regression

The Windows test runs the exact published v3.2.1 EXE against an exclusively locked installed target. It reproduces one orphan target-volume update file. The v3.2.2 helper refuses the same unsafe installation, preserves the installed file bytes, logs the cause, and leaves zero target-volume temporary files. The previous published binary is checksum-pinned in the test. Source CI for this fix: https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/actions/runs/40169661939, source commit 61fb5ef25ffde8764a229d64cf2c3cb5f0bec5f6.

## Prepared updater test release 3.2.3

The user requested a small visible application change and a published update to test the updater. Prepared version 3.2.3 adds a Portable EXE badge beside the existing header badges. The icon, layout structure, collectors, report code, settings behavior, and updater implementation are otherwise unchanged. Application, collector, script, and Windows resource versions advance together. The published-upgrade workflow uses the exact published v3.2.2 EXE, SHA-256 68a816ae191fab47526893bca06deb0466a9193a543f2b2410955bd7f6e8d9cc, as its previous-version test input. Source CI passed at commit b9ba2f934d4f039ac27d47284b7d043771d6c483: https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/actions/runs/40171315650. Publication and the new Windows upgrade results are pending; the v3.2.2 validation record above remains historical evidence.
