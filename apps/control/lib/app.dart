import 'dart:async';

import 'package:flutter/material.dart';
import 'package:jastreamer_control/behavior_model.dart';
import 'package:jastreamer_control/control_events.dart';
import 'package:jastreamer_control/control_gateway.dart';
import 'package:jastreamer_control/control_models.dart';
import 'package:jastreamer_control/control_platform.dart';
import 'package:jastreamer_control/control_policy_state.dart';
import 'package:jastreamer_control/control_theme.dart';
import 'package:jastreamer_control/control_workflow.dart';
import 'package:jastreamer_control/credential_vault.dart';
import 'package:jastreamer_control/discovery_panel.dart';
import 'package:jastreamer_control/pairing_widgets.dart';
import 'package:jastreamer_control/policy_panel.dart';
import 'package:jastreamer_control/protocol_compatibility.dart';
import 'package:jastreamer_control/status_panels.dart';
import 'package:jastreamer_control/tls_fingerprint.dart';

part 'control_home_view.dart';
part 'control_app_pairing.dart';
part 'control_app_live.dart';
part 'control_app_mutations.dart';
part 'control_app_actions.dart';

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
  CatalogPage? catalogPage;
  ZonesSnapshot? inventory;
  PlaybackState? playback;
  ControlLiveSession? liveSession;
  StreamSubscription<LiveResourceUpdate>? liveUpdates;
  StreamSubscription<ControlEvent>? liveEvents;
  ZoneId selectedZone = const ZoneId('main');
  String? syncNotice;
  ControlFailure? workflowFailure;
  int mutationSequence = 0;
  bool busy = false;
  String? error;

  Uri? _serverFromLocation() {
    final raw = Uri.base.queryParameters['server'];
    return raw == null ? null : Uri.tryParse(raw);
  }

  void dispatch(ControlAction action) =>
      setState(() => state = state.reduce(action));

  void _update(VoidCallback callback) => setState(callback);

  @override
  void dispose() {
    origin.dispose();
    fingerprint.dispose();
    unawaited(liveUpdates?.cancel());
    unawaited(liveEvents?.cancel());
    unawaited(liveSession?.close());
    gateway?.close();
    if (ownsPlatform) platform.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => buildControlSurface(context);
}
