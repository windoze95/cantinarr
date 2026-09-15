import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/providers/library_refresh_provider.dart';
import '../../../core/widgets/horizontal_item_row.dart';
import '../../../core/widgets/media_card.dart';
import '../../../core/widgets/section_header.dart';
import '../../../core/widgets/see_all_button.dart';
import '../../request/data/request_service.dart';
import '../data/music_discovery_service.dart';
import '../data/music_models.dart';
import '../logic/music_browse_query.dart';
import '../logic/music_feed_provider.dart';
import 'catalog_prefetch.dart';

class MusicDiscoveryCard extends ConsumerWidget {
  final MusicAlbum album;
  final String? instanceId;
  final double width;
  const MusicDiscoveryCard({
    super.key,
    required this.album,
    required this.instanceId,
    this.width = 120,
  });

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final status = instanceId == null
        ? null
        : ref
            .watch(musicCardStatusProvider(
                (id: album.foreignId, instanceId: instanceId!)))
            .valueOrNull;
    final cover = musicArtworkSource(ref, album, instanceId);
    final badge = status?.isKnown == true && status?.isRequestable == false
        ? status!.status
        : null;
    final color = switch (badge) {
      RequestStatus.available => AppTheme.available,
      RequestStatus.downloading || RequestStatus.partial => AppTheme.accent,
      _ => AppTheme.requested,
    };
    return MediaCard(
      key: ValueKey(album.foreignId),
      id: album.foreignId,
      title: album.title,
      subtitle: album.subtitle.isEmpty ? null : album.subtitle,
      posterPath: cover?.url,
      posterHeaders: cover?.headers,
      placeholderIcon: Icons.album,
      artworkAspectRatio: 1,
      width: width,
      statusLabel: badge?.label,
      statusColor: color,
      onTap: () => context.push(album.detailLocation(instanceId), extra: album),
    );
  }
}

class MusicPeriodSelector extends StatelessWidget {
  final String period;
  final ValueChanged<String> onChanged;
  const MusicPeriodSelector(
      {super.key, required this.period, required this.onChanged});
  @override
  Widget build(BuildContext context) => Wrap(
        spacing: 8,
        runSpacing: 4,
        children: [
          for (final entry in musicPeriods.entries)
            ChoiceChip(
              label: Text(entry.value),
              selected: period == entry.key,
              onSelected: (selected) {
                if (selected) onChanged(entry.key);
              },
            ),
        ],
      );
}

class MusicFeedNotice extends StatelessWidget {
  final MusicFeedState state;
  final VoidCallback retry;
  const MusicFeedNotice({super.key, required this.state, required this.retry});
  @override
  Widget build(BuildContext context) {
    final message = state.error ??
        (state.items.isEmpty && !state.loading ? state.emptyMessage : null);
    if (message == null || message.isEmpty) return const SizedBox.shrink();
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
      child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
        Text(message, style: const TextStyle(color: AppTheme.textSecondary)),
        if (state.error != null && !state.unsupported)
          TextButton(
              onPressed: state.loading ? null : retry,
              child: const Text('Retry')),
      ]),
    );
  }
}

class MusicDiscoveryRow extends ConsumerWidget {
  final MusicBrowseQuery query;
  final ValueChanged<String>? onPeriodChanged;
  const MusicDiscoveryRow(
      {super.key, required this.query, this.onPeriodChanged});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final state = ref.watch(musicFeedProvider(query));
    final notifier = ref.read(musicFeedProvider(query).notifier);
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (context.mounted) notifier.enablePrefetch();
    });
    final viewport = MediaQuery.sizeOf(context).width;
    final width = viewport >= 900
        ? 124.0
        : viewport >= 600
            ? 116.0
            : 108.0;
    return CatalogArtworkPrefetch(
        sources: state.upcoming
            .take(6)
            .map((a) => musicArtworkSource(ref, a, query.instanceId))
            .whereType<({String url, Map<String, String>? headers})>()
            .toList(),
        child: Padding(
          padding: const EdgeInsets.only(top: 20),
          child:
              Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
            Padding(
              padding:
                  EdgeInsets.symmetric(horizontal: viewport >= 900 ? 24 : 16),
              child: SectionHeader(
                  title: query.title,
                  trailing: Row(
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      IconButton(
                        tooltip: 'Refresh ${query.title}',
                        icon: const Icon(Icons.refresh, size: 20),
                        onPressed: state.loading
                            ? null
                            : () {
                                ref
                                    .read(libraryRefreshTickProvider.notifier)
                                    .state++;
                                notifier.refresh();
                              },
                      ),
                      SeeAllButton(
                          rowTitle: query.title,
                          onPressed: () => context.push(query.location)),
                    ],
                  )),
            ),
            Padding(
              padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 4),
              child: onPeriodChanged == null
                  ? Text(query.description,
                      style: const TextStyle(color: AppTheme.textSecondary))
                  : MusicPeriodSelector(
                      period: query.period, onChanged: onPeriodChanged!),
            ),
            MusicFeedNotice(state: state, retry: notifier.retry),
            if (state.loading || state.items.isNotEmpty)
              HorizontalItemRow<MusicAlbum>(
                key: PageStorageKey(query.location),
                items: state.items,
                isLoading: state.loading,
                paginationExtent: viewport,
                cacheExtent: viewport,
                onItemAppear: (_) {
                  if (state.error == null) notifier.loadMore();
                },
                artworkAspectRatio: 1,
                height: width + MediaCard.subtitleRowExtraHeight,
                itemBuilder: (album) => MusicDiscoveryCard(
                    album: album, instanceId: query.instanceId, width: width),
              ),
            if (!state.loading &&
                state.items.isEmpty &&
                state.nextPage != null &&
                state.error == null)
              TextButton(
                  onPressed: notifier.loadMore, child: const Text('Load more')),
          ]),
        ));
  }
}

class MusicGenreStrip extends ConsumerWidget {
  final String? instanceId;
  const MusicGenreStrip({super.key, required this.instanceId});
  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final genres = ref.watch(musicGenresProvider(instanceId));
    final previous = genres.valueOrNull;
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 20, 16, 0),
      child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
        const SectionHeader(title: 'Browse by genre'),
        const SizedBox(height: 8),
        if (genres.isLoading && previous == null)
          const LinearProgressIndicator(),
        if (genres.hasError) ...[
          Text(genres.error is MusicDiscoveryUnsupported
              ? musicUpdateMessage
              : previous == null
                  ? 'Could not load music genres.'
                  : 'Refresh failed. Your previous genres are still shown.'),
          if (genres.error is! MusicDiscoveryUnsupported)
            TextButton(
                onPressed: () =>
                    ref.invalidate(musicGenresProvider(instanceId)),
                child: const Text('Retry')),
        ],
        if (previous != null)
          Wrap(spacing: 8, runSpacing: 4, children: [
            for (final genre in previous)
              ActionChip(
                  label: Text(genre.name),
                  onPressed: () => context.push(MusicBrowseQuery(
                          feed: 'genre',
                          instanceId: instanceId,
                          genre: genre.id)
                      .location)),
          ]),
      ]),
    );
  }
}
