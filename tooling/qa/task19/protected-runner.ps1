[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$Plan,
    [Parameter(Mandatory = $true)][string]$Output
)

$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $true
$TASK19_SETUP_TIMEOUT_MS = 1_200_000
$TASK19_DRIVER_TIMEOUT_MS = 5_100_000
$TASK19_CLEANUP_TIMEOUT_MS = 600_000
$WindowsPackageName = 'io.jastreamer.control'
$AndroidApplicationId = 'io.jastreamer.control'
$cleanupFailures = [Collections.Generic.List[string]]::new()
$ownedProcesses = [Collections.Generic.List[int]]::new()

function Invoke-NativeChecked {
    param([string]$FilePath, [string[]]$Arguments, [string]$Code, [int]$TimeoutMs = 600_000)
    $start = [Diagnostics.ProcessStartInfo]::new()
    $start.FileName = $FilePath
    $start.UseShellExecute = $false
    foreach ($argument in $Arguments) { $start.ArgumentList.Add($argument) }
    $process = [Diagnostics.Process]::Start($start)
    try {
        if (!$process.WaitForExit($TimeoutMs)) { Stop-TaskProcessTree $process.Id; throw "TASK19_CHILD_TIMEOUT:$Code" }
        if ($process.ExitCode -ne 0) { throw "TASK19_INSTALLER_EXIT_CODE:$Code:$($process.ExitCode)" }
    }
    finally { $process.Dispose() }
}

function Assert-PhaseDeadline {
    param([Diagnostics.Stopwatch]$Stopwatch, [int]$LimitMs, [string]$Code)
    if ($Stopwatch.ElapsedMilliseconds -ge $LimitMs) { throw $Code }
}

function Get-RemainingPhaseMs {
    param([Diagnostics.Stopwatch]$Stopwatch, [int]$LimitMs, [string]$Code)
    Assert-PhaseDeadline $Stopwatch $LimitMs $Code
    return [Math]::Max(1, $LimitMs - [int]$Stopwatch.ElapsedMilliseconds)
}

function Stop-TaskProcessTree {
    param([int]$Id)
    if ($Id -gt 0 -and (Get-Process -Id $Id -ErrorAction SilentlyContinue)) {
        & taskkill.exe /PID $Id /T /F | Out-Null
        if ($LASTEXITCODE -ne 0 -and (Get-Process -Id $Id -ErrorAction SilentlyContinue)) {
            throw "TASK19_PROCESS_TREE_TERMINATION_FAILED:$Id"
        }
    }
}

function Get-MsiProductCode {
    param([string]$Path)
    $installer = New-Object -ComObject WindowsInstaller.Installer
    try {
        $database = $installer.GetType().InvokeMember('OpenDatabase', 'InvokeMethod', $null, $installer, @($Path, 0))
        $view = $database.OpenView("SELECT `Value` FROM `Property` WHERE `Property`='ProductCode'")
        $view.Execute()
        $record = $view.Fetch()
        $code = $record.StringData(1)
        if ($code -notmatch '^\{[0-9A-Fa-f-]{36}\}$') { throw 'TASK19_MSI_PRODUCT_CODE_INVALID' }
        return $code
    }
    finally {
        if ($installer) { [Runtime.InteropServices.Marshal]::FinalReleaseComObject($installer) | Out-Null }
    }
}

function Test-MsiProductCodeInstalled {
    param([string]$ProductCode)
    $paths = @("Registry::HKEY_LOCAL_MACHINE\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\$ProductCode", "Registry::HKEY_LOCAL_MACHINE\SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall\$ProductCode")
    return @($paths | Where-Object { Test-Path -LiteralPath $_ }).Count -gt 0
}

