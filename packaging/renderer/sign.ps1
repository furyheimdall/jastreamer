param([string]$Path, [string]$Pfx, [string]$Password)
$ErrorActionPreference = "Stop"
if (!(Test-Path $Pfx)) { throw "protected PFX is required" }
if (!$env:JASTREAMER_SIGNTOOL -or !(Test-Path $env:JASTREAMER_SIGNTOOL)) {
  throw "pinned SignTool is required"
}
& $env:JASTREAMER_SIGNTOOL sign /fd SHA256 /f $Pfx /p $Password $Path
if ($LASTEXITCODE -ne 0) { throw "SignTool failed: $Path" }

$flags = [Security.Cryptography.X509Certificates.X509KeyStorageFlags]::EphemeralKeySet
$certificate = [Security.Cryptography.X509Certificates.X509Certificate2]::new($Pfx, $Password, $flags)
try {
  $signature = Get-AuthenticodeSignature $Path
  if (!$signature.SignerCertificate) { throw "missing Authenticode signature: $Path" }
  if ($signature.SignerCertificate.GetCertHashString("SHA256") -ne $certificate.GetCertHashString("SHA256")) {
    throw "unexpected Authenticode signer: $Path"
  }
} finally {
  $certificate.Dispose()
}
