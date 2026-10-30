# Synthetic diagnostic only: never execute the inventory collector's top-level code.
[CmdletBinding()]
param([Parameter(Mandatory=$true)][string]$CollectorPath)
$ErrorActionPreference = 'Stop'
$expectedHash = 'B072C3CF15F1CEF14DF397718A311A753CFAE0A7B12EBF4380FBED4A2D01C091'
$actualHash = (Get-FileHash -LiteralPath $CollectorPath -Algorithm SHA256).Hash
if ($actualHash -ne $expectedHash) { throw 'This diagnostic requires the exact published 3.2.10 collector.' }
Write-Host ('PowerShell: ' + $PSVersionTable.PSVersion.ToString())
Write-Host ('OS: ' + [Environment]::OSVersion.VersionString)
Write-Host ('64-bit process: ' + [Environment]::Is64BitProcess)
Write-Host ('Collector SHA256: ' + $actualHash)
$tokens=$null; $parseErrors=$null
$ast=[Management.Automation.Language.Parser]::ParseFile($CollectorPath,[ref]$tokens,[ref]$parseErrors)
if ($parseErrors.Count) { throw 'Collector parsing failed.' }
$functions=@($ast.EndBlock.Statements | Where-Object { $_ -is [Management.Automation.Language.FunctionDefinitionAst] })
foreach ($function in $functions) { . ([scriptblock]::Create($function.Extent.Text)) }
Write-Host ('Loaded function definitions only: ' + $functions.Count)
$tests=@($ast.EndBlock.Statements | Where-Object {
 $_ -is [Management.Automation.Language.TryStatementAst] -and $_.Extent.Text.Contains('$reportSelfTestGaps = New-Object')
})
if ($tests.Count -ne 1) { throw 'Expected exactly one published startup report self-test block.' }
foreach ($statement in $tests[0].Body.Statements) {
 $line=$statement.Extent.StartLineNumber
 Write-Host ('BEGIN startup self-test source line ' + $line)
 . ([scriptblock]::Create($statement.Extent.Text))
 Write-Host ('PASS startup self-test source line ' + $line)
}
Write-Host 'PASS: exact published startup report self-tests completed. No inventory commands executed.'
