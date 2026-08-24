param([string]$Version, [string]$Out = "dist")
$ErrorActionPreference = "Stop"
New-Item -ItemType Directory -Force $Out | Out-Null
$exe = "target/x86_64-pc-windows-msvc/release/jastreamer-renderer.exe"
if (!(Test-Path $exe)) { throw "renderer executable was not produced" }
# WiX is a build-only tool: its binaries and intermediate files never enter the MSI.
wix build packaging/renderer/renderer.wxs -dProductVersion=$Version -dSourceExe=$exe -o "$Out/jastreamer-renderer_${Version}_windows_amd64.msi"
