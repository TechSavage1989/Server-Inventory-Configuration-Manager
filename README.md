# Server Inventory Configuration Manager

Portable Windows server inventory and configuration capture application. The maintained product is the executable application, preserving the original v3.0.10 interface, collectors, report generation, viewer, configuration controls, and user-approved flat icon.

Download Server-Inventory-Configuration-Manager.exe from [GitHub Releases](https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/releases/latest). The original v3.0.10 executable and historical v3.1.0 PS1 application require one manual download to enter the executable update line. The PS1 release is retained unchanged.

## Build

Requires Go 1.20.14, Python 3, Node.js 22 for build-only icon rendering, and Windows PowerShell for validation. The updater is native Go/Win32. Inventory collection uses built-in Windows PowerShell as in the original executable.

1. Run npm ci --prefix scripts/icon-tools and node scripts/build_icons.cjs --check, then python scripts/verify_baseline.py.
2. Run python scripts/build_resources.py --version 3.2.8.
3. Run scripts/Test-Source.ps1.
4. Run go build -trimpath -ldflags "-s -w -H=windowsgui" -o build/Server-Inventory-Configuration-Manager.exe .
5. Run go test -v ./... and go vet ./....
6. Run scripts/Test-Binary.ps1 and scripts/Test-UpdateInstall.ps1 with the EXE path and expected version.

The original collector is committed directly. Original icon bytes are retained under baseline/v3.0.10/icons. The new SVG-derived icon and Windows version metadata are linked during compilation, without patching the built EXE.

## Editable application icon

Edit assets/icons/server-inventory.svg in Inkscape, Illustrator or another SVG editor. The rack, drawers, indicators and checkmark are named vector shapes in separate groups, using four solid colors and no embedded bitmap. Run node scripts/build_icons.cjs after editing to regenerate the 256px PNG, ten Windows ICO sizes and icon-verification.json. The renderer is a build tool only; the portable application and updater do not require Node.js.

The editable SVG and its checksum are also included in each executable release. The same ICO is compiled into the EXE for Explorer. After the interface starts, native shell notifications refresh that executable and existing shortcuts targeting it. Current-user desktop shortcuts use a persistent artwork-specific ICO beneath LocalAppData/Server Inventory Configuration Manager/Icons; public-desktop and portable-folder shortcuts use the embedded EXE icon so they remain readable by other users. Shortcut targets and launch properties are preserved. Unrelated shortcuts are not changed, and Explorer is not restarted. Redirected desktop folders are discovered through Windows known folders.

## Browser isolation

The interface opens in a dedicated Edge or Chrome guest app session with extensions and account sync disabled. It never falls back to your personal browser profile or changes your browser settings or policies. If guest browsing is blocked by organization policy, startup reports the limitation instead of opening a signed-in browser.

## Window placement

The app remembers its last normal position and size, including whether it was maximized. Window state is saved separately in %LOCALAPPDATA%/Server Inventory Configuration Manager/Settings/window.json and survives guest-profile cleanup and updates. Minimized windows reopen in their last usable normal or maximized state. A saved position outside the current display layout is moved into the nearest available work area.

## Updates

Checks start after the interface renders, repeat every five active minutes, and run after focus return when at least two minutes have elapsed. Downloads, installation, and restart always require explicit actions. Installation is blocked during collection or report/folder operations.

Downloads use HTTPS, exact repository/version paths, host allowlists, size/SHA-256 verification, PE validation, and Windows application/version metadata. SHA-256 validates bytes against the release manifest; it is not publisher signing. A native application helper performs replacement and restores the previous EXE if rendered, visible, process-matched healthy startup is not confirmed.

Settings, logs, cache, staging, rollback, and browser session files are under %LOCALAPPDATA%/Server Inventory Configuration Manager. Reports stay in the selected output folder. Updates preserve the current executable path and shortcut targets.

See AGENTS.md, DEVELOPMENT_HANDOFF.md, and VALIDATION.md for provenance, release steps, test evidence, and limitations.
