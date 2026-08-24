param([Parameter(Mandatory=$true)][string]$Version, [string]$Output = "dist/server/windows")
$ErrorActionPreference = "Stop"
$root = (Resolve-Path "$PSScriptRoot/../..").Path
$source = Join-Path $Output "source"
New-Item -ItemType Directory -Force $source | Out-Null
if ((go version) -notmatch 'go1\.25\.6 windows/amd64') { throw "Go 1.25.6 windows/amd64 is required" }
Push-Location "$root/apps/server"
try {
  $env:GOOS="windows"; $env:GOARCH="amd64"; $env:CGO_ENABLED="0"
  go build -trimpath -ldflags "-s -w -X main.productVersion=$Version -X main.sourceRevision=$env:GITHUB_SHA" -o "$root/$source/jstreamer-server-core.exe" ./cmd/jstreamer-server
  go build -trimpath -ldflags "-s -w" -o "$root/$source/jstreamer-server.exe" ../../packaging/server/windows-service.go
} finally { Pop-Location }
Copy-Item "$root/LICENSE" "$source/LICENSE"
Copy-Item "$root/packaging/server/windows-server.json" "$source/server.json"
Copy-Item "$root/apps/server/migrations/*.sql" $source
Copy-Item "$root/packaging/container/THIRD_PARTY_NOTICES" "$source/THIRD_PARTY_NOTICES"
$wixPath = "$env:RUNNER_TEMP/wix"
if (Test-Path $wixPath) { Remove-Item $wixPath -Recurse -Force }
dotnet tool install --tool-path $wixPath wix --version 6.0.2
$wixVersion = (& "$wixPath/wix.exe" --version).Trim()
if ($wixVersion -notmatch '^6\.0\.2(?:\+|$)') { throw "unexpected official WiX version: $wixVersion" }
$signToolPath = $env:JSTREAMER_SIGNTOOL
if (!$signToolPath -or !(Test-Path $signToolPath) -or $signToolPath -notmatch 'Microsoft\.Windows\.SDK\.BuildTools\.10\.0\.26100\.3916') {
  throw "pinned Microsoft.Windows.SDK.BuildTools 10.0.26100.3916 SignTool is required"
}
$signVersion = [Diagnostics.FileVersionInfo]::GetVersionInfo($signToolPath).FileVersion
$dotnetVersion = [Version](dotnet --version)
if ($dotnetVersion -ne [Version]'8.0.419') { throw ".NET SDK 8.0.419 is required" }
@{
  go=(go version); dotnet=$dotnetVersion.ToString(); signTool=$signVersion; signToolPath=$signToolPath; wix=$wixVersion
  wixExeSha256=(Get-FileHash "$wixPath/wix.exe" -Algorithm SHA256).Hash
  classification='native-windows-amd64'; phase='unsigned-inputs'
} | ConvertTo-Json | Set-Content "$Output/windows-tool-receipt.json"
