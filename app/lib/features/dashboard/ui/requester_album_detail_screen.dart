import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../core/layout/adaptive.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/providers/instance_provider.dart';
import '../../../core/providers/library_refresh_provider.dart';
import '../../../core/providers/realtime_provider.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/cached_image.dart';
import '../../auth/logic/auth_provider.dart';
import '../../issues/ui/report_problem_sheet.dart';
import '../../lidarr/data/lidarr_api_service.dart';
import '../../lidarr/data/lidarr_image.dart';
import '../../lidarr/data/lidarr_models.dart';
import '../../media_download/data/media_download_models.dart';
import '../../media_download/ui/media_download_button.dart';
import '../../request/data/album_ownership.dart';
import '../../request/data/request_service.dart';
import '../../request/ui/album_request_panel.dart';
import '../data/music_library_service.dart';

/// Requester-facing detail for one album, addressed by its MusicBrainz
/// release-group id. Search navigation supplies [initialAlbum] for an
/// immediate, metadata-rich presentation; notification/deep links resolve the
/// same data from the title hint when possible, and the owned-albums digest
/// remains the live source of ownership.
class RequesterAlbumDetailScreen extends ConsumerStatefulWidget {
  final String foreignId;
  final String? titleHint;
  final LidarrAlbum? initialAlbum;
  final String? instanceId;

  /// The term the requester searched to reach this album, when they arrived
  /// from search. Requesting an untracked album makes the server find this
  /// exact metadata record again, and this is the search already proven to
  /// return it.
  final String? searchTerm;

  const RequesterAlbumDetailScreen({
    super.key,
    required this.foreignId,
    this.titleHint,
    this.initialAlbum,
    this.instanceId,
    this.searchTerm,
  });

  @override
  ConsumerState<RequesterAlbumDetailScreen> createState() =>
      _RequesterAlbumDetailScreenState();
}

