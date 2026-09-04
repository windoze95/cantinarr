import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import '../../../core/providers/instance_provider.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/horizontal_item_row.dart';
import '../../../core/widgets/media_card.dart';
import '../../../core/widgets/section_header.dart';
import '../../lidarr/data/lidarr_image.dart';
import '../../request/data/album_ownership.dart';
import '../data/music_library_service.dart';
import '../data/recent_albums_service.dart';

/// The Music tab's "Recently Added" row: what actually landed in the library,
/// newest first.
///
/// The row hides itself entirely when there is nothing to show, when the user
/// has no Lidarr access, or when the library cannot be read. None of those is
/// an error worth interrupting a search for.
class RecentlyAddedAlbumsRow extends ConsumerWidget {
  const RecentlyAddedAlbumsRow({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final instanceId = ref.watch(instanceProvider).activeLidarrInstance?.id;
    if (instanceId == null) return const SizedBox.shrink();

    final recent = ref.watch(recentAlbumsProvider);
    final owned = ref.watch(ownedAlbumsProvider);
    final albums = recent.valueOrNull ?? const <RecentAlbum>[];
    // Nothing to show, no access, an unreachable library, or an unreadable
    // ownership digest all look the same: no row — the same reasoning as the
    // books row this mirrors. hasError also covers a failed refresh that
    // retained stale albums.
    if (recent.hasError ||
        owned.hasError ||
        (albums.isEmpty && !recent.isLoading)) {
      return const SizedBox.shrink();
    }

    final viewportWidth = MediaQuery.sizeOf(context).width;
    final cardWidth =
        viewportWidth >= 900 ? 124.0 : (viewportWidth >= 600 ? 116.0 : 108.0);

    // Both ids come from Lidarr's own foreignAlbumId on the same library
    // record, so exact string equality is the correct join. Empty keys never
    // collide.
    final byForeignAlbumId = <String, OwnedAlbum>{
      for (final album in owned.valueOrNull ?? const <OwnedAlbum>[])
        if (album.foreignAlbumId.trim().isNotEmpty)
          album.foreignAlbumId.trim(): album,
    };

    return Padding(
      padding: const EdgeInsets.only(top: 20),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Padding(
            padding: EdgeInsets.symmetric(
              horizontal: viewportWidth >= 900 ? 24 : 16,
            ),
            child: const SectionHeader(title: 'Recently Added'),
          ),
          const SizedBox(height: 12),
          HorizontalItemRow<RecentAlbum>(
            items: albums,
            isLoading: recent.isLoading,
            height: cardWidth * 1.5 + 68,
            itemBuilder: (album) {
              final cover = lidarrImageSource(ref, album.cover, instanceId);
              final canOpen = album.foreignAlbumId.trim().isNotEmpty;
              final ownedAlbum =
                  byForeignAlbumId[album.foreignAlbumId.trim()];
              // Music has no format axis, so the verdict is the single
              // downloaded/monitored pair; an unmatched or contradictory
              // digest row renders no pill at all, never a guessed one.
              final (label, color) = switch (ownedAlbum) {
                null => (null, null),
                OwnedAlbum(downloaded: true) =>
                  ('Available', AppTheme.available),
                OwnedAlbum(monitored: true) =>
                  ('Requested', AppTheme.requested),
                _ => (null, null),
              };
              return MediaCard(
                id: album.albumId,
                title: album.title,
                posterPath: cover?.url,
                posterHeaders: cover?.headers,
                placeholderIcon: Icons.album,
                subtitle: album.artist.isEmpty ? null : album.artist,
                statusLabel: label,
                statusColor: color,
                width: cardWidth,
                onTap: canOpen
                    ? () => context.push(
                          '/detail/album/${Uri.encodeComponent(album.foreignAlbumId)}'
                          '?title=${Uri.encodeQueryComponent(album.title)}'
                          '&instance_id=${Uri.encodeQueryComponent(instanceId)}',
                        )
                    : null,
              );
            },
          ),
        ],
      ),
    );
  }
}
