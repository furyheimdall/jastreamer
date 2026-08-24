import {
  executeControl,
  executeRenderer,
  executeServer,
} from "./adapter-components";
import {
  CandidateBuilds,
  orderTrace,
} from "./adapter-process";
import type {
  AdapterEvidence,
  ComponentEvidence,
} from "./adapter-process";
import type { Artifact, Cell } from "./parser";

export class AdapterRuntime {
  static prepare(
    workspaceRoot: string,
    fixtureRoot: string,
  ): AdapterRuntime {
    return new AdapterRuntime(
      CandidateBuilds.prepare(workspaceRoot, fixtureRoot),
    );
  }

  private constructor(private readonly builds: CandidateBuilds) {}

  dispose(): void {
    this.builds.dispose();
  }

  execute(
    cell: Cell,
    subject: Artifact,
    peer: Artifact,
    wireFile: string,
    expectedMajor: number | undefined,
  ): AdapterEvidence {
    const evidence = this.executeComponent(
      cell,
      subject,
      peer,
      wireFile,
      expectedMajor,
    );
    return {
      ...evidence,
      candidateSha256: this.builds.hashes[subject.component],
      trace: [
        ...orderTrace(cell, subject, peer),
        ...evidence.trace,
      ],
    };
  }

  private executeComponent(
    cell: Cell,
    subject: Artifact,
    peer: Artifact,
    wireFile: string,
    expectedMajor: number | undefined,
  ): ComponentEvidence {
    switch (subject.component) {
      case "control":
        return executeControl(
          this.builds,
          cell,
          peer,
          wireFile,
          expectedMajor,
        );
      case "renderer":
        return executeRenderer(
          this.builds,
          cell,
          peer,
          wireFile,
          expectedMajor,
        );
      case "server":
        return executeServer(
          this.builds,
          cell,
          subject,
          peer,
          wireFile,
          expectedMajor,
        );
    }
  }
}
