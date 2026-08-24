param([string]$Dist = "dist")
$ErrorActionPreference = "Stop"
Get-ChildItem $Dist -File | Get-FileHash -Algorithm SHA256 | ForEach-Object { "$($_.Hash)  $($_.Path | Split-Path -Leaf)" } | Set-Content "$Dist/SHA256SUMS"
if (!(Test-Path "$Dist/certificate-fingerprint.txt")) { throw "certificate fingerprint is required" }
