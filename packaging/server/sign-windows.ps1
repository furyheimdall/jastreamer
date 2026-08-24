param(
  [Parameter(Mandatory=$true)][string]$Version,
  [Parameter(Mandatory=$true)][string]$Directory,
  [Parameter(Mandatory=$true)][string]$Cer
)
$ErrorActionPreference = "Stop"
if (!$env:WINDOWS_SIGNING_PFX_B64 -or !$env:WINDOWS_SIGNING_PFX_PASSWORD) { throw "protected signing secrets are required" }
if (!$env:JASTREAMER_SIGNTOOL -or !(Test-Path $env:JASTREAMER_SIGNTOOL)) { throw "pinned SignTool is required" }
$pfx = Join-Path $env:RUNNER_TEMP "jastreamer-server-signing.pfx"
$trustedPath = $null
$machineSecretSet = $false
$pfxCertificate = $null
$publishedCertificate = $null
function CertificateSha256($certificate) {
  [Convert]::ToHexString([Security.Cryptography.SHA256]::HashData($certificate.RawData))
}
function AssertExpectedSignature([string]$path, [string]$expected) {
  $signature = Get-AuthenticodeSignature $path
  if (!$signature.SignerCertificate) { throw "missing Authenticode signature: $path" }
  if ((CertificateSha256 $signature.SignerCertificate) -ne $expected) { throw "unexpected signer certificate: $path" }
  $signature
}
try {
  [IO.File]::WriteAllBytes($pfx, [Convert]::FromBase64String($env:WINDOWS_SIGNING_PFX_B64))
  $publishedCertificate = [Security.Cryptography.X509Certificates.X509Certificate2]::new((Resolve-Path $Cer).Path)
  $expected = CertificateSha256 $publishedCertificate
  $flags = [Security.Cryptography.X509Certificates.X509KeyStorageFlags]::EphemeralKeySet
  $pfxCertificate = [Security.Cryptography.X509Certificates.X509Certificate2]::new(
    $pfx, $env:WINDOWS_SIGNING_PFX_PASSWORD, $flags
  )
  if ((CertificateSha256 $pfxCertificate) -ne $expected) { throw "PFX does not match published certificate" }
  $sourceExecutables = (Get-ChildItem "$Directory/source/*.exe").FullName
  if ($sourceExecutables.Count -ne 2) { throw "expected unsigned SCM host and core inputs" }
  foreach ($exe in $sourceExecutables) {
    & $env:JASTREAMER_SIGNTOOL sign /fd SHA256 /f $pfx /p $env:WINDOWS_SIGNING_PFX_PASSWORD $exe
    AssertExpectedSignature $exe $expected | Out-Null
  }
  $published = "$Directory/jastreamer-server_${Version}_windows_amd64.exe"
  Copy-Item "$Directory/source/jastreamer-server-core.exe" $published
  AssertExpectedSignature $published $expected | Out-Null

  & "$PSScriptRoot/build-windows-msi.ps1" -Version $Version -Directory $Directory -Cer $Cer
  $msi = "$Directory/jastreamer-server_${Version}_windows_amd64.msi"
  & $env:JASTREAMER_SIGNTOOL sign /fd SHA256 /f $pfx /p $env:WINDOWS_SIGNING_PFX_PASSWORD $msi
  AssertExpectedSignature $msi $expected | Out-Null

  $extract = Join-Path $env:RUNNER_TEMP "jastreamer-msi-extract"
  Remove-Item $extract -Recurse -Force -ErrorAction SilentlyContinue
  New-Item -ItemType Directory $extract | Out-Null
  & "$env:RUNNER_TEMP/wix/wix.exe" msi decompile $msi -x $extract -o "$extract/decompiled.wxs"
  if ($LASTEXITCODE -ne 0) { throw "MSI extraction failed" }
  $extractedExecutables = (Get-ChildItem $extract -Recurse -Filter '*.exe').FullName
  if ($extractedExecutables.Count -ne 2) { throw "MSI must embed exactly signed host and core EXEs" }
  foreach ($exe in $extractedExecutables) { AssertExpectedSignature $exe $expected | Out-Null }

  $allSigned = @($published) + $sourceExecutables + @($msi) + $extractedExecutables
  $beforeTrust = Get-AuthenticodeSignature $allSigned
  if (($beforeTrust | Where-Object Status -eq Valid).Count -ne 0) { throw "self-signed artifacts unexpectedly had Public Trust" }
  if (Get-Service jastreamer-server -ErrorAction SilentlyContinue) { throw "service existed before clean install" }
  $blocked = Start-Process msiexec.exe -ArgumentList @('/i', $msi, '/qn', '/norestart') -Wait -PassThru
  if ($blocked.ExitCode -eq 0 -or (Get-Service jastreamer-server -ErrorAction SilentlyContinue)) { throw "untrusted MSI install was not blocked cleanly" }

  $imported = Import-Certificate -FilePath $Cer -CertStoreLocation Cert:\LocalMachine\TrustedPeople
  $trustedPath = "Cert:\LocalMachine\TrustedPeople\$($imported.Thumbprint)"
  foreach ($path in $allSigned) { & $env:JASTREAMER_SIGNTOOL verify /pa /all /v $path }
  $afterTrust = Get-AuthenticodeSignature $allSigned
  if (($afterTrust | Where-Object Status -ne Valid).Count -ne 0) { throw "signature invalid after explicit trust" }

  $install = Start-Process msiexec.exe -ArgumentList @('/i', $msi, '/qn', '/norestart') -Wait -PassThru
  if ($install.ExitCode -ne 0) { throw "trusted MSI install failed: $($install.ExitCode)" }
  $serviceKey = 'HKLM:\SYSTEM\CurrentControlSet\Services\jastreamer-server'
  New-ItemProperty -Path $serviceKey -Name Environment -PropertyType MultiString -Value @('JASTREAMER_SETUP_SECRET=windows-native-release-smoke') -Force | Out-Null
  $machineSecretSet = $true
  $service = [ServiceProcess.ServiceController]::new('jastreamer-server')
  $service.Start()
  $service.WaitForStatus([ServiceProcess.ServiceControllerStatus]::Running, [TimeSpan]::FromSeconds(30))
  if ($service.Status -ne [ServiceProcess.ServiceControllerStatus]::Running) { throw "service did not reach Running" }
  $health = Invoke-WebRequest https://127.0.0.1:8443/healthz -SkipCertificateCheck -UseBasicParsing
  if ($health.StatusCode -ne 200 -or $health.Content.Trim() -ne '{"status":"ready"}') { throw "native HTTPS health check failed" }
  $process = Get-CimInstance Win32_Service -Filter "Name='jastreamer-server'"
  if (!$process -or $process.StartName -ne 'LocalSystem' -or $process.ProcessId -eq 0) { throw "running SCM service evidence invalid" }
  $service.Stop(); $service.WaitForStatus([ServiceProcess.ServiceControllerStatus]::Stopped, [TimeSpan]::FromSeconds(30))
  Remove-ItemProperty -Path $serviceKey -Name Environment -Force
  $machineSecretSet = $false
  $uninstall = Start-Process msiexec.exe -ArgumentList @('/x', $msi, '/qn', '/norestart') -Wait -PassThru
  if ($uninstall.ExitCode -ne 0 -or (Get-Service jastreamer-server -ErrorAction SilentlyContinue)) { throw "MSI uninstall/service removal failed" }

  @{ classification='native-windows-amd64'; signingOrder=@('source-host','source-core','wix-build','outer-msi'); expectedCertificateSha256=$expected
    signedPaths=$allSigned; blockedUntrustedInstallExit=$blocked.ExitCode; trustedInstallExit=$install.ExitCode; serviceStatus='Running'
    serviceProcessId=$process.ProcessId; healthStatus=$health.StatusCode; healthBody=$health.Content.Trim(); uninstallExit=$uninstall.ExitCode
  } | ConvertTo-Json -Depth 5 | Set-Content "$Directory/windows-signature-inspection.json"
  $installer = New-Object -ComObject WindowsInstaller.Installer
  $db = $installer.GetType().InvokeMember('OpenDatabase','InvokeMethod',$null,$installer,@($msi,0))
  $view = $db.GetType().InvokeMember('OpenView','InvokeMethod',$null,$db,@('SELECT `Name`,`StartName` FROM `ServiceInstall`'))
  $view.GetType().InvokeMember('Execute','InvokeMethod',$null,$view,$null)
  $record = $view.GetType().InvokeMember('Fetch','InvokeMethod',$null,$view,$null)
  @{ inspector='WindowsInstaller COM'; service=$record.StringData(1); account=$record.StringData(2); architecture='amd64'; extractedExeSignatures=2 } | ConvertTo-Json | Set-Content "$Directory/windows-msi-inspection.json"
} finally {
  $cleanupFailures = [Collections.Generic.List[Exception]]::new()
  if ($publishedCertificate) { try { $publishedCertificate.Dispose() } catch { $cleanupFailures.Add($_.Exception) } }
  if ($pfxCertificate) { try { $pfxCertificate.Dispose() } catch { $cleanupFailures.Add($_.Exception) } }
  if (Test-Path $pfx) { try { Remove-Item $pfx -Force } catch { $cleanupFailures.Add($_.Exception) } }
  if (Test-Path $pfx) { $cleanupFailures.Add([Exception]::new("PFX still present")) }
  if (Get-Service jastreamer-server -ErrorAction SilentlyContinue) {
    try {
      if (Test-Path "$Directory/jastreamer-server_${Version}_windows_amd64.msi") {
        $cleanupUninstall = Start-Process msiexec.exe -ArgumentList @('/x', "$Directory/jastreamer-server_${Version}_windows_amd64.msi", '/qn', '/norestart') -Wait -PassThru
        if ($cleanupUninstall.ExitCode -ne 0) { throw "cleanup uninstall failed: $($cleanupUninstall.ExitCode)" }
      }
    } catch { $cleanupFailures.Add($_.Exception) }
  }
  if (Get-Service jastreamer-server -ErrorAction SilentlyContinue) { $cleanupFailures.Add([Exception]::new("service still installed")) }
  if ($machineSecretSet -and (Test-Path 'HKLM:\SYSTEM\CurrentControlSet\Services\jastreamer-server')) {
    try { Remove-ItemProperty -Path 'HKLM:\SYSTEM\CurrentControlSet\Services\jastreamer-server' -Name Environment -Force } catch { $cleanupFailures.Add($_.Exception) }
  }
  if ($trustedPath -and (Test-Path $trustedPath)) { try { Remove-Item $trustedPath -Force } catch { $cleanupFailures.Add($_.Exception) } }
  if ($trustedPath -and (Test-Path $trustedPath)) { $cleanupFailures.Add([Exception]::new("certificate trust still present")) }
  try {
    Remove-Item Env:WINDOWS_SIGNING_PFX_B64, Env:WINDOWS_SIGNING_PFX_PASSWORD -ErrorAction SilentlyContinue
  } catch { $cleanupFailures.Add($_.Exception) }
  if (Test-Path Env:WINDOWS_SIGNING_PFX_B64) { $cleanupFailures.Add([Exception]::new("PFX environment still present")) }
  if ($cleanupFailures.Count) { throw [AggregateException]::new("Windows cleanup failures", $cleanupFailures) }
}
