<p align="center"><img src="assets/icons/server-inventory.svg" width="112" alt="Server Inventory Configuration Manager icon"></p>

<h1 align="center">Server Inventory Configuration Manager</h1>

<p align="center"><strong>Capture server configuration. Find the important details. Keep a useful handoff.</strong></p>

A portable **Windows x64 application** for collecting server inventory, exporting supported configurations, and reviewing clear, searchable reports.

<p align="center"><a href="https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/releases/latest/download/Server-Inventory-Configuration-Manager.exe">Download Windows EXE</a> · <a href="https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/releases">Releases & checksums</a> · <a href="#getting-started">Get started</a> · <a href="#features">Features</a> · <a href="#updates-and-settings">Updates</a> · <a href="#development">Development</a></p>

## Features

| Capture | Review | Keep working |
| --- | --- | --- |
| Choose inventory categories and optional configuration exports. | Start with an executive summary and visible collection issues. | Run a portable EXE with an isolated local browser interface. |
| Use presets, then tailor the scope to your server. | Search collapsed report sections and inspect supporting exports. | Keep settings, window placement, and shortcut targets through updates. |
| Collect available server-role details without treating missing roles as present. | Open HTML offline, print retained report data, or use Word/text outputs. | Choose when to download, install, and restart. |

## Download

Get the maintained **Windows EXE** from [the current release](https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/releases/latest), with its checksum and release notes. The historical PS1 application is not the current product.

## Requirements

- **Windows x64**, with Windows Server 2012+ as the compatibility target. Older Windows/PowerShell and individual server roles still require field validation; see [VALIDATION.md](VALIDATION.md).
- **Built-in Windows PowerShell** for inventory collection and the permissions required to query/export the selected scope. Run with administrator rights for administrator-only data.
- **Microsoft Edge or Chrome** with guest browsing permitted by your organization. The app does not bypass browser policies.
- A writable application location and output folder. Go, Python, and Node.js are **build tools**, not normal application requirements.

## Getting started

1. Download **`Server-Inventory-Configuration-Manager.exe`** from the current [release](https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/releases/latest). Use its checksum file if you want to verify downloaded bytes.
2. Keep the EXE in a permanent writable folder, then launch it with the permissions appropriate to your authorized server work.
3. In **Setup**, choose the **Output parent directory**. Pick **Inventory only**, **Core server**, or **Maximum capture**, then review the individual categories and optional exports.
4. Start collection and follow progress and logs in **Collection**.
5. Open **Report Viewer**, inspect collection failures/recovery gaps first, then search the details and supporting files in your output folder.

> **Protect your exports.** Inventory and configuration files can contain sensitive infrastructure information. Review the scope and sensitive-data options before collecting; store and share the output securely. Configuration exports and recovery references are not a substitute for a tested backup, and no restore runs automatically.

**Coming from an older version?** The original v3.0.10 EXE and historical v3.1.0 PS1 application need one manual download of the maintained EXE. The PS1 application is historical—not the current product or build baseline. Existing executable updates preserve the installed path, including custom filenames.

## How to use

New HTML reports lead with a concise executive summary. Collection issues and recovery gaps remain prominent; routine datasets expand on demand. Search includes collapsed sections and raw text. Printing includes all rows retained by the report snapshot; truncation notices still point to complete supporting exports.

HTML reports work offline without external fonts, libraries, or image services. Existing reports remain readable in the viewer; **generate a new report to get the redesigned layout**. Word, text, and raw outputs remain available.

## Updates and settings

Use **Check for updates → Download → Install and restart**. Checks also run after the interface renders, every five active minutes, and after focus return when at least two minutes have elapsed. Download, installation, and restart always require explicit actions. Installation is blocked during collection and report/folder operations.

The updater verifies HTTPS hosts, repository/version paths, size, SHA-256, PE structure, and Windows application/version metadata. **A matching hash is integrity validation, not publisher signing.** A native helper replaces the EXE and restores the previous one if the expected rendered, visible startup is not confirmed.

