# Executable updater validation

Prepared release: v3.2.1, based on the original v3.0.10 executable source.

## Local validation

- The uploaded v3.0.10 EXE and the original source ZIP's EXE are byte-identical.
- Original collector, wrapper, and icon bytes pass provenance validation; only the collector version is normalized.
- Go 1.20.14 Windows cross-build and Windows-targeted go vet pass.
- Built EXE passes PE structure, mapped exact application/version identity, and Windows resource metadata validation.
- JavaScript test script syntax passes.

## Automated Windows coverage

The CI workflow builds the real GUI EXE before tests. Tests exercise the real updater HTTP/state/download/staging code with deterministic network responses, including:

- BOM manifest negative control reproducing the v3.2.0 parsing failure
- Cached same-version state and offline connection failure
- HTTP errors, 304 ETag reuse, and failed-response ETag poisoning
- Retry-After, GitHub shared-IP rate reset, persistent exponential backoff, manual cooldown and single-flight
- Native startup/scheduler/focus bindings and production time thresholds
- Actual EXE bytes downloaded into staging, progress, SHA-256, wrong hash/version/application, malformed PE, and DLL rejection
- Collection/report-operation install interlocks, settings serialization, and cleanup ownership

The native helper script launches the real app and requires a visible owned browser window plus rendered UI. It also rejects a tampered executable with a previously valid expected hash. No headless-health override is used.

## Published upgrade coverage

The post-publication Windows job starts the exact published v3.2.0 executable through a shortcut and drives the real application HTML interface with Playwright while the native Edge app window is visible. It checks the public feed, clicks Download, clicks Install and restart, waits for the new visible healthy app, verifies settings and previous-version bytes, verifies and relaunches the same shortcut, waits through actual two- and five-minute scheduling thresholds, and forces a missing-health rollback with real native restoration/relaunch. When staging and target drives differ, this also exercises cross-volume staging.

The JSON report, logs, and screenshots are CI artifacts. Only test data is used; customer inventory is not uploaded.

## Limits

- The local Linux session cannot run Windows GUI/UAC interactions.
- Hosted automation is not a human-operated desktop test. UAC consent transitions remain untested.
- A synthetic DOM focus event exercises the bound focus handler after the real two-minute threshold; physical mouse/keyboard focus return is untested.
- Offline/HTTP/rate-limit/ETag/malicious-file cases use deterministic native test transport, not an intentionally disconnected Windows host or exhausted public GitHub quota.
- The v3.0.10 baseline has no updater; its first move to this line is a manual download.
- Full inventory collection against every Windows Server version/role and report format is not covered by this updater test.
- Windows Server 2012 compatibility remains a build target requiring field validation.

Windows CI and published upgrade results: pending. Update this file with exact workflow links and outcomes before handoff.
