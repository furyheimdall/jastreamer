#Requires -Version 5.1
<#
Read-only Android snapshot for jastreamer. No install, restart, force-stop,
log clearing, settings changes, root, bugreport, or automatic upload.
Run AFTER the failure, preferably before reopening/reconnecting the app.
USB power/debugging can change sleep behavior: this snapshot is NOT a
screen-off reproduction or proof that background playback is fixed.
#>
[CmdletBinding()]
param(
    [string]$AdbPath,
    [string]$Serial,
    [string]$OutputDirectory,
    [ValidateRange(3, 120)][int]$CommandTimeoutSeconds = 20,
    [ValidateRange(100, 20000)][int]$LogLines = 6000
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version 2.0
$utf8 = New-Object System.Text.UTF8Encoding($false)
$records = New-Object 'System.Collections.Generic.List[object]'
$targetPids = New-Object 'System.Collections.Generic.HashSet[string]'
$packages = @('io.jastreamer.android.debug', 'io.jastreamer.android')
$installed = New-Object 'System.Collections.Generic.List[string]'
$started = [DateTimeOffset]::UtcNow

# Resolve defaults after parameter binding, from the actual script file.
# Resolve PowerShell provider paths before passing them to .NET filesystem APIs.
$scriptDirectory = (Get-Item -LiteralPath $MyInvocation.MyCommand.Path).DirectoryName
if ([string]::IsNullOrWhiteSpace($OutputDirectory)) {
    $OutputDirectory = $scriptDirectory
}
$outputProvider = $null
$outputDrive = $null
try {
    $root = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath(
        $OutputDirectory, [ref]$outputProvider, [ref]$outputDrive)
    if ($outputProvider.Name -ne 'FileSystem') {
        throw 'The output directory must be a filesystem path.'
    }
    [void][IO.Directory]::CreateDirectory($root)
} catch {
    throw "Cannot use output directory '$OutputDirectory': $($_.Exception.Message)"
}

if ([string]::IsNullOrWhiteSpace($AdbPath)) {
    $localAdb = Join-Path $scriptDirectory 'adb.exe'
    if (Test-Path -LiteralPath $localAdb -PathType Leaf) {
        $AdbPath = $localAdb
    } else {
        $found = Get-Command adb.exe -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
        if ($null -eq $found) { throw 'adb.exe not found. Put this script beside adb.exe or supply -AdbPath.' }
        $AdbPath = $found.Source
    }
}
$AdbPath = (Resolve-Path -LiteralPath $AdbPath).ProviderPath

function Invoke-Adb([string[]]$Arguments) {
    # All arguments are fixed tokens or validated device identifiers, never shell expressions.
    foreach ($argument in $Arguments) {
        if ($argument -notmatch '\A[A-Za-z0-9_.:/=+@~-]+\z') { throw 'Unsafe ADB argument refused.' }
    }
    $info = New-Object System.Diagnostics.ProcessStartInfo
    $info.FileName = $AdbPath
    $info.Arguments = $Arguments -join ' '
    $info.UseShellExecute = $false
    $info.CreateNoWindow = $true
    $info.RedirectStandardOutput = $true
    $info.RedirectStandardError = $true
    $info.StandardOutputEncoding = $utf8
    $info.StandardErrorEncoding = $utf8
    $process = New-Object System.Diagnostics.Process
    $process.StartInfo = $info
    try {
        if (-not $process.Start()) { throw 'Could not start adb.' }
        $stdout = $process.StandardOutput.ReadToEndAsync()
        $stderr = $process.StandardError.ReadToEndAsync()
        if (-not $process.WaitForExit($CommandTimeoutSeconds * 1000)) {
            # Stop only this PC-side query, not the ADB server or any phone process.
            $process.Kill()
            $process.WaitForExit()
            return [pscustomobject]@{ ExitCode = -1; Status = 'timeout'; Text = "Command timed out after $CommandTimeoutSeconds seconds." }
        }
        $text = $stdout.GetAwaiter().GetResult() + "`n" + $stderr.GetAwaiter().GetResult()
        $status = 'ok'
        if ($process.ExitCode -ne 0) { $status = 'failed' }
        if ($text -match '(?im)^\s*(Permission Denial:|Unknown command:|Unknown option:|Can.t find service:)') { $status = 'unavailable' }
        return [pscustomobject]@{ ExitCode = $process.ExitCode; Status = $status; Text = $text.TrimEnd() }
    } finally {
        $process.Dispose()
    }
}

function Protect-Text([string]$Text) {
    $safe = foreach ($line in ($Text -split '\r?\n')) {
        if ($line -match '(?i)(authorization|cookie|owner[_-]?token|access[_-]?token|refresh[_-]?token|password|passwd|secret)["\x27]?\s*[=:]') {
            '[REDACTED credential-bearing line]'
        } else {
            $clean = [regex]::Replace($line, '(?i)\bhttps?://[^\s<>"\x27]+', '[REDACTED URL]')
            if (-not [string]::IsNullOrEmpty($Serial)) { $clean = $clean.Replace($Serial, '[DEVICE]') }
            $clean
        }
    }
    return ($safe -join "`r`n")
}

function Save-Text([string]$Name, [string]$Text) {
    [IO.File]::WriteAllText((Join-Path $folder $Name), (Protect-Text $Text), $utf8)
}

function Save-Query([string]$Name, [string[]]$Arguments, [string]$KeepPattern = '') {
    Write-Host "Collecting $Name"
    $queryStart = [DateTimeOffset]::UtcNow
    $result = Invoke-Adb (@('-s', $Serial) + $Arguments)
    $body = $result.Text
    if ($KeepPattern) {
        $body = (($body -split '\r?\n') | Where-Object { $_ -match $KeepPattern }) -join "`n"
        if ([string]::IsNullOrWhiteSpace($body)) { $body = '[No matching fields; this does not prove absence of a restriction.]' }
    }
    $records.Add([pscustomobject]@{
        file = $Name; command = ($Arguments -join ' '); startedUtc = $queryStart.ToString('o')
        status = $result.Status; exitCode = $result.ExitCode
    })
    Save-Text $Name ("UTC: " + $queryStart.ToString('o') + "`nCommand: adb <selected-device> " + ($Arguments -join ' ') + "`nStatus: " + $result.Status + "`nExit code: " + $result.ExitCode + "`n`n" + $body)
    return $result
}

$devicesResult = Invoke-Adb @('devices')
if ($devicesResult.Status -ne 'ok') { throw 'adb devices failed or timed out. Check ADB, then retry.' }
$devices = @([regex]::Matches($devicesResult.Text, '(?m)^([^\s]+)\s+device\s*$') | ForEach-Object { $_.Groups[1].Value })
if ([string]::IsNullOrWhiteSpace($Serial)) {
    if ($devices.Count -ne 1) { throw "Expected exactly one authorized device, found $($devices.Count). Use -Serial when several devices are attached; approve the USB prompt if unauthorized." }
    $Serial = $devices[0]
} elseif ($devices -cnotcontains $Serial) {
    throw 'The requested device is not connected and authorized.'
}

$name = 'jastreamer-android-' + $started.ToString('yyyyMMdd-HHmmss') + '-' + [Guid]::NewGuid().ToString('N').Substring(0, 8)
$folder = Join-Path $root $name
[IO.Directory]::CreateDirectory($folder) | Out-Null
Write-Host 'Read-only snapshot. Do not reopen/stop the app or change battery settings during collection.'
Write-Host 'No data is uploaded. Review the ZIP before sharing it.'

foreach ($package in $packages) {
    $presence = Save-Query "$package-installed.txt" @('shell', 'pm', 'path', $package)
    if ($presence.Status -ne 'ok' -or $presence.Text -notmatch '(?m)^package:') { continue }
    $installed.Add($package)
    $exitInfo = Save-Query "$package-exit-info.txt" @('shell', 'dumpsys', 'activity', 'exit-info', $package)
    $baseApk = [regex]::Match($presence.Text, '(?m)^package:(/[A-Za-z0-9_./=+@~-]+/base\.apk)\s*$')
    if ($baseApk.Success) {
        $null = Save-Query "$package-apk-sha256.txt" @('shell', 'sha256sum', $baseApk.Groups[1].Value)
    }
    foreach ($match in [regex]::Matches($exitInfo.Text, '\bpid=(\d+)')) { [void]$targetPids.Add($match.Groups[1].Value) }
    $pidResult = Save-Query "$package-pid.txt" @('shell', 'pidof', $package)
    if ($pidResult.ExitCode -eq 0) {
        foreach ($id in ($pidResult.Text.Trim() -split '\s+')) {
            if ($id -match '^\d+$') { [void]$targetPids.Add($id) }
        }
    }
    $null = Save-Query "$package-services.txt" @('shell', 'dumpsys', 'activity', 'services', $package)
    $null = Save-Query "$package-package.txt" @('shell', 'dumpsys', 'package', $package)
    $null = Save-Query "$package-background-appop.txt" @('shell', 'cmd', 'appops', 'get', $package, 'RUN_ANY_IN_BACKGROUND')
    $null = Save-Query "$package-standby-bucket.txt" @('shell', 'am', 'get-standby-bucket', $package)
}

# Keep only package-named lines or lines from current/recent app PIDs.
# The unfiltered cross-app log buffer is never written to disk or included in the ZIP.
Write-Host 'Collecting filtered recent logcat'
$logStart = [DateTimeOffset]::UtcNow
$logs = Invoke-Adb @('-s', $Serial, 'logcat', '-d', '-v', 'threadtime', '-b', 'main', '-b', 'system', '-b', 'events', '-b', 'crash', '-t', [string]$LogLines)
$filtered = New-Object 'System.Collections.Generic.List[string]'
if ($logs.Status -eq 'ok') {
    foreach ($line in ($logs.Text -split '\r?\n')) {
        $keep = $line -match 'io\.jastreamer\.android(?:\.debug)?\b|NativePlaybackService'
        if (-not $keep -and $line -match '^\d{2}-\d{2}\s+\S+\s+(\d+)\s+\d+\s+[VDIWEF]\s') {
            $keep = $targetPids.Contains($Matches[1])
        }
        if ($keep) { $filtered.Add($line) }
    }
} else {
    $filtered.Add("Logcat unavailable: status=$($logs.Status), exitCode=$($logs.ExitCode). Raw cross-app output was not saved.")
}
$records.Add([pscustomobject]@{ file = 'logcat-filtered.txt'; command = 'logcat -d (bounded; package/PID-filtered locally)'; startedUtc = $logStart.ToString('o'); status = $logs.Status; exitCode = $logs.ExitCode })
Save-Text 'logcat-filtered.txt' ("Logcat uses device time. Queried at UTC " + $logStart.ToString('o') + "`nRecent PIDs may be reused; review before sharing. No matches is not proof of no crash.`n`n" + ($filtered -join "`n"))
$logs = $null

$null = Save-Query 'device-time.txt' @('shell', 'date')
foreach ($property in @('ro.product.manufacturer', 'ro.product.model', 'ro.build.version.release', 'ro.build.version.sdk', 'ro.build.version.security_patch', 'ro.build.version.incremental', 'persist.sys.timezone')) {
    $null = Save-Query "$property.txt" @('shell', 'getprop', $property)
}
$null = Save-Query 'battery.txt' @('shell', 'dumpsys', 'battery')
$null = Save-Query 'power-filtered.txt' @('shell', 'dumpsys', 'power') '(?i)jastreamer|mWakefulness=|mIsPowered=|mPlugType=|mLowPowerModeEnabled=|mDeviceIdleMode=|mLightDeviceIdleMode='
$null = Save-Query 'deviceidle-filtered.txt' @('shell', 'dumpsys', 'deviceidle') '(?i)jastreamer|mState=|mLightState=|mScreenOn=|mCharging=|mNetworkConnected=|mForceIdle='
$null = Save-Query 'low-power-setting.txt' @('shell', 'settings', 'get', 'global', 'low_power')

$unavailable = @($records | Where-Object { $_.status -ne 'ok' })
$manifest = [ordered]@{
    collectorVersion = 2; startedUtc = $started.ToString('o'); finishedUtc = [DateTimeOffset]::UtcNow.ToString('o')
    powershellVersion = $PSVersionTable.PSVersion.ToString(); installedPackages = @($installed.ToArray())
    collectionIssues = $unavailable.Count; queries = @($records.ToArray())
    limitations = @(
        'This is a post-failure snapshot, not a screen-off reproduction or playback qualification.',
        'USB power/debugging can change sleep behavior; current state may differ from failure-time state.',
        'A nonzero pidof result may simply mean the app is no longer running.',
        'Exit history and recent logs may be unavailable or rolled over; absence is not proof.',
        'PID reuse may include unrelated log lines. URL/credential redaction is best effort; review before sharing.',
        'Installed base.apk SHA-256 is requested where available; source correspondence still needs comparison with the delivered artifact.',
        'No media files, cookies, app databases, full notification dumps, or full bugreport are collected.'
    )
}
Save-Text 'collection.json' ($manifest | ConvertTo-Json -Depth 6)
Save-Text 'SHARE-NOTICE.txt' @"
This folder is a read-only jastreamer Android diagnostic snapshot.
Review all files before sharing. URLs and common credential fields are redacted,
but stack traces, paths, log messages, device build details and app metadata may
still contain private information. Nothing is uploaded automatically.

Open collection.json for per-command exit codes, timeouts and limitations.
Failure/empty output must not be interpreted as a healthy service.
USB power may prevent the original screen-off condition from reproducing.
The collector did not start, stop, install, uninstall or reconnect the app.
"@

$zip = $folder + '.zip'
try {
    Compress-Archive -LiteralPath $folder -DestinationPath $zip -CompressionLevel Optimal
} catch {
    Write-Warning "ZIP creation failed. Uncompressed evidence remains at: $folder"
    throw
}
Write-Host ''
Write-Host "ZIP: $zip"
Write-Host "Collection issues (including an app that is not running): $($unavailable.Count)"
if ($installed.Count -eq 0) { Write-Warning 'Neither known jastreamer package was found. Inspect the installed-package results.' }
Write-Host 'Review and share the ZIP through your chosen private channel. No automatic upload occurred.'
