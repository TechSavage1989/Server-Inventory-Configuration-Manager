# Executable updater validation

## 3.2.4 source validation passed, publication pending

Windows source CI passed at commit 36385ac478e1252844fd307e8663c304edc797c7: https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/actions/runs/40173525342. Native profile-scoped modal closure, preservation of another browser session, live listener ownership, all four renderer checks, existing updater/security tests and native helper healthy install passed.

The prior published test automatically accepted page dialogs and did not verify disappearance of the old native browser process. The v3.2.3 updater results below remain historical evidence, but do not establish popup-free account-isolated startup or complete old-window closure. New source and publication gates exercise both behaviors and record unexpected dialogs. Hosted tests do not reproduce the user's Microsoft account, browser policies or UAC desktop.

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
- No live inventory collection, report-generation run, or report-viewer workflow was exercised during this updater task. Those source paths were preserved from the confirmed baseline. Default-browser fallback modes, Windows Server 2012, other legacy versions, and full server-role coverage were not validated.
- The original v3.0.10 executable has no updater and requires one manual download into the executable release line.
- SHA-256 checks validate bytes against the HTTPS manifest; they do not establish publisher signing.
- Published tags/assets were preserved, and the workflow refuses overwrites. GitHub's separate repository-level immutable-release lock is not enabled; its API immutable flag is false.
