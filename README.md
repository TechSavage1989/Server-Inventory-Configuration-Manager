# Server Inventory Configuration Manager

Portable Windows desktop inventory and configuration-capture utility for Windows Server.

The application collects server hardware, operating system, storage, networking, roles, applications, services, security, policies, SQL metadata, Hyper-V, IIS, Active Directory, DHCP, DNS, file services, and other rebuild/migration/recovery information where available.

## Release format

The maintained application is a single portable Windows executable:

`Server-Inventory-Configuration-Manager.exe`

Runtime settings, updater state, downloads, logs, health markers, and rollback files are stored below:

`%LOCALAPPDATA%\Server Inventory Configuration Manager`

## Build

Requires Go 1.20.x for the broad Windows Server 2012+ compatibility target and Python 3 for icon-resource injection.

```powershell
python .\scripts\generate_assets.py --output-root .
.\scripts\Extract-Collector.ps1 -Version 3.2.0
go test ./...
go vet ./...
go build -trimpath -ldflags "-s -w -H=windowsgui" -o .\build\Server-Inventory-Configuration-Manager.raw.exe .
python .\scripts\embed_icon.py --input .\build\Server-Inventory-Configuration-Manager.raw.exe --output .\build\Server-Inventory-Configuration-Manager.exe --icon .\server_inventory.ico
.\scripts\Test-Binary.ps1 -ExecutablePath .\build\Server-Inventory-Configuration-Manager.exe -ExpectedVersion 3.2.0
```

## Updates

The app checks the public GitHub Releases feed after startup, every five minutes while running, and after focus returns when the last check is at least two minutes old.

Updates are never automatically downloaded, installed, or restarted. Downloads are validated against the release manifest, SHA-256, PE architecture/subsystem, application identity, and version before installation is offered.

A matching SHA-256 validates the downloaded bytes against the trusted release manifest. It is not proof of publisher signing.