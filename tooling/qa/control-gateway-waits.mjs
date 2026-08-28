import { watch } from 'node:fs';
import { join } from 'node:path';
import { deadline, requestJSON } from './control-gateway-http.mjs';

export const waitForRendererStatus = (server, token, rendererId, expectedStatus) => {
  let watcher;
  let settled = false;
  let checking = false;
  let checkAgain = false;
  let resolveStatus;
  let rejectStatus;
  const result = deadline(new Promise((resolveValue, rejectValue) => {
    resolveStatus = resolveValue;
    rejectStatus = rejectValue;
  }), `renderer ${expectedStatus}`, 60_000).finally(() => {
    settled = true;
    watcher?.close();
  });
  const check = async () => {
    if (settled) return;
    if (checking) {
      checkAgain = true;
      return;
    }
    checking = true;
    try {
      const response = await requestJSON(server.origin, 'GET', '/api/v1/zones', token);
      if (response.status !== 200) throw new Error(`renderer inventory returned ${response.status}`);
      const renderer = response.body.renderers.find((item) => item.renderer_id === rendererId);
      if (renderer?.status === expectedStatus) {
        resolveStatus(renderer);
        return;
      }
    } catch (error) {
      rejectStatus(error);
      return;
    } finally {
      checking = false;
    }
    if (checkAgain) {
      checkAgain = false;
      await check();
    }
  };
  watcher = watch(join(server.directory, 'playback.sqlite'), () => { void check(); });
  void check();
  return result;
};

export const waitForPlaybackState = (server, token, expected, label) => {
  let watcher;
  let settled = false;
  let checking = false;
  let checkAgain = false;
  let resolveState;
  let rejectState;
  const result = deadline(new Promise((resolveValue, rejectValue) => {
    resolveState = resolveValue;
    rejectState = rejectValue;
  }), label, 60_000).finally(() => {
    settled = true;
    watcher?.close();
  });
  const check = async () => {
    if (settled) return;
    if (checking) {
      checkAgain = true;
      return;
    }
    checking = true;
    try {
      const response = await requestJSON(
        server.origin,
        'GET',
        '/api/v1/zones/main/playback-state',
        token,
      );
      if (response.status !== 200) throw new Error(`playback state returned ${response.status}`);
      if (expected(response.body)) {
        resolveState(response.body);
        return;
      }
    } catch (error) {
      rejectState(error);
      return;
    } finally {
      checking = false;
    }
    if (checkAgain) {
      checkAgain = false;
      await check();
    }
  };
  watcher = watch(join(server.directory, 'playback.sqlite'), () => { void check(); });
  void check();
  return result;
};
