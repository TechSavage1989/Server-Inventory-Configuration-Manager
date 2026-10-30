@echo off
setlocal
"%SystemRoot%\System32\WindowsPowerShell\v1.0\powershell.exe" -NoLogo -NoProfile -File "%~dp0Run-Diagnostic.ps1"
echo.
echo If script execution was blocked, stop and report that message. Do not change security settings.
pause
