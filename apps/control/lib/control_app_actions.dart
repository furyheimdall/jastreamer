part of 'app.dart';

extension _ControlAppActions on _ControlHomeState {
  Future<void> savePolicy() => _guard(
        () async {
          final active = gateway;
          if (active == null) {
            throw const FormatException(
                'Pair this Control before saving policy.');
          }
          final saved = await active.updatePolicy(
            PolicyWrite(
              mode: state.desiredPolicy.name,
              artistGap: state.artistCooldown,
              albumGap: state.albumCooldown,
              sessionOverride: state.sessionOverride?.name ?? '',
            ),
            state.policyRevision,
          );
          if (!mounted) return;
          _update(() => state = applyPolicyView(state, saved));
        },
        onStale: () async {
          final current = await gateway?.policy();
          if (current == null || !mounted) return;
          _update(() {
            state = state.reduce(
              PolicySaveRejected(serverRevision: current.revision),
            );
            if (current.mode.known case final policy?) {
              state = state.reduce(
                ServerPolicyRefreshed(
                    policy: policy, revision: current.revision),
              );
            }
          });
        },
      );

  Future<void> _guard(
    Future<void> Function() action, {
    Future<void> Function()? onStale,
  }) async {
    _update(() {
      busy = true;
      error = null;
    });
    try {
      await action();
    } on StalePolicyFailure {
      if (onStale == null) rethrow;
      await onStale();
    } on TokenRevokedFailure catch (failure) {
      await _forgetCredential();
      if (mounted) _update(() => error = failure.code);
    } on CertificateIdentityChangedFailure catch (failure) {
      await _forgetCredential();
      if (mounted) _update(() => error = failure.code);
    } on ControlApiFailure catch (failure) {
      if (mounted) {
        _update(() => error = 'Server request failed: ${failure.code}');
      }
    } on ControlFailure catch (failure) {
      if (mounted) {
        _update(() => error = '${failure.code}: ${failure.message}');
      }
    } on UnsupportedProtocolMajor catch (failure) {
      if (mounted) _update(() => error = failure.code);
    } on FormatException catch (failure) {
      if (mounted) _update(() => error = failure.message.toString());
    } finally {
      if (mounted) _update(() => busy = false);
    }
  }

  Future<void> _forgetCredential() async {
    await platform.vault.delete();
    await liveUpdates?.cancel();
    await liveEvents?.cancel();
    await liveSession?.close();
    await gateway?.close();
    gateway = null;
    liveSession = null;
    if (!mounted || state.servers.length != 1) return;
    final server = state.servers.single;
    _update(() {
      state = state.reduce(
        ServerDiscovered(server.withPairing(PairingStatus.available)),
      );
    });
  }
}
