part of 'control_workflow.dart';

final class _ExplicitQueuePanel extends StatelessWidget {
  const _ExplicitQueuePanel({
    required this.playback,
    required this.tracks,
    required this.disabled,
    required this.mutate,
  });
  final PlaybackState playback;
  final List<CatalogTrack> tracks;
  final bool disabled;
  final ValueChanged<QueueMutationIntent> mutate;

  @override
  Widget build(BuildContext context) => _Surface(
        title: 'Explicit queue',
        subtitle:
            'User-authored order only. Automatic continuation never appears here.',
        trailing: TextButton.icon(
          onPressed: disabled || playback.entries.isEmpty
              ? null
              : () => mutate(const QueueMutationIntent.clear()),
          icon: const Icon(Icons.clear_all),
          label: const Text('Clear'),
        ),
        child: playback.entries.isEmpty
            ? const _EmptyState(
                icon: Icons.queue_music,
                title: 'Explicit queue empty',
                detail: 'Add a track from the Server catalog.',
              )
            : Column(
                children: [
                  for (var index = 0; index < playback.entries.length; index++)
                    _QueueRow(
                      entry: playback.entries[index],
                      title: _title(playback.entries[index].trackId),
                      index: index,
                      total: playback.entries.length,
                      disabled: disabled,
                      moveUp: () => mutate(
                        QueueMutationIntent.move(
                          playback.entries[index].id,
                          beforeEntryId: playback.entries[index - 1].id,
                        ),
                      ),
                      moveDown: () => mutate(
                        QueueMutationIntent.move(
                          playback.entries[index].id,
                          beforeEntryId: index + 2 < playback.entries.length
                              ? playback.entries[index + 2].id
                              : null,
                        ),
                      ),
                      remove: () => mutate(
                        QueueMutationIntent.remove(playback.entries[index].id),
                      ),
                      retry: () => mutate(
                        QueueMutationIntent.retryBlocked(
                          playback.entries[index].id,
                        ),
                      ),
                      skip: () => mutate(
                        QueueMutationIntent.skipBlocked(
                            playback.entries[index].id),
                      ),
                    ),
                ],
              ),
      );

  String _title(TrackId id) {
    for (final track in tracks) {
      if (track.id == id) return track.title;
    }
    return id.value;
  }
}

final class _QueueRow extends StatelessWidget {
  const _QueueRow({
    required this.entry,
    required this.title,
    required this.index,
    required this.total,
    required this.disabled,
    required this.moveUp,
    required this.moveDown,
    required this.remove,
    required this.retry,
    required this.skip,
  });
  final PlaybackQueueEntry entry;
  final String title;
  final int index;
  final int total;
  final bool disabled;
  final VoidCallback moveUp;
  final VoidCallback moveDown;
  final VoidCallback remove;
  final VoidCallback retry;
  final VoidCallback skip;

  @override
  Widget build(BuildContext context) {
    final blocked = entry.state.known == QueueEntryState.blocked;
    return Padding(
      padding: const EdgeInsets.only(bottom: 8),
      child: DecoratedBox(
        decoration: BoxDecoration(
          color: blocked
              ? ControlColors.surfaceDanger
              : ControlColors.surfaceElevated,
          borderRadius: const BorderRadius.all(Radius.circular(8)),
        ),
        child: Padding(
          padding: const EdgeInsets.fromLTRB(12, 8, 8, 8),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Row(
                children: [
                  Text(
                    '${index + 1}',
                    style: Theme.of(context).textTheme.labelMedium,
                  ),
                  const SizedBox(width: 12),
                  Expanded(
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Text(
                          title,
                          style: Theme.of(context).textTheme.titleMedium,
                        ),
                        Text(entry.state.wireValue),
                        Text(
                          entry.trackId.value,
                          style: Theme.of(context).textTheme.labelMedium,
                        ),
                      ],
                    ),
                  ),
                ],
              ),
              Align(
                alignment: Alignment.centerRight,
                child: Wrap(
                  spacing: 4,
                  children: [
                    _QueueAction(
                      label: 'Move entry ${entry.id.value} earlier',
                      tooltip: 'Move $title earlier',
                      visibleLabel: 'Earlier',
                      onPressed: disabled || index == 0 ? null : moveUp,
                      icon: Icons.arrow_upward,
                    ),
                    _QueueAction(
                      label: 'Move entry ${entry.id.value} later',
                      tooltip: 'Move $title later',
                      visibleLabel: 'Later',
                      onPressed:
                          disabled || index == total - 1 ? null : moveDown,
                      icon: Icons.arrow_downward,
                    ),
                    _QueueAction(
                      label: 'Remove entry ${entry.id.value}',
                      tooltip: 'Remove $title',
                      visibleLabel: 'Remove',
                      onPressed: disabled ? null : remove,
                      icon: Icons.remove_circle_outline,
                    ),
                  ],
                ),
              ),
              if (blocked) ...[
                Text(
                  entry.blockedError == null
                      ? 'Blocked by Server'
                      : '${entry.blockedError!.code}: ${entry.blockedError!.message}',
                  style: const TextStyle(color: ControlColors.statusError),
                ),
                Wrap(
                  spacing: 8,
                  children: [
                    TextButton.icon(
                      onPressed: disabled ? null : retry,
                      icon: const Icon(Icons.refresh),
                      label: const Text('Retry blocked track'),
                    ),
                    TextButton.icon(
                      onPressed: disabled ? null : skip,
                      icon: const Icon(Icons.skip_next),
                      label: const Text('Skip blocked track'),
                    ),
                  ],
                ),
              ],
            ],
          ),
        ),
      ),
    );
  }
}

final class _QueueAction extends StatelessWidget {
  const _QueueAction({
    required this.label,
    required this.tooltip,
    required this.visibleLabel,
    required this.onPressed,
    required this.icon,
  });
  final String label;
  final String tooltip;
  final String visibleLabel;
  final VoidCallback? onPressed;
  final IconData icon;

  @override
  Widget build(BuildContext context) => Semantics(
        container: true,
        button: true,
        enabled: onPressed != null,
        focusable: onPressed != null,
        onTap: onPressed,
        label: label,
        child: Tooltip(
          message: tooltip,
          child: SizedBox(
            height: 48,
            child: TextButton.icon(
              style: const ButtonStyle(
                minimumSize: WidgetStatePropertyAll(Size(48, 48)),
                tapTargetSize: MaterialTapTargetSize.padded,
                visualDensity: VisualDensity.standard,
              ),
              onPressed: onPressed,
              icon: Icon(icon),
              label: Text(visibleLabel),
            ),
          ),
        ),
      );
}
