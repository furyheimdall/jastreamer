const signalChild = (child, signal) => {
  if (!child.controlQaProcessGroup || process.platform === 'win32') {
    return child.kill(signal);
  }
  try {
    process.kill(-child.pid, signal);
    return true;
  } catch (error) {
    if (error instanceof Error && 'code' in error && error.code === 'ESRCH') return false;
    throw error;
  }
};

export const stopChild = async (child) => {
  if (child.exitCode !== null || child.signalCode !== null) return;
  let resolveExited;
  const exited = new Promise((resolve) => { resolveExited = resolve; });
  const finish = () => {
    child.off('exit', finish);
    child.off('close', finish);
    resolveExited(true);
  };
  child.once('exit', finish);
  child.once('close', finish);
  if (!signalChild(child, 'SIGTERM')) {
    finish();
    return;
  }
  let timeout;
  const graceful = await Promise.race([
    exited,
    new Promise((resolve) => { timeout = setTimeout(() => resolve(false), 10_000); }),
  ]);
  clearTimeout(timeout);
  if (graceful || child.exitCode !== null || child.signalCode !== null) {
    finish();
    return;
  }
  if (signalChild(child, 'SIGKILL')) await exited;
};
