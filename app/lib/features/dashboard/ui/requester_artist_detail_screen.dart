import 'dart:async';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/layout/adaptive.dart';
import '../../../core/providers/instance_provider.dart';
import '../../../core/providers/library_refresh_provider.dart';
import '../../../core/providers/realtime_provider.dart';
import '../../discover/data/music_discovery_service.dart';
import '../../discover/data/music_models.dart';
import '../../discover/logic/discovery_access.dart';
import '../../discover/logic/music_enrichment.dart';
import '../../discover/ui/music_search_results_view.dart';
import '../../request/data/album_ownership.dart';
import '../data/music_artists_service.dart';

/// Every artist opens a paginated exact-ID discography, even before Lidarr
/// tracks them. Library records supplement the public catalog independently.
class RequesterArtistDetailScreen extends ConsumerStatefulWidget {
  final String foreignArtistId;
  final String? nameHint;
  final String? instanceId;
  final String? searchTerm;
  final MusicArtist? initialArtist;
  const RequesterArtistDetailScreen(
      {super.key,
      required this.foreignArtistId,
      this.nameHint,
      this.instanceId,
      this.searchTerm,
      this.initialArtist});
  @override
  ConsumerState<RequesterArtistDetailScreen> createState() =>
      _RequesterArtistDetailScreenState();
}

