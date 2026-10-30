# DEVELOPMENT_HANDOFF.md

## 3.2.11 candidate: startup cycle handling and responsive ACL exports (not published)

The user authorized a focused build after a recursive permissions export on Server 2025 remained at the SMB collector stage for at least twenty minutes. ICACLS was still running with reported CPU activity; that is not proof of a deadlock. The published code emits no per-share heartbeat and has no timeout for this child. Requested scope is per-share status/elapsed time, a configurable bound, Skip remaining recursive ACL exports/Cancel, and explicit incomplete-export reporting. No live customer inventory, SMB settings change, or termination of the user's running process is authorized by this work. The configured bound covers the owned ICACLS process and bounded child/output cleanup, not every filesystem operation or collector. Synchronous output-folder setup, mapping writes, file promotion, earlier SMB queries and remote filesystem I/O can still wait; the independent application host and whole-collection Cancel remain the escape path.

Separately, the original 3.2.10 synthetic startup helper probe reproduced StackOverflowException on Windows Server 2019 / Windows PowerShell 5.1.17763.9121 / CLR 4.8.4811.0. All earlier probe checkpoints passed; the cyclic table snapshot call at original source line 3525 began but did not return. This establishes a startup report-table path failure before inventory, not a general .NET installation defect. The same original tests and byte-identical wrapper/collector startup passed hosted Server 2022/2025 with CLR 4.8.9363.0; that newer-runtime success does not establish Server 2019 compatibility. A fixed candidate needs confirmation against the affected runtime.

The exact published 3.2.10 collector and wrapper are archived under baseline/v3.2.10/collectors, pinned to their published SHA-256 identities. Approved edit regions have separate before/after hashes and a human-readable diff; unchanged bytes and the original 3.0.10 provenance remain protected. The original baseline-verification.json is unchanged. HTML report styling, Word/text rendering, icon, unrelated collectors, updater and release history are not intentionally changed.

Validation status: implementation and candidate validation are in progress. No new Windows-native, UI, timeout/cancellation, affected-2019, source-archive, or upgrade pass is claimed here until exact candidate evidence is recorded. Source version is being prepared as 3.2.11, while release/VERSION and the public feed remain 3.2.10. No release/tag/asset has been created or overwritten. Publication requires separate authorization.


## Collection workspace review: 3.2.10 published

The user requested a broad review and useful functional/visual improvements. This source adds clearer profile/selection styling, category and description search, expandable exports, scope summaries, saved collection choices, inline action errors, log search/severity filtering/live-follow control, keyboard tabs and compact-window layouts. It corrects forced inventory reselection and the previously unlocked event-history field during collection. See docs/APP_REVIEW.md for findings and behavior.

Safe choices are stored atomically in Settings/collection.json. Save choices does not run collection or probe/create an output folder. Collection start validates supported categories/dependencies and saves the choices. Sensitive consent and NPS full export are never restored; the existing sensitive-operation confirmation is retained. Existing settings.json, window.json, updater/rollback files and user output remain compatible. Original collectors/report functions/icons are protected by the existing provenance gates; only the standalone collector version changes.

Local checks: Go 1.20.14 Windows cross-build/test compilation/vet, baseline/report/icon/layout checks, four shutdown checks, three icon-policy checks and 14 real-browser workspace checks pass. The exact v3.2.9 HTML reproduces four prior failures. Screenshots at desktop, 800x600 and 390x844 were reviewed. Hosted Windows source CI passed at commit 37bc4ae75c3cd725dd9a16000ab3660bf3e279e9: https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/actions/runs/41013556970. All 32 native tests, 14 workspace browser checks, 18 report browser checks, five PowerShell report/Word checks, four shutdown and three icon-policy regressions, native helper install/visible health and source-archive rebuild passed. The Windows printed PDF retains the final-row and raw-output sentinels. Publication and the full real previous-to-new upgrade passed; final evidence is recorded below. Cross-compilation alone is not a Windows execution pass. The first hosted source run (41013218234) passed the native preference round trip and real executable save/close/relaunch, but failed an old wrong-version fixture that hard-coded 3.2.10 as invalid. The test now derives distinct versions from appVersion; no production validation is weakened. Source and release/VERSION now select 3.2.10 after corrected source CI passed. The latest published executable is now 3.2.10.


