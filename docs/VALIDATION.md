## Unpublished report candidate — local validation, 2026-10-30

- `Test-Source.ps1`, renderer embedding check, baseline verifier, seven collector-provenance negative controls, layout and icon-source tests: passed.
- Actual synthetic PowerShell report generation: four checks passed (collector parse, self-contained HTML, unchanged shared blocks, identical baseline fixture dataset).
- `Test-ReportValues.ps1`: actual legacy CSV negative control reproduces `System.String[]`; new array values survive CSV → HTML/Word; scalar formatting/schema, source inputs and full raw CLIXML preserved; bounded collection omissions explicit.
- `Test-ReportPerformance.ps1`: 52 conversion comparisons and 17 HTML fallback cells byte-identical; Word document XML byte-identical, including raw, null, missing, hashtable, long, Unicode, cyclic and error cases.
- `test_report_copy_logic.cjs`: ten production-JavaScript logic tests passed for filtering, stable natural sorting/reset, complete TSV, Clipboard API/legacy/manual paths and stale async callbacks. These VM tests are not browser, layout or actual clipboard proof.
- 80,000-cell synthetic benchmark (four tables × 2,500 rows × eight columns): mean snapshot + HTML + Word time **66.104s → 21.135s**; HTML **23.854s → 1.292s**, Word **29.275s → 8.573s**. All 80,032 cells/headers matched; Word document XML was byte-identical. One discarded warmup and two measured iterations per implementation; Linux PowerShell 7.4.13 only. Full reproducible evidence, hashes and limitations: `docs/verification/report-performance.json`.
- Not measured: live server collection, output ZIP compression, remote/filesystem latency, Windows PowerShell 5.1, Windows-native UI or the user's actual machine. Timing improvement is not a user-host guarantee.
- Actual browser/visual/clipboard/print suites are **not run locally**: Chromium launch was blocked by sandbox socket restrictions; the single escalation failed environment setup. Cloud browser also rejected the local-file protocol. No denied path/access was bypassed and no visual pass is claimed. JavaScript syntax and workflow YAML parsing passed; new copy/filter/sort browser tests and value/performance parity gates are wired into both Windows workflows for authorized hosted execution.
- Source versions advance consistently to 3.2.12; release/VERSION remains 3.2.11 until source CI passes. The user authorized branch validation followed by merge/publication only after passing checks. Published assets remain unchanged. No live customer inventory or private screenshot is in the changes.

## 3.2.11 published and verified — 2026-10-30

- Release/tag: [v3.2.11](https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/releases/tag/v3.2.11), commit `56969491c44b97df730e55d502d5fa57e4542538`; PR #2 merged with publication authorization.
- Stable EXE: 6,627,328 bytes; SHA-256 `f981f2c557452c6d72469b4b16b88e8522a4e56ed6d11390c47c1fd8ede5a519`. Downloaded bytes identify a clean Go 1.20.14 Windows x64 GUI build at that exact commit.
- [Exact-release Windows CI](https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/actions/runs/41118047135) and [publication/full upgrade](https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/actions/runs/41118047397) passed.
- All 43 actual 3.2.10-to-3.2.11 upgrade checks passed in one run, with no reused timer evidence and zero unexpected dialogs: automatic detection, explicit Download/Install, cross-volume replacement, old-profile closure, guest/extension/sync flags, visible native health, settings, shortcut/icon, actual two-minute focus/five-minute scheduler waits, saved profile/window restoration, staging cleanup and failed-health rollback/relaunch.
- All 12 public assets were independently downloaded and verified against sizes/digests; all five checksum sidecars match. The latest feed returns 3.2.11 and a genuine conditional HTTP 304. All 12 previous 3.2.10 asset identities/sizes/digests and its release identity/commit remain unchanged.
- Structured evidence and exact upgrade checks: `release/3.2.11-verification.json`. The upgrade artifact digest is `b7767634362fd318cc3392b9c145cd8beac12d5cdebe9f85e610abad1bdffab1`.

Limits: synthetic data and hosted Windows/Edge testing; the separately confirmed affected Server 2019 startup probe establishes only the targeted startup fix, not completed live inventory. No live server-role collection, customer ACL export, human UAC/desktop interaction, Office visual validation, physical monitor/mixed-DPI, native folder-dialog automation or Chrome guest field testing is claimed. Focus uses a DOM event after a real elapsed wait. The ICACLS bound is not a timeout for all filesystem/SMB operations. SHA-256 provides byte validation, not publisher signing; repository immutable locking remains disabled and published assets must never be replaced.

