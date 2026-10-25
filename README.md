# Server Inventory Configuration Manager

Portable Windows server inventory and configuration capture application. The maintained product is the executable application, preserving the original v3.0.10 interface, collectors, report generation, viewer, configuration controls, and icon.

Download Server-Inventory-Configuration-Manager.exe from [GitHub Releases](https://github.com/TechSavage1989/Server-Inventory-Configuration-Manager/releases/latest). The original v3.0.10 executable and historical v3.1.0 PS1 application require one manual download to enter the executable update line. The PS1 release is retained unchanged.

## Build

Requires Go 1.20.14, Python 3, and Windows PowerShell for validation. The updater is native Go/Win32. Inventory collection uses built-in Windows PowerShell as in the original executable.

1. Run python scripts/verify_baseline.py.
2. Run python scripts/build_resources.py --version 3.2.2.
3. Run scripts/Test-Source.ps1.
4. Run go build -trimpath -ldflags "-s -w -H=windowsgui" -o build/Server-Inventory-Configuration-Manager.exe .
5. Run go test -v ./... and go vet ./....
6. Run scripts/Test-Binary.ps1 and scripts/Test-UpdateInstall.ps1 with the EXE path and expected version.

The original collector and icon assets are committed directly. The icon and Windows version metadata are linked during compilation, without patching the built EXE.

## Updates

Checks start after the interface renders, repeat every five active minutes, and run after focus return when at least two minutes have elapsed. Downloads, installation, and restart always require explicit actions. Installation is blocked during collection or report/folder operations.

Downloads use HTTPS, exact repository/version paths, host allowlists, size/SHA-256 verification, PE validation, and Windows application/version metadata. SHA-256 validates bytes against the release manifest; it is not publisher signing. A native application helper performs replacement and restores the previous EXE if rendered, visible, process-matched healthy startup is not confirmed.

Settings, logs, cache, staging, rollback, and browser session files are under %LOCALAPPDATA%/Server Inventory Configuration Manager. Reports stay in the selected output folder. Updates preserve the current executable path and shortcut targets.

See AGENTS.md, DEVELOPMENT_HANDOFF.md, and VALIDATION.md for provenance, release steps, test evidence, and limitations.
