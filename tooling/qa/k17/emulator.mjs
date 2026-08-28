export const EMULATOR_SCENARIO_TESTS = Object.freeze({
  "identity-firmware-protocol": Object.freeze([
    "TestInspector_rejects_wrong_identity_firmware_and_protocolInfo",
    "TestInspector_accepts_minimum_or_newer_firmware_and_compatible_protocolInfo",
  ]),
  "hostile-location-private-network": Object.freeze([
    "TestInspector_rejects_description_redirect_off_subnet",
    "TestNewNetwork_rejects_public_and_overbroad_interface_networks",
    "TestAdapter_rejects_off_subnet_media_URL_without_SOAP",
  ]),
  "original-five-codec-media": Object.freeze([
    "TestHandler_serves_generated_five_codec_original_fixtures",
  ]),
  "l16-fallback": Object.freeze([
    "TestSelect_prefers_original_and_uses_L16_only_as_supported_fallback",
    "TestHandler_serves_L16_only_through_configured_provider_and_rejects_Range",
  ]),
  "https-and-media-only-http": Object.freeze([
    "TestAdapter_TLS_SOAP_fixture_pulls_signed_HTTPS_media_and_receives_escaped_DIDL",
    "Test_Transport_K17_start_uses_bound_private_HTTP_compatibility_origin",
  ]),
  "pause-seek-stop-natural-end": Object.freeze([
    "Test_K17Lifecycle_owned_playing_to_stopped_advances_once",
    "Test_K17Lifecycle_duplicate_stopped_does_not_advance_twice",
    "Test_K17Lifecycle_crash_restart_replay_does_not_double_advance",
    "Test_K17Lifecycle_explicit_stop_does_not_advance",
    "Test_K17Lifecycle_failed_start_stopped_does_not_advance",
    "Test_K17Lifecycle_external_URI_stopped_does_not_advance",
    "Test_K17Lifecycle_stale_stopped_after_next_reservation_is_ignored",
    "Test_K17Lifecycle_unavailable_does_not_advance",
    "Test_K17Lifecycle_success_terminalizes_dispatch_without_fabricating_observation",
    "Test_K17Lifecycle_URI_failure_suspends_and_terminalizes",
    "Test_K17Lifecycle_Play_failure_suspends_and_terminalizes",
    "Test_K17Lifecycle_duplicate_result_does_not_repeat_SOAP",
    "Test_K17Lifecycle_pending_recovery_dispatches_once",
    "Test_K17Lifecycle_restart_exposes_pending_dispatch_for_replay",
    "Test_K17Lifecycle_restart_after_claim_before_URI_suspends_without_SOAP_retry",
    "Test_K17Lifecycle_restart_after_URI_before_Play_suspends_without_SOAP_retry",
    "Test_K17Lifecycle_restart_after_Play_before_completion_suspends_without_SOAP_retry",
    "Test_K17Dispatch_success_completion_is_idempotent",
    "Test_K17Dispatch_failure_completion_is_idempotent",
    "Test_K17Dispatch_claim_rejects_wrong_zone_play_command_and_kind",
    "Test_K17Lifecycle_queue_exhausted_reaches_idle",
    "Test_K17Lifecycle_blocked_head_stays_blocked",
  ]),
  "expired-capability": Object.freeze([
    "TestSigner_rejects_expiry_tampering_and_wrong_identity",
  ]),
  "disappearance-reappearance": Object.freeze([
    "Test_K17Lifecycle_disappearance_and_external_override_suspend_without_consuming_queue",
  ]),
  "external-override": Object.freeze([
    "TestLifecycle_expires_suspends_and_reconciles_without_adopting_external_URI",
    "Test_K17Lifecycle_external_state_change_suspends_active_play_without_advancing",
  ]),
});

export const EMULATOR_SCENARIOS = Object.freeze(
  Object.keys(EMULATOR_SCENARIO_TESTS),
);

const passedTests = (output) => {
  const passed = new Set();
  for (const line of output.split(/\r?\n/)) {
    if (line === "") continue;
    let event;
    try {
      event = JSON.parse(line);
    } catch {
      continue;
    }
    if (event?.Action === "pass" && typeof event.Test === "string") {
      passed.add(event.Test);
    }
  }
  return passed;
};

export const runEmulatorMatrix = async (options) => {
  const process = options.spawn([
    "go", "test", "-json", "-race", "-shuffle=on", "-count=1",
    "./internal/upnp", "./internal/media", "./internal/playback", "./internal/api",
  ], { cwd: options.serverDirectory, stdout: "pipe", stderr: "pipe" });
  const [code, stdout, stderr] = await Promise.all([
    process.exited,
    new Response(process.stdout).text(),
    new Response(process.stderr).text(),
  ]);
  if (code !== 0) return { ok: false, code: "EMULATOR_MATRIX_FAILED", stdout, stderr };
  const passed = passedTests(stdout);
  const missing = Object.entries(EMULATOR_SCENARIO_TESTS)
    .flatMap(([scenario, tests]) =>
      tests
        .filter((test) => !passed.has(test))
        .map((test) => ({ scenario, test })),
    );
  if (missing.length !== 0) {
    return {
      ok: false,
      code: "EMULATOR_SCENARIO_MISSING",
      missing,
      stdout,
      stderr,
    };
  }
  return {
    ok: true,
    receipt: {
      schema_version: 1,
      kind: "k17_emulator_matrix",
      candidate_sha256: options.candidateSha256,
      status: "passed",
      scenarios: EMULATOR_SCENARIOS.map((id) => ({
        id,
        result: "passed",
        tests: EMULATOR_SCENARIO_TESTS[id],
      })),
      external_device_calls: 0,
    },
  };
};
