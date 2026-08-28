export const zoneInventorySemantics = Object.freeze({
  version: 1,
  validators: Object.freeze(['zones-inventory-v1']),
});

const rfc3339 = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.(\d{1,9}))?(Z|([+-])(\d{2}):(\d{2}))$/;

const isLeapYear = (year) => year % 4 === 0 && (year % 100 !== 0 || year % 400 === 0);

const daysInMonth = (year, month) => {
  if (month === 2) return isLeapYear(year) ? 29 : 28;
  return [4, 6, 9, 11].includes(month) ? 30 : 31;
};

export const isGoServerRfc3339 = (value) => {
  if (typeof value !== 'string') return false;
  const match = rfc3339.exec(value);
  if (match === null) return false;
  const [, yearText, monthText, dayText, hourText, minuteText, secondText, , zone, , offsetHourText, offsetMinuteText] = match;
  const year = Number(yearText);
  const month = Number(monthText);
  const day = Number(dayText);
  const hour = Number(hourText);
  const minute = Number(minuteText);
  const second = Number(secondText);
  if (month < 1 || month > 12 || day < 1 || day > daysInMonth(year, month) || hour > 23 || minute > 59 || second > 59) return false;
  if (zone === 'Z') return true;
  const offsetHour = Number(offsetHourText);
  const offsetMinute = Number(offsetMinuteText);
  return offsetHour <= 23 && offsetMinute <= 59 && (offsetHour !== 0 || offsetMinute !== 0);
};

export const registerSchemaFormats = (ajv) => {
  ajv.addFormat('date-time', isGoServerRfc3339);
  ajv.addKeyword({
    keyword: 'x-jastreamer-semantics',
    schemaType: 'object',
    valid: true,
  });
  return ajv;
};

const record = (value) =>
  typeof value === 'object' && value !== null && !Array.isArray(value)
    ? value
    : undefined;

const hasExactSemanticMarker = (schema) => {
  const root = record(schema);
  const marker = record(root?.['x-jastreamer-semantics']);
  return marker?.version === zoneInventorySemantics.version &&
    Array.isArray(marker.validators) &&
    marker.validators.length === 1 &&
    marker.validators[0] === zoneInventorySemantics.validators[0];
};

export const validateZoneInventorySemantics = (schema, value) => {
  if (!hasExactSemanticMarker(schema)) return false;
  const inventory = record(value);
  if (!Array.isArray(inventory?.zones) || !Array.isArray(inventory.renderers)) return false;
  const zoneIds = new Set();
  const rendererIds = new Set();
  for (const rawRenderer of inventory.renderers) {
    const rendererId = record(rawRenderer)?.renderer_id;
    if (typeof rendererId !== 'string' || rendererIds.has(rendererId)) return false;
    rendererIds.add(rendererId);
  }
  for (const rawZone of inventory.zones) {
    const zone = record(rawZone);
    const zoneId = zone?.zone_id;
    const rendererId = zone?.renderer_id;
    if (typeof zoneId !== 'string' || zoneIds.has(zoneId)) return false;
    if (rendererId !== null && (typeof rendererId !== 'string' || !rendererIds.has(rendererId))) return false;
    zoneIds.add(zoneId);
  }
  return true;
};

export const createZoneInventoryValidator = (ajv, schema) => {
  registerSchemaFormats(ajv);
  const validateStructure = ajv.compile(schema);
  return (value) =>
    validateStructure(value) && validateZoneInventorySemantics(schema, value);
};
