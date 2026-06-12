import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/network/backend_client.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/error_banner.dart';
import '../data/radarr_api_service.dart';
import '../data/radarr_models.dart';

enum _ReleaseSort { smart, seeders, size, age }

/// Interactive release search for a single movie.
/// Queries all indexers live (slow), lists releases and lets the admin grab
/// one manually.
class RadarrReleasesScreen extends ConsumerStatefulWidget {
  final String instanceId;
  final int movieId;
  final String movieTitle;

  const RadarrReleasesScreen({
    super.key,
    required this.instanceId,
    required this.movieId,
    required this.movieTitle,
  });

  @override
  ConsumerState<RadarrReleasesScreen> createState() =>
      _RadarrReleasesScreenState();
}

class _RadarrReleasesScreenState extends ConsumerState<RadarrReleasesScreen> {
  late final RadarrApiService _service;
  List<RadarrRelease> _releases = [];
  bool _isLoading = true;
  bool _isGrabbing = false;
  String? _error;
  _ReleaseSort _sort = _ReleaseSort.smart;
  bool _ascending = false;
  final Set<String> _expandedGuids = {};

  @override
  void initState() {
    super.initState();
    _service = RadarrApiService(
      backendDio: ref.read(backendClientProvider),
      instanceId: widget.instanceId,
    );
    WidgetsBinding.instance.addPostFrameCallback((_) => _search());
  }

  Future<void> _search() async {
    setState(() {
      _isLoading = true;
      _error = null;
    });
    try {
      final releases = await _service.getReleases(widget.movieId);
      if (!mounted) return;
      setState(() {
        _releases = releases;
        _isLoading = false;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _isLoading = false;
        _error = 'Release search failed: $e';
      });
    }
  }

  List<RadarrRelease> get _sorted {
    final list = [..._releases];
    switch (_sort) {
      case _ReleaseSort.smart:
        list.sort(_smartCompare);
        return list;
      case _ReleaseSort.seeders:
        list.sort((a, b) => (a.seeders ?? -1).compareTo(b.seeders ?? -1));
      case _ReleaseSort.size:
        list.sort((a, b) => a.size.compareTo(b.size));
      case _ReleaseSort.age:
        list.sort((a, b) => a.ageHours.compareTo(b.ageHours));
    }
    return _ascending ? list : list.reversed.toList();
  }

  /// Approved releases first; usenet sorted by age (newest first is ascending
  /// age), torrents by seeders; usenet listed before torrents.
  static int _smartCompare(RadarrRelease a, RadarrRelease b) {
    if (a.rejected != b.rejected) return a.rejected ? 1 : -1;
    if (a.protocol != b.protocol) {
      if (a.protocol == 'usenet') return -1;
      if (b.protocol == 'usenet') return 1;
    }
    if (a.isTorrent && b.isTorrent) {
      return (b.seeders ?? 0).compareTo(a.seeders ?? 0);
    }
    return a.ageHours.compareTo(b.ageHours);
  }

  String _sortLabel(_ReleaseSort sort) => switch (sort) {
        _ReleaseSort.smart => 'Default',
        _ReleaseSort.seeders => 'Seeders',
        _ReleaseSort.size => 'Size',
        _ReleaseSort.age => 'Age',
      };

  void _onSortSelected(_ReleaseSort sort) {
    setState(() {
      if (_sort == sort && sort != _ReleaseSort.smart) {
        _ascending = !_ascending;
      } else {
        _sort = sort;
        // Sensible default direction per field.
        _ascending = sort == _ReleaseSort.age;
      }
    });
  }

