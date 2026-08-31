import { expect, test } from 'bun:test';
import { spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { mkdir, mkdtemp, readFile, readdir, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';

const repository = resolve(import.meta.dirname, '../..');
const qaDirectory = join(repository, 'tooling/qa');
const exactWebZip = join(repository, 'dist/candidates/control/jastreamer-control_0.1.0_web.zip');
const exactControlManifest = join(repository, 'dist/candidates/control/manifest.json');

const declaredExactWebSha256 = async () => {
  const manifest = JSON.parse(await readFile(exactControlManifest, 'utf8'));
  const artifact = manifest.artifacts.find(({ name }) => name === 'jastreamer-control_0.1.0_web.zip');
  expect(artifact?.name).toBe('jastreamer-control_0.1.0_web.zip');
  return artifact.sha256;
};

const filesNamed = async (root, name) => {
  const entries = await readdir(root, { withFileTypes: true });
  const nested = await Promise.all(entries.map(async (entry) => {
    const path = join(root, entry.name);
    if (entry.isDirectory()) return filesNamed(path, name);
    return entry.name === name ? [path] : [];
  }));
  return nested.flat();
};

const runControlScenario = ({ fixture, inputRegression, output, webRoot, playwrightOutput, tempRoot }) => {
  const child = Bun.spawn([
    'bunx',
    '--no-install', 'playwright', 'test', '--config', 'playwright.config.mjs', 'control.playwright.mjs',
    '--browser', 'chromium', '--workers', '1', '--reporter', 'line',
    '--trace', 'on', '--output', playwrightOutput,
  ], {
    cwd: qaDirectory,
    env: {
      ...process.env,
      CONTROL_FIXTURE: fixture,
      ...(inputRegression ? { CONTROL_INPUT_REGRESSION: '1' } : {}),
      CONTROL_OUTPUT: output,
      CONTROL_WEB_ROOT: webRoot,
      NODE_OPTIONS: '--throw-deprecation',
      TMPDIR: tempRoot,
    },
    stdin: 'ignore',
    stdout: 'pipe',
    stderr: 'pipe',
  });
  const completed = Promise.all([
    child.exited,
    new Response(child.stdout).text(),
    new Response(child.stderr).text(),
  ]).then(([code, stdout, stderr]) => ({
    code,
    signal: child.signalCode,
    transcript: `${stdout}${stderr}`,
  }));
  const result = new Promise((resolveExit, reject) => {
    const timeout = setTimeout(() => {
      child.kill('SIGTERM');
      void completed.then(
        ({ transcript }) => reject(new Error(`Control scenario timed out\n${transcript}`)),
        reject,
      );
    }, 360_000);
    void completed.then(
      (value) => {
        clearTimeout(timeout);
        resolveExit(value);
      },
      (error) => {
        clearTimeout(timeout);
        reject(error);
      },
    );
  });
  return { child, result };
};

test('two concurrent exact-ZIP Control scenarios isolate browser input semantics', async () => {
  // Given
  const root = await mkdtemp(join(tmpdir(), 'control-concurrent-input-'));
  const archive = await readFile(exactWebZip);
  expect(createHash('sha256').update(archive).digest('hex')).toBe(await declaredExactWebSha256());
  const scenarios = [
    { kind: 'happy', fixtureName: 'control-policy-happy.yaml' },
    { kind: 'failure', fixtureName: 'control-stale-blocked.yaml' },
  ].map(({ kind, fixtureName }) => ({
    kind,
    fixture: join(repository, 'tooling/fixtures/e2e', fixtureName),
    inputRegression: true,
    output: join(root, kind, 'evidence'),
    webRoot: join(root, kind, 'web'),
    playwrightOutput: join(root, kind, 'playwright'),
    tempRoot: join(root, kind, 'tmp'),
  }));
  for (const scenario of scenarios) {
    await Promise.all([
      mkdir(scenario.tempRoot, { recursive: true }),
      mkdir(scenario.webRoot, { recursive: true }),
    ]);
    const extracted = spawnSync('unzip', ['-q', exactWebZip, '-d', scenario.webRoot], { encoding: 'utf8' });
    expect(extracted.status, extracted.stderr).toBe(0);
  }
  const invocations = scenarios.map(runControlScenario);
  expect(new Set(invocations.map(({ child }) => child.pid)).size).toBe(2);

  try {
    // When
    const results = await Promise.all(invocations.map(({ result }) => result));

    // Then
    for (let index = 0; index < scenarios.length; index++) {
      const scenario = scenarios[index];
      const result = results[index];
      expect(result.code, result.transcript).toBe(0);
      expect(result.signal, result.transcript).toBeNull();
      const match = result.transcript.match(/CONTROL_INPUT_SEMANTICS (\{.*\})/);
      expect(match, result.transcript).not.toBeNull();
      const semantics = JSON.parse(match[1]);
      expect(semantics.scenario).toBe(`control-policy-${scenario.kind}`);
      expect(semantics.cleared).toEqual([
        { phase: 'focused', focused: true, selection: [5, 5], value: 'first' },
        {
          phase: 'selected', focused: true, selection: [0, 5], value: 'first',
          keys: ['Control:keydown', 'A:keydown', 'A:keyup', 'Control:keyup'], inputs: [],
        },
        {
          phase: 'cleared', focused: true, selection: [0, 0], value: '',
          keys: ['Backspace:keydown', 'Backspace:keyup'], inputs: ['deleteContentBackward:'],
        },
        { phase: 'entered', focused: true, selection: [0, 0], value: '' },
      ]);
      expect((await filesNamed(scenario.playwrightOutput, 'trace.zip')).length).toBe(1);
      scenario.semanticsProcess = semantics.process;
    }
    expect(new Set(scenarios.map(({ semanticsProcess }) => semanticsProcess)).size).toBe(2);
    expect(new Set(scenarios.flatMap(({ output, webRoot, playwrightOutput, tempRoot }) => [
      output, webRoot, playwrightOutput, tempRoot,
    ])).size).toBe(8);
  } finally {
    for (const { child } of invocations) {
      if (child.exitCode === null && child.signalCode === null) child.kill('SIGKILL');
    }
    await rm(root, { recursive: true, force: true });
  }
}, 380_000);

test('two complete concurrent exact-ZIP Control scenarios publish isolated receipts and screenshots', async () => {
  // Given
  const root = await mkdtemp(join(tmpdir(), 'control-concurrent-complete-'));
  const scenarios = [
    { kind: 'happy', fixtureName: 'control-policy-happy.yaml' },
    { kind: 'failure', fixtureName: 'control-stale-blocked.yaml' },
  ].map(({ kind, fixtureName }) => ({
    kind,
    fixture: join(repository, 'tooling/fixtures/e2e', fixtureName),
    inputRegression: false,
    output: join(root, kind, 'evidence'),
    webRoot: join(root, kind, 'web'),
    playwrightOutput: join(root, kind, 'playwright'),
    tempRoot: join(root, kind, 'tmp'),
  }));
  for (const scenario of scenarios) {
    await Promise.all([
      mkdir(scenario.tempRoot, { recursive: true }),
      mkdir(scenario.webRoot, { recursive: true }),
    ]);
    const extracted = spawnSync('unzip', ['-q', exactWebZip, '-d', scenario.webRoot], { encoding: 'utf8' });
    expect(extracted.status, extracted.stderr).toBe(0);
  }
  const invocations = scenarios.map(runControlScenario);

  try {
    // When
    const results = await Promise.all(invocations.map(({ result }) => result));

    // Then
    const failures = results.flatMap((result, index) => result.code === 0 ? [] : [{
      scenario: scenarios[index].kind,
      signal: result.signal,
      transcript: result.transcript,
    }]);
    expect(failures).toEqual([]);
    for (const scenario of scenarios) {
      const receipt = await Bun.file(join(scenario.output, `control-policy-${scenario.kind}.json`)).json();
      expect(receipt).toMatchObject({ scenario: `control-policy-${scenario.kind}`, status: 'passed' });
      expect((await filesNamed(scenario.playwrightOutput, 'trace.zip')).length).toBe(1);
      const screenshots = (await readdir(scenario.output)).filter((name) => name.endsWith('.png'));
      expect(screenshots.length).toBeGreaterThan(0);
    }
  } finally {
    for (const { child } of invocations) {
      if (child.exitCode === null && child.signalCode === null) child.kill('SIGKILL');
    }
    await rm(root, { recursive: true, force: true });
  }
}, 380_000);
