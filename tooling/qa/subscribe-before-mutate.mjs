export class SubscriptionTimeoutError extends Error {
  name = "SubscriptionTimeoutError";
  constructor() { super("SUBSCRIPTION_TIMEOUT"); }
}

export const awaitSubscribedMutation = async ({ subscribe, mutate, timeoutMs }) => {
  const subscription = subscribe();
  let timeout;
  const expired = new Promise((_, reject) => {
    timeout = setTimeout(() => reject(new SubscriptionTimeoutError()), timeoutMs);
  });
  try {
    await mutate();
    return await Promise.race([subscription.signal, expired]);
  } finally {
    clearTimeout(timeout);
    subscription.unsubscribe();
  }
};
