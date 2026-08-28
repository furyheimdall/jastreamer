part of 'control_workflow.dart';

final class _CatalogPanel extends StatelessWidget {
  const _CatalogPanel({
    required this.page,
    required this.search,
    required this.disabled,
    required this.submitSearch,
    required this.add,
  });
  final CatalogPage page;
  final TextEditingController search;
  final bool disabled;
  final VoidCallback submitSearch;
  final ValueChanged<CatalogTrack> add;

  @override
  Widget build(BuildContext context) => _Surface(
        title: 'Browse catalog',
        subtitle: 'Search Server-indexed music and add it explicitly.',
        child: Column(
          children: [
            TextField(
              key: const Key('catalog-search'),
              controller: search,
              enabled: !disabled,
              textInputAction: TextInputAction.search,
              onSubmitted: (_) => submitSearch(),
              decoration: InputDecoration(
                labelText: 'Search title, artist, or album',
                border: const OutlineInputBorder(),
                suffixIcon: IconButton(
                  tooltip: 'Search catalog',
                  onPressed: disabled ? null : submitSearch,
                  icon: const Icon(Icons.search),
                ),
              ),
            ),
            const SizedBox(height: 12),
            if (page.tracks.isEmpty)
              const _EmptyState(
                icon: Icons.library_music_outlined,
                title: 'No matching tracks',
                detail: 'Change the search or wait for catalog indexing.',
              )
            else
              ...page.tracks.map(
                (track) => Padding(
                  padding: const EdgeInsets.only(bottom: 8),
                  child: DecoratedBox(
                    decoration: const BoxDecoration(
                      color: ControlColors.surfaceElevated,
                      borderRadius: BorderRadius.all(Radius.circular(8)),
                    ),
                    child: ListTile(
                      minVerticalPadding: 12,
                      onTap: disabled || !track.available
                          ? null
                          : () => add(track),
                      title: Semantics(
                        label: 'Add track ${track.id.value} to explicit queue',
                        excludeSemantics: true,
                        child: Text(track.title),
                      ),
                      subtitle: Text(
                        '${track.artists.join(', ')} · ${track.album}${track.available ? '' : ' · unavailable'}',
                      ),
                      trailing: ExcludeSemantics(
                        child: IconButton(
                          key: Key('catalog-add-${track.id.value}'),
                          tooltip: track.available
                              ? 'Add ${track.title} to explicit queue'
                              : '${track.title} is unavailable',
                          onPressed: disabled || !track.available
                              ? null
                              : () => add(track),
                          icon: const Icon(Icons.add_to_queue),
                        ),
                      ),
                    ),
                  ),
                ),
              ),
            Text(
              'Catalog revision ${page.revision}${page.nextCursor == null ? '' : ' · more results available'}',
              style: Theme.of(context).textTheme.labelMedium,
            ),
          ],
        ),
      );
}