  Future<void> _confirmGrab(RadarrRelease release) async {
    if (_isGrabbing) return;
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        backgroundColor: AppTheme.surface,
        title: const Text('Grab Release'),
        content: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(release.title,
                style:
                    const TextStyle(color: AppTheme.textPrimary, fontSize: 13)),
            if (release.rejected && release.rejections.isNotEmpty) ...[
              const SizedBox(height: 12),
              Text(
                'This release was rejected:\n'
                '${release.rejections.map((r) => '• $r').join('\n')}',
                style: const TextStyle(color: AppTheme.requested, fontSize: 12),
              ),
            ],
            const SizedBox(height: 12),
            const Text('Send this release to the download client?',
                style: TextStyle(color: AppTheme.textSecondary, fontSize: 13)),
          ],
        ),
        actions: [
          TextButton(
              onPressed: () => Navigator.pop(ctx, false),
              child: const Text('Cancel')),
          TextButton(
            onPressed: () => Navigator.pop(ctx, true),
            child: const Text('Grab'),
          ),
        ],
      ),
    );
    if (confirmed != true || !mounted) return;

    setState(() => _isGrabbing = true);
    try {
      await _service.grabRelease(
          guid: release.guid, indexerId: release.indexerId);
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text('Release sent to download client')));
      Navigator.of(context).pop();
    } catch (e) {
      if (!mounted) return;
      setState(() => _isGrabbing = false);
      ScaffoldMessenger.of(context)
          .showSnackBar(SnackBar(content: Text('Failed to grab release: $e')));
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: AppTheme.background,
      appBar: AppBar(
        backgroundColor: AppTheme.background,
        title: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            const Text('Interactive Search'),
            Text(
              widget.movieTitle,
              style: const TextStyle(
                  color: AppTheme.textSecondary,
                  fontSize: 12,
                  fontWeight: FontWeight.w400),
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
            ),
          ],
        ),
        actions: [
          PopupMenuButton<_ReleaseSort>(
            icon: const Icon(Icons.sort, color: AppTheme.textPrimary),
            color: AppTheme.surfaceVariant,
            tooltip: 'Sort releases',
            onSelected: _onSortSelected,
            itemBuilder: (_) => _ReleaseSort.values
                .map((s) => PopupMenuItem(
                      value: s,
                      child: Row(
                        children: [
                          if (s == _sort && s != _ReleaseSort.smart)
                            Icon(
                                _ascending
                                    ? Icons.arrow_upward
                                    : Icons.arrow_downward,
                                size: 16,
                                color: AppTheme.accent)
                          else if (s == _sort)
                            const Icon(Icons.check,
                                size: 16, color: AppTheme.accent)
                          else
                            const SizedBox(width: 16),
                          const SizedBox(width: 8),
                          Text(_sortLabel(s)),
                        ],
                      ),
                    ))
                .toList(),
          ),
          IconButton(
            icon: const Icon(Icons.refresh, color: AppTheme.textPrimary),
            tooltip: 'Search again',
            onPressed: _isLoading ? null : _search,
          ),
        ],
      ),
      body: Stack(
        children: [
          _buildBody(),
          if (_isGrabbing)
            Container(
              color: Colors.black54,
              child: const Center(
                  child: CircularProgressIndicator(color: AppTheme.accent)),
            ),
        ],
      ),
    );
  }

  Widget _buildBody() {
    if (_isLoading) {
      return const Center(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            CircularProgressIndicator(color: AppTheme.accent),
            SizedBox(height: 20),
            Text('Searching indexers...',
                style: TextStyle(
                    color: AppTheme.textPrimary,
                    fontSize: 16,
                    fontWeight: FontWeight.w500)),
            SizedBox(height: 6),
            Text('This can take up to a minute.',
                style: TextStyle(color: AppTheme.textSecondary, fontSize: 13)),
          ],
        ),
      );
    }
    if (_error != null) {
      return FullScreenError(message: _error!, onRetry: _search);
    }
    if (_releases.isEmpty) {
      return Center(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            const Icon(Icons.search_off,
                size: 48, color: AppTheme.textSecondary),
            const SizedBox(height: 12),
            const Text('No releases found',
                style: TextStyle(color: AppTheme.textSecondary, fontSize: 16)),
            const SizedBox(height: 20),
            ElevatedButton(
                onPressed: _search, child: const Text('Search Again')),
          ],
        ),
      );
    }

    final releases = _sorted;
    return ListView.separated(
      padding: const EdgeInsets.symmetric(vertical: 8),
      itemCount: releases.length,
      separatorBuilder: (_, __) =>
          const Divider(color: AppTheme.border, height: 1),
      itemBuilder: (context, index) {
        final release = releases[index];
        return _ReleaseTile(
          release: release,
          expanded: _expandedGuids.contains(release.guid),
          onToggleExpand: () => setState(() {
            if (!_expandedGuids.add(release.guid)) {
              _expandedGuids.remove(release.guid);
            }
          }),
          onTap: () => _confirmGrab(release),
        );
      },
    );
  }
}

