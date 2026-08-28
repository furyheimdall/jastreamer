import { constants } from 'node:fs';
import { access } from 'node:fs/promises';
import { isAbsolute, relative, resolve, sep } from 'node:path';

export const DRIVER_BUILD_COMMAND = 'bun tooling/qa/control-gateway-driver.mjs';

export const requireGatewayDriver = async ({
  environment = process.env,
  repository = resolve(import.meta.dirname, '../../..'),
  accessExecutable = (path) => access(path, constants.X_OK),
} = {}) => {
  const configured = environment.CONTROL_GATEWAY_DRIVER;
  if (!configured) {
    throw new Error(
      `CONTROL_GATEWAY_DRIVER is required; run '${DRIVER_BUILD_COMMAND}'. ` +
      'Prerequisite: pinned Flutter 3.35.0 or the pinned cached qualification image.',
    );
  }
  const driver = resolve(repository, configured);
  const generatedRoot = resolve(repository, 'apps/control/build');
  const generatedLocation = relative(generatedRoot, driver);
  if (generatedLocation === '' || (!isAbsolute(generatedLocation) &&
      generatedLocation !== '..' && !generatedLocation.startsWith(`..${sep}`))) {
    throw new Error(
      `CONTROL_GATEWAY_DRIVER must not use apps/control/build; run '${DRIVER_BUILD_COMMAND}'`,
    );
  }
  try {
    await accessExecutable(driver);
  } catch {
    throw new Error(
      `CONTROL_GATEWAY_DRIVER is not an executable file: ${driver}; run '${DRIVER_BUILD_COMMAND}'`,
    );
  }
  return driver;
};
