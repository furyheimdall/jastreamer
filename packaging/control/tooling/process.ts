export async function run(command: readonly string[], cwd: string, env: Record<string, string> = {}): Promise<string> {
  const child = Bun.spawn([...command], {
    cwd,
    env: { ...globalThis.process.env, ...env },
    stdout: "pipe",
    stderr: "pipe",
  });
  const [stdout, stderr, code] = await Promise.all([
    new Response(child.stdout).text(),
    new Response(child.stderr).text(),
    child.exited,
  ]);
  if (stderr) globalThis.process.stderr.write(stderr);
  if (code !== 0) throw new Error(`${command.join(" ")} exited ${code}`);
  return stdout;
}