class _ReleaseTile extends StatelessWidget {
  final RadarrRelease release;
  final bool expanded;
  final VoidCallback onToggleExpand;
  final VoidCallback onTap;

  const _ReleaseTile({
    required this.release,
    required this.expanded,
    required this.onToggleExpand,
    required this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    return InkWell(
      onTap: onTap,
      child: Opacity(
        opacity: release.rejected ? 0.6 : 1,
        child: Padding(
          padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 10),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(
                release.title,
                style: const TextStyle(
                    color: AppTheme.textPrimary,
                    fontSize: 13,
                    fontWeight: FontWeight.w500),
                maxLines: 2,
                overflow: TextOverflow.ellipsis,
              ),
              const SizedBox(height: 6),
              Wrap(
                spacing: 6,
                runSpacing: 4,
                children: [
                  _ReleaseBadge(
                    text: release.protocol.toUpperCase(),
                    color: release.isTorrent
                        ? AppTheme.downloading
                        : AppTheme.available,
                  ),
                  if (release.quality != null)
                    _ReleaseBadge(
                        text: release.quality!, color: AppTheme.accent),
                  _ReleaseBadge(
                      text: release.sizeFormatted,
                      color: AppTheme.textSecondary),
                  _ReleaseBadge(
                      text: release.ageFormatted,
                      color: AppTheme.textSecondary),
                  if (release.indexer != null && release.indexer!.isNotEmpty)
                    _ReleaseBadge(
                        text: release.indexer!, color: AppTheme.textSecondary),
                  if (release.isTorrent)
                    _ReleaseBadge(
                      text:
                          'S:${release.seeders ?? 0} L:${release.leechers ?? 0}',
                      color: (release.seeders ?? 0) > 0
                          ? AppTheme.available
                          : AppTheme.error,
                    ),
                ],
              ),
              if (release.rejected && release.rejections.isNotEmpty) ...[
                const SizedBox(height: 6),
                GestureDetector(
                  onTap: onToggleExpand,
                  behavior: HitTestBehavior.opaque,
                  child: Row(
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      const Icon(Icons.warning_amber_rounded,
                          size: 14, color: AppTheme.requested),
                      const SizedBox(width: 4),
                      const Text('Rejected',
                          style: TextStyle(
                              color: AppTheme.requested,
                              fontSize: 12,
                              fontWeight: FontWeight.w500)),
                      Icon(expanded ? Icons.expand_less : Icons.expand_more,
                          size: 16, color: AppTheme.requested),
                    ],
                  ),
                ),
                if (expanded)
                  Padding(
                    padding: const EdgeInsets.only(top: 4),
                    child: Text(
                      release.rejections.map((r) => '• $r').join('\n'),
                      style: const TextStyle(
                          color: AppTheme.textSecondary, fontSize: 12),
                    ),
                  ),
              ],
            ],
          ),
        ),
      ),
    );
  }
}

class _ReleaseBadge extends StatelessWidget {
  final String text;
  final Color color;

  const _ReleaseBadge({required this.text, required this.color});

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.15),
        borderRadius: BorderRadius.circular(4),
      ),
      child: Text(
        text,
        style: TextStyle(
            color: color, fontSize: 10.5, fontWeight: FontWeight.w500),
      ),
    );
  }
}
