import 'package:flutter/material.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/cached_image.dart';
import '../data/lidarr_models.dart';

/// List of Lidarr artists with explicit row actions and progress indicators.
/// Mirrors [ChaptarrAuthorList] adapted to the artist-centric music library.
class LidarrArtistList extends StatelessWidget {
  final List<LidarrArtist> artists;
  final void Function(LidarrArtist) onTap;
  final void Function(LidarrArtist)? onSearch;
  final bool embedded;

  const LidarrArtistList({
    super.key,
    required this.artists,
    required this.onTap,
    this.onSearch,
    this.embedded = false,
  });

  @override
  Widget build(BuildContext context) {
    if (artists.isEmpty) {
      return const Center(
        child: Text('No artists found',
            style: TextStyle(color: AppTheme.textSecondary)),
      );
    }

    return ListView.separated(
      shrinkWrap: embedded,
      physics: embedded ? const NeverScrollableScrollPhysics() : null,
      itemCount: artists.length,
      separatorBuilder: (_, __) =>
          const Divider(color: AppTheme.border, height: 1),
      itemBuilder: (context, index) {
        final artist = artists[index];
        return _ArtistTile(
          artist: artist,
          onTap: () => onTap(artist),
          onSearch: onSearch != null ? () => onSearch!(artist) : null,
        );
      },
    );
  }
}

class _ArtistTile extends StatelessWidget {
  final LidarrArtist artist;
  final VoidCallback onTap;
  final VoidCallback? onSearch;

  const _ArtistTile({
    required this.artist,
    required this.onTap,
    this.onSearch,
  });

  @override
  Widget build(BuildContext context) {
    final stats = artist.statistics;
    final percent = artist.percentComplete;

    return ListTile(
      onTap: onTap,
      contentPadding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
      leading: ClipRRect(
        borderRadius: BorderRadius.circular(6),
        child: SizedBox(
          width: 45,
          height: 67,
          child: CachedImage(
            url: artist.imageUrl,
            fit: BoxFit.cover,
            icon: Icons.mic_external_on,
          ),
        ),
      ),
      title: Text(
        artist.artistName,
        style: const TextStyle(
            color: AppTheme.textPrimary, fontWeight: FontWeight.w500),
        maxLines: 1,
        overflow: TextOverflow.ellipsis,
      ),
      subtitle: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const SizedBox(height: 4),
          Row(
            children: [
              Container(
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
                const SizedBox(width: 6),
                Text(
                  artist.albumCountLabel,
                  style: const TextStyle(
                      color: AppTheme.textSecondary, fontSize: 11),
                ),
              ],
            ],
          ),
          if (stats != null && stats.trackCount > 0) ...[
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
      trailing: onSearch != null
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
