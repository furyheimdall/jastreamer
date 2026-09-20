@echo off
setlocal
powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File "%~dp0Collect-JastreamerAndroid.ps1" %*
set "RESULT=%ERRORLEVEL%"
if not "%RESULT%"=="0" echo Collection failed. Read the error above; do not change phone settings to force success.
echo.
pause
exit /b %RESULT%