## Historical 3.2.11 prepublication verification

Full hosted Windows source CI passed at f480504618b0f26f292e5452a28b84f9293391de (run 41115762285) and release-preparation commit 4f45056ea7a752c9df09a265df2dfdea9330531a (run 41116306822). This includes 14 synthetic startup checks, 18 bounded ACL worker checks, standalone probe validation, original provenance and negative controls, report/Word fixtures, clean source archive rebuild, native Go tests/vet, production browser regressions, and real native helper installation/visible health. Neither run collects server inventory.

On 2026-10-30 the user also ran the candidate synthetic probe on the actual affected Server 2019 / Windows PowerShell 5.1.17763.9121 / CLR 4.8.4811.0 host: finished within timeout, child exit 0, all candidate startup checkpoints passed, stderr empty. Payload SHA256 BFA795D71C8D661C004ADD91E3C6D9E37DACE9A40A2E6433047A2F3CB48079DC; getter SHA256 D83AE3F322BD07B722576C6CA0F5F6E4BE702B51E4700D5954E575FD53AFE28F. The finite published getter negative control reproduced identity loss; the candidate retained identity and passed the previously fatal cyclic table snapshot. This confirms the targeted startup fix on that runtime, not full live inventory completion.

The user explicitly authorized publication after validation. This prepublication note is historical; publication and the full previous-to-new upgrade subsequently passed as recorded above.

# Historical 3.2.11 candidate validation plan

This candidate covers the user-approved startup cyclic table snapshot fix and bounded, observable recursive ACL export. At this historical candidate stage validation was not complete. Required gates included original-source provenance and negative controls; Windows PowerShell 5.1 synthetic startup and ACL worker tests (including simultaneous stdout/stderr, timeout, skip, nonzero exits and unrelated-process preservation); production UI/persistence/authenticated skip lifecycle checks; Go build/tests/vet; real native application and archive rebuild checks. The affected Server 2019 / PowerShell 5.1.17763 / CLR 4.8.4811 runtime requires explicit field confirmation; hosted newer-runtime results are not a substitute.

Original 3.2.10 diagnostic evidence: hosted exact-startup/wrapper and standalone probe/harness validation passed at https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/actions/runs/41100393834. Both hosted runtimes were CLR 4.8.9363.0. The user's separate synthetic-only Server 2019 result terminated at the original cyclic Add-ReportTable startup checkpoint (line 3525) with StackOverflowException. These are diagnosis results, not candidate validation or a successful live inventory run.

No customer inventory, ACL contents, credentials, host paths or memory dumps belong in validation artifacts. The configured bound covers the owned ICACLS process and bounded child/output cleanup, not every filesystem operation or collector. Synchronous output-folder setup, mapping writes, file promotion, earlier SMB queries and remote filesystem I/O can still wait; the independent application host and whole-collection Cancel remain the escape path. Use synthetic fixtures only. release/VERSION remains 3.2.10 and candidate publication is not authorized by the build request.

---

# Collection workspace 3.2.10: published and verified

Local Go 1.20.14 Windows cross-build/test compilation/vet, original provenance, report embedding, deterministic icon generation, layout, four shutdown and three image-policy browser checks pass. Fourteen real-browser workspace checks exercise the actual HTML/CSP using synthetic state and retain the exact v3.2.9 negative controls. Hosted source CI passed at commit 37bc4ae75c3cd725dd9a16000ab3660bf3e279e9: https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/actions/runs/41013556970. All 32 native tests, 14 workspace checks, 18 report checks, five PowerShell HTML/Word checks, four shutdown and three icon-policy regressions, source archive rebuild and native helper visible health passed. The real executable saved/restored collection choices through native close/reopen. Windows report printing retains the last row and raw-output sentinels. The first source run failed only the historical hard-coded wrong-version fixture; corrected candidates are derived from the built version. The full published v3.2.9-to-v3.2.10 update passed all 43 checks; no timer evidence was reused and no unexpected dialogs occurred. No live server collection, human UAC, physical monitor or Office visual validation is claimed.

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

# Historical icon source update validation — 2026-10-27

