part of 'status_panels.dart';

final class _Metric extends StatelessWidget {
  const _Metric({
    required this.label,
    required this.value,
    required this.detail,
  });
  final String label;
  final int value;
  final String detail;
  @override
  Widget build(BuildContext context) => Semantics(
        label: '$label, $value percent, $detail',
        liveRegion: true,
        excludeSemantics: true,
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              mainAxisAlignment: MainAxisAlignment.spaceBetween,
              children: [Text(label), Text('$value%')],
            ),
            const SizedBox(height: 6),
            LinearProgressIndicator(
              value: value / 100,
              backgroundColor: ControlColors.progressTrack,
              valueColor:
                  const AlwaysStoppedAnimation(ControlColors.accentPrimary),
            ),
            const SizedBox(height: 4),
            Text(detail),
          ],
        ),
      );
}

final class _StateRow extends StatelessWidget {
  const _StateRow({
    required this.icon,
    required this.title,
    required this.detail,
    this.danger = false,
  });
  final IconData icon;
  final String title;
  final String detail;
  final bool danger;
  @override
  Widget build(BuildContext context) => Semantics(
        container: true,
        child: DecoratedBox(
          decoration: BoxDecoration(
            color: danger
                ? ControlColors.surfaceDanger
                : ControlColors.surfaceElevated,
            borderRadius: const BorderRadius.all(Radius.circular(8)),
          ),
          child: Padding(
            padding: const EdgeInsets.all(12),
            child: Row(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Icon(
                  icon,
                  color: danger
                      ? ControlColors.statusError
                      : ControlColors.accentPrimary,
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
                      const SizedBox(height: 2),
                      Text(detail),
                    ],
                  ),
                ),
              ],
            ),
          ),
        ),
      );
}
