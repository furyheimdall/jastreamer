export class ControlCleanupError extends Error {
  name = 'ControlCleanupError';

  constructor(failures, details, primaryFailure) {
    super(
      `Control cleanup failed for ${failures.map(({ name }) => name).join(', ')}`,
      primaryFailure === undefined ? undefined : { cause: primaryFailure },
    );
    this.failures = failures;
    this.details = details;
  }
}

export const cleanupControlResources = async ({ operations, primaryFailure }) => {
  const failures = [];
  const details = [];
  for (const operation of operations) {
    switch (operation.state) {
      case 'absent':
        details.push({ name: operation.name, status: 'already_absent' });
        break;
      case 'present':
        try {
          await operation.close();
          details.push({ name: operation.name, status: 'closed' });
        } catch (error) {
          failures.push({
            name: operation.name,
            message: error instanceof Error ? error.message : String(error),
          });
          details.push({ name: operation.name, status: 'failed' });
        }
        break;
      default:
        throw new TypeError(`Unsupported Control cleanup state: ${operation.state}`);
    }
  }
  if (failures.length > 0) {
    throw new ControlCleanupError(failures, details, primaryFailure);
  }
  if (primaryFailure !== undefined) throw primaryFailure;
  return details;
};
