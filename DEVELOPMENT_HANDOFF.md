# DEVELOPMENT_HANDOFF.md

## Current baseline

- Primary product: Windows portable executable Server-Inventory-Configuration-Manager.exe.
- User-confirmed functional/visual baseline: Server Inventory & Configuration Exporter GUI v3.0.10.
- Uploaded baseline EXE SHA-256: 572bf173917330a2fde4b4bf67df998f9f29522c8814d5350b50826caf38ee4e.
- The original v3.0.10 source ZIP was located. Its bundled EXE matches the uploaded baseline exactly; no EXE decompilation was used.
- Source provenance and original file hashes: baseline-verification.json.
- Latest published executable: v3.2.5, source commit b02163944ff8189cd627a2f7af8963238d797f71.
- Earlier v3.2.4 remains unchanged at commit 437a809a00b8112132b9e4e54566e3cb2be33ab5.
- Earlier v3.2.3 remains unchanged at commit 6ee8048539ae429d05f7938e06c2baabdd34fb92.
- Earlier v3.2.2 remains unchanged at commit ab7fbb783ce4032f951bb717d745f4471ae4d889.
- Earlier v3.2.1 remains unchanged at commit 54d627d736f11335c8cf6ce4c89649f1c6351289.
- User confirmation still applies to the v3.0.10 functional baseline. The user reports v3.2.4 improved the browser behavior. v3.2.5 has hosted Windows validation, without a claim of full user field confirmation.
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
- Settings/window.json for native window placement and maximized state
- Logs/Updates.log
- Updates/Cache/update-cache.json
- Updates/Downloads and Updates/Ready
- Rollback/previous.exe and previous.json
- Health/current.json and last-update.json
- Runtime/application-url.txt, application.ico, and per-session guest browser profiles with application ownership records

User-selected inventory reports remain in their selected output directory. Unknown files are not removed by update cleanup.

## Validation and release status

- Current release: https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/releases/tag/v3.2.5.
- Exact tag/commit: v3.2.5 / b02163944ff8189cd627a2f7af8963238d797f71.
- Change: remembers normal window bounds and maximized state independently of disposable guest profiles and inventory settings. Flushes before shutdown/update-helper start, preserves the last non-minimized state and recovers unusable saved positions into an available monitor work area.
- Stable EXE: Server-Inventory-Configuration-Manager.exe, 6536192 bytes.
- SHA-256: bbd06ab0f9dadad77c0e3ed4bacf30f1482dc8ae4766426a5f68f40ab84d34b5.
- Prepublication Windows source CI passed: https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/actions/runs/40175008751, commit de7f4d73ff84f93d4cb72b164704bedb0f8ab18b.
- Exact-release Windows CI passed: https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/actions/runs/40175227948.
- Publication/public feed passed: https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/actions/runs/40175227858.
- All 24 native Go tests and four renderer regression checks passed. The real built EXE completes native close/reopen, exact position/size restore, maximize, minimized close, maximized reopen, restoration of normal bounds, off-screen saved-position recovery and inventory-settings preservation. Tampered-file, locked-target cleanup and real helper healthy-start tests also pass.
- Real published v3.2.4-to-v3.2.5 upgrade: corrected published validation passed, with 30 directly exercised checks and the two successful real timer waits retained from the earlier run against identical SHA-256 bytes. Actual native move/resize, saved state, shortcut relaunch at exact bounds, settings, old-window closure, native health, C: staging to D: target, cleanup and rollback passed with zero unexpected dialogs. Corrected run: https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/actions/runs/40176269574. Evidence: release/3.2.5-verification.json.
- Independent public EXE/checksum/manifest agreement, PE identity/version metadata, clean embedded Git revision and actual conditional HTTP 304 passed.
- Prior v3.2.4 release/asset IDs, sizes and digests are unchanged. Original collector/wrapper/icon provenance passes. Earlier releases including historical v3.1.0 PS1 are preserved. The release workflow refuses overwrites; repository-level immutable locking remains disabled.

Testing limits: hosted Windows Server 2025/Edge automation, no physical monitor removal or mixed-DPI hardware, no human desktop/UAC interaction, no reproduction of the user's Microsoft account or organization browser policies, no Chrome guest or legacy Windows validation and no live inventory/report collection. Focus events follow real elapsed waits but use a synthetic DOM event. Native network/security cases use deterministic responses. SHA-256 validates bytes, not publisher signing.

## Approved scope

Continue inventory/configuration capture, reporting, server-role coverage, migration/recovery assistance, desktop UX, executable updater reliability, release automation, and handoff documentation. Do not add server setup, CLI automation, unattended workers, payment setup, API-account setup, or embedded credentials.

## Locked-target cleanup regression

The Windows test runs the exact published v3.2.1 EXE against an exclusively locked installed target. It reproduces one orphan target-volume update file. The v3.2.2 helper refuses the same unsafe installation, preserves the installed file bytes, logs the cause, and leaves zero target-volume temporary files. The previous published binary is checksum-pinned in the test. Source CI for this fix: https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/actions/runs/40169661939, source commit 61fb5ef25ffde8764a229d64cf2c3cb5f0bec5f6.

## Published updater test release 3.2.3

Version 3.2.3 adds a Portable EXE badge beside the existing header badges. The icon, layout structure, collectors, report code, settings behavior, and updater implementation are otherwise unchanged. Application, collector, script, and Windows resource versions advance together. The published-upgrade workflow uses the exact published v3.2.2 EXE, SHA-256 68a816ae191fab47526893bca06deb0466a9193a543f2b2410955bd7f6e8d9cc, as its previous-version test input. Source CI passed at commit b9ba2f934d4f039ac27d47284b7d043771d6c483: https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/actions/runs/40171315650. Release v3.2.3 is published and its real v3.2.2-to-v3.2.3 upgrade passed all 24 checks. The v3.2.2 verification file remains historical evidence.

