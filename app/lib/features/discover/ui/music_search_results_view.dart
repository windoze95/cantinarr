import 'package:cantinarr/features/discover/ui/browse_labels.dart';
import 'package:cantinarr/l10n/l10n.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import '../../../core/layout/adaptive.dart';
import '../../../core/providers/instance_provider.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/cached_image.dart';
import '../../dashboard/data/music_library_service.dart';
import '../../dashboard/logic/book_ownership_matcher.dart'
    show titleMatchesQuery;
import '../../lidarr/data/lidarr_image.dart';
import '../../request/data/album_ownership.dart';
import '../../shell/logic/library_artist_index.dart';
import '../../shell/logic/shell_music_search_provider.dart';
import '../data/music_discovery_service.dart';
import '../data/music_models.dart';
import '../logic/discovery_access.dart';
import '../logic/music_enrichment.dart';

/// Albums retain catalog relevance order. Artist and status updates never
/// replace the list or move owned albums ahead of the selected result.
class MusicSearchResultsView extends ConsumerStatefulWidget {
  final List<MusicAlbum> results;
  final List<MusicArtist> artists;
  final String query;
  final bool isLoading;
  final bool artistsLoading;
  final bool searched;
  final MusicSearchError? error;
  final bool artistsUnavailable;
  final VoidCallback? onResultTap;
  const MusicSearchResultsView(
      {super.key,
      required this.results,
      this.artists = const [],
      required this.query,
      required this.isLoading,
      this.artistsLoading = false,
      required this.searched,
      required this.error,
      this.artistsUnavailable = false,
      this.onResultTap});
  @override
  ConsumerState<MusicSearchResultsView> createState() =>
      _MusicSearchResultsViewState();
}