## Published release 3.2.10 verification

- Release/tag commit: v3.2.10 / 24e79e92e50d4eba3f8b6afb492e7b9e3991c322.
- Stable EXE: 6,598,144 bytes; SHA-256 8c1de02da5fa4ae4d0d6b94131d9930af7a0b2782e5ecb2f664c873ef6ef2c2e.
- Release: https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/releases/tag/v3.2.10.
- Exact-release Windows CI passed: https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/actions/runs/41014112454.
- Publication and full published upgrade passed: https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/actions/runs/41014112612.
- All 43 actual v3.2.9-to-v3.2.10 upgrade checks passed in one full run, with no reused timer evidence and zero unexpected dialogs. Automatic detection, explicit download/validation/staging, cross-volume replacement, old-profile closure, restart, visible native health, settings, shortcut/icon, real two-minute focus and five-minute scheduler waits, window geometry, cleanup, one rollback generation and actual failed-health rollback/relaunch passed. The updated application's Save choices action and visible profile restoration after a real stable-shortcut relaunch also passed.
- All 12 public assets and checksum files were independently downloaded and verified. PE x64/GUI identity/version metadata and clean embedded Git revision match the tag. Public feed returns the expected manifest and a genuine conditional HTTP 304. The application/report previews match validated publisher screenshots; the Illustrator master is byte-preserved. Earlier v3.2.9 tag, release and all eight asset identities/sizes/digests are unchanged.
- Structured evidence, complete test names and limits: release/3.2.10-verification.json. SHA-256 is byte validation, not publisher signing. Repository-level immutable locking remains disabled; release workflows reject overwrites.

Limits remain: hosted Windows Server/Edge/PowerShell 5.1 and synthetic UI/report fixtures. No live server-role collection, Office visual validation, human UAC/desktop interaction, native folder-dialog automation, physical mixed-DPI/monitor removal, legacy Windows/PowerShell or Chrome guest field testing. Focus uses a DOM event after a real elapsed wait; network/security error cases use deterministic native responses.

## Supplied Illustrator source — 2026-10-27

The user supplied the editable Illustrator SVG for current and future builds. Its original 49,437 bytes (SHA-256 `4eb6eb78435ebaf64dbc5b71c836af8f1b6544235bba3adbfcd0a7e2a7c3a775`) are preserved in `src/app/assets/icons/source/server-inventory-illustrator.svg`, with Git text conversion disabled for that master. No artwork was redesigned. The source renders pixel-identically at all ten icon sizes to the existing approved PNG/ICO. Direct inspection of the downloaded, checksum-pinned published v3.2.9 EXE confirms every native RT_ICON frame matches; evidence is in `docs/verification/2026-10-27-icon-source.json`. At that stage v3.2.9, its tag, assets, application versions and `release/VERSION` remained unchanged, avoiding a redundant artwork-only release. The later 3.2.10 workspace release includes this source work.

`prepare_icon.py` reads the master without resolving external DTDs/entities, rejects unsupported/active content, removes editor-only metadata and inlines its simple CSS fills/strokes. The generated safe runtime SVG is visually identical; no original editor payload is embedded in the application. `build_icons.cjs --check` verifies its exact derivation, every rendered size against the CSS/vector source, and generated asset hashes. Two converter tests include source identity and ten hostile/unsupported input controls. Both the untouched Illustrator master and safe SVG plus checksums are packaged and publicly verified for future new releases. Native icon/resource/shortcut tests, application CSP tests, baseline collector/report checks and compile-time resource embedding remain required. No finished EXE is patched.

