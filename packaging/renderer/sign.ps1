param([string]$Path, [string]$Pfx, [string]$Password)
$ErrorActionPreference = "Stop"
if (!(Test-Path $Pfx)) { throw "protected PFX is required" }
if (!$env:JASTREAMER_SIGNTOOL -or !(Test-Path $env:JASTREAMER_SIGNTOOL)) {
  throw "pinned SignTool is required"
}
& $env:JASTREAMER_SIGNTOOL sign /fd SHA256 /f $Pfx /p $Password $Path
& $env:JASTREAMER_SIGNTOOL verify /pa /all $Path