function Assert-PlanFile {
    param($Reference)
    if (!(Test-Path -LiteralPath $Reference.path -PathType Leaf)) { throw 'TASK19_PLAN_FILE_MISSING' }
    $item = Get-Item -LiteralPath $Reference.path -Force
    if ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) { throw 'TASK19_PLAN_REPARSE_POINT_REJECTED' }
    if ($item.Length -ne $Reference.size) { throw 'TASK19_PLAN_FILE_SIZE_MISMATCH' }
    if ((Get-FileHash -LiteralPath $item.FullName -Algorithm SHA256).Hash.ToLowerInvariant() -ne $Reference.sha256) { throw 'TASK19_PLAN_FILE_DIGEST_MISMATCH' }
}

$trustedPlan = Get-Content -LiteralPath $Plan -Raw | ConvertFrom-Json
$planSha256 = (Get-FileHash -LiteralPath $Plan -Algorithm SHA256).Hash.ToLowerInvariant()
if ($trustedPlan.schemaVersion -ne 2 -or $trustedPlan.kind -ne 'task19_trusted_execution_plan' -or $trustedPlan.qualificationReady -ne $true) { throw 'TASK19_TRUSTED_PLAN_INVALID' }
if ($trustedPlan.identities.windowsPackageName -ne $WindowsPackageName -or $trustedPlan.identities.androidApplicationId -ne $AndroidApplicationId) { throw 'TASK19_REPOSITORY_IDENTITY_MISMATCH' }
if ($trustedPlan.driver.contract -ne 'task19-installed-scenario-driver-v1') { throw 'TASK19_DRIVER_CONTRACT_INVALID' }
foreach ($property in $trustedPlan.files.PSObject.Properties) { Assert-PlanFile $property.Value }
foreach ($property in $trustedPlan.receipts.PSObject.Properties) { Assert-PlanFile $property.Value }
$driver = Join-Path $PSScriptRoot 'scenario-driver.ps1'
$scenarioRuntime = Join-Path $PSScriptRoot 'scenario-runtime.mjs'
if ((Get-FileHash -LiteralPath $driver -Algorithm SHA256).Hash.ToLowerInvariant() -ne $trustedPlan.driver.sha256) { throw 'TASK19_REPOSITORY_DRIVER_DIGEST_MISMATCH' }
if ((Get-FileHash -LiteralPath $scenarioRuntime -Algorithm SHA256).Hash.ToLowerInvariant() -ne $trustedPlan.driver.runtimeSha256) { throw 'TASK19_SCENARIO_RUNTIME_DIGEST_MISMATCH' }
$runtimeBindings = @(
    @{ Path = $trustedPlan.driver.harnessPath; Sha256 = $trustedPlan.driver.harnessSha256; Code = 'TASK19_SCENARIO_HARNESS_DIGEST_MISMATCH' },
    @{ Path = $trustedPlan.driver.scenarioContractPath; Sha256 = $trustedPlan.driver.scenarioContractSha256; Code = 'TASK19_SCENARIO_CONTRACT_DIGEST_MISMATCH' },
    @{ Path = $trustedPlan.driver.scenarioProvisionerPath; Sha256 = $trustedPlan.driver.scenarioProvisionerSha256; Code = 'TASK19_SCENARIO_PROVISIONER_DIGEST_MISMATCH' },
    @{ Path = $trustedPlan.driver.nativeCapturePath; Sha256 = $trustedPlan.driver.nativeCaptureSha256; Code = 'TASK19_NATIVE_CAPTURE_DIGEST_MISMATCH' },
    @{ Path = $trustedPlan.driver.webOriginPath; Sha256 = $trustedPlan.driver.webOriginSha256; Code = 'TASK19_WEB_ORIGIN_DIGEST_MISMATCH' },
    @{ Path = $trustedPlan.driver.tlsIdentityGeneratorPath; Sha256 = $trustedPlan.driver.tlsIdentityGeneratorSha256; Code = 'TASK19_TLS_IDENTITY_GENERATOR_DIGEST_MISMATCH' },
    @{ Path = $trustedPlan.driver.operationAdapterPath; Sha256 = $trustedPlan.driver.operationAdapterSha256; Code = 'TASK19_OPERATION_ADAPTER_DIGEST_MISMATCH' },
    @{ Path = $trustedPlan.driver.inventoryAdapterPath; Sha256 = $trustedPlan.driver.inventoryAdapterSha256; Code = 'TASK19_INVENTORY_ADAPTER_DIGEST_MISMATCH' },
    @{ Path = $trustedPlan.driver.processAdapterPath; Sha256 = $trustedPlan.driver.processAdapterSha256; Code = 'TASK19_PROCESS_ADAPTER_DIGEST_MISMATCH' }
)
foreach ($binding in $runtimeBindings) {
    if (!(Test-Path -LiteralPath $binding.Path -PathType Leaf) -or (Get-FileHash -LiteralPath $binding.Path -Algorithm SHA256).Hash.ToLowerInvariant() -ne $binding.Sha256) { throw $binding.Code }
}

