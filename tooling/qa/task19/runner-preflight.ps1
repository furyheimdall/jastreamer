[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$Output
)

$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $true

if ($env:RUNNER_OS -ne 'Windows' -or $env:RUNNER_ARCH -ne 'X64') {
    throw 'TASK19_RUNNER_PLATFORM_INVALID'
}

$adb = Get-Command adb -ErrorAction SilentlyContinue
if (!$adb) {
    throw 'TASK19_ADB_MISSING'
}

$adbVersion = & $adb.Source version
if ($LASTEXITCODE -ne 0) {
    throw 'TASK19_ADB_VERSION_FAILED'
}

$deviceLines = & $adb.Source devices -l
if ($LASTEXITCODE -ne 0) {
    throw 'TASK19_ADB_ENUMERATION_FAILED'
}

$entries = @(
    $deviceLines |
        Select-Object -Skip 1 |
        Where-Object { $_ -match '\S' } |
        ForEach-Object {
            $fields = $_ -split '\s+'
            [PSCustomObject]@{
                Id = $fields[0]
                State = $fields[1]
            }
        }
)
if ($entries.Count -ne 1) {
    throw 'TASK19_ADB_DEVICE_COUNT_INVALID'
}
if ($entries[0].State -ne 'device') {
    throw "TASK19_ADB_DEVICE_STATE_INVALID:$($entries[0].State)"
}

$deviceId = $entries[0].Id
$deviceHash = [Convert]::ToHexString(
    [Security.Cryptography.SHA256]::HashData(
        [Text.Encoding]::UTF8.GetBytes($deviceId)
    )
).ToLowerInvariant()
$adbHash = (Get-FileHash $adb.Source -Algorithm SHA256).Hash.ToLowerInvariant()
$versionHash = [Convert]::ToHexString(
    [Security.Cryptography.SHA256]::HashData(
        [Text.Encoding]::UTF8.GetBytes(($adbVersion -join "`n"))
    )
).ToLowerInvariant()

$receipt = [ordered]@{
    schemaVersion = 1
    kind = 'task19_runner_preflight'
    recordedAt = (Get-Date).ToUniversalTime().ToString('o')
    runner = [ordered]@{
        os = $env:RUNNER_OS
        arch = $env:RUNNER_ARCH
    }
    android = [ordered]@{
        authorizedDeviceCount = 1
        androidDeviceSerialSha256 = $deviceHash
        adbExecutableSha256 = $adbHash
        adbVersionOutputSha256 = $versionHash
    }
    publicationWrites = 0
}

$target = [IO.Path]::GetFullPath($Output)
$parent = [IO.Path]::GetDirectoryName($target)
if ($parent) {
    [IO.Directory]::CreateDirectory($parent) | Out-Null
}
$json = $receipt | ConvertTo-Json -Depth 4
[IO.File]::WriteAllText(
    $target,
    "$json`n",
    [Text.UTF8Encoding]::new($false)
)

Write-Output 'TASK19_RUNNER_PREFLIGHT_PASSED'
