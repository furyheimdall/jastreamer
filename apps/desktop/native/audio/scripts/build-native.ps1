param(
  [ValidateSet("Release", "RelWithDebInfo", "Debug")]
  [string]$Configuration = "Release"
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

if (-not [Environment]::Is64BitOperatingSystem -or $env:PROCESSOR_ARCHITECTURE -ne "AMD64") {
  throw "The native Windows audio helper requires native Windows x64"
}
foreach ($tool in @("node", "cmake", "cl.exe")) {
  if (-not (Get-Command $tool -ErrorAction SilentlyContinue)) {
    throw "Required native build tool is unavailable: $tool"
  }
}

$audioRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$lock = Get-Content (Join-Path $audioRoot "dependency-lock.json") -Raw | ConvertFrom-Json
$bash = if ($env:JASTREAMER_MSYS_BASH) { $env:JASTREAMER_MSYS_BASH } else { "C:\msys64\usr\bin\bash.exe" }
if (-not (Test-Path -LiteralPath $bash -PathType Leaf)) {
  throw "MSYS2 bash was not found at $bash; set JASTREAMER_MSYS_BASH to its absolute path"
}
$env:MSYS2_PATH_TYPE = "inherit"
$env:JASTREAMER_MSVC_BIN = Split-Path -Parent (Get-Command cl.exe).Source
& $bash -lc 'export PATH="$(cygpath -u "$JASTREAMER_MSVC_BIN"):$PATH"; command -v make >/dev/null && command -v nasm >/dev/null && command -v cygpath >/dev/null && command -v cl.exe >/dev/null && command -v lib.exe >/dev/null'
if ($LASTEXITCODE -ne 0) {
  throw "MSYS2 make, nasm, cygpath, and the inherited MSVC tools are required to build FFmpeg"
}

& node (Join-Path $PSScriptRoot "fetch-native-deps.mjs")
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

$ffmpegSource = (Resolve-Path (Join-Path $audioRoot "deps\src\$($lock.ffmpeg.sourceDirectory)")).Path
$ffmpegPrefix = Join-Path $audioRoot "deps\install\ffmpeg"
$recipe = (Resolve-Path (Join-Path $PSScriptRoot "build-ffmpeg.sh")).Path
$env:JASTREAMER_FFMPEG_SOURCE = $ffmpegSource
$env:JASTREAMER_FFMPEG_PREFIX = $ffmpegPrefix
$env:JASTREAMER_FFMPEG_RECIPE = $recipe
$env:JASTREAMER_FFMPEG_TOOLCHAIN = "msvc"
$env:JOBS = if ($env:NUMBER_OF_PROCESSORS) { $env:NUMBER_OF_PROCESSORS } else { "4" }
if (-not $env:SOURCE_DATE_EPOCH) { $env:SOURCE_DATE_EPOCH = "0" }
if ($env:SOURCE_DATE_EPOCH -notmatch "^\d+$") { throw "SOURCE_DATE_EPOCH must be a non-negative integer" }
& $bash -lc 'export PATH="$(cygpath -u "$JASTREAMER_MSVC_BIN"):$PATH"; exec sh "$(cygpath -u "$JASTREAMER_FFMPEG_RECIPE")" "$(cygpath -u "$JASTREAMER_FFMPEG_SOURCE")" "$(cygpath -u "$JASTREAMER_FFMPEG_PREFIX")"'
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

$build = Join-Path $audioRoot "build"
$dist = Join-Path $audioRoot "dist"
Remove-Item -LiteralPath $build -Recurse -Force -ErrorAction SilentlyContinue
Remove-Item -LiteralPath $dist -Recurse -Force -ErrorAction SilentlyContinue

$nlohmannSource = (Resolve-Path (Join-Path $audioRoot "deps\src\$($lock.nlohmannJson.sourceDirectory)")).Path
& cmake -S $audioRoot -B $build -G "Visual Studio 17 2022" -A x64 `
  "-DJASTREAMER_FFMPEG_ROOT=$ffmpegPrefix" `
  "-DJASTREAMER_NLOHMANN_ROOT=$nlohmannSource" `
  "-DBUILD_TESTING=ON"
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

& cmake --build $build --config $Configuration --parallel --target `
  jastreamer-audio `
  jastreamer-decoder-smoke `
  jastreamer-decoder-behavior `
  jastreamer-packing-test `
  jastreamer-protocol-smoke
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

$expectedRuntime = @("jastreamer-audio.exe") + @($lock.ffmpeg.runtime)
$actualRuntime = @(Get-ChildItem -LiteralPath $dist -File | ForEach-Object Name | Sort-Object)
$expectedRuntime = @($expectedRuntime | Sort-Object)
if (Compare-Object -ReferenceObject $expectedRuntime -DifferenceObject $actualRuntime) {
  throw "Native dist inventory does not match the locked helper and FFmpeg DLL set"
}
Write-Host "Built native helper: $(Join-Path $dist 'jastreamer-audio.exe')"
