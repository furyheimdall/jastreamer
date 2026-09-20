#Requires -Version 5.1
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version 2.0
if ($PSVersionTable.PSEdition -ne 'Desktop' -or $PSVersionTable.PSVersion.Major -ne 5) {
    throw 'This verification must run on Windows PowerShell 5.1, not PowerShell Core.'
}
$root = Join-Path $env:RUNNER_TEMP ('collector-windows-' + [Guid]::NewGuid().ToString('N'))
[IO.Directory]::CreateDirectory($root) | Out-Null
$evidence = Join-Path $PSScriptRoot 'evidence'
[IO.Directory]::CreateDirectory($evidence) | Out-Null
$fixture = Join-Path $root 'fixture adb.exe'
Add-Type -OutputAssembly $fixture -OutputType ConsoleApplication -TypeDefinition @'
using System;
using System.IO;
using System.Linq;
class AdbFixture {
    static int Main(string[] a) {
        File.AppendAllText(Environment.GetEnvironmentVariable("FIXTURE_CALLS"), String.Join(" ", a) + "\n");
        if (a.SequenceEqual(new [] { "devices" })) {
            Console.WriteLine("List of devices attached\nFIXTURE_SERIAL\tdevice");
            return 0;
        }
        if (a.Length < 3 || a[0] != "-s" || a[1] != "FIXTURE_SERIAL") return 70;
        string s = String.Join(" ", a.Skip(2));
        if (s == "shell pm path io.jastreamer.android.debug") Console.WriteLine("package:/data/app/~~abc==/io.jastreamer.android.debug-xyz==/base.apk");
        else if (s == "shell pm path io.jastreamer.android") { }
        else if (s == "shell dumpsys activity exit-info io.jastreamer.android.debug") Console.WriteLine("ApplicationExitInfo: pid=4321 reason=4 (CRASH)\nprocess=io.jastreamer.android.debug");
        else if (s.StartsWith("shell pidof ")) return 1;
        else if (s.StartsWith("shell sha256sum ")) Console.WriteLine(new String('1',64) + "  /data/app/base.apk");
        else if (s.StartsWith("shell dumpsys activity services ")) Console.WriteLine("No services match: io.jastreamer.android.debug");
        else if (s.StartsWith("shell dumpsys package ")) Console.WriteLine("Package io.jastreamer.android.debug versionName=0.2.0-debug\n{\"owner_token\":\"PRIVATE_TOKEN_CANARY\"}\nhttps://private.example/PRIVATE_URL_CANARY");
        else if (s.StartsWith("shell cmd appops get ")) Console.WriteLine("Unknown command: appops");
        else if (s.StartsWith("shell am get-standby-bucket ")) Console.WriteLine("10");
        else if (s.StartsWith("logcat -d ")) Console.WriteLine("09-20 15:38:39.100 4321 4321 E AndroidRuntime: FATAL EXCEPTION: main\n09-20 15:38:39.101 4321 4321 E AndroidRuntime: java.lang.SecurityException: fixture\n09-20 15:38:39.102 9999 9999 I Other: PRIVATE_OTHER_APP_CANARY");
        else if (s == "shell date") Console.WriteLine("Sun Sep 20 15:45:00 UTC 2026");
        else if (s.StartsWith("shell getprop ")) Console.WriteLine("fixture");
        else if (s == "shell dumpsys battery") Console.WriteLine("USB powered: true\nlevel: 70");
        else if (s == "shell dumpsys power") Console.WriteLine("mWakefulness=Awake\ncom.other PRIVATE_POWER_CANARY");
        else if (s == "shell dumpsys deviceidle") Console.WriteLine("mState=ACTIVE\nmScreenOn=true\ncom.other PRIVATE_IDLE_CANARY");
        else if (s == "shell settings get global low_power") Console.WriteLine("0");
        else { Console.Error.WriteLine("Unexpected fixture command: " + s); return 71; }
        return 0;
    }
}
'@

