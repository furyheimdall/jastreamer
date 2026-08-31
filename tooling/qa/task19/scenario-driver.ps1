[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$Plan,
    [Parameter(Mandatory = $true)][string]$Output
)

$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $true
$trustedPlan = Get-Content -LiteralPath $Plan -Raw | ConvertFrom-Json
if ($trustedPlan.schemaVersion -ne 2 -or $trustedPlan.kind -ne 'task19_trusted_execution_plan' -or $trustedPlan.driver.contract -ne 'task19-installed-scenario-driver-v1') { throw 'TASK19_TRUSTED_PLAN_INVALID' }
if ($trustedPlan.qualificationReady -ne $true) { throw 'TASK19_NATIVE_QUALIFICATION_PENDING' }
$runtime = Join-Path $PSScriptRoot 'scenario-runtime.mjs'
if ((Get-FileHash -LiteralPath $runtime -Algorithm SHA256).Hash.ToLowerInvariant() -ne $trustedPlan.driver.runtimeSha256) { throw 'TASK19_SCENARIO_RUNTIME_DIGEST_MISMATCH' }
& node.exe $runtime --plan ([IO.Path]::GetFullPath($Plan)) --output ([IO.Path]::GetFullPath($Output))
if ($LASTEXITCODE -ne 0) { throw "TASK19_SCENARIO_RUNTIME_FAILED:$LASTEXITCODE" }
if (!(Test-Path -LiteralPath $Output -PathType Leaf)) { throw 'TASK19_EXECUTION_RESULT_MISSING' }
