param([Parameter(Mandatory=$true)][string]$Directory)
$ErrorActionPreference = 'Stop'
Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
using System.Runtime.InteropServices.ComTypes;
public static class PortableLocks {
  [StructLayout(LayoutKind.Sequential)] public struct UniqueProcess { public uint Id; public FILETIME Started; }
  [StructLayout(LayoutKind.Sequential, CharSet=CharSet.Unicode)] public struct ProcessInfo {
    public UniqueProcess Process;
    [MarshalAs(UnmanagedType.ByValTStr, SizeConst=256)] public string Application;
    [MarshalAs(UnmanagedType.ByValTStr, SizeConst=64)] public string Service;
    public uint Type, Status, Session;
    [MarshalAs(UnmanagedType.Bool)] public bool Restartable;
  }
  [DllImport("rstrtmgr.dll", CharSet=CharSet.Unicode)] public static extern int RmStartSession(out uint session, uint flags, string key);
  [DllImport("rstrtmgr.dll", CharSet=CharSet.Unicode)] public static extern int RmRegisterResources(uint session, uint count, string[] files, uint applications, IntPtr processes, uint services, IntPtr names);
  [DllImport("rstrtmgr.dll")] public static extern int RmGetList(uint session, out uint needed, ref uint count, [In,Out] ProcessInfo[] processes, ref uint reasons);
  [DllImport("rstrtmgr.dll")] public static extern int RmEndSession(uint session);
}
'@
$desktopProcesses = @(Get-CimInstance Win32_Process -Filter "Name='jastreamer-desktop.exe'" | ForEach-Object {
  $type = [regex]::Match($_.CommandLine, '--type=([^\s"]+)').Groups[1].Value
  [pscustomobject]@{pid=$_.ProcessId; parent=$_.ParentProcessId; executable=$_.ExecutablePath; type=$type}
})
$files = @([IO.Directory]::GetFiles($Directory, '*', [IO.SearchOption]::AllDirectories))
if ($files.Count -gt 8192) { throw 'Unexpected portable fixture file count' }
[uint32]$session = 0
$result = [PortableLocks]::RmStartSession([ref]$session, 0, [guid]::NewGuid().ToString('N'))
if ($result -ne 0) { throw "RmStartSession: $result" }
try {
  $registered = [PortableLocks]::RmRegisterResources($session, $files.Count, $files, 0, [IntPtr]::Zero, 0, [IntPtr]::Zero)
  if ($registered -ne 0) { throw "RmRegisterResources: $registered" }
  [uint32]$needed = 0; [uint32]$count = 0; [uint32]$reasons = 0
  $result = [PortableLocks]::RmGetList($session, [ref]$needed, [ref]$count, $null, [ref]$reasons)
  $owners = @()
  if ($result -eq 234) {
    if ($needed -gt 1024) { throw 'Unexpected resource owner count' }
    $processes = New-Object 'PortableLocks+ProcessInfo[]' $needed
    $count = $needed
    $result = [PortableLocks]::RmGetList($session, [ref]$needed, [ref]$count, $processes, [ref]$reasons)
    if ($result -eq 0) { $owners = @($processes | Select-Object -First $count | ForEach-Object { [pscustomobject]@{pid=$_.Process.Id; application=$_.Application; service=$_.Service; type=$_.Type} }) }
  }
  [pscustomobject]@{desktopProcesses=$desktopProcesses; registeredFiles=$files.Count; restartManagerResult=$result; resourceOwners=$owners; controllerPid=$PID} | ConvertTo-Json -Depth 5 -Compress
} finally { [void][PortableLocks]::RmEndSession($session) }
