import { createHash } from "node:crypto";
import { readdir, readFile } from "node:fs/promises";
import { extname, join } from "node:path";
import process from "node:process";

const root = join(import.meta.dirname, "../..");
const forbiddenDependencies = /\b(sqflite|drift|sqlite3|onnx|tensorflow|tflite|ml_algo|recommendation_engine)\b/i;
const forbiddenLogic = /\b(jaccard|cosine(?:Similarity|Distance)?|mfcc|seed_similarity|tie_prefix|score_band_width|playback_decision_attempts|playback_queue|sqlite)\b/i;

const filesUnder = async (directory) => {
  const entries = await readdir(directory, { withFileTypes: true });
  const nested = await Promise.all(entries.map(async (entry) => {
    const path = join(directory, entry.name);
    return entry.isDirectory() ? filesUnder(path) : [path];
  }));
  return nested.flat();
};

const scanControl = async (controlRoot) => {
  const candidates = [join(controlRoot, "pubspec.yaml"), join(controlRoot, "pubspec.lock")];
  const libraryFiles = (await filesUnder(join(controlRoot, "lib"))).filter((path) => extname(path) === ".dart");
  for (const path of [...candidates, ...libraryFiles]) {
    const source = await readFile(path, "utf8");
    const dependency = forbiddenDependencies.exec(source);
    if (dependency) throw new Error(`forbidden Control dependency ${dependency[0]} in ${path}`);
    const logic = forbiddenLogic.exec(source);
    if (logic) throw new Error(`forbidden Server-only logic/storage term ${logic[0]} in ${path}`);
  }
  const bundle = join(controlRoot, "build/web/main.dart.js");
  try {
    const source = await readFile(bundle, "utf8");
    const logic = forbiddenLogic.exec(source);
    if (logic) throw new Error(`forbidden Server-only term ${logic[0]} in built web bundle`);
  } catch (error) {
    if (!(error instanceof Error && "code" in error && error.code === "ENOENT")) throw error;
  }
};

if (process.argv[2] === "--scan") {
  const scanRoot = process.argv[3];
  if (!scanRoot) throw new TypeError("--scan requires a Control root");
  await scanControl(scanRoot);
  process.stdout.write("Control boundary scan passed\n");
  process.exit(0);
}

const contract = await readFile(join(root, "contracts/control-api/http-api-v1.json"));
const generated = await readFile(join(root, "apps/control/lib/generated/control_contract.dart"), "utf8");
const digest = createHash("sha256").update(contract).digest("hex");
if (!generated.includes(`'${digest}'`)) throw new Error(`generated Control contract digest drifted: ${digest}`);
const parsed = JSON.parse(contract.toString("utf8"));
for (const reason of parsed.reasonEnums) {
  if (!generated.includes(`'${reason}'`)) throw new Error(`generated Control contract omitted reason ${reason}`);
}
await scanControl(join(root, "apps/control"));
process.stdout.write(`${digest}\n`);
