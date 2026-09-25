import 'package:flutter/material.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/storage/library_view_preferences.dart';
import '../../../core/widgets/library_collection.dart';
import '../../../core/widgets/cached_image.dart';
import '../data/lidarr_models.dart';

/// List or artwork grid of Lidarr artists
/// with explicit actions and progress indicators.
/// Mirrors [ChaptarrAuthorList] adapted to the artist-centric music library.
class LidarrArtistList extends StatelessWidget {
  final List<LidarrArtist> artists;
  final void Function(LidarrArtist) onTap;
  final void Function(LidarrArtist)? onSearch;
  final bool embedded;
  final LibraryViewMode viewMode;
  final String scrollKey;
  final ImageSource? Function(LidarrArtist)? imageSourceFor;

  const LidarrArtistList({
    super.key,
    required this.artists,
    required this.onTap,
    this.onSearch,
    this.embedded = false,
    this.viewMode = LibraryViewMode.list,
    this.scrollKey = 'lidarr-library',
    this.imageSourceFor,
  });

  @override
  Widget build(BuildContext context) {
    return LibraryCollection(
      viewMode: viewMode,
      scrollKey: scrollKey,
      embedded: embedded,
      emptyMessage: 'No artists found',
      itemCount: artists.length,
      itemBuilder: (context, index) {
        final artist = artists[index];
        return _ArtistTile(
          key: ValueKey(artist.id),
          grid: viewMode == LibraryViewMode.grid,
          image: imageSourceFor == null
              ? (url: artist.imageUrl ?? '', headers: null)
              : imageSourceFor!(artist),
          artist: artist,
          onTap: () => onTap(artist),
          onSearch: onSearch != null ? () => onSearch!(artist) : null,
        );
      },
    );
  }
}

class _ArtistTile extends StatelessWidget {
  final bool grid;
  final ImageSource? image;
  final LidarrArtist artist;
  final VoidCallback onTap;
  final VoidCallback? onSearch;

  const _ArtistTile({
    super.key,
    required this.grid,
    this.image,
    required this.artist,
    required this.onTap,
    this.onSearch,
  });

  @override
  Widget build(BuildContext context) {
    final stats = artist.statistics;
    final percent = artist.percentComplete;

    return LibraryItem(
      grid: grid,
      name: artist.artistName,
      artworkAspectRatio: 1,
      onTap: onTap,
      artwork: CachedImage(
        url: image?.url,
        headers: image?.headers,
        fit: BoxFit.cover,
        icon: Icons.mic_external_on,
      ),
      details: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const SizedBox(height: 4),
          Wrap(
            spacing: 6,
            runSpacing: 4,
            crossAxisAlignment: WrapCrossAlignment.center,
            children: [
              if (!grid) Container(
                padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 1),
                decoration: BoxDecoration(
                  color: _statusColor.withValues(alpha: 0.15),
                  borderRadius: BorderRadius.circular(4),
                ),
                child: Text(
                  _statusText,
                  style: TextStyle(
                    color: _statusColor,
                    fontSize: 11,
                    fontWeight: FontWeight.w500,
                  ),
                ),
              ),
              if (stats != null) ...[
                Text(
                  artist.albumCountLabel,
                  style: const TextStyle(
                      color: AppTheme.textSecondary, fontSize: 11),
                ),
              ],
            ],
          ),
          if (!grid && stats != null && stats.trackCount > 0) ...[
            const SizedBox(height: 6),
            ClipRRect(
              borderRadius: BorderRadius.circular(3),
              child: LinearProgressIndicator(
                value: percent,
                backgroundColor: AppTheme.surfaceVariant,
                valueColor: AlwaysStoppedAnimation(_progressColor),
                minHeight: 4,
              ),
            ),
          ],
        ],
      ),
      actions: onSearch != null
          ? IconButton(
              icon: const Icon(Icons.search, color: AppTheme.textSecondary),
              tooltip: 'Find albums automatically',
              onPressed: onSearch,
            )
          : null,
    );
  }

  Color get _statusColor => switch (artist.status) {
        'continuing' => AppTheme.downloading,
        'ended' => AppTheme.textSecondary,
        _ => AppTheme.requested,
      };

  String get _statusText => switch (artist.status) {
        'continuing' => 'Active',
        'ended' => 'Ended',
        _ => 'Unknown',
      };

  /// Mirrors the author tile's progress grammar: green only when an ended
  /// artist's monitored tracks are all on disk, info/ember when merely caught
  /// up, red/amber for monitored/unmonitored gaps.
  Color get _progressColor {
    if (artist.percentComplete >= 1.0) {
      return artist.status == 'ended'
          ? AppTheme.available
          : AppTheme.downloading;
    }
    return artist.monitored ? AppTheme.error : AppTheme.requested;
  }
}