Local Go 1.20.14 Windows cross-build/vet, source-identity/negative tests, deterministic ten-size render parity, resource generation, original provenance, report embedding and layout checks pass. Actual 512px, 32px and 16px renders were reviewed. The cloud sandbox cannot launch Chromium's local sockets; full Windows/browser/visible-health validation is delegated to the required hosted CI, not claimed from cross-compilation. Hosted source CI passed for this source update: https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/actions/runs/40561712690.

## Repository layout — 2026-10-26

The maintained Go Windows executable package and unchanged embedded collector/UI/icon inputs moved to `src/app/`. Root `go.mod` remains; build target is `./src/app`. Validation and provenance moved into `docs/`; release notes into `docs/release-notes`. Logical provenance keys and original hashes are unchanged. Native tests resolve repository build fixtures/history two levels above their package. `scripts/check_layout.py` is called by source validation. Version 3.2.9, release/VERSION, updater behavior and published assets are unchanged. This is source organization only.


## Current baseline

- Primary product: Windows portable executable Server-Inventory-Configuration-Manager.exe.
- User-confirmed functional/visual baseline: Server Inventory & Configuration Exporter GUI v3.0.10.
- Uploaded baseline EXE SHA-256: 572bf173917330a2fde4b4bf67df998f9f29522c8814d5350b50826caf38ee4e.
- The original v3.0.10 source ZIP was located. Its bundled EXE matches the uploaded baseline exactly; no EXE decompilation was used.
- Source provenance and original file hashes: docs/verification/baseline-verification.json.
- Latest published executable: v3.2.10, source commit 24e79e92e50d4eba3f8b6afb492e7b9e3991c322.
- Earlier v3.2.9 remains unchanged at commit 96d30c340565e6ea6e2ea68b70ff87b4f4d1894f. Its redesigned HTML report is preserved.
- Earlier v3.2.8 remains unchanged at commit 2294a921d4a85fbbb1ea5f6e551ad6861973acea. Its desktop/Explorer icon behavior is retained in 3.2.9.
- Earlier v3.2.7 remains unchanged at commit aabec80e9e8fa95aba8297ca0e6a2941479db8df. Its native EXE icon is correct; 3.2.8 additionally repairs old shortcut icon references and refreshes shell items.
- Earlier v3.2.6 remains unchanged at commit 10290a1c6f62808a30ec66fe11ac9281a4439b2f. Its header SVG was blocked by the old application policy and is superseded by 3.2.7.
- Earlier v3.2.5 remains unchanged at commit b02163944ff8189cd627a2f7af8963238d797f71.
- Earlier v3.2.4 remains unchanged at commit 437a809a00b8112132b9e4e54566e3cb2be33ab5.
- Earlier v3.2.3 remains unchanged at commit 6ee8048539ae429d05f7938e06c2baabdd34fb92.
- Earlier v3.2.2 remains unchanged at commit ab7fbb783ce4032f951bb717d745f4471ae4d889.
- Earlier v3.2.1 remains unchanged at commit 54d627d736f11335c8cf6ce4c89649f1c6351289.
- User confirmation still applies to the v3.0.10 functional baseline. The user reports v3.2.4 improved the browser behavior. v3.2.10 has hosted Windows validation, without a claim of full user field confirmation.
- Repository is public: https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager.

## Historical PS1 release and feed

v3.1.0 is the mistaken PS1-only application line. Its existing tag, release, and assets must remain unchanged. Its source is retained in legacy for history, not as executable build input.

The shared latest-release manifest now belongs exclusively to the executable line. The old PS1 updater cannot change application formats and should not be used. Its built-in latest-release URL can encounter executable metadata and reject it. Its version-specific v3.1.0 manifest remains historical. Users of v3.0.10 or the PS1 release must download the maintained EXE once; v3.0.10 has no updater to retrofit remotely.