- At this historical stage, v3.2.9 stayed current because the supplied artwork was visually identical. The source work is included in the later 3.2.10 release; v3.2.9 assets remain unchanged.
- The exact published EXE SHA-256 is `5483a308e0da37e5e9a518f12b3d344ecf12fb267c4419c50860149394fb53c3`. All ten native icon resource frames match the supplied-master-derived ICO byte for byte; see [structured evidence](verification/2026-10-27-icon-source.json).
- Original Illustrator master: 49,437 bytes, SHA-256 `4eb6eb78435ebaf64dbc5b71c836af8f1b6544235bba3adbfcd0a7e2a7c3a775`. Master bytes and original historical baseline hashes are preserved.
- Local passes: converter identity and hostile-input controls; all ten original CSS/runtime render comparisons; deterministic icon regeneration; original collector/report/icon provenance; report-embedding check; layout tests; compile-time resource generation; Go 1.20.14 Windows x64 cross-build and vet. Rendered 16/32/512px images reviewed.
- Historical icon-source hosted Windows/browser/visible-health CI passed: https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/actions/runs/40561712690. The earlier local sandbox limitation did not establish a Windows failure.
- Earlier release evidence below is historical and unchanged.

# Executable updater validation

## Historical release: v3.2.9

- Exact tag/commit: v3.2.9 / 96d30c340565e6ea6e2ea68b70ff87b4f4d1894f.
- EXE: 6568448 bytes; SHA-256 5483a308e0da37e5e9a518f12b3d344ecf12fb267c4419c50860149394fb53c3.
- Report-Preview.png: 1440x1000, synthetic data; SHA-256 8eab9e066dca7be579f7aab6bd86fa6cc9d6a2452cf885c432f406af8ab9d46d.
- Editable SVG unchanged: SHA-256 fc9211e88bb1f3d0bc34380c82bdd010456b57e59433650b85ae03cdaaf382cd.
- Source CI passed before publication at commit 071c28e94883235ca6174a87ce7f50e0bf856985: https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/actions/runs/40224067507.
- Exact-release CI passed: https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/actions/runs/40224435714.
- Publication/public assets passed: https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/actions/runs/40224435804.
- All 29 native Go, 18 report-browser, five HTML/Word generation, four UI-shutdown and three production image-policy checks passed. Native helper install/restart and visible healthy start passed.
- Report parity against the exact original renderer covers 21 datasets, 1545 retained rows, original column identities/cells and raw multiline text. The negative control has seven visible summary tables and 27 visible subsection links; the new presentation has two and eight respectively. All retained data remains available. Failure/gap priority, readable field labels, collapsed search, raw-output search, deep links, keyboard controls, mobile, empty data, no-script/offline behavior, escaping and print-state restoration pass. Both actual app HTML/CSP viewer variants work; the Go endpoint retains authentication and content isolation.
- The actual release Windows PDF independently contains the final row of the 1500-row table and the raw-output sentinel. Word package content retains both. No live server data is used.
- The public preview matches the validated renderer output. Public EXE/SVG/preview checksums, PE/version metadata, exact clean embedded Git revision and public feed HTTP 200/conditional 304 pass.
- Actual published v3.2.8-to-v3.2.9 upgrade: passed all 41 checks in one full run with real two-minute focus and five-minute timer waits and no reused timer evidence. Automatic detection, explicit download/validation/staging/cross-volume install/restart, visible native health, byte-identical settings, stable shortcut/icon, native window placement/relaunch, one rollback generation, cleanup and missing-health rollback/relaunch passed. Zero unexpected or legacy shutdown dialogs occurred. Structured evidence: release/3.2.9-verification.json.

Original collector/Word/text/wrapper/icon provenance is preserved outside the approved HTML renderer and version. New source/embedded hashes are in report-verification.json. New styling applies to newly generated reports; the legacy viewer continues to read existing HTML. No human desktop/UAC, native report-folder dialog automation, physical mixed-DPI/monitor removal, legacy Windows/PowerShell 3/4/Chrome guest, Office visual rendering or live inventory/role validation is claimed. Report browser integration uses real app HTML/production CSP with local fixture responses; the real endpoint is separately tested. Full structured evidence: release/3.2.9-verification.json.

## Previously published release: v3.2.8

