import { readFileSync, writeFileSync } from "node:fs";
import { consumeAuthoritativeReduction } from "./qualification-authoritative-input";
const arguments_ = process.argv.slice(2); if (arguments_.length !== 4 || arguments_[0] !== "--reducer" || arguments_[2] !== "--output" || arguments_[1] === undefined || arguments_[3] === undefined) throw new Error("AUTHORITATIVE_INPUT_USAGE");
const output = consumeAuthoritativeReduction(readFileSync(arguments_[1])); writeFileSync(arguments_[3], `${JSON.stringify(output, null, 2)}\n`); console.log(JSON.stringify({ reducerSha256: output.reducerSha256, reducerResult: output.reducerResult }));
