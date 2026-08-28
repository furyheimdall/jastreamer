import 'package:flutter/material.dart';
import 'package:jastreamer_control/behavior_model.dart';
import 'package:jastreamer_control/control_theme.dart';

final class PolicyPanel extends StatelessWidget {
  const PolicyPanel({required this.state, required this.dispatch, super.key});
  final ControlState state;
  final ValueChanged<ControlAction> dispatch;

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
                  'Continuation policy',
                  style: Theme.of(context).textTheme.titleLarge,
                ),
              ),
              const SizedBox(height: 4),
              const Text(
                'Applied by the Server only after the explicit queue is empty.',
              ),
              const SizedBox(height: 16),
              Wrap(
                spacing: 8,
                runSpacing: 8,
                children: Policy.values
                    .map(
                      (policy) => ChoiceChip(
                        label: Text(policy.korean),
                        selected: state.pendingPolicy == policy,
                        selectedColor: ControlColors.accentPrimary,
                        onSelected: (_) => dispatch(SelectPolicy(policy)),
                      ),
                    )
                    .toList(growable: false),
              ),
              const SizedBox(height: 20),
              Text(
                'Per-session override',
                style: Theme.of(context).textTheme.titleMedium,
              ),
              const SizedBox(height: 8),
              DropdownButtonFormField<Policy?>(
                key: ValueKey(state.sessionOverride),
                initialValue: state.sessionOverride,
                decoration: const InputDecoration(
                  border: OutlineInputBorder(),
                  labelText: 'Current session only',
                ),
                items: [
                  const DropdownMenuItem<Policy?>(
                    value: null,
                    child: Text('Use persisted policy'),
                  ),
                  ...Policy.values.map(
                    (policy) => DropdownMenuItem<Policy?>(
                      value: policy,
                      child: Text(policy.korean),
                    ),
                  ),
                ],
                onChanged: (policy) => dispatch(SelectSessionOverride(policy)),
              ),
              const SizedBox(height: 20),
              _Cooldown(
                label: 'Artist cooldown',
                value: state.artistCooldown,
                onChanged: (value) => dispatch(SetArtistCooldown(value)),
              ),
              _Cooldown(
                label: 'Album cooldown',
                value: state.albumCooldown,
                onChanged: (value) => dispatch(SetAlbumCooldown(value)),
              ),
              Align(
                alignment: Alignment.centerRight,
                child: TextButton.icon(
                  onPressed: () => dispatch(const ResetCooldowns()),
                  icon: const Icon(Icons.restart_alt),
                  label: const Text('Restore defaults (artist 4 · album 10)'),
                ),
              ),
              if (state.hasStaleIntent)
                Semantics(
                  liveRegion: true,
                  child: const Text(
                    'Server revision changed. Your selected intent is preserved; review and save again.',
                    style: TextStyle(color: ControlColors.statusWarning),
                  ),
                ),
            ],
          ),
        ),
      );
}

final class _Cooldown extends StatelessWidget {
  const _Cooldown({
    required this.label,
    required this.value,
    required this.onChanged,
  });
  final String label;
  final int value;
  final ValueChanged<int> onChanged;

  @override
  Widget build(BuildContext context) => Semantics(
        label: '$label, $value tracks',
        slider: true,
        child: Row(
          children: [
            SizedBox(width: 128, child: Text(label)),
            Expanded(
              child: Slider(
                value: value.toDouble(),
                min: 0,
                max: 20,
                divisions: 20,
                label: '$value',
                onChanged: (next) => onChanged(next.round()),
              ),
            ),
            SizedBox(width: 24, child: Text('$value')),
          ],
        ),
      );
}
