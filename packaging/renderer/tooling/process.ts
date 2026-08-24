export async function run(command: readonly string[], cwd: string): Promise<void> {
  const process = Bun.spawn([...command], {
    cwd,
    stdout: "inherit",
    stderr: "inherit",
  });
  const exitCode = await process.exited;
  if (exitCode !== 0) {
    throw new Error(`${command.join(" ")} exited ${exitCode}`);
  }
}
