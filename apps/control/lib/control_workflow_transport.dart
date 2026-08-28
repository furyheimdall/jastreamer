part of 'control_workflow.dart';

final class _ZoneTransportPanel extends StatelessWidget {
  const _ZoneTransportPanel({
    required this.inventory,
    required this.playback,
    required this.disabled,
    required this.seekSeconds,
    required this.selectZone,
    required this.assign,
    required this.transport,
  });
  final ZonesSnapshot inventory;
  final PlaybackState playback;
  final bool disabled;
  final TextEditingController seekSeconds;
  final ValueChanged<ZoneId> selectZone;
  final ValueChanged<RendererId?> assign;
  final ValueChanged<TransportMutationIntent> transport;

  @override
  Widget build(BuildContext context) {
    final zone = inventory.zones.cast<ZoneView?>().firstWhere(
          (candidate) => candidate?.id == playback.zoneId,
          orElse: () => null,
        );
    return _Surface(
      title: 'Zone & playback',
      subtitle: 'Server intent and observed Renderer truth remain separate.',
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          DropdownButtonFormField<ZoneId>(
            key: ValueKey(playback.zoneId),
            isExpanded: true,
            initialValue: playback.zoneId,
            decoration: const InputDecoration(
              labelText: 'Playback zone',
              border: OutlineInputBorder(),
            ),
            items: inventory.zones
                .map(
                  (value) => DropdownMenuItem(
                    value: value.id,
                    child: Text(value.name),
                  ),
                )
                .toList(growable: false),
            onChanged: disabled
                ? null
                : (value) {
                    if (value != null && value != playback.zoneId) {
                      selectZone(value);
                    }
                  },
          ),
          const SizedBox(height: 12),
          DropdownButtonFormField<RendererId?>(
            key: ValueKey(zone?.rendererId),
            isExpanded: true,
            initialValue: zone?.rendererId,
            decoration: const InputDecoration(
              labelText: 'Assigned Renderer',
              border: OutlineInputBorder(),
            ),
            items: [
              const DropdownMenuItem<RendererId?>(
                value: null,
                child: Text('No Renderer assigned'),
              ),
              ...inventory.renderers.map(
                (renderer) => DropdownMenuItem<RendererId?>(
                  value: renderer.id,
                  child: Text(
                    '${renderer.name} · ${renderer.status.wireValue}',
                  ),
                ),
              ),
            ],
            onChanged: disabled ? null : assign,
          ),
          const SizedBox(height: 16),
          Wrap(
            spacing: 8,
            runSpacing: 8,
            children: [
              _TruthChip(
                icon: Icons.cloud_outlined,
                label: 'Server intent: ${playback.logicalTransport.wireValue}',
                color: ControlColors.accentPrimary,
              ),
              _TruthChip(
                icon: Icons.speaker_outlined,
                label:
                    'Renderer observed: ${playback.observedTransport.wireValue}',
                color: switch (playback.observedTransport.known) {
                  ObservedTransport.error => ControlColors.statusError,
                  ObservedTransport.unknown ||
                  null =>
                    ControlColors.textSecondary,
                  _ => ControlColors.statusSuccess,
                },
              ),
              _TruthChip(
                icon: Icons.schedule,
                label: playback.pendingCommandId == null
                    ? 'No pending command'
                    : 'Pending command: ${playback.pendingCommandId!.value}',
                color: playback.pendingCommandId == null
                    ? ControlColors.textSecondary
                    : ControlColors.statusWarning,
              ),
            ],
          ),
          const SizedBox(height: 16),
          Wrap(
            spacing: 8,
            runSpacing: 8,
            children: [
              _TransportButton(
                key: const Key('transport-previous'),
                label: 'Previous',
                icon: Icons.skip_previous,
                disabled: disabled,
                press: () =>
                    transport(const TransportMutationIntent.previous()),
              ),
              _TransportButton(
                key: const Key('transport-play'),
                label: 'Play',
                icon: Icons.play_arrow,
                disabled: disabled,
                press: () => transport(const TransportMutationIntent.start()),
              ),
              _TransportButton(
                key: const Key('transport-pause'),
                label: 'Pause',
                icon: Icons.pause,
                disabled: disabled,
                press: () => transport(const TransportMutationIntent.pause()),
              ),
              _TransportButton(
                key: const Key('transport-resume'),
                label: 'Resume',
                icon: Icons.play_circle_outline,
                disabled: disabled,
                press: () => transport(const TransportMutationIntent.resume()),
              ),
              _TransportButton(
                key: const Key('transport-next'),
                label: 'Next',
                icon: Icons.skip_next,
                disabled: disabled,
                press: () => transport(const TransportMutationIntent.skip()),
              ),
              _TransportButton(
                key: const Key('transport-stop'),
                label: 'Stop',
                icon: Icons.stop,
                disabled: disabled,
                press: () => transport(const TransportMutationIntent.stop()),
              ),
            ],
          ),
          const SizedBox(height: 12),
          Row(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Expanded(
                child: TextField(
                  key: const Key('seek-seconds'),
                  controller: seekSeconds,
                  enabled: !disabled,
                  keyboardType: TextInputType.number,
                  inputFormatters: [FilteringTextInputFormatter.digitsOnly],
                  decoration: const InputDecoration(
                    labelText: 'Seek position (seconds)',
                    border: OutlineInputBorder(),
                  ),
                  onSubmitted: (_) => _seek(),
                ),
              ),
              const SizedBox(width: 8),
              FilledButton.icon(
                key: const Key('transport-seek'),
                onPressed: disabled ? null : _seek,
                icon: const Icon(Icons.timeline),
                label: const Text('Seek'),
              ),
            ],
          ),
        ],
      ),
    );
  }

  void _seek() {
    final seconds = int.tryParse(seekSeconds.text);
    if (seconds != null) {
      transport(TransportMutationIntent.seek(seconds * 1000));
    }
  }
}
