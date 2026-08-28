import Ajv2020 from '../qa/node_modules/ajv/dist/2020.js';
import k17Schema from '../qa/k17/qualification-receipt.schema.json' with { type: 'json' };
import { registerSchemaFormats } from '../qa/schema-validation.mjs';
import wasapiSchema from '../qa/windows-audio/qualification-receipt.schema.json' with { type: 'json' };
import observationSchema from './product-gate-observation.schema.json' with { type: 'json' };
import schema from './product-gate.schema.json' with { type: 'json' };
import securitySchema from './product-gate-security.schema.json' with { type: 'json' };

const ajv = registerSchemaFormats(
  new Ajv2020({ allErrors: true, strict: false }),
);

export const validateSchema = ajv.compile(schema);
export const validateK17Schema = ajv.compile(k17Schema);
export const validateWasapiSchema = ajv.compile(wasapiSchema);
export const validateObservationSchema = ajv.compile(observationSchema);
export const validateSecuritySchema = ajv.compile(securitySchema);
