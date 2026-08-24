param([string]$Version, [string]$Out = "dist")
$ErrorActionPreference = "Stop"
New-Item -ItemType Directory -Force $Out | Out-Null
$stage = Join-Path $env:TEMP "jastreamer-renderer-$Version"
Remove-Item $stage -Recurse -Force -ErrorAction SilentlyContinue; New-Item $stage -ItemType Directory | Out-Null
Copy-Item "target/x86_64-pc-windows-msvc/release/jastreamer-renderer.exe" $stage
Copy-Item packaging/renderer/README.md $stage
Compress-Archive "$stage/*" "$Out/jastreamer-renderer_${Version}_windows_amd64_diagnostic.zip" -Force
