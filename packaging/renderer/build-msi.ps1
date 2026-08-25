param([string]$Version, [string]$Out = "dist")
$ErrorActionPreference = "Stop"
New-Item -ItemType Directory -Force $Out | Out-Null
$exe = "target/x86_64-pc-windows-msvc/release/jastreamer-renderer.exe"
if (!(Test-Path $exe)) { throw "renderer executable was not produced" }
# WiX is a build-only tool: its binaries and intermediate files never enter the MSI.
$msi = "$Out/jastreamer-renderer_${Version}_windows_amd64.msi"
wix build packaging/renderer/renderer.wxs -arch x64 -d "ProductVersion=$Version" -d "SourceExe=$exe" -o $msi
if ($LASTEXITCODE -ne 0 -or !(Test-Path $msi)) { throw "Renderer WiX MSI build failed" }
