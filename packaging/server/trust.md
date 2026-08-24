# Trust the jastreamer signing certificate

The Server EXE and MSI use a project-owned, self-signed Code Signing certificate. It is **not Public Trust**: Windows and SmartScreen can warn before explicit trust. Verify `server.cer` against `fingerprint.txt` before importing it.

On an elevated Windows PowerShell prompt:

```powershell
(Get-FileHash .\server.cer -Algorithm SHA256).Hash
Import-Certificate -FilePath .\server.cer -CertStoreLocation Cert:\LocalMachine\TrustedPeople
```

The first output, with separators removed, must equal the published SHA-256 fingerprint. Import only into `TrustedPeople`; this leaf has `CA=false` and must not be placed in a Certification Authority store. Then verify the MSI with `Get-AuthenticodeSignature` and install it normally. Clean-Windows trust behavior is verified only by the Windows release runner, never inferred from the Linux local rehearsal.