class _MusicSearchResultsViewState
    extends ConsumerState<MusicSearchResultsView> {
  late final ScrollController _scroll;
  String? _scope;
  @override
  void initState() {
    super.initState();
    _scroll = ScrollController(
        initialScrollOffset:
            ref.read(shellMusicSearchProvider.notifier).scrollOffset);
    _scroll.addListener(() => ref
        .read(shellMusicSearchProvider.notifier)
        .scrollOffset = _scroll.offset);
  }

  @override
  void dispose() {
    _scroll.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final id = ref.watch(instanceProvider).activeLidarrInstance?.id;
    final scope =
        '${ref.watch(catalogDiscoveryScopeProvider)}:$id:${widget.query}';
    if (_scope != null && _scope != scope) {
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (mounted && _scroll.hasClients) _scroll.jumpTo(0);
      });
    }
    _scope = scope;
    if (!ref.watch(discoveryAccessProvider).canBrowse('lidarr', id) ||
        widget.error == MusicSearchError.forbidden) {
      return Center(
          child: Text(context.l10n.browseMusicIsNotAvailableForThisAccount));
    }
    final library = id == null
        ? const AsyncData(<OwnedAlbum>[])
        : ref.watch(ownedAlbumsForInstanceProvider(id));
    final saved = id == null
        ? const AsyncData(<Map<String, dynamic>>[])
        : ref.watch(savedMusicRequestsProvider(id));
    final artistLibrary = id == null
        ? const AsyncData(LibraryArtistIndex.empty)
        : ref.watch(libraryArtistIndexProvider);
    final records = library.valueOrNull ?? const <OwnedAlbum>[];
    final requests = saved.valueOrNull ?? const <Map<String, dynamic>>[];
    final catalogIds = widget.results
        .expand((a) => verifiedMusicIDs(a.foreignId, requests))
        .toSet();
    final supplements = <OwnedAlbum>[];
    final represented = <String>{};
    for (final row in records) {
      if (row.foreignAlbumId.isEmpty) continue;
      if (catalogIds.contains(row.foreignAlbumId) &&
          represented.add(row.foreignAlbumId)) {
        continue;
      }
      if (titleMatchesQuery(widget.query, row.title) ||
          titleMatchesQuery(widget.query, '${row.artist} ${row.title}')) {
        supplements.add(row);
      }
    }
    final artistIds = widget.artists.map((a) => a.foreignId).toSet();
    final extraArtists = artistLibrary.valueOrNull
            ?.recordsWhere((name) => titleMatchesQuery(widget.query, name))
            .where((a) => !artistIds.contains(a.foreignArtistId))
            .toList() ??
        [];
    final notifier = ref.read(shellMusicSearchProvider.notifier);
    final state = ref.watch(shellMusicSearchProvider);
    Widget label(String text) => Padding(
        padding: const EdgeInsets.fromLTRB(16, 16, 16, 8),
        child: Text(text, style: Theme.of(context).textTheme.titleMedium));
    Widget notice(String text, {VoidCallback? retry}) => Padding(
        padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
        child: Row(children: [
          Expanded(
              child: Text(text, style: Theme.of(context).textTheme.bodySmall)),
          if (retry != null)
            TextButton(onPressed: retry, child: Text(context.l10n.browseRetry))
        ]));
    return Material(
        type: MaterialType.transparency,
        child: LayoutBuilder(
            builder: (context, constraints) => ListView(
                    key: PageStorageKey('music-search:$scope'),
                    controller: _scroll,
                    padding: EdgeInsets.symmetric(
                        horizontal: AppBreakpoints.centeredContentPadding(
                            constraints.maxWidth,
                            minPadding: 0)),
                    children: [
                      label(context.l10n.browseAlbums),
                      for (var i = 0; i < widget.results.length; i++)
                        MusicAlbumResultTile(
                            key: ValueKey(
                                'catalog:${widget.results[i].foreignId}:$i'),
                            album: widget.results[i],
                            instanceId: id,
                            query: widget.query,
                            badge: musicBadge(
                                widget.results[i].foreignId, records, requests),
                            onTap: widget.onResultTap),
                      for (var i = 0; i < supplements.length; i++)
                        MusicAlbumResultTile(
                            key: ValueKey(
                                'library:${supplements[i].recordId}:$i'),
                            album: libraryMusicAlbum(supplements[i]),
                            instanceId: id,
                            query: widget.query,
                            libraryAlbum: supplements[i],
                            badge: musicBadge(supplements[i].foreignAlbumId,
                                records, requests),
                            onTap: widget.onResultTap),
                      if (widget.isLoading)
                        const Padding(
                            padding: EdgeInsets.all(16),
                            child: LinearProgressIndicator()),
                      if (widget.error != null)
                        notice(
                            context.l10n
                                .browseTheAlbumCatalogCouldNotBeSearchedLibraryMatchesRemainUsable,
                            retry: notifier.retryAlbums),
                      if (widget.searched &&
                          widget.results.isEmpty &&
                          supplements.isEmpty)
                        notice(context.l10n
                            .browseNoAlbumsEPsOrSinglesMatchedThisCatalogPage),
                      if (state.nextPage != null && !widget.isLoading)
                        TextButton(
                            onPressed: notifier.loadMore,
                            child: Text(context.l10n.browseMoreAlbums)),
                      if (id != null)
                        notice(library.hasError
                            ? context
                                .l10n.browseLibraryAvailabilityCouldNotBeChecked
                            : saved.hasError
                                ? context
                                    .l10n.browseSavedRequestsCouldNotBeChecked
                                : library.isLoading || saved.isLoading
                                    ? context.l10n
                                        .browseCheckingLibraryAndSavedRequests
                                    : context.l10n
                                        .browseLibraryAndSavedRequestsChecked),
                      label(context.l10n.browseArtists),
                      for (var i = 0; i < widget.artists.length; i++)
                        _artist(widget.artists[i], id, i),
                      for (var i = 0; i < extraArtists.length; i++)
                        _artist(
                            MusicArtist(
                                foreignId:
                                    extraArtists[i].foreignArtistId ?? '',
                                name: extraArtists[i].artistName),
                            id,
                            widget.artists.length + i),
                      if (widget.artistsLoading)
                        const Padding(
                            padding: EdgeInsets.all(16),
                            child: LinearProgressIndicator()),
                      if (widget.artistsUnavailable)
                        notice(context.l10n.browseArtistsCouldNotBeSearched,
                            retry: notifier.retryArtists),
                      if (artistLibrary.hasError)
                        notice(
                            context.l10n.browseLibraryArtistsCouldNotBeChecked),
                      if (!widget.artistsLoading &&
                          !widget.artistsUnavailable &&
                          widget.artists.isEmpty &&
                          extraArtists.isEmpty)
                        notice(
                            context.l10n.browseNoArtistsMatchedThisCatalogPage),
                      if (state.nextArtistPage != null &&
                          !widget.artistsLoading)
                        TextButton(
                            onPressed: notifier.loadMoreArtists,
                            child: Text(context.l10n.browseMoreArtists)),
                    ])));
  }

  Widget _artist(MusicArtist artist, String? id, int index) => ListTile(
      key: ValueKey('artist:${artist.foreignId}:$index'),
      leading: const Icon(Icons.person),
      title: Text(artist.name),
      subtitle: artist.subtitle.isEmpty ? null : Text(artist.subtitle),
      trailing: const Icon(Icons.chevron_right),
      onTap: () {
        widget.onResultTap?.call();
        context.push(artist.detailLocation(id, query: widget.query),
            extra: artist);
      });
}

/// Shared album row for search and exact-artist discographies.
class MusicAlbumResultTile extends ConsumerWidget {
  final MusicAlbum album;
  final String? instanceId;
  final String? query;
  final String? badge;
  final OwnedAlbum? libraryAlbum;
  final VoidCallback? onTap;
  const MusicAlbumResultTile(
      {super.key,
      required this.album,
      this.instanceId,
      this.query,
      this.badge,
      this.libraryAlbum,
      this.onTap});
  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final art = libraryAlbum != null && instanceId != null
        ? lidarrImageSource(ref, libraryAlbum!.cover, instanceId!)
        : musicArtworkSource(ref, album, instanceId);
    return SizedBox(
        height: 88,
        child: ListTile(
          leading: ClipRRect(
              borderRadius: BorderRadius.circular(AppTheme.radiusSmall),
              child: CachedImage(
                  url: art?.url,
                  headers: art?.headers,
                  width: 52,
                  height: 52,
                  icon: Icons.album)),
          title:
              Text(album.title, maxLines: 2, overflow: TextOverflow.ellipsis),
          subtitle: Text(
              [
                album.subtitle,
                album.releaseDate.split('-').first,
                album.disambiguation
              ].where((s) => s.isNotEmpty).join(' · '),
              maxLines: 1,
              overflow: TextOverflow.ellipsis),
          trailing: SizedBox(
              width: 92,
              child: Text(browseLabel(context, badge ?? ''),
                  textAlign: TextAlign.end,
                  style: Theme.of(context).textTheme.labelSmall)),
          onTap: () {
            onTap?.call();
            context.push(album.detailLocation(instanceId, query: query),
                extra: album);
          },
        ));
  }
}