- Exact tag/commit: v3.2.8 / 2294a921d4a85fbbb1ea5f6e551ad6861973acea.
- EXE: 6549504 bytes; SHA-256 376332d8aae05110c111aa97b9437f039b28c628eeb0a26e255fa25688fff187.
- Editable SVG unchanged: 1831 bytes; SHA-256 fc9211e88bb1f3d0bc34380c82bdd010456b57e59433650b85ae03cdaaf382cd.
- Source CI passed before publication at commit 4fd4f30c76d8829c85fb41e38f1706549d4adf4c: https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/actions/runs/40217768305.
- Exact-release CI passed: https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/actions/runs/40218088783.
- Publication/public assets passed: https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/actions/runs/40218088811.
- All 28 native Go tests passed, including shell-rendered EXE and desktop shortcut icons, exact old-icon negative control, native shortcut-arrow composition, file-identity alias matching, preserved target/arguments/directory/description/hotkey/window style, portable/shared icon targets, unrelated/malformed links, idempotence and persistent ICO lifetime/corruption recovery. Native evidence PNGs are retained in CI. The exact published shell-pixel probe is gated before publication.
- Four UI shutdown and three production image-policy regressions pass. Actual native helper install/visible health, updater/security tests, native window persistence, source vet, reproducible icon generation and original provenance pass.
- Independent public downloads/checksums, PE identity/version metadata, exact clean embedded revision and public feed HTTP 200/conditional 304 passed.
- Actual published v3.2.7-to-v3.2.8 upgrade: passed all 41 checks in the full run, with no reused timer evidence and zero unexpected or legacy shutdown dialogs. The actual desktop shortcut icon and installed EXE render the exact approved pixels, including the native shortcut arrow. Persistent artwork survives application/browser shutdown; target and launch properties are preserved, and unrelated links remain byte-identical. Real download/staging/cross-volume install/restart, visible native health, byte-identical settings, one rollback generation, stable shortcut/window placement, real two-minute focus and five-minute scheduler waits, cleanup and missing-health rollback/relaunch passed. Full evidence: release/3.2.8-verification.json.

The actual current-user desktop folder is used by the published-upgrade fixture; its owned test shortcuts are removed afterward. Hosted native shell/pixel validation does not establish human desktop-paint/UAC, physical mixed-DPI/monitor removal, legacy Windows/Chrome guest, user account/policy or live inventory/report behavior. Network/security errors use deterministic native responses. SHA-256 validates bytes, not publisher signing. Full structured evidence: release/3.2.8-verification.json.

## Previously published release: v3.2.7

- Exact tag/commit: v3.2.7 / aabec80e9e8fa95aba8297ca0e6a2941479db8df.
- EXE: 6531072 bytes; SHA-256 0bb0f753a3a8dd58087c5e41cd39cf857c5d31f104aef55ab13a61e236c0ca8d.
- Editable SVG: 1831 bytes; SHA-256 fc9211e88bb1f3d0bc34380c82bdd010456b57e59433650b85ae03cdaaf382cd.
- Source CI passed before publication: https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/actions/runs/40180422255, commit 9f1f2daf11e8c392e3dae23a782bc32326ffb97c.
- Exact-release CI passed: https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/actions/runs/40180646660.
- Publication/public EXE/SVG verification passed: https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/actions/runs/40180646656.
- Actual published v3.2.6-to-v3.2.7 upgrade: passed all 35 checks in the full run with real two-minute focus and five-minute scheduler waits. The loaded SVG header, exact SVG/PNG/favicon bytes, old-profile shutdown, guest isolation, cross-volume install/restart, visible native health, byte-identical settings, stable shortcut, window placement, cleanup, one rollback generation and missing-health rollback/relaunch passed. No timer evidence was reused; zero unexpected or legacy shutdown dialogs occurred. Full evidence: release/3.2.7-verification.json.
- All 27 native Go tests passed. Compiled icon frames match the SVG-derived ICO, and Windows extracts valid large/small shell icons. The actual application window close/reopen/maximize/minimized-close/settings tests and updater/security cases pass.
- Four UI shutdown regressions and three icon policy regressions pass. The old 3.2.6 policy blocks the header image in the negative control; the corrected production policy displays the actual SVG and keeps remote images blocked. The separate report policy remains unchanged.
- Original collector/wrapper and archived icons match unchanged baseline hashes. Current SVG/PNG/ICO hashes are checked separately. Re-rendering from the editable SVG is byte-reproducible on Linux and Windows.
- Independent EXE/SVG/checksum downloads, PE/version metadata, clean embedded Git revision and actual HTTP 304 passed.