class _RequesterAlbumDetailScreenState
    extends ConsumerState<RequesterAlbumDetailScreen>
    with WidgetsBindingObserver {
  late final RequestService _requestService;
  LidarrAlbum? _metadata;
  bool _metadataLoading = false;
  int _loadGeneration = 0;
  String? _instanceId;

  /// The foreignAlbumId the library files this album under, when the server
  /// reported it differs from [widget.foreignId] (MusicBrainz merges
  /// release-groups). Ownership binding and the request panel follow this id
  /// once known.
  String? _canonicalForeignId;

  /// Live Lidarr file records for the owned album, fetched only when this
  /// instance offers downloads. Lookup records and the requester digest lack
  /// trustworthy numeric ids, so only this live list backs downloads.
  List<LidarrTrackFile> _trackFiles = const [];

  /// Track records keyed by the file that holds them — the naming join, so a
  /// download row reads "3. Track Title" instead of a file basename.
  Map<int, LidarrTrack> _trackByFileId = const {};
  int _recordsLoadGeneration = 0;

  String get _effectiveForeignId => _canonicalForeignId ?? widget.foreignId;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    _requestService =
        RequestService(backendDio: ref.read(backendClientProvider));
    _startLoads();
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    super.dispose();
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.resumed) _refreshMusicTruth();
  }

  @override
  void didUpdateWidget(covariant RequesterAlbumDetailScreen oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.foreignId != widget.foreignId ||
        oldWidget.initialAlbum != widget.initialAlbum ||
        oldWidget.titleHint != widget.titleHint ||
        oldWidget.instanceId != widget.instanceId) {
      _startLoads();
    }
  }

  void _startLoads() {
    final generation = ++_loadGeneration;
    _instanceId =
        widget.instanceId ?? ref.read(instanceProvider).activeLidarrInstance?.id;
    _metadata = widget.initialAlbum;
    _canonicalForeignId = null;
    _metadataLoading = widget.initialAlbum == null &&
        (widget.titleHint?.trim().isNotEmpty ?? false);
    _trackFiles = const [];
    _trackByFileId = const {};
    WidgetsBinding.instance.addPostFrameCallback((_) {
      _resolveMetadata(generation);
      _resolveTrackFiles();
    });
  }

  LidarrApiService? _lidarrService() {
    final instanceId = _instanceId;
    if (instanceId == null) return null;
    return LidarrApiService(
      backendDio: ref.read(backendClientProvider),
      instanceId: instanceId,
    );
  }

  /// Notification links carry only a title and foreign id. Resolve their
  /// metadata with the same read-only lookup as Music search — only an exact
  /// foreignAlbumId match is ever accepted, so a lookalike listing can never
  /// impersonate this album.
  Future<void> _resolveMetadata(int generation) async {
    if (_metadata != null) return;
    final term = widget.titleHint?.trim() ?? '';
    final service = _lidarrService();
    if (term.isEmpty || service == null) {
      if (mounted && generation == _loadGeneration) {
        setState(() => _metadataLoading = false);
      }
      return;
    }
    LidarrAlbum? match;
    try {
      final results = await service.lookupAlbum(term);
      for (final album in results) {
        if (album.foreignAlbumId == widget.foreignId) {
          match = album;
          break;
        }
      }
    } catch (_) {
      // The title hint still gives the requester a useful fallback.
    }
    if (!mounted || generation != _loadGeneration) return;
    setState(() {
      _metadata = match;
      _metadataLoading = false;
    });
  }

  /// Follows the library's canonical id once the server reports one: the
  /// digest row can then bind, and the request panel re-keys onto the id
  /// every future read will agree on.
  void _onCanonicalForeignId(String canonical) {
    if (!mounted || canonical.isEmpty || canonical == _effectiveForeignId) {
      return;
    }
    setState(() => _canonicalForeignId = canonical);
  }

  /// A problem can be reported only for an album the library actually holds
  /// (the ownership digest row is the requester-visible proof) and when the
  /// server allows reporting — the same shape as the book gate.
  bool _canReportAlbum(OwnedAlbum? owned) {
    final allow = ref
            .watch(authProvider)
            .valueOrNull
            ?.connection
            ?.allowReporting ??
        false;
    return allow && _instanceId != null && owned != null;
  }

  Future<void> _onRequestCompleted() async {
    // The request may have created the library record immediately. Refresh
    // the ownership digest in place.
    ref.invalidate(ownedAlbumsForInstanceProvider(_instanceId));
    ref.read(libraryRefreshTickProvider.notifier).state++;
  }

  Future<void> _refreshMusicTruth() async {
    ref.invalidate(ownedAlbumsForInstanceProvider(_instanceId));
    ref.read(libraryRefreshTickProvider.notifier).state++;
    _resolveTrackFiles();
  }

  /// Resolves the live album record and its files for the download button.
  /// Gated on the instance actually offering downloads (server-computed:
  /// explicit path mappings exist), which also covers admins — without
  /// mappings the server has no bytes to serve anyone.
  Future<void> _resolveTrackFiles() async {
    final auth = ref.read(authProvider).valueOrNull;
    final downloadsEnabled =
        auth?.connection?.mediaDownloadsEnabledFor(_instanceId) ?? false;
    if (!downloadsEnabled) return;
    final service = _lidarrService();
    if (service == null) return;
    final generation = ++_recordsLoadGeneration;
    try {
      final albums = await service.getAlbums();
      LidarrAlbum? record;
      for (final album in albums) {
        if (album.id > 0 && album.foreignAlbumId == _effectiveForeignId) {
          record = album;
          break;
        }
      }
      if (record == null) {
        if (mounted && generation == _recordsLoadGeneration) {
          setState(() {
            _trackFiles = const [];
            _trackByFileId = const {};
          });
        }
        return;
      }
      final results = await Future.wait([
        service.getTrackFiles(albumId: record.id),
        service.getTracks(albumId: record.id).then<List<LidarrTrack>>(
          (tracks) => tracks,
          onError: (_) => const <LidarrTrack>[],
        ),
      ]);
      final files = (results[0] as List<LidarrTrackFile>)
          .where((file) => file.id > 0)
          .toList(growable: false);
      final byFile = <int, LidarrTrack>{};
      for (final track in results[1] as List<LidarrTrack>) {
        if (track.trackFileId > 0) {
          byFile.putIfAbsent(track.trackFileId, () => track);
        }
      }
      if (!mounted || generation != _recordsLoadGeneration) return;
      setState(() {
        _trackFiles = files;
        _trackByFileId = byFile;
      });
    } catch (_) {
      // The page stays useful without downloads; the button simply
      // doesn't render, exactly like an instance with no mappings.
    }
  }

  /// One download choice per file on disk, ordered by disc + track number
  /// (unmatched files trail, by name). Labels come from the track record —
  /// "3. Track Title" — with the file basename as the fallback.
  List<MediaDownloadChoice> _trackDownloadChoices() {
    final ordered = [..._trackFiles];
    int orderOf(LidarrTrackFile file) {
      final track = _trackByFileId[file.id];
      if (track == null) return 1 << 20;
      return track.mediumNumber * 1000 + track.absoluteTrackNumber;
    }

    ordered.sort((a, b) {
      final byTrack = orderOf(a).compareTo(orderOf(b));
      if (byTrack != 0) return byTrack;
      return a.fileName.compareTo(b.fileName);
    });
    return [
      for (final file in ordered)
        MediaDownloadChoice(
          fileId: file.id,
          label: _trackLabel(file),
          subtitle: _trackDetails(file),
          reportedPath: file.path,
        ),
    ];
  }

  String? _trackDetails(LidarrTrackFile file) {
    final details = [
      if (file.qualityName?.isNotEmpty ?? false) file.qualityName!,
      if (file.size > 0) file.sizeFormatted,
    ].join(' · ');
    return details.isEmpty ? null : details;
  }

  String _trackLabel(LidarrTrackFile file) {
    final track = _trackByFileId[file.id];
    if (track == null || track.title.isEmpty) return file.fileName;
    final number =
        track.absoluteTrackNumber > 0 ? '${track.absoluteTrackNumber}. ' : '';
    final disc = track.mediumNumber > 1 ? 'Disc ${track.mediumNumber} · ' : '';
    return '$disc$number${track.title}';
  }

  @override
  Widget build(BuildContext context) {
    ref.listen(libraryChangedEventsProvider, (_, next) {
      if (next.hasValue) _refreshMusicTruth();
    });
    ref.listen(
      instanceProvider.select((state) => state.activeLidarrInstance?.id),
      (previous, next) {
        if (previous == next || widget.instanceId != null) return;
        WidgetsBinding.instance.addPostFrameCallback((_) {
          if (mounted) setState(_startLoads);
        });
      },
    );
    final digest = ref.watch(ownedAlbumsForInstanceProvider(_instanceId));
    return Scaffold(
      appBar: AppBar(title: const Text('Album details')),
      // Metadata renders immediately; ownership and request truth resolve in
      // their own rows instead of blanking the whole page behind one digest.
      body: _resolved(digest.valueOrNull ?? const []),
    );
  }

  Widget _resolved(List<OwnedAlbum> titles) {
    OwnedAlbum? owned;
    for (final album in titles) {
      if (album.foreignAlbumId.isNotEmpty &&
          album.foreignAlbumId == _effectiveForeignId) {
        owned = album;
        break;
      }
    }

    final hintedTitle = widget.titleHint?.trim() ?? '';
    final title = _firstText([
      _metadata?.title,
      owned?.title,
      hintedTitle,
    ]);
    if (title.isEmpty) {
      return _metadataLoading
          ? const Center(
              child: CircularProgressIndicator(color: AppTheme.accent),
            )
          : _notFound();
    }

    final artist = _firstText([
      _metadata?.artistName,
      owned?.artist,
    ]);
    final year = _metadata?.year ?? owned?.year ?? 0;
    final albumType = _metadata?.albumType?.trim() ?? '';
    final overview = _metadata?.overview?.trim() ?? '';
    final genres = _metadata?.genres ?? const <String>[];
    final trackCount = _metadata?.statistics?.trackCount ?? 0;
    final instanceId = _instanceId;

    final requestRefreshTick = ref.watch(libraryRefreshTickProvider);
    LidarrImageSource? cover;
    if (instanceId != null) {
      final rawOwnedCover = owned?.cover.trim() ?? '';
      final ownedCover = rawOwnedCover.toLowerCase().startsWith('http')
          ? ''
          : rawOwnedCover;
      // Lookup covers are remote metadata-CDN URLs and load directly; a live
      // arr-origin absolute URL is never surfaced.
      final remoteCover = _metadata?.remoteCover?.trim() ?? '';
      cover = lidarrImageSource(
        ref,
        _firstText([ownedCover, remoteCover]),
        instanceId,
      );
    }

    return CenteredContent(
      child: ListView(
        // Build the request panel even when large accessibility text pushes
        // it just below the viewport; it owns this album's live request state.
        cacheExtent: MediaQuery.sizeOf(context).height * 2,
        padding: const EdgeInsets.fromLTRB(24, 20, 24, 32),
        children: [
          Center(
            child: ClipRRect(
              borderRadius: BorderRadius.circular(AppTheme.radiusMedium),
              child: CachedImage(
                url: cover?.url,
                headers: cover?.headers,
                width: 172,
                height: 172,
                icon: Icons.album,
                iconSize: 36,
              ),
            ),
          ),
          const SizedBox(height: 20),
          Semantics(
            header: true,
            child: Text(
              title,
              textAlign: TextAlign.center,
              style: Theme.of(context).textTheme.headlineSmall,
            ),
          ),
          if (artist.isNotEmpty) ...[
            const SizedBox(height: 6),
            Text(
              artist,
              textAlign: TextAlign.center,
              style: Theme.of(context).textTheme.bodyLarge?.copyWith(
                    color: AppTheme.textSecondary,
                  ),
            ),
          ],
          if (year > 0 || albumType.isNotEmpty || trackCount > 0) ...[
            const SizedBox(height: 6),
            Text(
              [
                if (year > 0) '$year',
                if (albumType.isNotEmpty) albumType,
                if (trackCount > 0) '$trackCount tracks',
              ].join(' · '),
              textAlign: TextAlign.center,
              style: Theme.of(context).textTheme.bodySmall,
            ),
          ],
          const SizedBox(height: 24),
          AlbumRequestPanel(
            foreignId: _effectiveForeignId,
            title: title,
            instanceId: instanceId,
            searchTerm: widget.searchTerm,
            service: _requestService,
            ownership: owned,
            refreshTick: requestRefreshTick,
            onCanonicalForeignId: _onCanonicalForeignId,
            onRequestCompleted: _onRequestCompleted,
          ),
          if (instanceId != null && _trackFiles.isNotEmpty) ...[
            const SizedBox(height: 14),
            MediaDownloadChoiceButton(
              instanceId: instanceId,
              choices: _trackDownloadChoices(),
              label: _trackFiles.length == 1
                  ? 'Download track'
                  : 'Download tracks',
              sheetTitle: 'Download a track',
              outlined: true,
            ),
          ],
          if (_canReportAlbum(owned)) ...[
            const SizedBox(height: 18),
            // Music has no format axis, so the shared button suffices — no
            // picker between records.
            ReportProblemButton(
              scope: ReportScope.album(
                instanceId: _instanceId!,
                foreignId: _effectiveForeignId,
                title: title,
              ),
            ),
          ],
          if (genres.isNotEmpty) ...[
            const SizedBox(height: 22),
            Wrap(
              alignment: WrapAlignment.center,
              spacing: 6,
              runSpacing: 6,
              children: genres
                  .map((genre) => Chip(
                        label: Text(genre),
                        backgroundColor: AppTheme.surfaceVariant,
                        side: const BorderSide(color: AppTheme.border),
                        materialTapTargetSize: MaterialTapTargetSize.shrinkWrap,
                        visualDensity: VisualDensity.compact,
                      ))
                  .toList(),
            ),
          ],
          if (overview.isNotEmpty) ...[
            const SizedBox(height: 24),
            Text('About this album',
                style: Theme.of(context).textTheme.titleMedium),
            const SizedBox(height: 8),
            Text(
              overview,
              style: Theme.of(context).textTheme.bodyMedium?.copyWith(
                    color: AppTheme.textPrimary,
                  ),
            ),
          ],
        ],
      ),
    );
  }

  Widget _notFound() {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(32),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            const Icon(Icons.album, size: 48, color: AppTheme.textSecondary),
            const SizedBox(height: 12),
            const Text(
              'This album could not be found. It may have been removed from '
              'the library.',
              textAlign: TextAlign.center,
              style: TextStyle(color: AppTheme.textSecondary),
            ),
            const SizedBox(height: 16),
            OutlinedButton(
              onPressed: () => context.go('/dashboard/music'),
              child: const Text('Browse Music'),
            ),
          ],
        ),
      ),
    );
  }
}

String _firstText(Iterable<String?> values) {
  for (final value in values) {
    final text = value?.trim() ?? '';
    if (text.isNotEmpty) return text;
  }
  return '';
}