$windowsPackage = $trustedPlan.files.controlWindows.path
Assert-PlanFile $trustedPlan.files.controlWindows
$windowsSignature = Get-AuthenticodeSignature -LiteralPath $windowsPackage
if ($windowsSignature.Status -ne 'Valid') { throw 'TASK19_MSIX_SIGNATURE_INVALID' }
$windowsCertificate = $windowsSignature.SignerCertificate.GetCertHashString('SHA256').ToLowerInvariant()
if ($windowsCertificate -ne $trustedPlan.identities.msixCertificateSha256) { throw 'TASK19_MSIX_SIGNING_LINEAGE_MISMATCH' }
$androidPackage = $trustedPlan.files.controlAndroid.path
Assert-PlanFile $trustedPlan.files.controlAndroid
$androidCertificate = (& apksigner verify --verbose --print-certs $androidPackage | Select-String 'Signer #1 certificate SHA-256 digest:' | ForEach-Object { ($_ -split ':', 2)[1].Trim().ToLowerInvariant() })
if ($LASTEXITCODE -ne 0 -or $androidCertificate -ne $trustedPlan.identities.apkLineageSha256) { throw 'TASK19_APK_SIGNING_LINEAGE_MISMATCH' }
Assert-PlanFile $trustedPlan.files.renderer
$rendererCode = Get-MsiProductCode $trustedPlan.files.renderer.path
$preexisting = [Collections.Generic.List[string]]::new()
if (Get-AppxPackage -Name $WindowsPackageName) { $preexisting.Add('msix') }
$androidState = @(& adb.exe shell pm path $AndroidApplicationId)
if ($LASTEXITCODE -ne 0) { throw 'TASK19_ANDROID_PRE_STATE_QUERY_FAILED' }
if ($androidState.Count -ne 0) { $preexisting.Add('android') }
if (Test-MsiProductCodeInstalled $rendererCode) { $preexisting.Add('msi') }
& wsl.exe --exec dpkg-query -W jastreamer-server 2>$null | Out-Null
if ($LASTEXITCODE -eq 0) { $preexisting.Add('wsl') }
elseif ($LASTEXITCODE -gt 1) { throw 'TASK19_WSL_PRE_STATE_QUERY_FAILED' }
if (Get-Process -Name 'jastreamer-server','jastreamer-control','jastreamer-renderer' -ErrorAction SilentlyContinue) { $preexisting.Add('process') }
if ($preexisting.Count -ne 0) { throw "TASK19_PREEXISTING_PRODUCT_STATE:$($preexisting -join ',')" }
$cleanupPath = Join-Path ([IO.Path]::GetDirectoryName([IO.Path]::GetFullPath($Output))) 'cleanup-evidence.json'
$setupClock = [Diagnostics.Stopwatch]::StartNew()
Write-Output '[TASK19_PHASE]setup'
try {
    foreach ($property in $trustedPlan.files.PSObject.Properties) { Assert-PlanFile $property.Value }
    Assert-PhaseDeadline $setupClock $TASK19_SETUP_TIMEOUT_MS 'TASK19_SETUP_TIMEOUT'
    $setupClock.Stop()
    if ((Get-FileHash -LiteralPath $Plan -Algorithm SHA256).Hash.ToLowerInvariant() -ne $planSha256) { throw 'TASK19_PLAN_BINDING_DRIFT' }
    if ((Get-FileHash -LiteralPath $driver -Algorithm SHA256).Hash.ToLowerInvariant() -ne $trustedPlan.driver.sha256) { throw 'TASK19_REPOSITORY_DRIVER_DIGEST_MISMATCH' }
    if ((Get-FileHash -LiteralPath $scenarioRuntime -Algorithm SHA256).Hash.ToLowerInvariant() -ne $trustedPlan.driver.runtimeSha256) { throw 'TASK19_SCENARIO_RUNTIME_DIGEST_MISMATCH' }
    foreach ($binding in $runtimeBindings) {
        if ((Get-FileHash -LiteralPath $binding.Path -Algorithm SHA256).Hash.ToLowerInvariant() -ne $binding.Sha256) { throw $binding.Code }
    }
    Write-Output '[TASK19_PHASE]driver'
    $process = Start-Process -FilePath 'powershell.exe' -ArgumentList @('-NoProfile', '-NonInteractive', '-File', $driver, '-Plan', ([IO.Path]::GetFullPath($Plan)), '-Output', ([IO.Path]::GetFullPath($Output))) -PassThru -NoNewWindow
    $ownedProcesses.Add($process.Id)
    try {
        if (!$process.WaitForExit($TASK19_DRIVER_TIMEOUT_MS)) { throw 'TASK19_EXECUTION_TIMEOUT' }
        if ($process.ExitCode -ne 0) { throw "TASK19_SCENARIO_DRIVER_FAILED:$($process.ExitCode)" }
    }
    finally {
        if (!$process.HasExited) { Stop-TaskProcessTree $process.Id; $process.WaitForExit() }
        $process.Dispose()
    }
    if (!(Test-Path -LiteralPath $Output -PathType Leaf)) { throw 'TASK19_EXECUTION_RESULT_MISSING' }
}
finally {
    Write-Output '[TASK19_PHASE]cleanup'
    $cleanupClock = [Diagnostics.Stopwatch]::StartNew()
    foreach ($id in $ownedProcesses) { try { Stop-TaskProcessTree $id } catch { $cleanupFailures.Add($_.Exception.Message) } }
    $androidRemaining = @(& adb.exe shell pm path $AndroidApplicationId)
    if ($LASTEXITCODE -ne 0) { $cleanupFailures.Add('TASK19_ANDROID_POST_STATE_QUERY_FAILED') }
    elseif ($androidRemaining.Count -ne 0) { try { Invoke-NativeChecked 'adb.exe' @('uninstall', $AndroidApplicationId) 'ANDROID_FAILSAFE_UNINSTALL' (Get-RemainingPhaseMs $cleanupClock $TASK19_CLEANUP_TIMEOUT_MS 'TASK19_CLEANUP_TIMEOUT') } catch { $cleanupFailures.Add($_.Exception.Message) } }
    foreach ($package in @(Get-AppxPackage -Name $WindowsPackageName)) { try { Remove-AppxPackage -Package $package.PackageFullName -ErrorAction Stop } catch { $cleanupFailures.Add($_.Exception.Message) } }
    if (Test-MsiProductCodeInstalled $rendererCode) { try { Invoke-NativeChecked 'msiexec.exe' @('/x', $rendererCode, '/qn', '/norestart') 'MSI_FAILSAFE_UNINSTALL' (Get-RemainingPhaseMs $cleanupClock $TASK19_CLEANUP_TIMEOUT_MS 'TASK19_CLEANUP_TIMEOUT') } catch { $cleanupFailures.Add($_.Exception.Message) } }
    & wsl.exe --exec dpkg-query -W jastreamer-server 2>$null | Out-Null
    if ($LASTEXITCODE -eq 0) { try { Invoke-NativeChecked 'wsl.exe' @('--exec', 'sudo', 'dpkg', '--purge', 'jastreamer-server') 'SERVER_FAILSAFE_UNINSTALL' (Get-RemainingPhaseMs $cleanupClock $TASK19_CLEANUP_TIMEOUT_MS 'TASK19_CLEANUP_TIMEOUT') } catch { $cleanupFailures.Add($_.Exception.Message) } }
    elseif ($LASTEXITCODE -gt 1) { $cleanupFailures.Add('TASK19_WSL_POST_STATE_QUERY_FAILED') }
    & wsl.exe --exec test -e /var/lib/jastreamer
    if ($LASTEXITCODE -eq 0) { try { Invoke-NativeChecked 'wsl.exe' @('--exec', 'sudo', 'rm', '-rf', '/var/lib/jastreamer', '/etc/jastreamer') 'SERVER_STATE_FAILSAFE_CLEANUP' (Get-RemainingPhaseMs $cleanupClock $TASK19_CLEANUP_TIMEOUT_MS 'TASK19_CLEANUP_TIMEOUT') } catch { $cleanupFailures.Add($_.Exception.Message) } }
    elseif ($LASTEXITCODE -gt 1) { $cleanupFailures.Add('TASK19_WSL_STATE_QUERY_FAILED') }
    $remainingProcesses = @(Get-Process -Name 'jastreamer-server','jastreamer-control','jastreamer-renderer' -ErrorAction SilentlyContinue)
    $androidPost = @(& adb.exe shell pm path $AndroidApplicationId)
    if ($LASTEXITCODE -ne 0 -or $androidPost.Count -ne 0) { $cleanupFailures.Add('TASK19_ANDROID_POST_STATE_FAILED') }
    if (Get-AppxPackage -Name $WindowsPackageName) { $cleanupFailures.Add('TASK19_MSIX_POST_STATE_FAILED') }
    if (Test-MsiProductCodeInstalled $rendererCode) { $cleanupFailures.Add("TASK19_MSI_POST_STATE_FAILED:$rendererCode") }
    & wsl.exe --exec dpkg-query -W jastreamer-server 2>$null | Out-Null
    if ($LASTEXITCODE -eq 0 -or $LASTEXITCODE -gt 1) { $cleanupFailures.Add('TASK19_WSL_POST_STATE_FAILED') }
    if ($remainingProcesses.Count -ne 0) { $cleanupFailures.Add('TASK19_CLEANUP_POST_STATE_FAILED') }
    $cleanup = [ordered]@{ schemaVersion = 1; kind = 'task19_cleanup_evidence'; complete = $cleanupFailures.Count -eq 0; packageFullName = $null; packageFamilyName = $null; productCodes = @($rendererCode); packagePaths = @($trustedPlan.files.renderer.path); serverUpgradeCode = '67C753EA-8D02-4F04-B37B-B96682B40F53'; failures = @($cleanupFailures); externalWrites = 0 }
    [IO.File]::WriteAllBytes($cleanupPath, [Text.Encoding]::UTF8.GetBytes(($cleanup | ConvertTo-Json -Depth 5 -Compress)))
    $cleanupEvidenceSha256 = (Get-FileHash -LiteralPath $cleanupPath -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($cleanupClock.ElapsedMilliseconds -ge $TASK19_CLEANUP_TIMEOUT_MS) { $cleanupFailures.Add('TASK19_CLEANUP_TIMEOUT') }
    $cleanupClock.Stop()
    if ($cleanupFailures.Count -ne 0) { throw "TASK19_CLEANUP_FAILED:$($cleanupFailures -join ',')" }
    Write-Output '[TASK19_PHASE]done'
}
$execution = Get-Content -LiteralPath $Output -Raw | ConvertFrom-Json
$execution | Add-Member -NotePropertyName cleanup -NotePropertyValue ([ordered]@{ complete = $true; cleanupEvidenceSha256 = $cleanupEvidenceSha256 }) -Force
[IO.File]::WriteAllText([IO.Path]::GetFullPath($Output), "$(($execution | ConvertTo-Json -Depth 20))`n", [Text.UTF8Encoding]::new($false))
