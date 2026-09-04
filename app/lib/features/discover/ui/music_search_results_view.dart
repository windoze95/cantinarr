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
import '../../lidarr/data/lidarr_models.dart';
import '../../request/data/album_ownership.dart';
import '../../shell/logic/library_artist_index.dart';
import '../../shell/logic/shell_music_search_provider.dart';

/// Music-search results overlay for the shell toolbar, rendered on
/// `/dashboard/music` in the same [Positioned.fill] slot [SearchResultsView]
/// occupies for every other module. Mirrors [BookSearchResultsView] without
/// the fuzzy identity machinery: Lidarr lookup results and library records
/// share MusicBrainz ids on both artists and albums, so exact id equality is
/// the join everywhere.
class MusicSearchResultsView extends ConsumerWidget {
  final List<LidarrAlbum> results;

  /// Artist matches for [query]. Rendered above the album rows: someone who
  /// typed an artist's name is usually after the artist, and the albums by
  /// them are one tap further in.
  final List<LidarrArtist> artists;
  final String query;
  final bool isLoading;

  /// True once a lookup has completed successfully for [query] — the signal
  /// that distinguishes "no albums found" from "hasn't searched yet".
  final bool searched;
  final MusicSearchError? error;

  /// The artist lookup failed while the album lookup succeeded. An empty
  /// artist section then means "could not look" rather than "nobody
  /// matched", and has to say so.
  final bool artistsUnavailable;
  final VoidCallback? onResultTap;

  /// Re-runs the search for an artist the library does not hold, so their
  /// albums land in this same overlay and can be requested. A metadata-only
  /// artist has no page to open — Cantinarr's artist screen renders library
  /// albums with ownership pills — so "show me their albums" is the
  /// destination, and it is the search the user could have typed.
  final ValueChanged<String>? onArtistDrillDown;

