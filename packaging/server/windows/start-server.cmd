@echo off
setlocal DisableDelayedExpansion
cd /d "%~dp0"
set "JASTREAMER_PORTABLE_ROOT=%~dp0"
powershell.exe -NoLogo -NoProfile -NonInteractive -Command "$ErrorActionPreference='Stop'; $root=[IO.Path]::GetFullPath($env:JASTREAMER_PORTABLE_ROOT); $configPath=Join-Path $root 'server.json'; if (-not (Test-Path -LiteralPath $configPath -PathType Leaf)) { if (Test-Path -LiteralPath $configPath) { throw 'server.json exists but is not a regular file' }; $config=Get-Content -Raw -LiteralPath (Join-Path $root 'server.template.json') | ConvertFrom-Json; $config.data_dir=Join-Path $root 'data'; $music=Join-Path $root 'music'; $config.library_roots=@([pscustomobject]@{id='music';name='Music';path=$music}); [IO.Directory]::CreateDirectory($config.data_dir) | Out-Null; [IO.Directory]::CreateDirectory($music) | Out-Null; $utf8=New-Object System.Text.UTF8Encoding($false); $bytes=$utf8.GetBytes(($config | ConvertTo-Json -Depth 10)); $stream=[IO.File]::Open($configPath,[IO.FileMode]::CreateNew,[IO.FileAccess]::Write,[IO.FileShare]::None); try { $stream.Write($bytes,0,$bytes.Length); $stream.Flush() } finally { $stream.Dispose() }; Write-Host 'Created server.json, data, and music beside the server.' }"
if errorlevel 1 goto setup_failed
"%~dp0jastreamer-server.exe" --check-config "%~dp0server.json"
if errorlevel 1 goto setup_failed
echo.
echo jastreamer Windows x64 portable Server 0.2.0
echo Unsigned public preview; not production-qualified.
echo Default first-run URL: http://127.0.0.1:18080. For custom listeners, use the configured address.
echo AirPlay and FFmpeg transcoding are not bundled.
echo Press Ctrl+C to stop. Keep this window open while using the server.
echo.
"%~dp0jastreamer-server.exe" --config "%~dp0server.json"
set "JASTREAMER_SERVER_EXIT=%ERRORLEVEL%"
if not "%JASTREAMER_SERVER_EXIT%"=="0" if not defined JASTREAMER_NO_PAUSE pause
exit /b %JASTREAMER_SERVER_EXIT%
:setup_failed
echo Setup or configuration validation failed. Existing configuration and data were not replaced.
if not defined JASTREAMER_NO_PAUSE pause
exit /b 1