No human desktop icon-cache/UAC test, physical mixed-DPI/monitor-removal test, legacy Windows/Chrome guest validation, user account/policy reproduction or live inventory/report collection was performed. Full structured evidence: release/3.2.7-verification.json.

## Historical 3.2.6 source validation

The new editable SVG is the master for the header, PNG/favicon and compile-time Windows ICO. CI compares regenerated assets, archived original provenance, every compiled icon frame and native shell extraction. The published v3.2.5-to-new upgrade also checks icon endpoint bytes and the loaded header image. Source CI passed at commit b50541f76bb10d89aefc39628ea4c3a8b1a23493: https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/actions/runs/40179122995. All icon/resource/extraction, window, updater/security, renderer and native helper tests passed. Version 3.2.6 was published, but the live upgrade test correctly failed its blocked-header assertion. It is superseded by 3.2.7; its original tag/assets remain unchanged. Physical desktop icon caching, UAC, mixed-DPI and live inventory/report workflows are not claimed as tested.

## Historical release: v3.2.5

- Exact tag/commit: v3.2.5 / b02163944ff8189cd627a2f7af8963238d797f71.
- EXE: 6536192 bytes; SHA-256 bbd06ab0f9dadad77c0e3ed4bacf30f1482dc8ae4766426a5f68f40ab84d34b5.
- Prepublication source CI passed: https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/actions/runs/40175008751, commit de7f4d73ff84f93d4cb72b164704bedb0f8ab18b.
- Exact-release Windows CI passed: https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/actions/runs/40175227948.
- Publication/public feed passed: https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/actions/runs/40175227858.
- All 24 native tests and four renderer checks passed, plus the actual native helper installation/health and locked-target/tampered-byte controls.
- The native test launches the actual built application and visible guest window. It moves/resizes the window, closes it through the native title bar, reopens it and compares exact normal bounds. It verifies maximized state, closing while minimized, maximized reopening, normal restore bounds, off-screen saved-position recovery, unchanged inventory settings and temporary-file cleanup.
- Invalid schema, malformed/unsafe bounds, negative monitor coordinates and work-area recovery cases passed.
- Real published v3.2.4-to-v3.2.5 upgrade: corrected published validation passed, with 30 directly exercised checks and the two successful real timer waits retained from the earlier run against identical SHA-256 bytes. Actual native move/resize, saved state, shortcut relaunch at exact bounds, settings, old-window closure, native health, C: staging to D: target, cleanup and rollback passed with zero unexpected dialogs. Corrected run: https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/actions/runs/40176269574. Evidence: release/3.2.5-verification.json.
- Independent public download/checksum/manifest, PE identity/version, clean embedded Git revision and actual HTTP 304 passed.

Physical monitor removal, mixed-DPI hardware and human desktop/UAC interaction were not tested. Hosted automation uses Windows Server 2025 and Edge. The user's Microsoft account, browser policies, Chrome guest fallback, legacy Windows and live inventory/report workflows are not reproduced. Full evidence is recorded in release/3.2.5-verification.json.

## Historical release: v3.2.4

- Exact tag/commit: v3.2.4 / 437a809a00b8112132b9e4e54566e3cb2be33ab5.
- EXE size: 6520320 bytes.
- SHA-256: 6319f6ad5c2b15882110b4a34ba63f99812be879166de3a754b70e794c1c4ab2.
- Prepublication Windows source CI passed: https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/actions/runs/40173525342, commit 36385ac478e1252844fd307e8663c304edc797c7.
- Exact-release Windows CI passed: https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/actions/runs/40173688357.
- Publication/public-feed checks passed: https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/actions/runs/40173688380.
- Real published v3.2.3-to-v3.2.4 upgrade final report: all 29 checks passed, zero unexpected or legacy shutdown dialogs; native old-profile exit, actual guest flags, settings, shortcuts, real two/five-minute waits, C: staging to D: target, cleanup, healthy start and forced rollback/relaunch verified. Structured evidence: release/3.2.4-verification.json.
- All 22 top-level native Go tests passed, including actual modal browser closure with no launcher PID, preservation of a different browser profile, live legacy TCP-owner matching, updater scheduling/security, staging and rollback validation.
- Four renderer checks passed: the real v3.2.3 HTML reproduces four status-0 modal alerts; the fixed page has zero unexpected dialogs and no post-install requests; manual action failures appear inline; installation rejection restores controls and polling.
- The native helper install test confirms a rendered interface and visible owned window. Locked-target cleanup and tampered-byte controls also pass.
- Public asset/checksum/manifest, PE identity/version, clean embedded Git revision, browser isolation flags and real HTTP 304 checks passed independently.