Settings, logs, cache, staging, rollback, and browser-session files live under:

```text
%LOCALAPPDATA%\Server Inventory Configuration Manager\
```

Reports stay in the selected output folder. Updates preserve your executable path and shortcut targets.

### Desktop behavior

- **Isolated browser session:** Edge/Chrome runs in an app-owned guest profile with extensions and account sync disabled. The app never falls back to your personal profile or changes browser settings/policies. If guest mode is blocked, it reports the limitation.
- **Remembered window:** the last usable position, size, and maximized state are saved separately in `Settings/window.json`. Minimized windows reopen normally or maximized; off-screen positions move into an available work area.
- **Consistent icon:** the editable SVG is compiled into the EXE. Targeted shell notifications and exact-target shortcut icon repairs preserve launch properties, leave unrelated shortcuts alone, and do not restart Explorer.

## Development

Read [AGENTS.md](AGENTS.md) and [DEVELOPMENT_HANDOFF.md](DEVELOPMENT_HANDOFF.md) first. [VALIDATION.md](VALIDATION.md) records provenance, hosted test evidence, and field-testing limits. The maintained application preserves the original executable's collectors and behavior; `legacy` is historical material, not the build source.

<details>
<summary><strong>Build, icon editing, and report-renderer notes</strong></summary>

### Build

Requires Go 1.20.14, Python 3, Node.js 22 for build-only icon rendering, and Windows PowerShell for validation. The updater is native Go/Win32. Inventory collection uses built-in Windows PowerShell as in the original executable.

1. Run npm ci --prefix scripts/icon-tools and node scripts/build_icons.cjs --check, then python scripts/verify_baseline.py.
2. Run python scripts/embed_report_renderer.py --check and scripts/Test-Report.ps1. Generate fixture reports before Go tests; run scripts/test_report.cjs with Playwright/Edge before packaging.
3. Run `python scripts/build_resources.py --version <source-version>` using the version in `main.go`.
4. Run scripts/Test-Source.ps1.
5. Run go build -trimpath -ldflags "-s -w -H=windowsgui" -o build/Server-Inventory-Configuration-Manager.exe .
6. Run go test -v ./... and go vet ./....
7. Run scripts/Test-Binary.ps1 and scripts/Test-UpdateInstall.ps1 with the EXE path and expected version.

The original collector is committed directly. Original icon bytes are retained under baseline/v3.0.10/icons. The new SVG-derived icon and Windows version metadata are linked during compilation, without patching the built EXE.

### Editable application icon

Edit assets/icons/server-inventory.svg in Inkscape, Illustrator or another SVG editor. The rack, drawers, indicators and checkmark are named vector shapes in separate groups, using four solid colors and no embedded bitmap. Run node scripts/build_icons.cjs after editing to regenerate the 256px PNG, ten Windows ICO sizes and icon-verification.json. The renderer is a build tool only; the portable application and updater do not require Node.js.

The editable SVG and its checksum are also included in each executable release. The same ICO is compiled into the EXE for Explorer. After the interface starts, native shell notifications refresh that executable and existing shortcuts targeting it. Current-user desktop shortcuts use a persistent artwork-specific ICO beneath LocalAppData/Server Inventory Configuration Manager/Icons; public-desktop and portable-folder shortcuts use the embedded EXE icon so they remain readable by other users. Shortcut targets and launch properties are preserved. Unrelated shortcuts are not changed, and Explorer is not restarted. Redirected desktop folders are discovered through Windows known folders.


### Report renderer

Edit the presentation sources under reporting, then run python scripts/embed_report_renderer.py. This embeds CSS/JS into the original self-contained collector. report-verification.json tracks approved presentation hashes; the original renderer and whole-script baseline hash remain unchanged. The Word report, text summary, collectors and raw outputs are preserved. The app viewer supports both report generations. Generate a new report to use the new layout.

</details>