## Browser and shutdown fix 3.2.4

The user reported Edge automatic Windows-account sign-in, extension restore/install tabs on every app launch, and an old app window left open with repeated Request failed with status 0 alerts during update. The earlier v3.2.3 automation accepted every page dialog and did not assert that the old native browser process closed. Its successful updater result did not establish either of those behaviors.

The 3.2.4 source launches only an app-owned guest Edge/Chrome session with extensions, sync, first-run and background mode disabled. It removes the personal-browser fallback and reads guest/sign-in policies without changing them. Browser ownership is matched by the exact application Runtime profile using native process command-line metadata, so launcher handoff does not lose the window. Shutdown closes the owned browser before stopping the backend. The downloaded helper can also discover the live legacy application's profile by its token-bound URL and native TCP listener PID, close it, and clean only verified orphan app windows.

The UI cancels pending requests and polling before installation, ignores focus during shutdown, displays update-action failures inline, and restores controls/polling if installation is rejected. Original inventory/report behavior and icon assets are preserved. The only collector difference is scriptVersion.

New gates include the actual v3.2.3 HTML negative control, fixed-page no-dialog/no-post-shutdown-request checks, and a real native modal-browser close test with launcher PID deliberately absent and another profile kept alive. The published-upgrade test input advances to the immutable v3.2.3 EXE with pinned SHA-256 8cd753daefddc1072ee2cc69a48c0269c4a9ea5b646f7d21215c29a5575e29b5. It now asserts old-profile process exit and the new native browser's guest/extension/sync flags. Unexpected dialogs are recorded and fail the test. Exact legacy status-0 alerts may be recorded only while leaving the unchanged previous page.

Windows source CI passed at commit 36385ac478e1252844fd307e8663c304edc797c7: https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/actions/runs/40173525342. This includes the native modal close with a separate profile preserved, live legacy TCP-owner validation, renderer negative control and zero-dialog fixed interface, updater/security tests, and actual helper install with rendered/visible healthy start. The new release is published; the real v3.2.3-to-v3.2.4 upgrade passed all 29 checks with zero unexpected dialogs and zero legacy shutdown alerts. Native browser flags, complete old-profile process exit, settings, shortcuts, timers, cleanup, health and rollback passed. Structured evidence: release/3.2.4-verification.json. The user's actual signed-in Microsoft account, organization browser policies, physical desktop and UAC consent are not reproduced by hosted CI.

## Window persistence 3.2.5

The user reports v3.2.4 is better and requests remembered window size/position. Keep the original v3.0.10 functional/visual baseline and the v3.2.4 guest-session and shutdown fixes.

The 3.2.5 source adds a native observer bound to the verified app-owned window. It stores normal WINDOWPLACEMENT bounds and maximized state in Settings/window.json, independently of Settings/settings.json and disposable browser profiles. Changes are saved while the window is open; shutdown and installation flush before the browser/helper closes it. Minimized state never overwrites the last usable normal/maximized state. Restoration uses the matching native placement API, checks the current monitor work area, and moves/resizes unusable saved bounds into that area. Thread-specific DPI context is restored after each operation; no process-wide dialog setting or personal browser setting is changed.

Native tests run the actual built EXE through native title-bar close/reopen, normal geometry restoration, maximize, minimized close, maximized reopen, restored normal bounds and an off-screen saved-position recovery. They verify existing inventory settings remain byte-identical. Additional validation rejects malformed/oversized/schema-invalid state and exercises negative monitor coordinates. The published-upgrade input advances to the immutable v3.2.4 EXE, SHA-256 6319f6ad5c2b15882110b4a34ba63f99812be879166de3a754b70e794c1c4ab2, and verifies actual native geometry after a stable-shortcut relaunch.

Windows source CI passed at commit de7f4d73ff84f93d4cb72b164704bedb0f8ab18b: https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/actions/runs/40175008751. This includes the actual EXE close/reopen/maximize/minimized-close/off-screen tests, original source provenance, updater/security tests, renderer regressions and real helper healthy installation. The release is published. The corrected published upgrade and window relaunch validation passed; final evidence is recorded above. Hosted Windows automation does not test physical monitor removal, mixed-DPI hardware, a human desktop/UAC interaction, the user's Microsoft account, Chrome guest mode or live inventory/report collection.

## Published-window validation harness correction

The original publication job successfully built/published/verified 3.2.5. Its upgrade job passed the real v3.2.4 update and both actual two/five-minute waits, then failed the new resize test setup. PowerShell field writes to a nested RECT value did not update the parent placement structure. The old operation is retained as a negative control: LegacyNestedWriteValue is 0 after an attempted Left=87 write. The corrected test assigns a complete RECT, finds the exact visible owned native window through EnumWindows, and independently verifies the applied bounds before checking persistence. Request, actual native placement and saved JSON all match Left=87, Top=63, Right=987, Bottom=663.

Corrected validation: https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/actions/runs/40176269574, harness commit d36360c94211fb4ad65a0c88c624c6c603e307b7. It reruns the actual published download/install/restart, window save/relaunch, settings, shortcuts, health, cleanup and forced rollback. Only the two already passed real timer waits are reused, guarded by the exact unchanged published SHA-256. The failed first report is preserved in release/3.2.5-first-upgrade-evidence.json; do not describe its entire workflow as successful. No release tag, EXE or asset was changed for the test repair. The standard release workflow still runs all real waits by default. The separate validation workflow can be dispatched with no evidence input to run all waits again.
