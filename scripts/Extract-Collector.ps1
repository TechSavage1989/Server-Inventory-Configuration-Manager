[CmdletBinding()]
param(
    [string]$SourcePath=(Join-Path $PSScriptRoot '..\legacy\Server-Inventory-Configuration-Manager-v3.1.0.ps1'),
    [string]$OutputPath=(Join-Path $PSScriptRoot '..\Export-ServerInventory.ps1'),
    [Parameter(Mandatory=$true)][string]$Version
)
$ErrorActionPreference='Stop'
$tokens=$null;$errors=$null
$ast=[Management.Automation.Language.Parser]::ParseFile([IO.Path]::GetFullPath($SourcePath),[ref]$tokens,[ref]$errors)
if($errors -and $errors.Count){throw (($errors|ForEach-Object{$_.Message}) -join [Environment]::NewLine)}
$fn=$ast.Find({param($node) $node -is [Management.Automation.Language.FunctionDefinitionAst] -and $node.Name -eq 'Invoke-EmbeddedServerInventoryCollector'},$true)
if(-not $fn){throw 'Invoke-EmbeddedServerInventoryCollector was not found in the historical single-file source.'}
$body=$fn.Body.Extent.Text
if($body.Length -lt 2 -or $body[0] -ne '{' -or $body[$body.Length-1] -ne '}'){throw 'Collector function body was malformed.'}
$content=$body.Substring(1,$body.Length-2).Trim()
$content=[regex]::Replace($content,"\$scriptVersion\s*=\s*'[^']+'",("`$scriptVersion = '"+$Version+"'"),1)
if($content -notmatch "\$scriptVersion\s*=\s*'"+[regex]::Escape($Version)+"'"){throw 'Collector version replacement failed.'}
[IO.File]::WriteAllText([IO.Path]::GetFullPath($OutputPath),$content,(New-Object Text.UTF8Encoding($true)))
Write-Host "Collector extracted to $OutputPath"