Executable public feed: https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/releases/latest/download/update-manifest.json.

## Source and preserved behavior

The application uses the original Go Windows host and embedded HTML interface, original standalone Export-ServerInventory.ps1 and Run-Inventory.ps1, and the user-approved SVG-derived icon. Original icon bytes are preserved under baseline/v3.0.10/icons. Through 3.2.8 the collector differed from the original only in scriptVersion. The user-approved 3.2.9 HTML redesign changes only Write-ConsolidatedHtmlReport in addition to that version value; all collectors, block construction, Word/text generation and raw exports remain unchanged. Canonical presentation sources are in reporting and are embedded by scripts/embed_report_renderer.py. Original renderer bytes are archived under baseline/v3.0.10/reports; current hashes are in docs/verification/report-verification.json. scripts/verify_baseline.py enforces the original collector/wrapper and archived-icon hashes, normalizing scriptVersion and substituting the exact archived original HTML function before checking the original whole-script hash. It validates current icon hashes separately against docs/verification/icon-verification.json; docs/verification/baseline-verification.json remains unchanged.

The legacy-derived collector build was removed from the active workflow. Historical generators are in legacy/build-tools. Build resources from the approved active ICO generated from the editable SVG with scripts/build_resources.py; Go links them with Windows FileVersion/ProductVersion and application identity metadata. No compiled EXE patching is used.

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

- Settings/collection.json for validated remembered scope/output/event-history choices, excluding sensitive consent and NPS full export
- Settings/settings.json
- Settings/window.json for native window placement and maximized state
- Icons/application-<artwork SHA-256>.ico, persistent artwork for current-user desktop shortcuts
- Logs/Updates.log
- Updates/Cache/update-cache.json
- Updates/Downloads and Updates/Ready
- Rollback/previous.exe and previous.json
- Health/current.json and last-update.json
- Runtime/application-url.txt, application.ico, and per-session guest browser profiles with application ownership records

User-selected inventory reports remain in their selected output directory. Unknown files are not removed by update cleanup.

## Historical validation and release status: 3.2.9

- Historical release: https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/releases/tag/v3.2.9.
- Exact tag/commit: v3.2.9 / 96d30c340565e6ea6e2ea68b70ff87b4f4d1894f.
- Stable EXE: Server-Inventory-Configuration-Manager.exe, 6568448 bytes; SHA-256 5483a308e0da37e5e9a518f12b3d344ecf12fb267c4419c50860149394fb53c3.
- Editable SVG is unchanged, 1831 bytes; SHA-256 fc9211e88bb1f3d0bc34380c82bdd010456b57e59433650b85ae03cdaaf382cd.
- Report-Preview.png is a 1440x1000 rendering of synthetic data, 101139 bytes; SHA-256 8eab9e066dca7be579f7aab6bd86fa6cc9d6a2452cf885c432f406af8ab9d46d. It matches the validated release renderer output.
- Windows source CI passed before publication: https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/actions/runs/40224067507, source commit 071c28e94883235ca6174a87ce7f50e0bf856985.
- Exact-release Windows CI passed: https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/actions/runs/40224435714.
- Publication/public-download verification passed: https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/actions/runs/40224435804, publish job 121262697039.
- All 29 native Go tests passed, including old/new report authentication/content isolation, update/security/negative controls, shell icons, browser isolation and native window persistence. Real helper install/visible health passed. Four renderer-shutdown and three production icon-policy browser regressions passed.
- All 18 report browser checks and five actual report-function generation checks passed on Windows PowerShell 5.1/Edge. The exact original renderer is the negative control. All 21 table datasets, 1545 retained rows, original column identities/cell values and multiline output agree. Visible summary tables drop from seven to two; visible subsection links drop from 27 to eight in the fixture, with no datasets removed. Case-sensitive field-label formatting preserves ordinary words and readable camel-case headings. Source Word generation preserves the final row/raw output, and the release Windows printed PDF independently retains FINALROW-1500 and LAST-RAW-LINE.
- New HTML remains self-contained. Search reveals inactive/collapsed datasets, print expands every dataset and restores state, unknown metadata is labelled Not collected, and offline/no-script/mobile/deep-link/keyboard/empty/escaping cases pass. The actual app HTML and production CSP are used for the new and legacy viewer fixtures. Previously saved files remain readable; generate a new inventory/report to obtain the new layout.
- Original collectors, block creation, Word/text generation and wrapper/icon provenance are byte-protected outside the approved HTML function/version normalization. Current template/CSS/JS/embedded hashes are in docs/verification/report-verification.json; docs/verification/baseline-verification.json is unchanged.
- Actual published v3.2.8-to-v3.2.9 upgrade: passed all 41 checks in one full run with real two-minute focus and five-minute timer waits and no reused timer evidence. Automatic detection, explicit download/validation/staging/cross-volume install/restart, visible native health, byte-identical settings, stable shortcut/icon, native window placement/relaunch, one rollback generation, cleanup and missing-health rollback/relaunch passed. Zero unexpected or legacy shutdown dialogs occurred. Structured evidence: release/3.2.9-verification.json.
- Independent public EXE/SVG/preview downloads and all three checksum files agree. PE identity/version metadata, the exact clean embedded Git revision, feed HTTP 200 and conditional 304 passed. The earlier 3.2.8 tag/release/asset identities, sizes and digests are unchanged.
- The workflow refuses release/tag/asset overwrites; repository-level immutable locking remains disabled. SHA-256 validates bytes, not publisher signing.

