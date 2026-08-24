import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:jastreamer_control/behavior_model.dart';
import 'package:jastreamer_control/control_theme.dart';

final class DiscoveryPanel extends StatelessWidget {
  const DiscoveryPanel({
    required this.state,
    required this.origin,
    required this.fingerprint,
    required this.busy,
    required this.error,
    required this.discover,
    required this.openPairing,
    super.key,
  });
  final ControlState state;
  final TextEditingController origin;
  final TextEditingController fingerprint;
  final bool busy;
  final String? error;
  final VoidCallback discover;
  final ValueChanged<DiscoveredServer> openPairing;

  @override
  Widget build(BuildContext context) => Card(
        child: Padding(
          padding: const EdgeInsets.all(16),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Semantics(
                header: true,
                child: Text(
                  'Find a jastreamer server',
                  style: Theme.of(context).textTheme.titleLarge,
                ),
              ),
              const SizedBox(height: 4),
              const Text(
                'Enter a local Server HTTPS address. Control checks public identity before pairing.',
              ),
              const SizedBox(height: 12),
              TextField(
                controller: origin,
                enabled: !busy,
                keyboardType: TextInputType.url,
                autofillHints: const [AutofillHints.url],
                decoration: const InputDecoration(
                  border: OutlineInputBorder(),
                  labelText: 'Server HTTPS address',
                  hintText: 'https://music-server.local:8443',
                ),
                onSubmitted: (_) => discover(),
              ),
              const SizedBox(height: 12),
              TextField(
                controller: fingerprint,
                enabled: !busy,
                inputFormatters: [
                  FilteringTextInputFormatter.allow(RegExp('[0-9A-Fa-f:-]')),
                ],
                decoration: const InputDecoration(
                  border: OutlineInputBorder(),
                  labelText: 'Advertised SHA-256 fingerprint',
                  helperText:
                      'Required on Windows and Android; Web uses browser certificate trust.',
                  helperMaxLines: 2,
                ),
                onSubmitted: (_) => discover(),
              ),
              const SizedBox(height: 12),
              FilledButton.icon(
                key: const Key('discover-server-button'),
                style: ButtonStyle(
                  side: WidgetStateProperty.resolveWith((states) => BorderSide(
                        color: states.contains(WidgetState.focused)
                            ? ControlColors.textPrimary
                            : Colors.transparent,
                        width: 2,
                      )),
                ),
                onPressed: busy ? null : discover,
                icon: const Icon(Icons.radar),
                label: const Text('Discover Server'),
              ),
              if (busy) ...[
                const SizedBox(height: 12),
                const LinearProgressIndicator(
                    semanticsLabel: 'Contacting Server'),
              ],
              if (error != null) ...[
                const SizedBox(height: 12),
                Semantics(
                  liveRegion: true,
                  child: Text(
                    error!,
                    style: const TextStyle(color: ControlColors.statusError),
                  ),
                ),
              ],
              if (state.discovered)
                ...state.servers.map(
                  (server) =>
                      ServerCard(server: server, openPairing: openPairing),
                ),
            ],
          ),
        ),
      );
}

final class ServerCard extends StatelessWidget {
  const ServerCard({
    required this.server,
    required this.openPairing,
    super.key,
  });
  final DiscoveredServer server;
  final ValueChanged<DiscoveredServer> openPairing;

  @override
  Widget build(BuildContext context) {
    final paired = server.pairing == PairingStatus.paired;
    return Padding(
      padding: const EdgeInsets.only(top: 12),
      child: DecoratedBox(
        decoration: const BoxDecoration(
          color: ControlColors.surfaceElevated,
          borderRadius: BorderRadius.all(Radius.circular(8)),
        ),
        child: Padding(
          padding: const EdgeInsets.all(12),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Row(
                children: [
                  Icon(
                    paired ? Icons.verified_user : Icons.dns_outlined,
                    color: paired
                        ? ControlColors.statusSuccess
                        : ControlColors.accentPrimary,
                  ),
                  const SizedBox(width: 8),
                  Expanded(
                    child: Text(
                      server.name,
                      style: Theme.of(context).textTheme.titleMedium,
                    ),
                  ),
                  if (paired) const Chip(label: Text('Paired device')),
                ],
              ),
              const SizedBox(height: 8),
              SelectableText(server.origin.toString()),
              const SizedBox(height: 4),
              const Text('SHA-256 certificate fingerprint'),
              SelectableText(
                server.certificateSha256,
                style: const TextStyle(fontFamily: 'monospace'),
              ),
              const SizedBox(height: 8),
              Semantics(
                liveRegion: true,
                child: Text(
                  paired
                      ? 'Paired device · authenticated discovery verified · session token held only in memory'
                      : 'Compare this fingerprint with the Server console before entering a one-time code.',
                ),
              ),
              const SizedBox(height: 8),
              if (!paired)
                OutlinedButton.icon(
                  onPressed: () => openPairing(server),
                  icon: const Icon(Icons.open_in_new),
                  label: const Text('Open pairing page'),
                ),
            ],
          ),
        ),
      ),
    );
  }
}