  const MusicSearchResultsView({
    super.key,
    required this.results,
    this.artists = const [],
    required this.query,
    required this.isLoading,
    required this.searched,
    required this.error,
    this.artistsUnavailable = false,
    this.onResultTap,
    this.onArtistDrillDown,
  });

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    if (error != null) {
      final message = switch (error!) {
        MusicSearchError.noInstance => 'No Lidarr instance is available.',
        MusicSearchError.forbidden =>
          'You do not have access to search this music library.',
        MusicSearchError.requestFailed =>
          'Music could not be searched. Check the connection and try again.',
      };
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(24),
          child: Text(
            message,
            textAlign: TextAlign.center,
            style: const TextStyle(color: AppTheme.error),
          ),
        ),
      );
    }

    if (isLoading) {
      return const Center(
        child: CircularProgressIndicator(color: AppTheme.accent),
      );
    }

    // What the user's library already tracks, keyed by the same MusicBrainz
    // release-group id lookup results carry — the exact join books cannot
    // have.
    final digest =
        ref.watch(ownedAlbumsProvider).valueOrNull ?? const <OwnedAlbum>[];
    final ownedById = <String, OwnedAlbum>{
      for (final album in digest)
        if (album.foreignAlbumId.trim().isNotEmpty)
          album.foreignAlbumId.trim(): album,
    };

    // Mark each lookup result with its ownership and float owned albums to
    // the top, preserving Lidarr's relevance order within each bucket. No
    // deduping: two records that came back separately stay separate rows.
    final owned = <_ResolvedAlbumResult>[];
    final rest = <_ResolvedAlbumResult>[];
    final lookupIds = <String>{};
    for (var lookupIndex = 0; lookupIndex < results.length; lookupIndex++) {
      final album = results[lookupIndex];
      final fid = album.foreignAlbumId?.trim() ?? '';
      if (fid.isNotEmpty) lookupIds.add(fid);
      final match = fid.isEmpty ? null : ownedById[fid];
      ((match?.owned ?? false) ? owned : rest).add(
        _ResolvedAlbumResult(
          album: album,
          ownership: match,
          sourceIdentity: 'lookup:$lookupIndex',
          cover: (match != null && match.cover.isNotEmpty)
              ? match.cover
              : album.coverUrl,
        ),
      );
    }
    // Owned albums the metadata search missed are still real answers to the
    // query; inject them so the overlay never claims the library lacks an
    // album the Music tab behind it shows.
    final injected = <_ResolvedAlbumResult>[
      for (final album in digest)
        if (album.owned &&
            album.foreignAlbumId.trim().isNotEmpty &&
            !lookupIds.contains(album.foreignAlbumId.trim()) &&
            (titleMatchesQuery(query, album.title) ||
                titleMatchesQuery(query, '${album.artist} ${album.title}')))
          _ResolvedAlbumResult(
            album: _ownedAlbumAsLookup(album),
            ownership: album,
            sourceIdentity: 'library:${album.foreignAlbumId.trim()}',
            cover: album.cover.isNotEmpty ? album.cover : null,
          ),
    ];
    final ordered = <_ResolvedAlbumResult>[...injected, ...owned, ...rest];

    // Resolve each looked-up artist against the library's own records — by
    // MusicBrainz id, which both sides share (unlike books' derived author
    // ids).
    final index = ref.watch(libraryArtistIndexProvider).valueOrNull ??
        LibraryArtistIndex.empty;
    final inLibrary = <_ResolvedArtist>[];
    final elsewhere = <_ResolvedArtist>[];
    for (final artist in artists) {
      final record = index.match(artist.foreignArtistId);
      final resolved = _ResolvedArtist(lookup: artist, record: record);
      (record == null ? elsewhere : inLibrary).add(resolved);
    }
    // The library answers for its own artists even when the metadata lookup
    // does not. Records already on screen through a resolved lookup row are
    // skipped — that is the same record, not a distinct one.
    final presentRecordIds = <int>{
      for (final resolved in inLibrary)
        if (resolved.record != null) resolved.record!.id,
    };
    final injectedArtists = <_ResolvedArtist>[
      for (final record
          in index.recordsWhere((name) => titleMatchesQuery(query, name)))
        if (!presentRecordIds.contains(record.id))
          _ResolvedArtist(lookup: record, record: record),
    ];
    final orderedArtists = <_ResolvedArtist>[
      ...injectedArtists,
      ...inLibrary,
      ...elsewhere,
    ];

    if (ordered.isEmpty && orderedArtists.isEmpty) {
      if (!searched) return const SizedBox.shrink();
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(32),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              const Icon(Icons.album, size: 48, color: AppTheme.textSecondary),
              const SizedBox(height: 12),
              // Says what was actually searched. With the artist lookup down
              // this cannot claim no artist matched — only that no album did.
              Text(
                artistsUnavailable
                    ? 'No albums found, and artists could not be searched. '
                        'Try a different search.'
                    : 'No albums or artists found. Try a different search.',
                textAlign: TextAlign.center,
                style: const TextStyle(color: AppTheme.textSecondary),
              ),
            ],
          ),
        ),
      );
    }

    final instanceId = ref.watch(instanceProvider).activeLidarrInstance?.id;
    return LayoutBuilder(builder: (context, constraints) {
      final hPad = AppBreakpoints.centeredContentPadding(
        constraints.maxWidth,
        minPadding: 0,
      );
      // One flat list of rows so artists and albums share a single scroll
      // surface; headers appear only when both kinds are present.
      final showSections = orderedArtists.isNotEmpty && ordered.isNotEmpty;
      final children = <Widget>[];
      final isResult = <bool>[];
      void addRow(Widget child, {bool result = false}) {
        children.add(child);
        isResult.add(result);
      }

      if (showSections) addRow(const _SectionLabel('Artists'));
      for (final resolved in orderedArtists) {
        addRow(
          _ArtistResultTile(
            resolved: resolved,
            image: instanceId == null
                ? null
                : lidarrImageSource(ref, resolved.portraitUrl, instanceId),
            instanceId: instanceId,
            onTap: onResultTap,
            onDrillDown: onArtistDrillDown,
          ),
          result: true,
        );
      }
      if (artistsUnavailable) {
        addRow(const _OverlayNotice('Artists could not be searched.'));
      }
      if (showSections) addRow(const _SectionLabel('Albums'));
      for (final result in ordered) {
        addRow(
          _AlbumResultTile(
            album: result.album,
            ownership: result.ownership,
            sourceIdentity: result.sourceIdentity,
            cover: instanceId == null
                ? null
                : lidarrImageSource(ref, result.cover, instanceId),
            instanceId: instanceId,
            searchedTerm: query,
            onTap: onResultTap,
          ),
          result: true,
        );
      }

      return ListView.separated(
        padding: EdgeInsets.fromLTRB(hPad, 8, hPad, 8),
        itemCount: children.length,
        separatorBuilder: (_, i) => isResult[i] && isResult[i + 1]
            ? const Divider(height: 1, color: AppTheme.border)
            : const SizedBox(height: 8),
        itemBuilder: (_, i) => children[i],
      );
    });
  }
}

