export const createWebControlRuntime = ({ chromium, verifyOrigin }) => {
  const sessions = new Map();
  return {
    start: async ({ runId, url, binding, spkiPinBase64 }) => {
      await verifyOrigin(url, binding); const browser = await chromium.launch({ headless: false, args: [`--ignore-certificate-errors-spki-list=${spkiPinBase64}`] }); const pid = browser.process()?.pid;
      if (!Number.isInteger(pid) || pid < 1) { await browser.close(); throw new Error("TASK19_WEB_BROWSER_PID_INVALID"); }
      const context = await browser.newContext(); const page = await context.newPage(); await page.goto(url); const placeholder = page.locator("flt-semantics-placeholder"); if (await placeholder.isVisible()) await placeholder.click(); sessions.set(runId, { browser, context, page, url, binding, pid }); return { pid };
    },
    operation: async ({ runId, scenarioId, perform }) => {
      const session = sessions.get(runId); if (session === undefined || typeof scenarioId !== "string") throw new Error("TASK19_WEB_SESSION_INVALID"); await verifyOrigin(session.url, session.binding); return perform(session.page, session.context);
    },
    restart: async (runId) => {
      const session = sessions.get(runId); if (session === undefined) throw new Error("TASK19_WEB_SESSION_INVALID"); await verifyOrigin(session.url, session.binding); const pid = session.browser.process()?.pid; if (!Number.isInteger(pid) || pid < 1 || pid !== session.pid || session.browser.isConnected?.() === false) throw new Error("TASK19_WEB_BROWSER_PID_INVALID"); try { await session.page.reload(); const placeholder = session.page.locator("flt-semantics-placeholder"); if (await placeholder.isVisible()) await placeholder.click(); } catch (error) { throw error; } return { pid };
    },
    close: async (runId) => { const session = sessions.get(runId); if (session === undefined) return; sessions.delete(runId); await session.context.close(); await session.browser.close(); },
  };
};