The prior published test automatically accepted page dialogs and did not assert old native process disappearance. Its v3.2.3 results below remain historical evidence, but do not establish popup-free account-isolated startup or complete old-window closure. The new gates record unexpected dialogs and assert real browser ownership and exit. Hosted CI cannot reproduce the user's Microsoft account, synced extensions, organization policies or physical UAC desktop. No live inventory/report workflow or Chrome guest fallback was tested.

## Historical release: v3.2.3

- Source commit: 6ee8048539ae429d05f7938e06c2baabdd34fb92.
- Small visible change: Portable EXE header badge. Inventory/report/updater logic and icon bytes are unchanged.
- EXE size: 6491648 bytes.
- SHA-256: 8cd753daefddc1072ee2cc69a48c0269c4a9ea5b646f7d21215c29a5575e29b5.
- Exact-release source CI passed: https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/actions/runs/40171462464.
- Publication/public-feed/real v3.2.2-to-v3.2.3 upgrade passed: https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/actions/runs/40171462463.
- All 24 actual published-upgrade checks passed, including settings, shortcut relaunch, healthy start, two/five-minute waits, cross-volume staging, cleanup, and rollback.
- Independent public download/hash, embedded badge, Windows metadata, Git revision, and HTTP 304 checks passed.
- Structured evidence: release/3.2.3-verification.json. The explicit testing limits below continue to apply. No live inventory or report workflow was exercised.

## Historical v3.2.2 validation

Published executable: v3.2.2.

- Exact release commit: ab7fbb783ce4032f951bb717d745f4471ae4d889.
- EXE: Server-Inventory-Configuration-Manager.exe, 6491648 bytes.
- SHA-256: 68a816ae191fab47526893bca06deb0466a9193a543f2b2410955bd7f6e8d9cc.
- Exact-release Windows CI passed: https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/actions/runs/40169800117.
- Publication, public feed verification, and real v3.2.1-to-v3.2.2 upgrade passed: https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/actions/runs/40169800158.
- The real v3.2.0-to-v3.2.1 upgrade also passed: https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/actions/runs/40169175169.

Both published upgrade runs passed all 24 checks. Structured final evidence is in release/3.2.2-verification.json. JSON reports, logs, and screenshots are available as workflow artifacts. Only test data was used; customer inventory was not uploaded.

## Original executable source

The uploaded v3.0.10 EXE and the original source ZIP's bundled EXE are byte-identical. No decompilation was used. Original collector, wrapper, and icon hashes pass validation, normalizing only the collector version. The UI and report viewer were preserved apart from updater integration and the requested application name.

## Windows validation coverage

The tests build and execute the actual Windows GUI EXE on Windows Server 2025 with Edge. Playwright drives the application's real HTML page while its native Edge app window is visible. It does not simulate a substitute updater.

| Requirement | Validation | Result |
| --- | --- | --- |
| Window placement persistence | Real EXE native close/reopen, exact bounds/maximized state, minimized close and off-screen saved position | Passed in native Windows tests |
| Guest browser isolation | Actual new native process has guest, disabled extensions and disabled sync flags; no account/policy simulation | Passed on hosted Edge |
| Old application browser closure | Real published upgrade verifies old profile processes exit; native modal test preserves another profile without launcher PID | Passed |
| Update shutdown UI | Real previous HTML reproduces status-0 alerts; fixed renderer cancels requests and records zero unexpected dialogs | Passed |
| Normal startup and automatic update detection | Published previous EXE launched through a shortcut and detected the public release | Passed |
| Startup after first paint | Authenticated UI-ready event starts the native scheduler; pre-render focus requests are blocked | Passed |
| Five-minute checks | Real elapsed wait and changed check timestamp from the native scheduler | Passed |
| Focus-return checks | Real two-minute wait followed by the bound DOM focus event | Passed, physical focus untested |
| Manual check and cooldown | Clicked the actual button and verified repeated requests were suppressed | Passed |
| Offline and HTTP errors | Native HTTP transport returned connection and server failures; status never became up to date | Passed, simulated network conditions |
| Rate limits and backoff | Retry-After, GitHub reset headers, exponential backoff, manual/focus suppression | Passed, deterministic responses |
| ETag and cached state | Native 304/cached-manifest/error-ETag cases plus an independent real public HTTP 304 | Passed |
| Download and progress | Actual Download action, staged file, visible progress, exact public bytes | Passed |
| SHA-256 and malformed-file rejection | Actual EXE fixtures, tampered executable, wrong hash/version/application, truncated PE, DLL and overlay checks | Passed |
| Staging and replacement | Same-volume native helper test and actual C: staging to D: executable installation | Passed |
| Install and restart | Explicit application action invoked the new EXE's native helper and relaunched the new version | Passed |
| Settings preservation | Settings file remained byte-identical through upgrade and rollback | Passed |
| Stable shortcuts | Target remained unchanged; the existing shortcut launched the updated EXE | Passed |
| Rollback | Withheld expected health confirmation; native helper restored previous bytes and relaunched the previous app | Passed |
| Cleanup | Download/ready cleanup and a real locked-target failure regression control | Passed |
| Healthy start | Matching token, version, PID and path; rendered UI and visible owned window; child remained alive | Passed |
| Critical-operation interlocks | Collection/report operations block install, and installation blocks new operations | Passed in native tests |

