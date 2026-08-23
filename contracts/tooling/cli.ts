import { createHash } from "node:crypto";
import { readFile } from "node:fs/promises";
import { join } from "node:path";

const root = process.env.CONTRACTS_ROOT ?? join(import.meta.dir, "..");
const names = ["control-api", "renderer-protocol"] as const;
type ContractName = (typeof names)[number];
type Lock = { component: string; contracts: Record<ContractName, { version: string; digest: string }> };

const pathFor = (name: ContractName, version: "v1" | "v2") => join(root, name, "archived", version, "schema.json");
const digest = async (path: string) => `sha256:${createHash("sha256").update(await readFile(path)).digest("hex")}`;
const canonical = async (path: string) => JSON.stringify(JSON.parse(await readFile(path, "utf8")));

const verifyArchives = async (): Promise<boolean> => {
  for (const name of names) for (const version of ["v1", "v2"] as const) {
    if (!(await Bun.file(pathFor(name, version)).exists())) return false;
  }
  return true;
};

const generate = async (repeat: number): Promise<number> => {
  if (repeat < 1 || !(await verifyArchives())) return 1;
  let previous = "";
  for (let pass = 0; pass < repeat; pass++) {
    const current = (await Promise.all(names.map((name) => canonical(join(root, name, "schema.json"))))).join("");
    if (pass > 0 && current !== previous) return 1;
    previous = current;
  }
  for (const name of names) if (await canonical(join(root, name, "schema.json")) !== await canonical(pathFor(name, "v2"))) return 1;
  return 0;
};

const locks = async (): Promise<number> => {
  const expected: Record<string, readonly ContractName[]> = { control: ["control-api"], renderer: ["renderer-protocol"], server: names };
  for (const [component, contracts] of Object.entries(expected)) {
    const lock = JSON.parse(await readFile(join(root, "locks", `${component}.json`), "utf8")) as Lock;
    if (lock.component !== component || Object.keys(lock.contracts).length !== contracts.length) return 1;
    for (const name of contracts) if (lock.contracts[name]?.version !== "v2" || lock.contracts[name]?.digest !== await digest(pathFor(name, "v2"))) return 1;
  }
  return 0;
};

const compatibility = async (path: string, output: string | undefined): Promise<number> => {
  const sections = new Map<string, number[]>();
  let section: string | undefined;
  for (const line of (await readFile(path, "utf8")).split("\n")) {
    const header = /^(control|renderer|server):\s*$/.exec(line);
    if (header) { section = header[1]; sections.set(section, []); continue; }
    const majors = /^\s+supported_majors:\s*\[([^\]]*)\]\s*$/.exec(line);
    if (majors && section) sections.set(section, majors[1].split(",").filter(Boolean).map((value) => Number(value.trim())));
  }
  const commonMajors = [2, 1].filter((major) => ["control", "renderer", "server"].every((name) => sections.get(name)?.includes(major)));
  const result = commonMajors.length ? { compatible: true, commonMajors } : { compatible: false, commonMajors: [], error: "INCOMPATIBLE_PROTOCOL_MAJOR" };
  if (output) await Bun.write(output, `${JSON.stringify(result)}\n`);
  if (!result.compatible) { console.error(result.error); return 65; }
  return 0;
};

const args = process.argv.slice(2);
const value = (flag: string) => { const index = args.indexOf(flag); return index >= 0 ? args[index + 1] : undefined; };
const command = args[0] === "generate" ? await generate(Number(value("--repeat") ?? 1)) : args[0] === "locks" ? await locks() : args[0] === "compatibility" ? await compatibility(value("--matrix") ?? "", value("--output")) : args[0] === "breaking" ? await (async () => { const fixture = JSON.parse(await readFile(value("--fixture") ?? "", "utf8")) as { before: { required: string[] }; after: { required: string[] }; majorChanged: boolean }; const breaking = fixture.after.required.some((field) => !fixture.before.required.includes(field)) && !fixture.majorChanged; if (breaking) console.error("BREAKING_CHANGE_WITHOUT_MAJOR"); return breaking ? 65 : 0; })() : 64;
process.exit(command);
