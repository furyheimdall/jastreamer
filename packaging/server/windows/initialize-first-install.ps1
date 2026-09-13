$ErrorActionPreference = 'Stop'

$root = [IO.Path]::GetFullPath((Split-Path -Parent $MyInvocation.MyCommand.Path))
$configPath = Join-Path $root 'server.json'
if (Test-Path -LiteralPath $configPath -PathType Leaf) {
    exit 0
}
if (Test-Path -LiteralPath $configPath) {
    throw 'server.json exists but is not a regular file'
}

function Get-SampleSha256([string]$Path) {
    $algorithm = [Security.Cryptography.SHA256]::Create()
    try {
        $inputStream = [IO.File]::OpenRead($Path)
        try {
            return [BitConverter]::ToString($algorithm.ComputeHash($inputStream)).Replace('-', '').ToLowerInvariant()
        } finally {
            $inputStream.Dispose()
        }
    } finally {
        $algorithm.Dispose()
    }
}

$config = Get-Content -Raw -LiteralPath (Join-Path $root 'server.template.json') | ConvertFrom-Json
$config.data_dir = Join-Path $root 'data'
$music = Join-Path $root 'music'
$config.library_roots = @([pscustomobject]@{ id = 'music'; name = 'Music'; path = $music })
foreach ($directory in @($config.data_dir, $music)) {
    $existing = Get-Item -LiteralPath $directory -Force -ErrorAction SilentlyContinue
    if ($null -ne $existing -and (-not $existing.PSIsContainer -or ($existing.Attributes -band [IO.FileAttributes]::ReparsePoint))) {
        throw "refusing to initialize a non-directory or reparse point: $directory"
    }
    [IO.Directory]::CreateDirectory($directory) | Out-Null
}

$sampleSource = Join-Path $root 'samples'
$manifestPath = Join-Path $sampleSource 'manifest.json'
$noticeName = 'THIRD-PARTY-NOTICES.txt'
$trackNames = @('sample-01.mp3', 'sample-02.mp3', 'sample-03.mp3')
if (-not (Test-Path -LiteralPath $manifestPath -PathType Leaf)) {
    throw 'sample manifest is missing'
}
$manifest = Get-Content -Raw -LiteralPath $manifestPath | ConvertFrom-Json
if ($manifest.schema -ne 1 -or @($manifest.tracks).Count -ne 3) {
    throw 'sample manifest schema or track count is invalid'
}
for ($index = 0; $index -lt $trackNames.Count; $index++) {
    $name = $trackNames[$index]
    $track = @($manifest.tracks)[$index]
    $source = Join-Path $sampleSource $name
    if ($track.file -cne $name -or -not (Test-Path -LiteralPath $source -PathType Leaf)) {
        throw "sample manifest entry is invalid: $name"
    }
    $sourceFile = Get-Item -LiteralPath $source
    $sourceHash = Get-SampleSha256 $source
    if ($sourceFile.Length -ne [long]$track.bytes -or $sourceHash -cne [string]$track.sha256) {
        throw "sample does not match manifest: $name"
    }
}
$noticeSource = Join-Path $sampleSource $noticeName
if (-not (Test-Path -LiteralPath $noticeSource -PathType Leaf) -or (Get-Item -LiteralPath $noticeSource).Length -eq 0) {
    throw 'sample source and license notice is missing or empty'
}

$sampleDestination = Join-Path $music 'jastreamer-samples'
$existing = Get-Item -LiteralPath $sampleDestination -Force -ErrorAction SilentlyContinue
if ($null -ne $existing -and (-not $existing.PSIsContainer -or ($existing.Attributes -band [IO.FileAttributes]::ReparsePoint))) {
    throw "refusing to initialize a non-directory or reparse point: $sampleDestination"
}
[IO.Directory]::CreateDirectory($sampleDestination) | Out-Null
$payloadNames = @($trackNames) + @('manifest.json', $noticeName)
foreach ($name in $payloadNames) {
    $source = Join-Path $sampleSource $name
    $target = Join-Path $sampleDestination $name
    if (Test-Path -LiteralPath $target) {
        if (-not (Test-Path -LiteralPath $target -PathType Leaf) -or ((Get-Item -LiteralPath $target -Force).Attributes -band [IO.FileAttributes]::ReparsePoint)) {
            throw "refusing to replace non-file sample destination: $target"
        }
        $sourceFile = Get-Item -LiteralPath $source
        $targetFile = Get-Item -LiteralPath $target
        $sourceHash = Get-SampleSha256 $source
        $targetHash = Get-SampleSha256 $target
        if ($sourceFile.Length -ne $targetFile.Length -or $sourceHash -cne $targetHash) {
            throw "refusing to overwrite different existing sample file: $target"
        }
    }
}
foreach ($name in $payloadNames) {
    $source = Join-Path $sampleSource $name
    $target = Join-Path $sampleDestination $name
    if (Test-Path -LiteralPath $target) {
        if (-not (Test-Path -LiteralPath $target -PathType Leaf) -or ((Get-Item -LiteralPath $target -Force).Attributes -band [IO.FileAttributes]::ReparsePoint)) {
            throw "sample destination changed to a non-file during setup: $target"
        }
        $sourceFile = Get-Item -LiteralPath $source
        $targetFile = Get-Item -LiteralPath $target
        $sourceHash = Get-SampleSha256 $source
        $targetHash = Get-SampleSha256 $target
        if ($sourceFile.Length -ne $targetFile.Length -or $sourceHash -cne $targetHash) {
            throw "sample destination changed during setup: $target"
        }
    } else {
        [IO.File]::Copy($source, $target, $false)
    }
}

$utf8 = New-Object System.Text.UTF8Encoding($false)
$bytes = $utf8.GetBytes(($config | ConvertTo-Json -Depth 10))
$stream = [IO.File]::Open($configPath, [IO.FileMode]::CreateNew, [IO.FileAccess]::Write, [IO.FileShare]::None)
try {
    $stream.Write($bytes, 0, $bytes.Length)
    $stream.Flush()
} finally {
    $stream.Dispose()
}
Write-Host 'Created server.json and data beside the server, with optional sample music under music\jastreamer-samples.'