Testing limits: hosted Windows Server 2025/Edge and Windows PowerShell 5.1, plus local PowerShell 7/Chromium. Report generation and browser fixtures use synthetic data; no live server/role collection, Office visual rendering, native report-folder dialog automation, human desktop/UAC interaction, physical mixed-DPI/monitor removal, legacy Windows/PowerShell 3/4 or Chrome guest field testing was performed. The viewer browser fixture uses real app HTML/CSP with local fixture responses; the real Go report endpoint is tested separately. Focus uses a synthetic DOM event after real elapsed waits. Offline/HTTP/rate-limit/cache/security cases use deterministic native responses. Historical full 3.2.7-to-3.2.8 evidence remains in release/3.2.8-verification.json.

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

## Editable flat icon 3.2.6

The user explicitly requested the server/checkmark design as an editable SVG and authorized applying it and publishing the application. This authorizes replacing the previous active icon. src/app/assets/icons/server-inventory.svg is a true vector master with named shapes/layers, transparent background and four solid fills (#132F4C, #2563EB, #FFFFFF, #13B8A6). It contains no embedded image, gradient, shading or external content.

Pinned build-only resvg 2.6.2 renders the SVG to app-icon.png and ten PNG-backed ICO sizes (16, 20, 24, 32, 40, 48, 64, 96, 128, 256) through scripts/build_icons.cjs. The active ICO is linked into the executable's native resources during compilation. The header uses the embedded SVG directly, favicon/PNG endpoints use the same generated art, and the existing native window-icon path loads that active ICO. The original icon files were copied unchanged to baseline/v3.0.10/icons. Original hashes remain unchanged; current SVG/output hashes are in docs/verification/icon-verification.json.

CI re-renders and compares icon bytes, checks archived original provenance, compares every compiled icon resource frame with the current ICO, and tests Windows shell large/small icon extraction. Publication includes Server-Inventory-Configuration-Manager.svg and its SHA-256 file. The previous-release input advances to immutable v3.2.5, SHA-256 bbd06ab0f9dadad77c0e3ed4bacf30f1482dc8ae4766426a5f68f40ab84d34b5. The actual published-upgrade test verifies the SVG/PNG/favicon bytes and loaded header image, plus the existing install/restart/settings/shortcut/window/timer/rollback checks.

Windows source CI passed at commit b50541f76bb10d89aefc39628ea4c3a8b1a23493: https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/actions/runs/40179122995. This includes reproducible SVG assets, archived baseline provenance, all compiled icon frames, native large/small shell extraction, window persistence, security/updater tests, renderer regressions and actual helper healthy installation. Version 3.2.6 was published at commit 10290a1c6f62808a30ec66fe11ac9281a4439b2f. Its live upgrade test caught the blocked header SVG under the original image policy; it is superseded by 3.2.7. No 3.2.6 tag or asset was changed. No normal application or update runtime dependency was added. Existing inventory/report behavior, window persistence and guest isolation are preserved. Human desktop icon-cache/UAC behavior, physical mixed-DPI hardware, Chrome/legacy Windows and live inventory/report workflows remain untested.

## Header image policy correction 3.2.7

The v3.2.6 icon assets, executable icon and editable SVG are valid, but the live published-upgrade test found that the application page's existing img-src data: policy blocks its new same-origin header SVG. v3.2.6 remains unchanged and must be superseded by a new immutable version. The initial startup test also encountered a feed response still advertising v3.2.5; the retry detected/downloaded/installed 3.2.6 and then correctly failed the blocked-header assertion.

The 3.2.7 change permits same-origin images in the application page only through applicationPageCSP. Remote images remain blocked. The separate report policy is unchanged. The editable SVG and generated PNG/ICO files remain identical to 3.2.6. Application, collector, script and Windows resource versions advance together.

scripts/test_icon_render.cjs uses the actual HTML, SVG and production policy. Its negative control reproduces the published data-only policy failure; the positive control decodes the header SVG; a routed remote-image probe confirms remote images are still blocked without contacting an external server. This regression runs before publication in both Windows workflows. Native endpoint tests verify the real application response policy. The published-upgrade timeout allows seven minutes for automatic startup/five-minute checks to observe GitHub feed propagation instead of assuming immediate CDN freshness. No manual check substitutes for automatic detection.

Previous upgrade input is now published v3.2.6, SHA-256 ddeeb2256e457b0a6f3d5c71cc442507f7bee282eeeba191c45601ad6e6cb221. Local real-browser policy regression and cross-build/vet pass. Windows source CI passed at commit 9f1f2daf11e8c392e3dae23a782bc32326ffb97c: https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/actions/runs/40180422255. The real-browser blocked-image negative control, corrected-header rendering, remote-image blocking, native icon resources/shell extraction, window persistence, updater/security checks and helper installation all passed. The corrected release is published and its full 35-check actual upgrade passed; the final evidence is recorded above and in release/3.2.7-verification.json.

## Desktop and Explorer icon refresh 3.2.8

The user requests the new flat icon for desktop shortcuts and Explorer and authorizes publication. The compiled 3.2.7 EXE already contains the correct art, but startup does not notify the shell or repair a shortcut that explicitly references the historical icon. Version 3.2.8 adds native targeted SHChangeNotify notifications and exact-target IShellLinkW/IPersistFile icon repair after the first authenticated UI paint. It uses the actual redirected user/public desktop locations and portable executable folder. Current-user desktop icons use an artwork-specific persistent LocalAppData Icons ICO; public/portable shortcuts use the compiled EXE icon so they remain readable across users. Link targets and other launch properties are preserved, and no new shortcut is created. Unrelated links, global icon caches and Explorer processes are unchanged. Per-session Runtime/application.ico remains disposable and is never used as the repaired desktop link icon.

The downloaded native helper also notifies the shell after install and rollback replacement. All active SVG/PNG/ICO bytes and original provenance hashes are unchanged. No UI, inventory/report behavior, settings, browser isolation, window persistence or updater security change is intended. Versions advance together to 3.2.8. Earlier published tags/assets remain unchanged.

Prepublication Windows source CI passed at commit 4fd4f30c76d8829c85fb41e38f1706549d4adf4c: https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/actions/runs/40217768305. All 28 native tests, four renderer shutdown regressions, three icon-policy regressions, vet, original provenance and real native helper install/visible health passed. Version 3.2.8 is published and its full 41-check previous-to-new upgrade passed; evidence is recorded above and in release/3.2.8-verification.json. The new native regression renders actual SHGetFileInfoW HICON pixels for the built EXE and real shortcut, reproduces the obsolete-icon reference, and checks preservation/idempotence/unrelated links/corruption recovery. The published-upgrade input is the immutable 3.2.7 EXE, SHA-256 0bb0f753a3a8dd58087c5e41cd39cf857c5d31f104aef55ab13a61e236c0ca8d. Its desktop fixture uses the real known desktop folder and is removed after validation. Full previous-to-new Download/Install/restart/health/settings/window/rollback checks remain required.

Prepublication Windows validation exposed 8.3-versus-long-path aliases in shortcut targets; matching now checks actual Windows file identity as well as case-insensitive paths. The native icon test initializes COM on a locked thread and compares every desktop pixel against the approved art with the identical native Windows shortcut arrow; it does not remove the standard arrow or ignore differing pixels. The negative control renders the exact historical icon plus that arrow. Evidence PNGs are retained by CI. No previously published release was modified during these fixes.

The actual published test started with an automatic response still advertising 3.2.7 at 17:50:47 UTC. Its real five-minute automatic retry detected 3.2.8 at 17:55:47 UTC; no manual check replaced automatic detection. All 41 checks completed successfully in the same full run, including the later real focus/scheduler waits.

## HTML report redesign 3.2.9

The user requests a cleaner, less overwhelming HTML report. The design opens on the executive summary, with server facts, collection counters and actionable collection failures/recovery gaps ahead of routine details. Navigation exposes subsections only for the selected section. Datasets use accessible disclosure panels and readable column labels; every original cell, column, row, raw multiline output, empty-state explanation and existing snapshot-limit note is retained. No data filtering or new row cap was introduced. Global search reveals matches in inactive/collapsed panels. Print-all expands every dataset for printing and restores screen expansion state afterward. Offline/no-script fallbacks retain the complete report.

The report is self-contained inside the original standalone collector. No normal runtime or external asset dependency was added. Only the HTML renderer is replaced; original collectors, report block construction, Word/text generation, raw exports, updater/security, icon, settings and native window behavior are preserved. The original report function and its bytes remain archived; the baseline hash is unchanged. The viewer recognizes the new responsive layout, opens collapsed search hits, and retains its existing compatibility path for previously saved reports. A new inventory/report is needed to generate the new design; existing exported HTML files are not rewritten.

Local PowerShell/browser checks passed using synthetic data, including 1500 routing rows, all original table-data parity, escaped HTML-like values, raw text, missing data, existing row-cap notices, native app HTML/CSP fixtures, offline/no-script/mobile/deep links/keyboard search, print-state restoration and old-report compatibility. The actual printed PDF contains FINALROW-1500 and LAST-RAW-LINE. Windows source CI passed at commit 071c28e94883235ca6174a87ce7f50e0bf856985: https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/actions/runs/40224067507. All 29 native Go tests, 18 report browser checks, five report/Word generation checks, four shutdown and three icon-policy regressions, vet, source/icon provenance and real helper install/visible health passed. The actual hosted Windows printed PDF retains FINALROW-1500 and LAST-RAW-LINE. Version 3.2.9 is published and the full 41-check real previous-to-new upgrade passed; final evidence is recorded above and in release/3.2.9-verification.json. The published upgrade input advances to immutable 3.2.8, SHA-256 376332d8aae05110c111aa97b9437f039b28c628eeb0a26e255fa25688fff187. That previous version already repairs desktop icons, so the regression now expects that correct behavior instead of requiring the old 3.2.7 stale-icon failure. The real native helper/update checks remain unchanged.
