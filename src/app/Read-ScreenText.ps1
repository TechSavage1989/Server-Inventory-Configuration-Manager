<#
.SYNOPSIS
Reads the text in one picture of a VM console with Windows' own text
recognition (Windows.Media.Ocr), on this computer.

.DESCRIPTION
VM console (rule 68): "Copy text from the screen". The console window crops
the part of the VM's screen the operator chose and saves it as a PNG under
LocalAppData; this script reads the text in it and prints one JSON line:
{"ok":true,"text":"...","language":"en-US"} or {"ok":false,"error":"..."}.

It runs only on this computer, starts nothing, and changes nothing. The picture
and the text are never written anywhere else; the application deletes the
picture when the script ends. Windows text recognition needs Windows 10 or
Windows Server 2016 or later with the Desktop Experience, and a language whose
text recognition is installed (most Windows installations have one).
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$ImagePath
)

$ErrorActionPreference = 'Stop'

function Write-Answer([hashtable]$Answer) {
    $json = $Answer | ConvertTo-Json -Compress
    [Console]::Out.WriteLine($json)
}

try {
    if (-not (Test-Path -LiteralPath $ImagePath -PathType Leaf)) {
        throw 'The picture to read was not found.'
    }
    try {
        Add-Type -AssemblyName System.Runtime.WindowsRuntime
        $null = [Windows.Storage.StorageFile, Windows.Storage, ContentType = WindowsRuntime]
        $null = [Windows.Storage.FileAccessMode, Windows.Storage, ContentType = WindowsRuntime]
        $null = [Windows.Storage.Streams.IRandomAccessStream, Windows.Storage.Streams, ContentType = WindowsRuntime]
        $null = [Windows.Graphics.Imaging.BitmapDecoder, Windows.Graphics, ContentType = WindowsRuntime]
        $null = [Windows.Graphics.Imaging.SoftwareBitmap, Windows.Graphics, ContentType = WindowsRuntime]
        $null = [Windows.Media.Ocr.OcrEngine, Windows.Foundation, ContentType = WindowsRuntime]
        $null = [Windows.Media.Ocr.OcrResult, Windows.Foundation, ContentType = WindowsRuntime]
    } catch {
        throw 'Windows text recognition is not available on this computer. It needs Windows 10 or Windows Server 2016 or later with the Desktop Experience.'
    }

    $asTaskMethod = [System.WindowsRuntimeSystemExtensions].GetMethods() | Where-Object {
        $_.Name -eq 'AsTask' -and $_.GetParameters().Count -eq 1 -and $_.GetParameters()[0].ParameterType.Name -eq 'IAsyncOperation`1'
    } | Select-Object -First 1
    function Wait-WinRT($Operation, [Type]$ResultType) {
        $task = $asTaskMethod.MakeGenericMethod($ResultType).Invoke($null, @($Operation))
        if (-not $task.Wait(30000)) { throw 'Windows text recognition did not answer within 30 seconds.' }
        $task.Result
    }

    $engine = [Windows.Media.Ocr.OcrEngine]::TryCreateFromUserProfileLanguages()
    if ($null -eq $engine) {
        foreach ($language in [Windows.Media.Ocr.OcrEngine]::AvailableRecognizerLanguages) {
            $engine = [Windows.Media.Ocr.OcrEngine]::TryCreateFromLanguage($language)
            if ($null -ne $engine) { break }
        }
    }
    if ($null -eq $engine) {
        throw 'Windows has no text recognition language installed. Add one in Settings > Time and language > Language (the "Optical character recognition" feature).'
    }

    $full = (Resolve-Path -LiteralPath $ImagePath).ProviderPath
    $file = Wait-WinRT ([Windows.Storage.StorageFile]::GetFileFromPathAsync($full)) ([Windows.Storage.StorageFile])
    $stream = Wait-WinRT ($file.OpenAsync([Windows.Storage.FileAccessMode]::Read)) ([Windows.Storage.Streams.IRandomAccessStream])
    try {
        $decoder = Wait-WinRT ([Windows.Graphics.Imaging.BitmapDecoder]::CreateAsync($stream)) ([Windows.Graphics.Imaging.BitmapDecoder])
        $max = [Windows.Media.Ocr.OcrEngine]::MaxImageDimension
        if ($decoder.PixelWidth -gt $max -or $decoder.PixelHeight -gt $max) {
            throw ('The picture is larger than Windows text recognition reads (' + $max + ' pixels). Choose a smaller area.')
        }
        $bitmap = Wait-WinRT ($decoder.GetSoftwareBitmapAsync()) ([Windows.Graphics.Imaging.SoftwareBitmap])
        $result = Wait-WinRT ($engine.RecognizeAsync($bitmap)) ([Windows.Media.Ocr.OcrResult])
    } finally {
        $stream.Dispose()
    }
    $lines = @()
    foreach ($line in $result.Lines) { $lines += $line.Text }
    Write-Answer @{ ok = $true; text = ($lines -join "`n"); language = $engine.RecognizerLanguage.LanguageTag }
} catch {
    Write-Answer @{ ok = $false; error = $_.Exception.Message }
}
