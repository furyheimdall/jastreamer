interface ImportMeta {
  readonly dir: string;
}

declare const Bun: {
  spawnSync(
    command: readonly string[],
    options?: {
      readonly cwd?: string;
      readonly stdout?: "pipe";
      readonly stderr?: "pipe";
    },
  ): {
    readonly exitCode: number;
    readonly stdout: Uint8Array;
    readonly stderr: Uint8Array;
  };
};