/// A group label ("Artists" / "Albums") above a run of result rows.
class _SectionLabel extends StatelessWidget {
  final String title;

  const _SectionLabel(this.title);

  @override
  Widget build(BuildContext context) => Padding(
        padding: const EdgeInsets.fromLTRB(16, 8, 16, 4),
        child: Text(
          title.toUpperCase(),
          style: const TextStyle(
            color: AppTheme.textSecondary,
            fontSize: 11,
            fontWeight: FontWeight.w700,
            letterSpacing: 0.8,
          ),
        ),
      );
}

/// A non-result line in the overlay — currently only "artists could not be
/// searched", which keeps a missing artist section from reading as an empty
/// one.
class _OverlayNotice extends StatelessWidget {
  final String message;

  const _OverlayNotice(this.message);

  @override
  Widget build(BuildContext context) => Padding(
        padding: const EdgeInsets.fromLTRB(16, 4, 16, 4),
        child: Row(
          children: [
            const Icon(Icons.error_outline,
                size: 15, color: AppTheme.requested),
            const SizedBox(width: 6),
            Expanded(
              child: Text(
                message,
                style: const TextStyle(
                  color: AppTheme.requested,
                  fontSize: 12,
                  fontWeight: FontWeight.w600,
                ),
              ),
            ),
          ],
        ),
      );
}

/// One looked-up artist, paired with the library record it resolved to (by
/// MusicBrainz id). A null record is a metadata-only match.
class _ResolvedArtist {
  final LidarrArtist lookup;
  final LidarrArtist? record;

  const _ResolvedArtist({required this.lookup, required this.record});

  bool get inLibrary => record != null;

  String get name => lookup.artistName;

  /// Prefer the library record's art when it resolved: that copy is the one
  /// the instance proxy can serve. Otherwise the metadata CDN portrait.
  String? get portraitUrl => record?.portraitUrl ?? lookup.portraitUrl;

  /// Only a library record can say what the library holds. A metadata artist
  /// carries no statistics, and inventing "0 albums" for it would assert an
  /// empty shelf where nothing was counted.
  String get countLabel {
    final s = record?.statistics;
    if (s == null || s.albumCount <= 0) return '';
    return '${s.albumCount} album${s.albumCount == 1 ? '' : 's'}';
  }
}

/// One artist search result: portrait, name, and what the library holds by
/// them. An in-library artist opens the artist detail screen; a
/// metadata-only one searches for their albums instead — the thing a
/// requester actually wants from them.
class _ArtistResultTile extends StatelessWidget {
  final _ResolvedArtist resolved;
  final LidarrImageSource? image;
  final String? instanceId;

