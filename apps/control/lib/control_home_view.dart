part of 'app.dart';

extension _ControlHomeView on _ControlHomeState {
  Widget buildControlSurface(BuildContext context) => Scaffold(
        appBar: AppBar(
          title: const Row(
            children: [
              Icon(Icons.graphic_eq, color: ControlColors.accentPrimary),
              SizedBox(width: 8),
              Text('jastreamer'),
            ],
          ),
          backgroundColor: ControlColors.surfaceSecondary,
        ),
        body: CustomScrollView(
          key: const Key('control-scroll-body'),
          slivers: [
            SliverPadding(
              padding: const EdgeInsets.all(24),
              sliver: SliverToBoxAdapter(
                child: Center(
                  child: ConstrainedBox(
                    constraints: const BoxConstraints(maxWidth: 1120),
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Semantics(
                          header: true,
                          child: Text(
                            'Control room',
                            style: Theme.of(context).textTheme.headlineMedium,
                          ),
                        ),
                        const Text(
                          'Server truth, clearly separated from your playback intent.',
                        ),
                        const SizedBox(height: 24),
                        DiscoveryPanel(
                          state: state,
                          origin: origin,
                          fingerprint: fingerprint,
                          busy: busy,
                          error: error,
                          discover: discover,
                          openPairing: openPairing,
                        ),
                        if (gateway != null &&
                            catalog != null &&
                            queue != null &&
                            preview != null &&
                            decision != null) ...[
                          const SizedBox(height: 16),
                          PolicySaveStatus(
                            state: state,
                            busy: busy,
                            save: savePolicy,
                          ),
                          const SizedBox(height: 8),
                          Align(
                            alignment: Alignment.centerRight,
                            child: OutlinedButton.icon(
                              onPressed: busy ? null : refreshServerViews,
                              icon: const Icon(Icons.refresh),
                              label: const Text('Refresh Server state'),
                            ),
                          ),
                          const SizedBox(height: 16),
                          PolicyPanel(state: state, dispatch: dispatch),
                          const SizedBox(height: 16),
                          CoveragePanel(catalog: catalog!),
                          const SizedBox(height: 16),
                          DecisionPanel(decision: decision!),
                          const SizedBox(height: 16),
                          QueuePreviewPanel(queue: queue!, preview: preview!),
                        ],
                        const SizedBox(height: 32),
                      ],
                    ),
                  ),
                ),
              ),
            ),
          ],
        ),
      );
}
