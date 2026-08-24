import 'package:flutter/material.dart';
import 'package:jastreamer_control/app.dart';
import 'package:jastreamer_control/control_platform.dart';
import 'package:jastreamer_control/control_theme.dart';

final class ControlApp extends StatelessWidget {
  const ControlApp({
    this.startDiscovered = false,
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
  Widget build(BuildContext context) => MaterialApp(
        debugShowCheckedModeBanner: false,
        title: 'jastreamer Control',
        theme: controlTheme(),
        home: ControlHome(
          startDiscovered: startDiscovered,
          platform: platform,
          initialServer: initialServer,
          initialFingerprint: initialFingerprint,
        ),
      );
}
