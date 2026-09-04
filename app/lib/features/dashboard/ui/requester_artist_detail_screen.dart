import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../core/layout/adaptive.dart';
import '../../../core/providers/instance_provider.dart';
import '../../../core/providers/realtime_provider.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/cached_image.dart';
import '../../lidarr/data/lidarr_image.dart';
import '../../request/data/album_ownership.dart';
import '../data/music_artists_service.dart';

/// Requester-facing page for one artist, addressed by their MusicBrainz id.
///
/// It answers the question the Music tab's artists row raises — "what does
/// this library have by them?" — with the same ownership vocabulary the
/// search results and Recently Added row use, so an album reads the same
/// wherever the requester meets it.
class RequesterArtistDetailScreen extends ConsumerWidget {
  final String foreignArtistId;

  /// The name the row already displayed, so the app bar is right before the
  /// first byte arrives.
  final String? nameHint;

  /// The library this page is pinned to. A pinned id can never read another
  /// library's answer for the same artist, even if the drawer switches.
  final String? instanceId;

  const RequesterArtistDetailScreen({
    super.key,
    required this.foreignArtistId,
    this.nameHint,
    this.instanceId,
  });

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final pinned =
        instanceId ?? ref.watch(instanceProvider).activeLidarrInstance?.id;
    final target = (
      instanceId: pinned,
      foreignArtistId: foreignArtistId,
    );
    // A library change while the page is open (an import lands, a request is
    // approved) makes this page's ownership stale, and it is uncached
    // precisely so a refetch tells the truth.
    ref.listen(libraryChangedEventsProvider, (_, next) {
      if (next.hasValue) ref.invalidate(musicArtistDetailProvider(target));
    });

    final detail = ref.watch(musicArtistDetailProvider(target));
    final name = detail.valueOrNull?.artist.name.trim();
    return Scaffold(
      appBar: AppBar(
        title: Text(
          (name != null && name.isNotEmpty ? name : nameHint?.trim()) ??
              'Artist',
        ),
      ),
      body: RefreshIndicator(
        color: AppTheme.accent,
        onRefresh: () async {
          ref.invalidate(musicArtistDetailProvider(target));
          await ref.read(musicArtistDetailProvider(target).future);
        },
        child: detail.when(
          loading: () => const Center(
            child: CircularProgressIndicator(color: AppTheme.accent),
          ),
          error: (error, _) =>
              _ArtistError(message: _artistErrorMessage(error)),
          data: (data) => _ArtistBody(detail: data, instanceId: pinned),
        ),
      ),
    );
  }
}

/// Says which failure this was in requester vocabulary. The distinction that
/// matters is "this library has no such artist" versus "this library could
/// not be read at all" — rendered identically, a reader stops looking.
String _artistErrorMessage(Object error) {
  final status = error is DioException ? error.response?.statusCode : null;
  switch (status) {
    case 404:
      return 'This artist is not in your music library.\n'
          'Search for one of their albums to add them.';
    case 401:
    case 403:
      return 'You do not have access to this music library.';
    default:
      return 'This artist could not be loaded. '
          'Check the connection and try again.';
  }
}

class _ArtistError extends StatelessWidget {
  final String message;

  const _ArtistError({required this.message});

  @override
  Widget build(BuildContext context) {
    return LayoutBuilder(
      builder: (context, constraints) => SingleChildScrollView(
        physics: const AlwaysScrollableScrollPhysics(),
        child: ConstrainedBox(
          constraints: BoxConstraints(minHeight: constraints.maxHeight),
          child: Center(
            child: Padding(
              padding: const EdgeInsets.all(32),
              child: Column(
                mainAxisSize: MainAxisSize.min,
                children: [
                  const Icon(Icons.mic_off_outlined,
                      size: 48, color: AppTheme.textSecondary),
                  const SizedBox(height: 12),
                  Text(
                    message,
                    textAlign: TextAlign.center,
                    style: const TextStyle(color: AppTheme.textSecondary),
                  ),
                ],
              ),
            ),
          ),
        ),
      ),
    );
  }
}

class _ArtistBody extends ConsumerWidget {
  final MusicArtistDetail detail;
  final String? instanceId;

