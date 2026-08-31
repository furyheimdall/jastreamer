import { spawn } from "node:child_process";
import { createHash, randomBytes, X509Certificate } from "node:crypto";
import { chmod, mkdir, readFile, rm, stat } from "node:fs/promises";
import { resolve } from "node:path";

const sha256 = (bytes) => createHash("sha256").update(bytes).digest("hex");
const execute = (command, stdin) => new Promise((resolveResult, reject) => {
  const child = spawn(command[0], command.slice(1), { stdio: ["pipe", "pipe", "pipe"], windowsHide: true }); const stdout = []; const stderr = [];
  child.stdout.on("data", (bytes) => stdout.push(bytes)); child.stderr.on("data", (bytes) => stderr.push(bytes)); child.once("error", reject); child.once("exit", (exitCode) => resolveResult({ exitCode, stdout: Buffer.concat(stdout).toString("utf8"), stderr: Buffer.concat(stderr).toString("utf8") })); child.stdin.end(stdin ?? "");
});
const bindings = (certificate) => { const parsed = new X509Certificate(certificate); const spki = parsed.publicKey.export({ type: "spki", format: "der" }); return { certificateSha256: sha256(parsed.raw), spkiSha256: sha256(spki), spkiPinBase64: createHash("sha256").update(spki).digest("base64") }; };
const linuxIdentity = async (root, run) => {
  const keyPath = resolve(root, "tls-key.pem"); const certificatePath = resolve(root, "tls-cert.pem");
  const result = await run(["openssl", "req", "-x509", "-newkey", "rsa:2048", "-sha256", "-nodes", "-days", "1", "-subj", "/CN=Task19 Run-Ephemeral Origin", "-addext", "subjectAltName=IP:127.0.0.1,DNS:localhost", "-addext", "basicConstraints=critical,CA:FALSE", "-addext", "keyUsage=critical,digitalSignature,keyEncipherment", "-addext", "extendedKeyUsage=serverAuth", "-keyout", keyPath, "-out", certificatePath]);
  if (result.exitCode !== 0) throw new Error("TASK19_EPHEMERAL_TLS_GENERATION_FAILED"); await chmod(keyPath, 0o600); await chmod(certificatePath, 0o600); const [key, certificate] = await Promise.all([readFile(keyPath), readFile(certificatePath)]); if ((await stat(keyPath)).mode & 0o077) throw new Error("TASK19_EPHEMERAL_TLS_KEY_PERMISSIONS_INVALID");
  return { keyPath, certificatePath, key, certificate, ...bindings(certificate) };
};
const windowsIdentity = async (root, run) => {
  const passphrase = randomBytes(32).toString("hex"); const script = `$inputValue=([Console]::In.ReadToEnd()|ConvertFrom-Json);$secure=ConvertTo-SecureString $inputValue.password -AsPlainText -Force;$cert=New-SelfSignedCertificate -Subject 'CN=Task19 Run-Ephemeral Origin' -DnsName @('localhost') -CertStoreLocation 'Cert:\\CurrentUser\\My' -KeyAlgorithm RSA -KeyLength 2048 -HashAlgorithm SHA256 -KeyExportPolicy Exportable -NotAfter (Get-Date).AddHours(4);try{Export-PfxCertificate -Cert $cert -FilePath $inputValue.pfx -Password $secure -Force|Out-Null;Export-Certificate -Cert $cert -FilePath $inputValue.cer -Type CERT -Force|Out-Null}finally{Remove-Item -LiteralPath $cert.PSPath -Force};$sid=[Security.Principal.WindowsIdentity]::GetCurrent().User.Value;& icacls.exe $inputValue.root /inheritance:r /grant:r "*$($sid):(OI)(CI)F" '*S-1-5-18:(OI)(CI)F' '*S-1-5-32-544:(OI)(CI)F'|Out-Null;if($LASTEXITCODE -ne 0){throw 'TASK19_EPHEMERAL_TLS_ACL_FAILED'}`;
  const pfxPath = resolve(root, "tls-identity.pfx"); const certificatePath = resolve(root, "tls-cert.cer"); const result = await run(["powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script], JSON.stringify({ root, pfx: pfxPath, cer: certificatePath, password: passphrase })); if (result.exitCode !== 0) throw new Error("TASK19_EPHEMERAL_TLS_GENERATION_FAILED"); const [pfx, certificate] = await Promise.all([readFile(pfxPath), readFile(certificatePath)]);
  return { keyPath: pfxPath, certificatePath, pfx, passphrase, certificate, ...bindings(certificate) };
};

export const createEphemeralTlsIdentity = async ({ root, run = execute, platform = process.platform, remove = rm }) => {
  const identityRoot = resolve(root); await mkdir(identityRoot, { recursive: false, mode: 0o700 }); let material;
  try {
    material = platform === "win32" ? await windowsIdentity(identityRoot, run) : await linuxIdentity(identityRoot, run);
  } catch (error) {
    try { await remove(identityRoot, { recursive: true, force: true }); }
    catch (cleanupError) { throw new AggregateError([error, cleanupError], "TASK19_EPHEMERAL_TLS_GENERATION_CLEANUP_FAILED"); }
    throw error;
  }
  let cleaned = false; return { kind: "task19-run-ephemeral-tls", root: identityRoot, ...material, cleanup: async () => { if (cleaned) return; await remove(identityRoot, { recursive: true, force: true }); cleaned = true; } };
};
