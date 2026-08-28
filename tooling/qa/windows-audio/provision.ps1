[CmdletBinding()]
param(
  [Parameter(Mandatory)][string]$RendererArchive,
  [Parameter(Mandatory)][string]$RendererExecutableSha256File,
  [Parameter(Mandatory)][string]$ProbeExecutable,
  [Parameter(Mandatory)][string]$ProbeSha256File,
  [Parameter(Mandatory)][string]$ScenarioDriverArchive,
  [Parameter(Mandatory)][string]$ScenarioDriverSha256File,
  [Parameter(Mandatory)][string]$ServerPeerExecutable,
  [Parameter(Mandatory)][string]$ServerPeerSha256File,
  [Parameter(Mandatory)][string]$ServerPeerInput,
  [Parameter(Mandatory)][string]$ServerPeerInputSha256File,
  [Parameter(Mandatory)][string]$MediaFixturesArchive,
  [Parameter(Mandatory)][string]$MediaFixturesSha256File,
  [Parameter(Mandatory)][string]$MediaManifestSha256File,
  [Parameter(Mandatory)][string]$SourceSha256File,
  [Parameter(Mandatory)][string]$RendererContract,
  [Parameter(Mandatory)][string]$PeerSet,
  [Parameter(Mandatory)][string]$Candidate,
  [Parameter(Mandatory)][string]$CandidateManifest,
  [Parameter(Mandatory)][string]$Output,
  [string[]]$RunnerLabels = @(),
  [switch]$EndpointIdProtected
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$requiredLabels = @('self-hosted', 'windows', 'x64', 'jastreamer-audio')
$root = Resolve-Path (Join-Path $PSScriptRoot '../../..')
$temp = Join-Path ([IO.Path]::GetTempPath()) ("jastreamer-windows-audio-{0}" -f [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $temp | Out-Null
$tempOwned = $true
$token = $null
$endpointId = $null
$rsa = $null
$certificate = $null
$portReservation = $null
try {
  $guard = Join-Path $temp 'publication.json'
  & bun (Join-Path $root 'tooling/release/publication-guard-cli.ts') `
    --component renderer --event push --manifest $CandidateManifest --output $guard
  if ($LASTEXITCODE -ne 65) { throw 'PUBLICATION_DENIAL_UNVERIFIED' }
  $publication = Get-Content $guard -Raw | ConvertFrom-Json
  if ($publication.code -ne 'PRODUCT_GATE_REQUIRED' -or $publication.external_writes.Count -ne 0) {
    throw 'PUBLICATION_DENIAL_UNVERIFIED'
  }
  $candidateRecord = Get-Content $Candidate -Raw | ConvertFrom-Json
  $manifestDigest = (Get-FileHash $CandidateManifest -Algorithm SHA256).Hash.ToLowerInvariant()
  if ($manifestDigest -ne $candidateRecord.candidate.manifest_sha256) { throw 'CANDIDATE_MANIFEST_DIGEST_MISMATCH' }
  $manifest = Get-Content $CandidateManifest -Raw | ConvertFrom-Json
  $stagedInputs = @(
    $RendererArchive, $RendererExecutableSha256File, $ProbeExecutable, $ProbeSha256File,
    $ScenarioDriverArchive, $ScenarioDriverSha256File, $ServerPeerExecutable,
    $ServerPeerSha256File, $ServerPeerInput, $ServerPeerInputSha256File,
    $MediaFixturesArchive, $MediaFixturesSha256File, $MediaManifestSha256File, $SourceSha256File
  )
  foreach ($path in $stagedInputs) {
    $name = Split-Path $path -Leaf
    $record = @($manifest.artifacts | Where-Object { $_.name -eq $name })
    if ($record.Count -ne 1 -or (Get-FileHash $path -Algorithm SHA256).Hash.ToLowerInvariant() -ne $record[0].sha256) {
      throw "STAGED_MANIFEST_MISMATCH:$name"
    }
  }

  $digestInputs = [ordered]@{
    renderer_executable_sha256 = (Get-Content $RendererExecutableSha256File -Raw).Trim().ToLowerInvariant()
    probe_executable_sha256 = (Get-Content $ProbeSha256File -Raw).Trim().ToLowerInvariant()
    scenario_driver_sha256 = (Get-Content $ScenarioDriverSha256File -Raw).Trim().ToLowerInvariant()
    server_peer_sha256 = (Get-Content $ServerPeerSha256File -Raw).Trim().ToLowerInvariant()
    server_peer_input_sha256 = (Get-Content $ServerPeerInputSha256File -Raw).Trim().ToLowerInvariant()
    media_fixture_archive_sha256 = (Get-Content $MediaFixturesSha256File -Raw).Trim().ToLowerInvariant()
    media_fixture_manifest_sha256 = (Get-Content $MediaManifestSha256File -Raw).Trim().ToLowerInvariant()
    source_sha256 = (Get-Content $SourceSha256File -Raw).Trim().ToLowerInvariant()
  }
  foreach ($entry in $digestInputs.GetEnumerator()) {
    if ($entry.Value -notmatch '^[0-9a-f]{64}$') { throw "STAGED_DIGEST_INVALID:$($entry.Key)" }
  }
  $binding = [ordered]@{
    renderer_executable_sha256 = $digestInputs.renderer_executable_sha256
    probe_executable_sha256 = $digestInputs.probe_executable_sha256
    scenario_driver_sha256 = $digestInputs.scenario_driver_sha256
    server_peer_sha256 = $digestInputs.server_peer_sha256
    server_peer_input_sha256 = $digestInputs.server_peer_input_sha256
    media_fixture_archive_sha256 = $digestInputs.media_fixture_archive_sha256
    media_fixture_manifest_sha256 = $digestInputs.media_fixture_manifest_sha256
    source_sha256 = $digestInputs.source_sha256
    renderer_contract_sha256 = (Get-FileHash $RendererContract -Algorithm SHA256).Hash.ToLowerInvariant()
    peer_set_sha256 = (Get-FileHash $PeerSet -Algorithm SHA256).Hash.ToLowerInvariant()
    candidate_sha256 = (Get-FileHash $Candidate -Algorithm SHA256).Hash.ToLowerInvariant()
    endpoint_identity_sha256 = $null
  }
  $bindingPath = Join-Path $temp 'binding.json'
  $recordedAt = [DateTimeOffset]::UtcNow.ToString('O')
  $runnerLabelsValid = $RunnerLabels.Count -eq @($RunnerLabels | Select-Object -Unique).Count -and
    -not ($RunnerLabels | Where-Object { [string]::IsNullOrWhiteSpace($_) })
  $authorizedLabels = $runnerLabelsValid -and -not ($requiredLabels | Where-Object { $_ -notin $RunnerLabels })
  $endpointId = [Environment]::GetEnvironmentVariable('JASTREAMER_QA_ENDPOINT_ID')
  $authorized = $authorizedLabels -and $EndpointIdProtected -and -not [string]::IsNullOrEmpty($endpointId)
  if (-not $authorized) {
    $binding | ConvertTo-Json | Set-Content -Encoding utf8NoBOM $bindingPath
    & bun (Join-Path $root 'tooling/qa/windows-audio/cli.mjs') pending `
      --binding $bindingPath --publication $guard --recorded-at $recordedAt --output $Output
    if ($LASTEXITCODE -ne 0) { throw 'PENDING_RECEIPT_INVALID' }
    return
  }
  if (-not $IsWindows) { throw 'NATIVE_WINDOWS_REQUIRED' }

  $expanded = Join-Path $temp 'renderer'
  Expand-Archive -LiteralPath $RendererArchive -DestinationPath $expanded
  $renderers = @(Get-ChildItem $expanded -Recurse -Filter 'jastreamer-renderer.exe')
  if ($renderers.Count -ne 1) { throw 'STAGED_RENDERER_EXECUTABLE_MISSING' }
  $renderer = $renderers[0].FullName
  $verified = [ordered]@{
    RENDERER_DIGEST_MISMATCH = @($renderer, $binding.renderer_executable_sha256)
    PROBE_DIGEST_MISMATCH = @($ProbeExecutable, $binding.probe_executable_sha256)
    DRIVER_DIGEST_MISMATCH = @($ScenarioDriverArchive, $binding.scenario_driver_sha256)
    SERVER_PEER_DIGEST_MISMATCH = @($ServerPeerExecutable, $binding.server_peer_sha256)
    SERVER_PEER_INPUT_DIGEST_MISMATCH = @($ServerPeerInput, $binding.server_peer_input_sha256)
    MEDIA_FIXTURE_ARCHIVE_DIGEST_MISMATCH = @($MediaFixturesArchive, $binding.media_fixture_archive_sha256)
  }
  foreach ($entry in $verified.GetEnumerator()) {
    if ((Get-FileHash $entry.Value[0] -Algorithm SHA256).Hash.ToLowerInvariant() -ne $entry.Value[1]) {
      throw $entry.Key
    }
  }

  $endpointBytes = [Text.Encoding]::UTF8.GetBytes($endpointId)
  try {
    $binding.endpoint_identity_sha256 = [Convert]::ToHexString(
      [Security.Cryptography.SHA256]::HashData($endpointBytes)).ToLowerInvariant()
  } finally { [Array]::Clear($endpointBytes, 0, $endpointBytes.Length) }
  $binding | ConvertTo-Json | Set-Content -Encoding utf8NoBOM $bindingPath

  $driverRoot = Join-Path $temp 'driver'
  $mediaRoot = Join-Path $temp 'media'
  Expand-Archive -LiteralPath $ScenarioDriverArchive -DestinationPath $driverRoot
  Expand-Archive -LiteralPath $MediaFixturesArchive -DestinationPath $mediaRoot
  $mediaManifestPath = Join-Path $mediaRoot 'fixture-manifest.json'
  if ((Get-FileHash $mediaManifestPath -Algorithm SHA256).Hash.ToLowerInvariant() -ne
      $binding.media_fixture_manifest_sha256) { throw 'MEDIA_FIXTURE_MANIFEST_DIGEST_MISMATCH' }
  $mediaManifest = Get-Content $mediaManifestPath -Raw | ConvertFrom-Json
  foreach ($file in $mediaManifest.files) {
    $mediaPath = Join-Path $mediaRoot $file.name
    if ((Get-FileHash $mediaPath -Algorithm SHA256).Hash.ToLowerInvariant() -ne $file.sha256) {
      throw "MEDIA_FIXTURE_DIGEST_MISMATCH:$($file.name)"
    }
  }
  $ScenarioDriver = Join-Path $driverRoot 'windows-audio-scenario-driver.mjs'
  if (!(Test-Path $ScenarioDriver)) { throw 'BOUND_DRIVER_INCOMPLETE' }
  $serverPeer = (Resolve-Path $ServerPeerExecutable).Path

  $rsa = [Security.Cryptography.RSA]::Create(2048)
  $request = [Security.Cryptography.X509Certificates.CertificateRequest]::new(
    'CN=127.0.0.1', $rsa, [Security.Cryptography.HashAlgorithmName]::SHA256,
    [Security.Cryptography.RSASignaturePadding]::Pkcs1)
  $certificate = $request.CreateSelfSigned([DateTimeOffset]::UtcNow.AddMinutes(-1), [DateTimeOffset]::UtcNow.AddHours(1))
  $certificatePath = Join-Path $temp 'peer-cert.pem'
  $privateKeyPath = Join-Path $temp 'peer-key.pem'
  [IO.File]::WriteAllText($certificatePath, $certificate.ExportCertificatePem())
  [IO.File]::WriteAllText($privateKeyPath, $rsa.ExportPkcs8PrivateKeyPem())
  $token = [Convert]::ToHexString([Security.Cryptography.RandomNumberGenerator]::GetBytes(32)).ToLowerInvariant()
  $peerTemplate = Get-Content $ServerPeerInput -Raw | ConvertFrom-Json
  if ($peerTemplate.stable_loopback_port_required -ne $true) { throw 'STABLE_PEER_PORT_REQUIRED' }
  $portReservation = [Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback, 0)
  $portReservation.Start()
  $peerPort = ([Net.IPEndPoint]$portReservation.LocalEndpoint).Port
  $portReservation.Stop(); $portReservation.Dispose(); $portReservation = $null
  $runtimeConfig = [ordered]@{
    renderer_executable = $renderer; probe_executable = (Resolve-Path $ProbeExecutable).Path
    server_peer = $serverPeer; server_peer_config = (Join-Path $temp 'peer-runtime.json')
    work_directory = (Join-Path $temp 'run'); endpoint_id = $endpointId; token = $token
    recorded_at = $recordedAt; runner_labels = $RunnerLabels; binding = $binding
    peer_origin = "https://127.0.0.1:$peerPort"
  }
  $peerRuntime = [ordered]@{
    schema_version = $peerTemplate.schema_version; port = $peerPort
    certificate = $certificatePath; private_key = $privateKeyPath
    fingerprint = [Convert]::ToHexString([Security.Cryptography.SHA256]::HashData($certificate.RawData)).ToLowerInvariant()
    token = $token; media_directory = $mediaRoot; scenarios = $peerTemplate.scenarios
  }
  $peerRuntime | ConvertTo-Json -Depth 8 | Set-Content -Encoding utf8NoBOM $runtimeConfig.server_peer_config
  $configPath = Join-Path $temp 'runtime.json'
  $runtimeConfig | ConvertTo-Json -Depth 8 | Set-Content -Encoding utf8NoBOM $configPath
  $generated = Join-Path $temp 'qualification.json'
  & bun $ScenarioDriver --config $configPath --output $generated
  if ($LASTEXITCODE -ne 0 -or !(Test-Path $generated)) { throw 'SCENARIO_DRIVER_NOT_EXECUTED' }
  $token = $null; $endpointId = $null
  $certificate.Dispose(); $certificate = $null
  $rsa.Dispose(); $rsa = $null
  if (Test-Path Env:JASTREAMER_QA_ENDPOINT_ID) { Remove-Item Env:JASTREAMER_QA_ENDPOINT_ID }
  $finalization = @(& bun (Join-Path $root 'tooling/qa/windows-audio/finalize-qualification.mjs') `
    --evidence $generated --binding $bindingPath --workspace $temp `
    --recorded-at $recordedAt --output $Output)
  if ($finalization -contains 'WORKSPACE_REMOVED') { $tempOwned = $false }
  if ($LASTEXITCODE -ne 0) { throw 'QUALIFICATION_FINALIZATION_FAILED' }
} finally {
  $token = $null; $endpointId = $null
  if ($null -ne $portReservation) { $portReservation.Stop(); $portReservation.Dispose() }
  if ($null -ne $certificate) { $certificate.Dispose() }
  if ($null -ne $rsa) { $rsa.Dispose() }
  if (Test-Path Env:JASTREAMER_QA_ENDPOINT_ID) { Remove-Item Env:JASTREAMER_QA_ENDPOINT_ID }
  if ($tempOwned) { Remove-Item $temp -Recurse -Force }
}
