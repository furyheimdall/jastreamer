import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:jstreamer_control/behavior_model.dart';
import 'package:jstreamer_control/control_models.dart';
import 'package:jstreamer_control/control_theme.dart';

final class PairingCompletionDialog extends StatefulWidget {
  const PairingCompletionDialog({required this.server, super.key});
  final DiscoveredServer server;
  @override
  State<PairingCompletionDialog> createState() =>
      _PairingCompletionDialogState();
}

final class _PairingCompletionDialogState
    extends State<PairingCompletionDialog> {
  final token = TextEditingController();

  void submit() {
    final value = token.text.trim();
    if (value.isNotEmpty) Navigator.pop(context, SessionToken(value));
  }

  @override
  void dispose() {
    token.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => AlertDialog(
        title: const Text('Complete pairing return'),
        content: ConstrainedBox(
          constraints: const BoxConstraints(maxWidth: 520),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              const Text(
                'The Server opened separately. Register this device, copy the issued controller token once, then return here.',
              ),
              const SizedBox(height: 12),
              const Text(
                'Todo13 has no safe token callback. The token never enters a URL, browser history, screenshot, or log.',
              ),
              const SizedBox(height: 12),
              TextField(
                controller: token,
                obscureText: true,
                enableSuggestions: false,
                autocorrect: false,
                inputFormatters: [
                  FilteringTextInputFormatter.deny(RegExp(r'\s'))
                ],
                onSubmitted: (_) => submit(),
                decoration: const InputDecoration(
                  border: OutlineInputBorder(),
                  labelText: 'Controller token',
                ),
              ),
            ],
          ),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context),
            child: const Text('Cancel'),
          ),
          FilledButton(
            onPressed: submit,
            child: const Text('Complete pairing'),
          ),
        ],
      );
}

final class PolicySaveStatus extends StatelessWidget {
  const PolicySaveStatus({
    required this.state,
    required this.busy,
    required this.save,
    super.key,
  });
  final ControlState state;
  final bool busy;
  final VoidCallback save;

  @override
  Widget build(BuildContext context) => Card(
        child: Padding(
          padding: const EdgeInsets.all(12),
          child: Row(
            children: [
              Icon(
                state.hasStaleIntent ? Icons.sync_problem : Icons.cloud_done,
                color: state.hasStaleIntent
                    ? ControlColors.statusWarning
                    : ControlColors.statusSuccess,
              ),
              const SizedBox(width: 12),
              Expanded(
                child: Semantics(
                  liveRegion: true,
                  child: Text(
                    state.hasStaleIntent
                        ? 'Stale revision ${state.policyRevision} · Server state rebased · desired intent preserved for retry'
                        : 'Server policy revision ${state.policyRevision} · ${state.isPolicySaved ? 'saved' : 'unsaved changes'}',
                  ),
                ),
              ),
              FilledButton(
                onPressed: busy ? null : save,
                child: Text(
                  state.hasStaleIntent ? 'Retry saved intent' : 'Save policy',
                ),
              ),
            ],
          ),
        ),
      );
}
