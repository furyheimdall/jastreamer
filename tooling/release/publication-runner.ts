import { rehashCleanupAuthorization, rehashPublicationClosure } from "./publication-files";
import type {
  AuthorizedProviderCommand,
  PreparedPublication,
  ProviderCommand,
  ProviderResult,
  PublicationDriver,
  PublicationTrace,
} from "./publication-types";

export class AuthorizedRunner {
  /** Ordered transcript accumulator; mutation is the purpose of this boundary object. */
  readonly trace: PublicationTrace[] = [];

  constructor(
    private readonly prepared: PreparedPublication,
    private readonly driver: PublicationDriver,
  ) {}

  async run(command: ProviderCommand): Promise<ProviderResult> {
    const cleanup = command.phase === "cleanup";
    if (command.phase === "write" || command.mutates === true) this.trace.push({ sequence: this.trace.length + 1, kind: "write-intent", commandId: command.id, disposition: "possibly-committed" });
    const closureSha256 = cleanup ? rehashCleanupAuthorization(this.prepared) : rehashPublicationClosure(this.prepared);
    this.trace.push({ sequence: this.trace.length + 1, kind: "rehash", commandId: command.id, closureSha256 });
    const authorized: AuthorizedProviderCommand = {
      ...command,
      authorization: { kind: cleanup ? "cleanup-ownership-sha256" : "publication-closure-sha256", sha256: closureSha256 },
    };
    const result = await this.driver.run(authorized);
    this.trace.push({ sequence: this.trace.length + 1, kind: "provider", commandId: command.id, phase: command.phase, argv: command.argv, exitCode: result.exitCode });
    return result;
  }
}
