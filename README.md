# Server Inventory Configuration Manager

Server Inventory Configuration Manager is a portable Windows PowerShell/WPF application for broad server inventory, configuration capture, documentation, troubleshooting, migration planning, recovery assistance, and rebuild reference.

## Run

Download Server-Inventory-Configuration-Manager.ps1 and use **Run with PowerShell**. The application requests administrator elevation when required.

The release is intentionally a readable single PowerShell file rather than an EXE bootstrapper.

## Output

Collections can produce Word, interactive HTML, text summary, raw supporting exports, optional role/configuration exports, ZIP packages, and SHA-256 checksums.

## Updates

The application checks the public GitHub release feed after startup, every five minutes while running, and after focus returns when the previous check is old enough.

Updates are never downloaded or installed automatically. An available release exposes explicit **Download** and **Install and restart** actions.

Updater state, logs, staging, and rollback data are stored under:

%LOCALAPPDATA%\Server Inventory Configuration Manager

## Development

Read AGENTS.md first, then DEVELOPMENT_HANDOFF.md.

~~~powershell
powershell.exe -NoProfile -File .\scripts\Test-Source.ps1
~~~