  /// Called right before navigating away, so the shell can dismiss the
  /// keyboard.
  final VoidCallback? onTap;

  /// Re-runs the search for a metadata-only artist's albums.
  final ValueChanged<String>? onDrillDown;

  const _ArtistResultTile({
    required this.resolved,
    this.image,
    required this.instanceId,
    this.onTap,
    this.onDrillDown,
  });

  @override
  Widget build(BuildContext context) {
    final record = resolved.record;
    final libraryForeignId = record?.foreignArtistId?.trim() ?? '';
    final canOpen = resolved.inLibrary &&
        libraryForeignId.isNotEmpty &&
        instanceId != null;
    final canDrillDown = !resolved.inLibrary && onDrillDown != null;

    final count = resolved.countLabel;
    final String subtitle;
    if (resolved.inLibrary) {
      subtitle = count.isEmpty ? 'Artist · in your library' : 'Artist · $count';
    } else {
      subtitle = 'Artist · not in your library';
    }
    final String? guidance;
    if (resolved.inLibrary && !canOpen) {
      guidance = 'Ask an admin to check this artist’s library record';
    } else if (canDrillDown) {
      guidance = 'Tap to see their albums';
    } else {
      guidance = null;
    }

    return Material(
      type: MaterialType.transparency,
      child: ListTile(
        key: ValueKey('artist-result:${resolved.name}:$libraryForeignId'),
        contentPadding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
        leading: ClipOval(
          child: CachedImage(
            url: image?.url,
            headers: image?.headers,
            width: 44,
            height: 44,
            icon: Icons.mic_external_on,
          ),
        ),
        title: Text(
          resolved.name,
          maxLines: 2,
          overflow: TextOverflow.ellipsis,
          style: const TextStyle(
              color: AppTheme.textPrimary, fontWeight: FontWeight.w600),
        ),
        subtitle: Padding(
          padding: const EdgeInsets.only(top: 3),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(
                subtitle,
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
                style: const TextStyle(color: AppTheme.textSecondary),
              ),
              if (guidance != null) ...[
                const SizedBox(height: 4),
                Text(
                  guidance,
                  style: TextStyle(
                    color: canDrillDown
                        ? AppTheme.textSecondary
                        : AppTheme.requested,
                    fontSize: 12,
                    fontWeight: FontWeight.w600,
                  ),
                ),
              ],
            ],
          ),
        ),
        // Two different destinations get two different affordances, so the
        // row never promises a page it cannot open.
        trailing: canOpen
            ? const Icon(Icons.chevron_right, color: AppTheme.textSecondary)
            : canDrillDown
                ? const Icon(Icons.search, color: AppTheme.textSecondary)
                : null,
        onTap: canOpen
            ? () {
                onTap?.call();
                context.push(
                  '/detail/artist/${Uri.encodeComponent(libraryForeignId)}'
                  '?name=${Uri.encodeQueryComponent(resolved.name)}'
                  '&instance_id=${Uri.encodeQueryComponent(instanceId!)}',
                );
              }
            : canDrillDown
                ? () => onDrillDown!(resolved.name)
                : null,
      ),
    );
  }
}

class _ResolvedAlbumResult {
  final LidarrAlbum album;
  final OwnedAlbum? ownership;
  final String sourceIdentity;
  final String? cover;

  const _ResolvedAlbumResult({
    required this.album,
    required this.ownership,
    required this.sourceIdentity,
    required this.cover,
  });
}

class _AlbumResultTile extends StatelessWidget {
  final LidarrAlbum album;
  final OwnedAlbum? ownership;
  final String sourceIdentity;
  final LidarrImageSource? cover;
  final String? instanceId;

  /// The term these results belong to. It travels to the detail page so a
  /// request can hand the server the search that already found this record.
  final String searchedTerm;

  /// Called right before navigating away — the shell dismisses the keyboard
  /// on tap.
  final VoidCallback? onTap;