  const _ArtistBody({required this.detail, required this.instanceId});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final titles = detail.titles;
    return LayoutBuilder(builder: (context, constraints) {
      final hPad = AppBreakpoints.centeredContentPadding(
        constraints.maxWidth,
        minPadding: 0,
      );
      return ListView.separated(
        physics: const AlwaysScrollableScrollPhysics(),
        padding: EdgeInsets.fromLTRB(hPad, 0, hPad, 16),
        itemCount: titles.length + 1,
        separatorBuilder: (_, index) => index == 0
            ? const SizedBox.shrink()
            : const Divider(height: 1, color: AppTheme.border),
        itemBuilder: (context, index) {
          if (index == 0) {
            return _ArtistHeader(artist: detail.artist, instanceId: instanceId);
          }
          final title = titles[index - 1];
          return _ArtistAlbumTile(album: title, instanceId: instanceId);
        },
      );
    });
  }
}

class _ArtistHeader extends ConsumerWidget {
  final LibraryArtist artist;
  final String? instanceId;

  const _ArtistHeader({required this.artist, required this.instanceId});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final image = instanceId == null
        ? null
        : lidarrImageSource(ref, artist.image, instanceId!);
    final count = artist.countLabel;
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 20, 16, 20),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.center,
        children: [
          ClipOval(
            child: CachedImage(
              url: image?.url,
              headers: image?.headers,
              width: 84,
              height: 84,
              icon: Icons.mic_external_on,
              iconSize: 28,
            ),
          ),
          const SizedBox(width: 16),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              mainAxisSize: MainAxisSize.min,
              children: [
                Text(
                  artist.name,
                  maxLines: 2,
                  overflow: TextOverflow.ellipsis,
                  style: const TextStyle(
                    color: AppTheme.textPrimary,
                    fontSize: 20,
                    fontWeight: FontWeight.w700,
                  ),
                ),
                if (count.isNotEmpty) ...[
                  const SizedBox(height: 4),
                  Text(
                    count,
                    style: const TextStyle(color: AppTheme.textSecondary),
                  ),
                ],
              ],
            ),
          ),
        ],
      ),
    );
  }
}

/// One album on the artist page. An album nobody has requested says so, an
/// undetermined state renders no pill at all rather than a guessed one, and
/// everything else carries the same Available/Requested vocabulary the rest
/// of the music surfaces use.
class _ArtistAlbumTile extends ConsumerWidget {
  final OwnedAlbum album;
  final String? instanceId;

  const _ArtistAlbumTile({required this.album, required this.instanceId});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final cover = instanceId == null
        ? null
        : lidarrImageSource(ref, album.cover, instanceId!);
    final fid = album.foreignAlbumId.trim();
    final year = album.year > 0 ? '${album.year}' : null;
    final (label, color) = switch (album) {
      OwnedAlbum(downloaded: true) => ('Available', AppTheme.available),
      OwnedAlbum(monitored: true) => ('Requested', AppTheme.requested),
      _ => (null, null),
    };

    return ListTile(
      key: ValueKey('artist-album:$fid'),
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
          color: AppTheme.textPrimary,
          fontWeight: FontWeight.w600,
        ),
      ),
      subtitle: Padding(
        padding: const EdgeInsets.only(top: 3),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            if (year != null)
              Text(
                year,
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
                style: const TextStyle(color: AppTheme.textSecondary),
              ),
            if (label != null && color != null) ...[
              if (year != null) const SizedBox(height: 4),
              _StatusPill(label: label, color: color),
            ],
          ],
        ),
      ),
      // Requests belong on the album's own page, which owns the request
      // action and every guard around it.
      trailing: fid.isEmpty
          ? null
          : const Icon(Icons.chevron_right, color: AppTheme.textSecondary),
      onTap: fid.isEmpty
          ? null
          : () => context.push(
                '/detail/album/${Uri.encodeComponent(fid)}'
                '?title=${Uri.encodeQueryComponent(album.title)}'
                '${instanceId == null ? '' : '&instance_id=${Uri.encodeQueryComponent(instanceId!)}'}',
              ),
    );
  }
}

class _StatusPill extends StatelessWidget {
  final String label;
  final Color color;

  const _StatusPill({required this.label, required this.color});

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.15),
        borderRadius: BorderRadius.circular(4),
      ),
      child: Text(
        label,
        style: TextStyle(
          color: color,
          fontSize: 10.5,
          fontWeight: FontWeight.w600,
        ),
      ),
    );
  }
}
