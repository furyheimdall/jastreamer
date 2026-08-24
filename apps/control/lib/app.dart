import 'package:flutter/material.dart';
import 'package:jstreamer_control/behavior_model.dart';
import 'package:jstreamer_control/control_gateway.dart';
import 'package:jstreamer_control/control_models.dart';
import 'package:jstreamer_control/control_platform.dart';
import 'package:jstreamer_control/control_policy_state.dart';
import 'package:jstreamer_control/control_theme.dart';
import 'package:jstreamer_control/discovery_panel.dart';
import 'package:jstreamer_control/pairing_widgets.dart';
import 'package:jstreamer_control/policy_panel.dart';
import 'package:jstreamer_control/protocol_compatibility.dart';
import 'package:jstreamer_control/status_panels.dart';
import 'package:jstreamer_control/tls_fingerprint.dart';

part 'control_home_view.dart';

final class ControlHome extends StatefulWidget {
  const ControlHome({
    required this.startDiscovered,
    this.platform,
    this.initialServer,
    this.initialFingerprint,
    super.key,
  });
  final bool startDiscovered;
  final ControlPlatform? platform;
  final Uri? initialServer;
  final String? initialFingerprint;

  @override
  State<ControlHome> createState() => _ControlHomeState();
}

final class _ControlHomeState extends State<ControlHome> {
  late final ControlPlatform platform = widget.platform ?? ControlPlatform();
  late final bool ownsPlatform = widget.platform == null;
  late final TextEditingController origin = TextEditingController(
    text: (widget.initialServer ?? _serverFromLocation())?.toString() ?? '',
  );
  late final TextEditingController fingerprint = TextEditingController(
    text: widget.initialFingerprint ??
        Uri.base.queryParameters['fingerprint'] ??
        '',
  );
  late ControlState state = widget.startDiscovered
      ? ControlState.fixture.reduce(const DiscoverServers())
      : ControlState.fixture;
  HttpControlGateway? gateway;
  CatalogView? catalog;
  QueueView? queue;
  PreviewView? preview;
  DecisionView? decision;
  bool busy = false;
  String? error;

  Uri? _serverFromLocation() {
    final raw = Uri.base.queryParameters['server'];
    return raw == null ? null : Uri.tryParse(raw);
  }

  void dispatch(ControlAction action) =>
      setState(() => state = state.reduce(action));

  Future<void> discover() async {
    final serverOrigin = Uri.tryParse(origin.text.trim());
    if (serverOrigin == null ||
        serverOrigin.scheme != 'https' ||
        !serverOrigin.hasAuthority) {
      setState(() => error = 'Enter a valid HTTPS Server address.');
      return;
    }
    await _guard(() async {
      final advertisedFingerprint = fingerprint.text.trim();
      final candidate = DiscoveredServer(
        id: const ServerId('discovered-server'),
        name: 'Jake Streamer Server',
        origin: serverOrigin,
        pairingUrl: serverOrigin.resolve('/pair/'),
        certificateSha256: advertisedFingerprint,
        pairing: PairingStatus.available,
      );
      final identity = advertisedFingerprint.isEmpty
          ? await platform.probe(serverOrigin).identity()
          : await platform.endpoint(candidate).identity();
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
    });
  }

  Future<void> openPairing(DiscoveredServer server) async {
    dispatch(OpenPairing(server.id));
    final opened = await platform.launcher.open(server.pairingUrl);
    if (!mounted) return;
    if (!opened) {
      setState(
        () => error = 'Could not open the Server-advertised pairing page.',
      );
      return;
    }
    final token = await showDialog<SessionToken>(
      context: context,
      builder: (context) => PairingCompletionDialog(server: server),
    );
    if (token == null || !mounted) return;
    await _completePairing(server, token);
  }

  Future<void> _completePairing(DiscoveredServer server, SessionToken token) =>
      _guard(() async {
        final endpoint = platform.endpoint(server);
        try {
          await platform.vault.store(token);
          final connected = endpoint.authenticated(token);
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
          if (!mounted) return;
          setState(() {
            gateway = connected;
            catalog = loadedCatalog;
            queue = loadedQueue;
            preview = loadedPreview;
            decision = loadedDecision;
            state = applyPolicyView(
              state.reduce(PairingReturned(server.id)),
              loadedPolicy,
            );
          });
        } on Object {
          await platform.vault.clear();
          endpoint.close();
          rethrow;
        }
      });

  Future<void> refreshServerViews() => _guard(() async {
        final active = gateway;
        if (active == null) {
          throw const FormatException(
              'Pair this Control before refreshing state.');
        }
        final loadedCatalog = await active.catalog();
        final loadedQueue = await active.queue();
        final loadedPreview = await active.preview();
        final loadedDecision = await active.explanation();
        if (!mounted) return;
        setState(() {
          catalog = loadedCatalog;
          queue = loadedQueue;
          preview = loadedPreview;
          decision = loadedDecision;
        });
      });

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
          setState(() => state = applyPolicyView(state, saved));
        },
        onStale: () async {
          final current = await gateway?.policy();
          if (current == null || !mounted) return;
          setState(() {
            state = state.reduce(
              PolicySaveRejected(serverRevision: current.revision),
            );
            if (current.mode.known case final policy?) {
              state = state.reduce(
                ServerPolicyRefreshed(
                  policy: policy,
                  revision: current.revision,
                ),
              );
            }
          });
        },
      );

  Future<void> _guard(
    Future<void> Function() action, {
    Future<void> Function()? onStale,
  }) async {
    setState(() {
      busy = true;
      error = null;
    });
    try {
      await action();
    } on StalePolicyFailure {
      if (onStale == null) rethrow;
      await onStale();
    } on ControlApiFailure catch (failure) {
      if (mounted) {
        setState(() => error = 'Server request failed: ${failure.code}');
      }
    } on UnsupportedProtocolMajor catch (failure) {
      if (mounted) setState(() => error = failure.code);
    } on FormatException catch (failure) {
      if (mounted) setState(() => error = failure.message.toString());
    } finally {
      if (mounted) setState(() => busy = false);
    }
  }

  @override
  void dispose() {
    origin.dispose();
    fingerprint.dispose();
    if (ownsPlatform) platform.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => buildControlSurface(context);
}
