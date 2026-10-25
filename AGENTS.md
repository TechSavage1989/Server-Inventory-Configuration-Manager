# AGENTS.md

Read this file and DEVELOPMENT_HANDOFF.md before making changes.

## Primary application baseline

- Maintain the Windows portable executable, Server-Inventory-Configuration-Manager.exe.
- The user-confirmed functional and visual baseline is Server Inventory & Configuration Exporter GUI v3.0.10.
- The original source ZIP's bundled EXE is byte-identical to the uploaded baseline. See baseline-verification.json.
- The v3.1.0 PS1 application was a mistaken product direction. It is historical only. Never use its UI, collector extraction, or build workflow as the executable baseline.
- v3.2.0 introduced executable updates. v3.2.1 restored direct original-source build inputs and hardened the updater. Maintain v3.2.5 or newer, including locked-target cleanup, guest browser isolation and profile-scoped shutdown.
- Public source and releases: https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager
- Public executable feed: https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/releases/latest/download/update-manifest.json
- Target Windows x64; Go 1.20.14 retains the broad Windows Server 2012+ target. Older Windows versions still require field validation.

## Development rules

1. Preserve the original layout, collectors, reporting, report viewer, scope controls, and icon unless the user requests a change. Run scripts/verify_baseline.py. Keep the original hashes in baseline-verification.json unchanged. For user-approved collector or icon changes, record the compared revision separately and update the verifier/tests without relabeling changed bytes as the original baseline. Original icon bytes now live in baseline/v3.0.10/icons. The user-approved editable icon is assets/icons/server-inventory.svg; active hashes are in icon-verification.json.
2. Update appVersion, collectorVersion, and the original standalone collector's scriptVersion consistently. Source files are committed directly; do not extract them from legacy PS1 applications.
3. Link the approved SVG-derived icon and Windows file/product version metadata during compilation with scripts/build_resources.py. Do not patch a compiled EXE.
4. Keep the portable filename stable. Install in place to preserve existing shortcuts, including legacy custom/versioned names. Do not rename a user's executable or shortcut silently.
5. Settings, updater logs/cache/downloads/health/rollback, and browser session files belong under per-user LocalAppData/Server Inventory Configuration Manager. The one transient replacement file on the executable's volume is removed after replacement or failure.
6. Startup checks follow the authenticated first UI paint. Native timers schedule checks every five minutes; focus checks require two minutes. Keep single-flight, ETags, cooldown, Retry-After, GitHub reset handling, and persistent backoff. Cached/offline/failed checks never mean up to date.
7. Never automatically download, install, or restart. Block installation during collection, report loading, or folder dialogs. Prevent new critical operations once installation starts.
8. Validate HTTPS/redirect hosts, exact repository and version paths, size, SHA-256, PE sections/entrypoint/architecture/subsystem, mapped application identity marker, and Windows version metadata before execution. The manifest hash is byte validation, not a publisher signature.
9. The downloaded application is its own native helper. Stage on the target volume, retain exactly one previous rollback EXE, and require token/PID/path-matched health with a rendered UI and visible owned window. Never ship a headless healthy-start bypass.
10. Launch the interface only in an app-owned guest browser profile with extensions and sync disabled. Never fall back to a personal profile or change user/system browser policies. Respect policies that block guest mode or require account sign-in. Discover and close the exact app-owned profile even when Edge hands off to another PID; never kill browsers by name. Quiesce UI requests before install/shutdown and show update-action errors inline.
11. Persist native window bounds/maximized state in Settings/window.json, separately from inventory settings and disposable guest profiles. Use GetWindowPlacement/SetWindowPlacement consistently, preserve the last non-minimized state, constrain restored bounds to available monitor work areas, and flush before closing the browser or starting the update helper. Keep per-thread DPI handling isolated from existing dialogs. The real-EXE window test must cover native close/reopen, maximize, minimized close, normal restore bounds, invalid state, off-screen recovery and unchanged inventory settings.
12. Keep credentials, tokens, private signing keys, and customer data out of source and artifacts. Do not add server setup, Codex CLI automation, unattended workers, payment setup, or API-account setup.

## Build and validate

Use Go 1.20.14, Python 3, and Windows PowerShell for build validation. The updater itself has no PowerShell/runtime dependency. The original inventory engine still uses built-in Windows PowerShell with the baseline process-scoped collector invocation.

1. Run npm ci --prefix scripts/icon-tools and node scripts/build_icons.cjs --check. After editing the SVG, run node scripts/build_icons.cjs to regenerate PNG/ICO/hash metadata, then review. Do not embed a raster image inside the SVG. The renderer is build-time only.
2. Run python scripts/verify_baseline.py.
3. Run python scripts/build_resources.py --version <source-version>.
4. Run scripts/Test-Source.ps1.
5. Build the stable GUI EXE with go build -trimpath and Windows GUI subsystem flags.
6. Run scripts/Test-Binary.ps1, go test -v ./..., and go vet ./.... Build the EXE before Go tests; malicious-download tests use those actual bytes.
7. Run scripts/Test-UpdateInstall.ps1. It launches the real GUI and requires real health confirmation.
8. Run scripts/test_ui_shutdown.cjs with Playwright and Edge. Its actual v3.2.3 HTML fixture must reproduce the prior status-0 popup, while the fixed page must have zero unexpected dialogs and no requests after accepted shutdown. Native Go tests must close a real modal app window without the launcher PID and preserve a separate profile. The published-upgrade test must assert the old profile processes are gone, inspect the actual new browser guest/extension/sync flags, and never blanket-accept dialogs.
9. Run scripts/Build-ReleaseManifest.ps1. Write UTF-8 JSON without a BOM so older executable updaters can parse it.

## Release and handoff

- Validate source CI first, then change release/VERSION in a separate publication commit.
- Use a new tag/version only. Never replace assets, releases, or tags that have already been published.
- Ship the editable SVG and its SHA-256 alongside the EXE. Native tests must match every compiled icon frame against the SVG-derived ICO and exercise Windows shell extraction. The published test verifies the SVG/PNG/favicon responses and loaded header image.
- The workflow uploads all assets to a draft, publishes it, and verifies public downloads/feed.
- Its separate Windows job runs the published previous executable (currently v3.2.5) through Download and Install and restart into the new release, then verifies native health, settings, shortcuts, scheduling, cleanup, and rollback.
- Record exact tag/commit, asset hash, CI links/results, and testing limitations in DEVELOPMENT_HANDOFF.md and VALIDATION.md.
- Do not call headless probes, helper-only tests, or cross-compilation a successful interactive Windows upgrade.
