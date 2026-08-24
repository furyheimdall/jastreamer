# Remove Jake Streamer certificate trust

In an elevated Windows PowerShell prompt, locate the certificate by the exact SHA-256 fingerprint in `fingerprint.txt`, confirm its subject is `CN=Jake Streamer`, and remove it from `Cert:\LocalMachine\TrustedPeople`:

```powershell
$wanted = ((Get-Content .\fingerprint.txt) -replace '^SHA256:\s*','' -replace ':','').Trim()
Get-ChildItem Cert:\LocalMachine\TrustedPeople | Where-Object {
  $_.Subject -eq 'CN=Jake Streamer' -and
  ([Convert]::ToHexString($_.RawData)) -ne $null -and
  ((Get-FileHash -InputStream ([IO.MemoryStream]::new($_.RawData)) -Algorithm SHA256).Hash -eq $wanted)
} | Remove-Item
```

Uninstall Jake Streamer Server separately through Windows Installed Apps. Removing trust does not uninstall the service or delete Server data.
