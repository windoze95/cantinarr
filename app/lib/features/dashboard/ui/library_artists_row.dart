import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../core/providers/instance_provider.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/cached_image.dart';
import '../../../core/widgets/horizontal_item_row.dart';
import '../../../core/widgets/section_header.dart';
import '../../../core/widgets/section_sort_menu.dart';
import '../../lidarr/data/lidarr_image.dart';
import '../data/music_artists_service.dart';

/// The Music tab's "Artists" row: whose music this library actually holds,
/// most-collected first.
///
/// Music discovery is search-first because Lidarr has no popular feed, which
/// leaves a requester who does not already have an album in mind with nothing
/// to do. Artists are the one axis a music library can be browsed along, so
/// this row gives that user somewhere to start.
///
/// Like the Recently Added row it hides itself entirely when there is nothing
/// to show, when the user has no Lidarr access, or when the library cannot be
/// read — none of those is an error worth interrupting a search for.
class LibraryArtistsRow extends ConsumerWidget {
  const LibraryArtistsRow({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final instanceId = ref.watch(instanceProvider).activeLidarrInstance?.id;
    if (instanceId == null) return const SizedBox.shrink();

    final artistsAsync = ref.watch(musicArtistsProvider);
    final page = artistsAsync.valueOrNull;
    final artists = page?.artists ?? const <LibraryArtist>[];
    // hasError also covers a failed refresh that retained stale artists: an
    // unreadable library must not keep claiming it holds these artists.
    if (artistsAsync.hasError || artists.isEmpty) {
      return const SizedBox.shrink();
    }

    final viewportWidth = MediaQuery.sizeOf(context).width;
    final avatarSize =
        viewportWidth >= 900 ? 104.0 : (viewportWidth >= 600 ? 96.0 : 88.0);

    return Padding(
      padding: const EdgeInsets.only(top: 20),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Padding(
            padding: EdgeInsets.symmetric(
              horizontal: viewportWidth >= 900 ? 24 : 16,
            ),
            child: SectionHeader(
              title: 'Artists',
              trailing: Row(
                mainAxisSize: MainAxisSize.min,
                children: [
                  SectionTruncationNote(
                    shown: artists.length,
                    total: page?.total ?? artists.length,
                  ),
                  SectionSortMenu<ArtistSort>(
                    tooltip: 'Sort artists',
                    options: ArtistSort.values,
                    selected: ref.watch(musicArtistsSortProvider),
                    labelOf: (option) => option.label,
                    onSelected: (next) => ref
                        .read(musicArtistsSortProvider.notifier)
                        .state = next,
                  ),
                ],
              ),
            ),
          ),
          const SizedBox(height: 12),
          HorizontalItemRow<LibraryArtist>(
            items: artists,
            isLoading: false,
            // Pinned statically rather than measured: a row whose height
            // depends on its longest name resizes as cards scroll in.
            height: artistAvatarRowHeight(avatarSize),
            itemBuilder: (artist) => ArtistAvatarCard(
              artist: artist,
              size: avatarSize,
              image: lidarrImageSource(ref, artist.image, instanceId),
              onTap: artist.foreignArtistId.isEmpty
                  ? null
                  : () => context.push(
                        '/detail/artist/${Uri.encodeComponent(artist.foreignArtistId)}'
                        '?name=${Uri.encodeQueryComponent(artist.name)}'
                        '&instance_id=${Uri.encodeQueryComponent(instanceId)}',
                      ),
            ),
          ),
        ],
      ),
    );
  }
}

/// The row's fixed height for a given avatar size: the circle, plus room for
/// a two-line name and a two-line count beneath it. The count gets two lines
/// because it is the line that must not be cut off.
double artistAvatarRowHeight(double avatarSize) => avatarSize + 76;

/// One artist in the browse row: a circular portrait, the name, and what the
/// library holds by them.
class ArtistAvatarCard extends StatelessWidget {
  final LibraryArtist artist;
  final double size;
  final LidarrImageSource? image;
  final VoidCallback? onTap;

  const ArtistAvatarCard({
    super.key,
    required this.artist,
    required this.size,
    this.image,
    this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    final count = artist.countLabel;
    return SizedBox(
      width: size,
      child: InkWell(
        onTap: onTap,
        borderRadius: BorderRadius.circular(12),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.center,
          children: [
            ClipOval(
              child: CachedImage(
                url: image?.url,
                headers: image?.headers,
                width: size,
                height: size,
                icon: Icons.mic_external_on,
                iconSize: size / 3,
              ),
            ),
            const SizedBox(height: 8),
            Text(
              artist.name,
              maxLines: 2,
              overflow: TextOverflow.ellipsis,
              textAlign: TextAlign.center,
              style: const TextStyle(
                color: AppTheme.textPrimary,
                fontSize: 12.5,
                fontWeight: FontWeight.w600,
                height: 1.2,
              ),
            ),
            if (count.isNotEmpty) ...[
              const SizedBox(height: 2),
              Text(
                count,
                maxLines: 2,
                overflow: TextOverflow.ellipsis,
                textAlign: TextAlign.center,
                style: const TextStyle(
                  color: AppTheme.textSecondary,
                  fontSize: 11,
                  height: 1.25,
                ),
              ),
            ],
          ],
        ),
      ),
    );
  }
}
