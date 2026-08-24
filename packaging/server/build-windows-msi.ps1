param(
  [Parameter(Mandatory=$true)][string]$Version,
  [Parameter(Mandatory=$true)][string]$Directory,
  [Parameter(Mandatory=$true)][string]$Cer
)
$ErrorActionPreference = "Stop"
$root = (Resolve-Path "$PSScriptRoot/../..").Path
$source = (Resolve-Path "$Directory/source").Path
$wix = "$env:RUNNER_TEMP/wix/wix.exe"
if (!(Test-Path $wix)) { throw "pinned official WiX is unavailable" }
$wixVersion = (& $wix --version).Trim()
if ($wixVersion -notmatch '^6\.0\.2(?:\+|$)') { throw "official WiX 6.0.2 is required" }
foreach ($name in @('jstreamer-server.exe','jstreamer-server-core.exe')) {
  $signature = Get-AuthenticodeSignature "$source/$name"
  if (!$signature.SignerCertificate) { throw "$name must be signed before wix build" }
}
$trustId = (Get-PfxCertificate $Cer).Thumbprint
$msi = "$Directory/jstreamer-server_${Version}_windows_amd64.msi"
& $wix build "$root/packaging/server/server.wxs" -arch x64 -d "Version=$Version" -d "SourceDir=$source" -d "CertThumbprint=$trustId" -o $msi
if ($LASTEXITCODE -ne 0 -or !(Test-Path $msi)) { throw "official WiX MSI build failed" }
