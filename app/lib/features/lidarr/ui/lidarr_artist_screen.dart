import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/network/backend_client.dart';
import '../../../navigation/ambient_page_route.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/app_ambient_background.dart';
import '../../../core/widgets/cached_image.dart';
import '../../../core/widgets/error_banner.dart';
import '../data/lidarr_api_service.dart';
import '../data/lidarr_models.dart';
import 'lidarr_releases_screen.dart';

/// Artist detail: an artist summary plus the artist's albums, each with an
/// availability line, a monitor toggle, and an automatic-search action.
/// Mirrors [ChaptarrAuthorDetailScreen] without the per-format machinery —
/// one album is one record.
class LidarrArtistScreen extends ConsumerStatefulWidget {
  final String instanceId;
  final int artistId;
  final String? artistName;

  const LidarrArtistScreen({
    super.key,
    required this.instanceId,
    required this.artistId,
    this.artistName,
  });

  @override
  ConsumerState<LidarrArtistScreen> createState() => _LidarrArtistScreenState();
}

class _LidarrArtistScreenState extends ConsumerState<LidarrArtistScreen> {
  late final LidarrApiService _service;
  LidarrArtist? _artist;
  List<LidarrAlbum> _albums = [];
  bool _isLoading = true;
  String? _error;
  final Set<int> _togglingAlbums = {};

  @override
  void initState() {
    super.initState();
    _service = LidarrApiService(
      backendDio: ref.read(backendClientProvider),
      instanceId: widget.instanceId,
    );
    WidgetsBinding.instance.addPostFrameCallback((_) => _load());
  }

  Future<void> _load() async {
    setState(() => _isLoading = true);
    try {
      // Kick off both requests, then await — effectively parallel without the
      // heterogeneous Future.wait cast.
      final artistFuture = _service.getArtistById(widget.artistId);
      final albumsFuture = _service.getAlbums(artistId: widget.artistId);
      final artist = await artistFuture;
      final albums = await albumsFuture;
      if (!mounted) return;
      albums.sort((a, b) => (b.releaseDate ?? DateTime(0))
          .compareTo(a.releaseDate ?? DateTime(0)));
      setState(() {
        _artist = artist;
        _albums = albums;
        _isLoading = false;
        _error = null;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _isLoading = false;
        _error = 'Failed to load artist: $e';
      });
    }
  }

  Future<void> _toggleMonitored(LidarrAlbum album) async {
    final target = !album.monitored;
    setState(() => _togglingAlbums.add(album.id));
    try {
      await _service.setAlbumMonitored([album.id], target);
      if (!mounted) return;
      setState(() {
        _albums = _albums
            .map((a) => a.id == album.id ? _withMonitored(a, target) : a)
            .toList();
      });
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(
          content: Text(target
              ? 'Monitoring "${album.title}"'
              : 'Stopped monitoring "${album.title}"')));
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context)
          .showSnackBar(SnackBar(content: Text('Failed to update: $e')));
    } finally {
      if (mounted) setState(() => _togglingAlbums.remove(album.id));
    }
  }

  LidarrAlbum _withMonitored(LidarrAlbum album, bool monitored) => LidarrAlbum(
        id: album.id,
        title: album.title,
        artistId: album.artistId,
        foreignAlbumId: album.foreignAlbumId,
        overview: album.overview,
        disambiguation: album.disambiguation,
        releaseDate: album.releaseDate,
        monitored: monitored,
        albumType: album.albumType,
        secondaryTypes: album.secondaryTypes,
        remoteCover: album.remoteCover,
        artist: album.artist,
        statistics: album.statistics,
        images: album.images,
        genres: album.genres,
      );

  void _interactiveSearch(LidarrAlbum album) {
    Navigator.of(context, rootNavigator: true).push(
      AmbientPageRoute(
        builder: (_) => LidarrReleasesScreen(
          instanceId: widget.instanceId,
          albumId: album.id,
          albumTitle: album.title,
        ),
      ),
    );
  }

  Future<void> _searchAlbum(LidarrAlbum album) async {
    try {
      await _service.searchAlbums([album.id]);
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('Search started for "${album.title}"')));
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context)
          .showSnackBar(SnackBar(content: Text('Failed to start search: $e')));
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: Colors.transparent,
      appBar: AppBar(
        title: Text(_artist?.artistName ?? widget.artistName ?? 'Artist'),
        backgroundColor: AppTheme.background,
      ),
      body: AppAmbientBackground(child: _buildBody()),
    );
  }

  Widget _buildBody() {
    if (_isLoading) {
      return const Center(
          child: CircularProgressIndicator(color: AppTheme.accent));
    }
    if (_error != null) {
      return FullScreenError(message: _error!, onRetry: _load);
    }
    final artist = _artist;
    if (artist == null) {
      return const Center(
        child: Text('Artist not found',
            style: TextStyle(color: AppTheme.textSecondary)),
      );
    }

    return RefreshIndicator(
      onRefresh: _load,
      color: AppTheme.accent,
      child: ListView(
        physics: const AlwaysScrollableScrollPhysics(),
        padding: const EdgeInsets.fromLTRB(16, 12, 16, 24),
        children: [
          _ArtistSummaryCard(artist: artist, albumCount: _albums.length),
          const SizedBox(height: 16),
          for (final album in _albums)
            _AlbumCard(
              album: album,
              toggling: _togglingAlbums.contains(album.id),
              onToggleMonitored: () => _toggleMonitored(album),
              onSearch: () => _searchAlbum(album),
              onInteractiveSearch: () => _interactiveSearch(album),
            ),
          if (_albums.isEmpty)
            const Padding(
              padding: EdgeInsets.only(top: 32),
              child: Center(
                child: Text('No albums for this artist',
                    style: TextStyle(color: AppTheme.textSecondary)),
              ),
            ),
        ],
      ),
    );
  }
}