class _RequesterArtistDetailScreenState
    extends ConsumerState<RequesterArtistDetailScreen> {
  MusicArtist? _artist;
  List<MusicAlbum> _albums = [];
  String? _id;
  String? _instanceId;
  String? _error;
  bool _artistFailed = false;
  bool _loading = true;
  int? _next;
  int _generation = 0;
  final _tokens = <CancelToken>{};
  @override
  void initState() {
    super.initState();
    _start();
  }

  @override
  void didUpdateWidget(covariant RequesterArtistDetailScreen oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.foreignArtistId != widget.foreignArtistId ||
        oldWidget.instanceId != widget.instanceId) {
      _start();
    }
  }

  void _cancel() {
    _generation++;
    for (final t in _tokens) {
      t.cancel();
    }
    _tokens.clear();
  }

  @override
  void dispose() {
    _cancel();
    super.dispose();
  }

  void _start() {
    _cancel();
    _id = widget.foreignArtistId;
    _instanceId = widget.instanceId ??
        ref.read(instanceProvider).activeLidarrInstance?.id;
    _artist = widget.initialArtist;
    _albums = [];
    _next = null;
    _loading = true;
    _error = null;
    _artistFailed = false;
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (mounted) {
        _loadArtist();
        _load(1);
      }
    });
  }

  Future<T> _read<T>(Future<T> Function(CancelToken) load) async {
    final token = CancelToken();
    _tokens.add(token);
    try {
      return await load(token).timeout(const Duration(seconds: 10),
          onTimeout: () {
        token.cancel();
        throw TimeoutException('Artist read timed out');
      });
    } finally {
      _tokens.remove(token);
    }
  }

  Future<void> _loadArtist() async {
    final generation = _generation;
    try {
      final artist = await _read((t) => ref
          .read(musicDiscoveryServiceProvider)
          .artist(_id!, _instanceId, cancelToken: t));
      if (!mounted || generation != _generation) return;
      setState(() => _artist = artist);
      if (artist.foreignId != _id) {
        _cancel();
        setState(() {
          _id = artist.foreignId;
          _albums = [];
          _next = null;
        });
        _load(1);
      }
    } catch (_) {
      if (mounted && generation == _generation) {
        setState(() => _artistFailed = true);
      }
    }
  }

  Future<void> _load(int page) async {
    final generation = _generation;
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      final result = await _read((t) => ref
          .read(musicDiscoveryServiceProvider)
          .artistAlbums(_id!, _instanceId, page: page, cancelToken: t));
      if (!mounted || generation != _generation) return;
      setState(() {
        _albums = page == 1 ? result.results : [..._albums, ...result.results];
        _next = result.nextPage;
        _loading = false;
      });
    } catch (_) {
      if (mounted && generation == _generation) {
        setState(() {
          _loading = false;
          _error = 'This artist’s catalog releases could not be loaded.';
        });
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    final access = ref.watch(discoveryAccessProvider);
    ref.listen(catalogDiscoveryScopeProvider, (previous, next) {
      if (previous != next) setState(_start);
    });
    final target = (
      instanceId: _instanceId,
      foreignArtistId: _id ?? widget.foreignArtistId
    );
    ref.listen(libraryChangedEventsProvider, (_, next) {
      if (next.hasValue) {
        ref.invalidate(musicArtistDetailProvider(target));
        ref.read(libraryRefreshTickProvider.notifier).state++;
      }
    });
    if (!access.canBrowse('lidarr', _instanceId)) {
      return Scaffold(
          appBar: AppBar(title: const Text('Artist')),
          body: const Center(
              child: Text('Music is not available for this account.')));
    }
    final library = _instanceId == null
        ? null
        : ref.watch(musicArtistDetailProvider(target));
    final records = library?.valueOrNull?.titles ?? const <OwnedAlbum>[];
    final saved = _instanceId == null
        ? null
        : ref.watch(savedMusicRequestsProvider(_instanceId!));
    final catalogIds = _albums
        .expand((a) =>
            verifiedMusicIDs(a.foreignId, saved?.valueOrNull ?? const []))
        .toSet();
    final represented = <String>{};
    final extras = records
        .where((a) =>
            a.foreignAlbumId.isNotEmpty &&
            (!catalogIds.contains(a.foreignAlbumId) ||
                !represented.add(a.foreignAlbumId)))
        .toList();
    final missingFromLibrary = library?.error is DioException &&
        (library!.error as DioException).response?.statusCode == 404;
    return Scaffold(
        appBar:
            AppBar(title: Text(_artist?.name ?? widget.nameHint ?? 'Artist')),
        body: CenteredContent(
            child: ListView(
                key: PageStorageKey(
                    'artist:${widget.foreignArtistId}:$_instanceId'),
                children: [
              if (_artist?.subtitle.isNotEmpty == true)
                Padding(
                    padding: const EdgeInsets.all(16),
                    child: Text(_artist!.subtitle)),
              if (_artistFailed)
                TextButton(
                    onPressed: _loadArtist,
                    child: const Text(
                        'Artist details could not be checked. Retry')),
              for (var i = 0; i < _albums.length; i++)
                MusicAlbumResultTile(
                    key: ValueKey('album:${_albums[i].foreignId}:$i'),
                    album: _albums[i],
                    instanceId: _instanceId,
                    query: widget.searchTerm,
                    badge: musicBadge(_albums[i].foreignId, records,
                        saved?.valueOrNull ?? const [])),
              for (var i = 0; i < extras.length; i++)
                MusicAlbumResultTile(
                    key: ValueKey('library:${extras[i].recordId}:$i'),
                    album: libraryMusicAlbum(extras[i]),
                    libraryAlbum: extras[i],
                    instanceId: _instanceId,
                    query: widget.searchTerm,
                    badge: musicBadge(extras[i].foreignAlbumId, records,
                        saved?.valueOrNull ?? const [])),
              if (_loading)
                const Padding(
                    padding: EdgeInsets.all(16),
                    child: LinearProgressIndicator()),
              if (_error != null)
                TextButton(
                    onPressed: () => _load(_next ?? 1),
                    child: Text('$_error Retry')),
              if (!_loading &&
                  _error == null &&
                  _albums.isEmpty &&
                  extras.isEmpty)
                const Padding(
                    padding: EdgeInsets.all(24),
                    child: Text(
                        'No albums, EPs, or singles on this catalog page.')),
              if (_next != null && !_loading)
                TextButton(
                    onPressed: () => _load(_next!),
                    child: const Text('More releases')),
              if (library?.hasError == true && !missingFromLibrary)
                const Padding(
                    padding: EdgeInsets.all(16),
                    child: Text('Library availability could not be checked.')),
              if (saved?.hasError == true)
                const Padding(
                    padding: EdgeInsets.all(16),
                    child: Text('Saved requests could not be checked.')),
            ])));
  }
}
