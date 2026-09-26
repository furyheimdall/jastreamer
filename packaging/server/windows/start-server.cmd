@echo off
setlocal DisableDelayedExpansion
cd /d "%~dp0"
powershell.exe -NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -File "%~dp0initialize-first-install.ps1"
if errorlevel 1 goto setup_failed
"%~dp0jastreamer-server.exe" --check-config "%~dp0server.json"
if errorlevel 1 goto setup_failed
echo.
echo jastreamer Windows x64 portable Server 0.2.1
echo Not Authenticode-signed; verify the published SHA-256 before use.
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