$results = New-Object 'System.Collections.Generic.List[object]'
$unicodeName = ([string][char]0xD55C + [char]0xAE00)
$cases = @('default-launcher', 'default-direct', 'empty', 'spaces-unicode', 'relative', 'provider-qualified', 'invalid-provider', 'invalid-path')
foreach ($case in $cases) {
    $caseRoot = Join-Path $root ($case + ' space ' + $unicodeName)
    [IO.Directory]::CreateDirectory($caseRoot) | Out-Null
    Copy-Item -LiteralPath (Join-Path $PSScriptRoot 'Collect-JastreamerAndroid.ps1') -Destination $caseRoot
    Copy-Item -LiteralPath (Join-Path $PSScriptRoot 'Run-Diagnostics.cmd') -Destination $caseRoot
    Copy-Item -LiteralPath $fixture -Destination (Join-Path $caseRoot 'adb.exe')
    $env:FIXTURE_CALLS = Join-Path $caseRoot 'calls.txt'
    $destination = $caseRoot
    $extra = ''
    switch ($case) {
        'empty' { $extra = ' -OutputDirectory ""' }
        'spaces-unicode' {
            $destination = Join-Path $caseRoot ('output space ' + $unicodeName)
            $extra = ' -OutputDirectory "' + $destination + '"'
        }
        'relative' {
            $destination = Join-Path $caseRoot 'relative-output'
            $extra = ' -OutputDirectory .\relative-output'
        }
        'provider-qualified' {
            $destination = Join-Path $caseRoot 'provider-output'
            $extra = ' -OutputDirectory "Microsoft.PowerShell.Core\FileSystem::' + $destination + '"'
        }
        'invalid-provider' { $extra = ' -OutputDirectory Registry::HKEY_CURRENT_USER\Software' }
        'invalid-path' { $extra = ' -OutputDirectory "C:\bad|path"' }
    }
    $start = New-Object System.Diagnostics.ProcessStartInfo
    $start.WorkingDirectory = $caseRoot
    $start.UseShellExecute = $false
    $start.CreateNoWindow = $true
    $start.RedirectStandardOutput = $true
    $start.RedirectStandardError = $true
    $start.RedirectStandardInput = $true
    if ($case -eq 'default-launcher') {
        $start.FileName = $env:ComSpec
        $start.Arguments = '/d /c Run-Diagnostics.cmd'
    } else {
        $start.FileName = Join-Path $PSHOME 'powershell.exe'
        $start.Arguments = '-NoLogo -NoProfile -ExecutionPolicy Bypass -File .\Collect-JastreamerAndroid.ps1' + $extra
    }
    $process = New-Object System.Diagnostics.Process
    $process.StartInfo = $start
    [void]$process.Start()
    $stdout = $process.StandardOutput.ReadToEndAsync()
    $stderr = $process.StandardError.ReadToEndAsync()
    $process.StandardInput.WriteLine(' ')
    $process.StandardInput.Close()
    if (-not $process.WaitForExit(120000)) { $process.Kill(); throw "Timed out: $case" }
    $code = $process.ExitCode
    $text = $stdout.GetAwaiter().GetResult() + "`n" + $stderr.GetAwaiter().GetResult()
    $process.Dispose()
    [IO.File]::WriteAllText((Join-Path $evidence ($case + '.txt')), $text)
    if ($case -like 'invalid-*') {
        if ($code -eq 0 -or $text -notmatch 'Cannot use output directory') { throw "Invalid path was not rejected clearly: $case`n$text" }
        if (Test-Path -LiteralPath $env:FIXTURE_CALLS) { throw "Queried ADB before rejecting path: $case" }
        $results.Add([pscustomobject]@{ case = $case; result = 'rejected before ADB' })
        continue
    }
    if ($code -ne 0) { throw "Collector failed ($code): $case`n$text" }
    $archives = @(Get-ChildItem -LiteralPath $destination -Filter '*.zip')
    if ($archives.Count -ne 1) { throw "Expected one ZIP in requested directory: $case" }
    $expanded = Join-Path $caseRoot 'expanded'
    Expand-Archive -LiteralPath $archives[0].FullName -DestinationPath $expanded
    $manifestFile = @(Get-ChildItem -LiteralPath $expanded -Recurse -Filter 'collection.json')[0]
    $manifest = Get-Content -LiteralPath $manifestFile.FullName -Raw | ConvertFrom-Json
    if ($manifest.collectorVersion -ne 2) { throw 'Wrong collector version' }
    if ($manifest.collectionIssues -ne 2) { throw "Unexpected query status: $case" }
    $all = ((Get-ChildItem -LiteralPath $expanded -Recurse -File | ForEach-Object { Get-Content -LiteralPath $_.FullName -Raw }) -join "`n")
    if ($all -match 'PRIVATE_\w+_CANARY|FIXTURE_SERIAL') { throw "Privacy canary leaked: $case" }
    if ($all -notmatch 'FATAL EXCEPTION: main' -or $all -notmatch 'reason=4') { throw "Crash evidence lost: $case" }
    $calls = Get-Content -LiteralPath $env:FIXTURE_CALLS
    if ($calls -match 'force-stop|install |uninstall|logcat -c|settings put|shell reboot') { throw 'Mutating ADB command found' }
    $results.Add([pscustomobject]@{ case = $case; result = 'ZIP and crash/privacy assertions passed'; sha256 = (Get-FileHash -LiteralPath $archives[0].FullName).Hash })
}
$report = [ordered]@{
    powershellVersion = $PSVersionTable.PSVersion.ToString(); edition = $PSVersionTable.PSEdition
    operatingSystem = [Environment]::OSVersion.VersionString; results = @($results.ToArray())
    collectorSha256 = (Get-FileHash -LiteralPath (Join-Path $PSScriptRoot 'Collect-JastreamerAndroid.ps1')).Hash
    launcherSha256 = (Get-FileHash -LiteralPath (Join-Path $PSScriptRoot 'Run-Diagnostics.cmd')).Hash
    physicalAndroidDevice = $false; adb = 'Compiled fixture executable; real Windows process execution'
}
$report | ConvertTo-Json -Depth 6 | Set-Content -LiteralPath (Join-Path $evidence 'verification.json') -Encoding UTF8
$report | ConvertTo-Json -Depth 6
