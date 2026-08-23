interface ImportMeta {
  readonly main: boolean;
}

declare const process: {
  exit(code?: number): never;
};

declare namespace Bun {
  interface File {
    arrayBuffer(): Promise<ArrayBuffer>;
    json(): Promise<unknown>;
    text(): Promise<string>;
  }

  interface Subprocess {
    readonly exited: Promise<number>;
    readonly stderr: ReadableStream<Uint8Array>;
    readonly stdout: ReadableStream<Uint8Array>;
  }

  class Glob {
    constructor(pattern: string);
    scan(options: Readonly<{ cwd: string }>): AsyncIterable<string>;
  }

  const argv: readonly string[];
  function file(path: string | URL): File;
  function spawn(
    command: readonly string[],
    options: Readonly<{ cwd?: string; stderr: "pipe"; stdout: "pipe" }>,
  ): Subprocess;
  function write(path: string, content: string): Promise<number>;
}

declare module "bun:test" {
  interface Matchers {
    toBe(expected: unknown): void;
    toContain(expected: unknown): void;
    toEqual(expected: unknown): void;
    toHaveLength(expected: number): void;
    toMatchObject(expected: unknown): void;
  }

  export function expect(actual: unknown): Matchers;
  export function test(name: string, body: () => unknown | Promise<unknown>): void;
}