class _ArtistSummaryCard extends StatelessWidget {
  final LidarrArtist artist;
  final int albumCount;

  const _ArtistSummaryCard({required this.artist, required this.albumCount});

  @override
  Widget build(BuildContext context) {
    final stats = artist.statistics;
    return Container(
      padding: const EdgeInsets.all(12),
      decoration: BoxDecoration(
        color: AppTheme.surface,
        borderRadius: BorderRadius.circular(12),
        border: Border.all(color: AppTheme.border, width: 0.5),
      ),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          ClipRRect(
            borderRadius: BorderRadius.circular(8),
            child: SizedBox(
              width: 72,
              height: 72,
              child: CachedImage(
                url: artist.imageUrl,
                fit: BoxFit.cover,
                icon: Icons.mic_external_on,
              ),
            ),
          ),
          const SizedBox(width: 12),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  artist.artistName,
                  style: const TextStyle(
                      color: AppTheme.textPrimary,
                      fontSize: 16,
                      fontWeight: FontWeight.w600),
                ),
                const SizedBox(height: 4),
                Text(
                  [
                    '$albumCount album${albumCount == 1 ? '' : 's'}',
                    if (stats != null && stats.trackCount > 0)
                      artist.trackCountLabel,
                    if (stats != null && stats.sizeOnDisk > 0)
                      stats.sizeFormatted,
                  ].join(' • '),
                  style: const TextStyle(
                      color: AppTheme.textSecondary, fontSize: 12),
                ),
                if (artist.overview != null &&
                    artist.overview!.trim().isNotEmpty) ...[
                  const SizedBox(height: 8),
                  Text(
                    artist.overview!,
                    style: const TextStyle(
                        color: AppTheme.textSecondary, fontSize: 12),
                    maxLines: 3,
                    overflow: TextOverflow.ellipsis,
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

class _AlbumCard extends StatelessWidget {
  final LidarrAlbum album;
  final bool toggling;
  final VoidCallback onToggleMonitored;
  final VoidCallback onSearch;
  final VoidCallback onInteractiveSearch;

  const _AlbumCard({
    required this.album,
    required this.toggling,
    required this.onToggleMonitored,
    required this.onSearch,
    required this.onInteractiveSearch,
  });

  @override
  Widget build(BuildContext context) {
    final stats = album.statistics;
    return Container(
      margin: const EdgeInsets.only(bottom: 8),
      padding: const EdgeInsets.fromLTRB(12, 10, 4, 10),
      decoration: BoxDecoration(
        color: AppTheme.surface,
        borderRadius: BorderRadius.circular(10),
        border: Border.all(color: AppTheme.border, width: 0.5),
      ),
      child: Row(
        children: [
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  album.title,
                  style: const TextStyle(
                      color: AppTheme.textPrimary,
                      fontSize: 14,
                      fontWeight: FontWeight.w500),
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                ),
                const SizedBox(height: 2),
                Text(
                  [
                    if (album.year > 0) '${album.year}',
                    if (album.albumType != null &&
                        album.albumType!.isNotEmpty)
                      album.albumType!,
                    _availabilityLabel(stats),
                  ].where((part) => part.isNotEmpty).join(' • '),
                  style: TextStyle(
                      color: _availabilityColor(stats), fontSize: 12),
                ),
              ],
            ),
          ),
          IconButton(
            icon: const Icon(Icons.search,
                color: AppTheme.textSecondary, size: 20),
            tooltip: 'Find automatically',
            onPressed: onSearch,
          ),
          IconButton(
            icon: const Icon(Icons.manage_search,
                color: AppTheme.textSecondary, size: 20),
            tooltip: 'Choose a download',
            onPressed: onInteractiveSearch,
          ),
          toggling
              ? const Padding(
                  padding: EdgeInsets.symmetric(horizontal: 12),
                  child: SizedBox(
                    width: 20,
                    height: 20,
                    child: CircularProgressIndicator(
                        strokeWidth: 2, color: AppTheme.accent),
                  ),
                )
              : IconButton(
                  icon: Icon(
                    album.monitored
                        ? Icons.bookmark_rounded
                        : Icons.bookmark_border_rounded,
                    color: album.monitored
                        ? AppTheme.accent
                        : AppTheme.textSecondary,
                    size: 22,
                  ),
                  tooltip:
                      album.monitored ? 'Stop monitoring' : 'Monitor album',
                  onPressed: onToggleMonitored,
                ),
        ],
      ),
    );
  }

  String _availabilityLabel(LidarrAlbumStatistics? stats) {
    if (stats == null || stats.trackCount <= 0) {
      return album.hasFiles ? 'On disk' : 'No files';
    }
    if (album.isComplete) return '${stats.trackCount} tracks · complete';
    return '${stats.trackFileCount} of ${stats.trackCount} tracks';
  }

  Color _availabilityColor(LidarrAlbumStatistics? stats) {
    if (album.isComplete) return AppTheme.available;
    if (album.hasFiles) return AppTheme.requested;
    return AppTheme.textSecondary;
  }
}
