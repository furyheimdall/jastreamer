part of 'app.dart';

extension _ControlAppPairing on _ControlHomeState {
  Future<void> discover() async {
    final serverOrigin = Uri.tryParse(origin.text.trim());
    if (serverOrigin == null ||
        serverOrigin.scheme != 'https' ||
        !serverOrigin.hasAuthority) {
      _update(() => error = 'Enter a valid HTTPS Server address.');
      return;
    }
    await _guard(() async {
      final advertisedFingerprint = fingerprint.text.trim();
      final candidate = DiscoveredServer(
        id: const ServerId('discovered-server'),
        name: 'jastreamer Server',
        origin: serverOrigin,
        pairingUrl: serverOrigin.resolve('/pair/'),
        certificateSha256: advertisedFingerprint,
        pairing: PairingStatus.available,
      );
      final identityEndpoint = advertisedFingerprint.isEmpty
          ? platform.probe(serverOrigin)
          : platform.endpoint(candidate);
      late final ServerIdentity identity;
      try {
        identity = await identityEndpoint.identity();
      } finally {
        identityEndpoint.close();
      }
      if (advertisedFingerprint.isNotEmpty &&
          !advertisedFingerprintsEqual(
            identity.certificateSha256,
            advertisedFingerprint,
          )) {
        throw const FormatException(
          'Advertised fingerprint does not match Server identity.',
        );
      }
      final server = DiscoveredServer(
        id: const ServerId('discovered-server'),
        name: identity.commonName,
        origin: serverOrigin,
        pairingUrl: identity.pairingUrl,
        certificateSha256: identity.certificateSha256,
        pairing: PairingStatus.available,
      );
      dispatch(ServerDiscovered(server));
      // A persisted credential is restored only after the user supplied pin
      // has matched the live identity. Discovery never auto-trusts a pin.
      if (advertisedFingerprint.isNotEmpty) {
        final binding = CredentialBinding(
          serverOrigin: server.origin,
          certificateSha256: server.certificateSha256,
        );
        final saved = await platform.vault.load(binding);
        if (saved != null) {
          await _activateCredential(server, saved, persist: false);
        }
      }
    });
  }

  Future<void> openPairing(DiscoveredServer server) async {
    dispatch(OpenPairing(server.id));
    final opened = await platform.launcher.open(server.pairingUrl);
    if (!mounted) return;
    if (!opened) {
      _update(
        () => error = 'Could not open the Server-advertised pairing page.',
      );
      return;
    }
    final token = await showDialog<SessionToken>(
      context: context,
      builder: (context) => PairingCompletionDialog(server: server),
    );
    if (token == null || !mounted) {
      if (mounted) {
        dispatch(ServerDiscovered(server.withPairing(PairingStatus.available)));
      }
      return;
    }
    await _completePairing(server, token);
  }

  Future<void> _completePairing(
    DiscoveredServer server,
    SessionToken token,
  ) async {
    await _guard(
      () => _activateCredential(
        server,
        ControlCredential(
          binding: CredentialBinding(
            serverOrigin: server.origin,
            certificateSha256: server.certificateSha256,
          ),
          token: token,
        ),
        persist: true,
      ),
    );
    if (mounted && gateway == null) {
      dispatch(ServerDiscovered(server.withPairing(PairingStatus.available)));
    }
  }

  Future<void> _activateCredential(
    DiscoveredServer server,
    ControlCredential credential, {
    required bool persist,
  }) async {
    final endpoint = platform.endpoint(server);
    final connected = endpoint.authenticated(
      credential.token,
      onCredentialInvalidated: platform.vault.delete,
    );
    try {
      final advertised = await connected.discovery();
      if (advertised.pairingUrl.scheme != 'https' ||
          !advertisedFingerprintsEqual(
            advertised.certificateSha256,
            server.certificateSha256,
          )) {
        throw const FormatException(
          'Server certificate identity changed during pairing.',
        );
      }
      final loadedPolicy = await connected.policy();
      final loadedCatalog = await connected.catalog();
      final loadedQueue = await connected.queue();
      final loadedPreview = await connected.preview();
      final loadedDecision = await connected.explanation();
      CatalogPage? loadedPage;
      ZonesSnapshot? loadedInventory;
      PlaybackState? loadedPlayback;
      ControlLiveSession? loadedSession;
      ZoneId loadedZone = selectedZone;
      if ((connected.negotiatedProtocolMajor ?? 0) >= 3) {
        loadedSession = await connected.subscribe();
        loadedInventory = await connected.zones();
        if (loadedInventory.zones.isEmpty) {
          throw const FormatException('Server has no playback zones.');
        }
        loadedZone = loadedInventory.zones.first.id;
        loadedSession.watchZones({loadedZone});
        loadedPlayback = await connected.playbackState(loadedZone);
        loadedPage = await connected.browseCatalog(limit: 100);
      }
      if (persist) await platform.vault.save(credential);
      if (!mounted) {
        await loadedSession?.close();
        return;
      }
      await gateway?.close();
      _update(() {
        gateway = connected;
        catalog = loadedCatalog;
        queue = loadedQueue;
        preview = loadedPreview;
        decision = loadedDecision;
        catalogPage = loadedPage;
        inventory = loadedInventory;
        playback = loadedPlayback;
        liveSession = loadedSession;
        selectedZone = loadedZone;
        workflowFailure = null;
        state = applyPolicyView(
          state.reduce(PairingReturned(server.id)),
          loadedPolicy,
        );
      });
      if (loadedSession != null) await _listenToLiveUpdates(loadedSession);
    } on Object catch (failure, stack) {
      await connected.close();
      try {
        await platform.vault.delete();
      } on Object {
        Error.throwWithStackTrace(const CredentialVaultFailure(), stack);
      }
      Error.throwWithStackTrace(failure, stack);
    }
  }
}