  const _AlbumResultTile({
    required this.album,
    this.ownership,
    required this.sourceIdentity,
    this.cover,
    required this.instanceId,
    this.searchedTerm = '',
    this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    final year = album.year;
    final subtitle = <String>[
      if (album.artistName.isNotEmpty) album.artistName,
      if (year > 0) '$year',
      if (album.albumType != null && album.albumType!.isNotEmpty)
        album.albumType!,
    ].join(' · ');
    final fid = album.foreignAlbumId?.trim() ?? '';
    final o = ownership;
    final chip = o == null || !o.owned
        ? null
        : _OwnershipChip(
            label: o.downloaded ? 'Available' : 'Requested',
            color: o.downloaded ? AppTheme.available : AppTheme.requested,
          );
    final canOpen = fid.isNotEmpty;

    // The shell overlay wraps this view in an opaque ColoredBox, which sits
    // between a bare ListTile and its ink-splash Material ancestor — give
    // the tile its own transparent Material so ink splashes paint correctly.
    return Material(
      type: MaterialType.transparency,
      child: ListTile(
        key: ValueKey('album-result:$fid:$sourceIdentity'),
        contentPadding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
        leading: ClipRRect(
          borderRadius: BorderRadius.circular(4),
          child: CachedImage(
            url: cover?.url,
            headers: cover?.headers,
            width: 52,
            height: 52,
            icon: Icons.album,
          ),
        ),
        title: Text(
          album.title,
          maxLines: 2,
          overflow: TextOverflow.ellipsis,
          style: const TextStyle(
              color: AppTheme.textPrimary, fontWeight: FontWeight.w600),
        ),
        subtitle: (subtitle.isEmpty && chip == null)
            ? null
            : Padding(
                padding: const EdgeInsets.only(top: 3),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    if (subtitle.isNotEmpty)
                      Text(subtitle,
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                          style:
                              const TextStyle(color: AppTheme.textSecondary)),
                    if (chip != null) ...[
                      if (subtitle.isNotEmpty) const SizedBox(height: 4),
                      chip,
                    ],
                  ],
                ),
              ),
        // Requests belong on the detail page. The search row has one clear
        // action: open that album.
        trailing: canOpen
            ? const Icon(Icons.chevron_right, color: AppTheme.textSecondary)
            : null,
        onTap: canOpen
            ? () {
                onTap?.call();
                context.push(
                  '/detail/album/${Uri.encodeComponent(fid)}'
                  '?title=${Uri.encodeQueryComponent(album.title)}'
                  // The term that surfaced this row travels with it:
                  // requesting the album makes the server find this exact
                  // record again, and this is the search already known to
                  // return it.
                  '${searchedTerm.isEmpty ? '' : '&q=${Uri.encodeQueryComponent(searchedTerm)}'}'
                  '${instanceId == null ? '' : '&instance_id=${Uri.encodeQueryComponent(instanceId!)}'}',
                  extra: album,
                );
              }
            : null,
      ),
    );
  }
}

/// A synthetic result for an owned library album the metadata search didn't
/// return.
LidarrAlbum _ownedAlbumAsLookup(OwnedAlbum a) => LidarrAlbum(
      id: 0,
      title: a.title,
      foreignAlbumId:
          a.foreignAlbumId.trim().isNotEmpty ? a.foreignAlbumId.trim() : null,
      artist: a.artist.isEmpty
          ? null
          : LidarrArtist(id: 0, artistName: a.artist),
      releaseDate: a.year > 0 ? DateTime(a.year) : null,
    );

/// A small colored pill marking that a search result is already in the
/// library.
class _OwnershipChip extends StatelessWidget {
  final String label;
  final Color color;

  const _OwnershipChip({required this.label, required this.color});

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.15),
        borderRadius: BorderRadius.circular(4),
      ),
      child: Text(label,
          style: TextStyle(
              color: color, fontSize: 10.5, fontWeight: FontWeight.w600)),
    );
  }
}