The updater never automatically downloaded, installed, or restarted during these tests. The forced failed-health rollback is distinct from the successful new-version healthy-start test.

## Negative controls

- The old JSON parser rejects the v3.2.0 BOM manifest; the corrected decoder accepts it. New manifests omit the BOM for older executable compatibility.
- The prior substring validation accepts a version prefix and an appended identity overlay; the corrected PE/metadata validator rejects them.
- A tampered executable that can start in helper mode is rejected against its previous trusted SHA-256.
- The actual published v3.2.1 helper, checksum-pinned in the Windows test, leaves one temporary update EXE with an exclusive lock on the installed target. The v3.2.2 helper refuses the same operation, leaves zero temporary files, and preserves the installed target bytes. This comparison runs the actual application helpers.
- Tampered rollback bytes are rejected without changing the installed target.

## Explicit testing limits

- The local Linux environment cannot interactively exercise Windows GUI/UAC behavior. Hosted Windows automation supplies the results above.
- No human-operated desktop test or UAC consent transition was performed.
- Focus testing uses a synthetic DOM event after a real two-minute threshold. Physical mouse/keyboard focus return remains untested.
- Offline, HTTP failures, rate limits, malicious downloads, and cached-response cases use deterministic native test transport. The host was not intentionally disconnected and the public GitHub quota was not exhausted. Public download/checksum and HTTP 304 checks were also performed independently.
- No live inventory collection, report-generation run, or report-viewer workflow was exercised during this updater task. Those source paths were preserved from the confirmed baseline. The personal-browser fallback was removed in v3.2.4. Chrome guest fallback, Windows Server 2012, other legacy versions, and full server-role coverage were not validated.
- The original v3.0.10 executable has no updater and requires one manual download into the executable release line.
- SHA-256 checks validate bytes against the HTTPS manifest; they do not establish publisher signing.
- Published tags/assets were preserved, and the workflow refuses overwrites. GitHub's separate repository-level immutable-release lock is not enabled; its API immutable flag is false.

## Published-window validation harness correction

The original publication job successfully built/published/verified 3.2.5. Its upgrade job passed the real v3.2.4 update and both actual two/five-minute waits, then failed the new resize test setup. PowerShell field writes to a nested RECT value did not update the parent placement structure. The old operation is retained as a negative control: LegacyNestedWriteValue is 0 after an attempted Left=87 write. The corrected test assigns a complete RECT, finds the exact visible owned native window through EnumWindows, and independently verifies the applied bounds before checking persistence. Request, actual native placement and saved JSON all match Left=87, Top=63, Right=987, Bottom=663.

Corrected validation: https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/actions/runs/40176269574, harness commit d36360c94211fb4ad65a0c88c624c6c603e307b7. It reruns the actual published download/install/restart, window save/relaunch, settings, shortcuts, health, cleanup and forced rollback. Only the two already passed real timer waits are reused, guarded by the exact unchanged published SHA-256. The failed first report is preserved in release/3.2.5-first-upgrade-evidence.json; do not describe its entire workflow as successful. No release tag, EXE or asset was changed for the test repair. The standard release workflow still runs all real waits by default. The separate validation workflow can be dispatched with no evidence input to run all waits again.
