param(
  [Parameter(Mandatory = $true)][string]$Version,
  [Parameter(Mandatory = $true)][string]$Out
)

$ErrorActionPreference = "Stop"

if (!$env:JASTREAMER_RENDERER_PFX_PATH -or !$env:JASTREAMER_RENDERER_PFX_PASSWORD) {
  throw "protected renderer signing inputs are required"
}
if (!(Test-Path $env:JASTREAMER_RENDERER_PFX_PATH)) {
  throw "renderer signing PFX was not found"
}

New-Item -ItemType Directory -Force $Out | Out-Null
cargo build --manifest-path apps/renderer/Cargo.toml --release --target x86_64-pc-windows-msvc --target-dir target
$executable = "target/x86_64-pc-windows-msvc/release/jastreamer-renderer.exe"
if (!(Test-Path $executable)) {
  throw "renderer executable was not produced"
}

./packaging/renderer/sign.ps1 `
  -Path $executable `
  -Pfx $env:JASTREAMER_RENDERER_PFX_PATH `
  -Password $env:JASTREAMER_RENDERER_PFX_PASSWORD
./packaging/renderer/build-msi.ps1 -Version $Version -Out $Out
./packaging/renderer/build-portable-zip.ps1 -Version $Version -Out $Out

$msi = Join-Path $Out "jastreamer-renderer_${Version}_windows_amd64.msi"
./packaging/renderer/sign.ps1 `
  -Path $msi `
  -Pfx $env:JASTREAMER_RENDERER_PFX_PATH `
  -Password $env:JASTREAMER_RENDERER_PFX_PASSWORD

$flags = [Security.Cryptography.X509Certificates.X509KeyStorageFlags]::EphemeralKeySet
$certificate = [Security.Cryptography.X509Certificates.X509Certificate2]::new(
  $env:JASTREAMER_RENDERER_PFX_PATH,
  $env:JASTREAMER_RENDERER_PFX_PASSWORD,
  $flags
)
try {
  [IO.File]::WriteAllBytes(
    (Join-Path $Out "certificate.cer"),
    $certificate.Export([Security.Cryptography.X509Certificates.X509ContentType]::Cert)
  )
  "SHA256: $($certificate.GetCertHashString('SHA256'))" |
    Set-Content (Join-Path $Out "certificate-fingerprint.txt")
} finally {
  $certificate.Dispose()
}

Copy-Item packaging/renderer/trust.md,packaging/renderer/remove-trust.md $Out
@{
  supported = @(
    @{ major = 1; capabilities = @("render") },
    @{ major = 2; capabilities = @("render") }
  )
} | ConvertTo-Json -Depth 4 | Set-Content (Join-Path $Out "protocol-range.json")
./packaging/renderer/verify.ps1 -Dist $Out
