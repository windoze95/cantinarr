import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../core/network/backend_client.dart';
import '../../../core/widgets/app_sheet.dart';
import '../../auth/logic/auth_provider.dart';

/// Sonarr can combine several catalog titles into one library series. Resolve
/// at the time of the tap so local mapping changes and library grants apply.
class TVLibraryLink extends ConsumerStatefulWidget {
  const TVLibraryLink({
    super.key,
    required this.instanceId,
    required this.seriesId,
    required this.builder,
    this.tmdbId,
    this.seasonNumber,
  });

  final String instanceId;
  final int seriesId;
  final int? tmdbId;
  final int? seasonNumber;
  final Widget Function(VoidCallback? onTap) builder;

  @override
  ConsumerState<TVLibraryLink> createState() => _TVLibraryLinkState();
}

class _TVLibraryLinkState extends ConsumerState<TVLibraryLink> {
  bool _loading = false;
  bool _choosing = false;

  Future<void> _open() async {
    final auth = ref.read(authProvider).valueOrNull;
    final router = GoRouter.of(context);
    final location = router.routeInformationProvider.value.uri;
    final instanceId = widget.instanceId;
    final seriesId = widget.seriesId;
    final season = widget.seasonNumber;
    bool stillCurrent() => mounted &&
        identical(ref.read(authProvider).valueOrNull, auth) &&
        widget.instanceId == instanceId && widget.seriesId == seriesId &&
        widget.seasonNumber == season &&
        router.routeInformationProvider.value.uri == location;
    void openTitle(int id) => router.push(
        '/detail/tv/$id?instance_id=${Uri.encodeQueryComponent(instanceId)}');

    if (auth?.connection?.tvLibraryNavigation != true) {
      if ((widget.tmdbId ?? 0) > 0) openTitle(widget.tmdbId!);
      return;
    }
    setState(() => _loading = true);
    try {
      final response = await ref.read(backendClientProvider).get(
        '/api/requests/tv-library-titles',
        queryParameters: {
          'instance_id': instanceId,
          'series_id': seriesId,
          if (season != null) 'season_number': season,
        },
      );
      if (!mounted || !stillCurrent()) return;
      final data = response.data as Map<String, dynamic>;
      final titles = (data['titles'] as List).cast<Map<String, dynamic>>();
      if (data['instance_id'] != instanceId || titles.isEmpty ||
          titles.any((title) => title['tmdb_id'] is! int ||
              (title['tmdb_id'] as int) <= 0 || title['title'] is! String)) {
        throw const FormatException('Invalid TV destination');
      }
      final int? id;
      if (titles.length == 1) {
        id = titles.single['tmdb_id'] as int;
      } else {
        setState(() => _choosing = true);
        id = await showAppSheet<int>(context, builder: (sheetContext) =>
          AppSheet(child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text('Choose a title', style: Theme.of(context).textTheme.titleLarge),
              const SizedBox(height: 8),
              const Text('This library series contains several separately listed titles.'),
              const SizedBox(height: 12),
              for (final title in titles)
                ListTile(
                  title: Text(title['title'] as String),
                  trailing: const Icon(Icons.chevron_right),
                  onTap: () => Navigator.of(sheetContext).pop(title['tmdb_id'] as int),
                ),
              const SizedBox(height: 16),
            ],
          )),
        );
      }
      if (id != null && stillCurrent()) openTitle(id);
    } catch (_) {
      if (mounted && stillCurrent()) {
        ScaffoldMessenger.of(context).showSnackBar(const SnackBar(content:
          Text('Could not resolve this TV title. Try again, or ask an admin to check TV Matches and library access.')));
      }
    } finally {
      if (mounted) {
        setState(() {
          _loading = false;
          _choosing = false;
        });
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    final supported = ref.watch(authProvider).valueOrNull
        ?.connection?.tvLibraryNavigation == true;
    final enabled = widget.seriesId > 0 && widget.instanceId.isNotEmpty &&
        (supported || (widget.tmdbId ?? 0) > 0);
    return Stack(children: [
      widget.builder(enabled && !_loading ? _open : null),
      if (_loading && !_choosing)
        const Positioned(top: 8, right: 8, child: SizedBox(
          width: 20, height: 20,
          child: CircularProgressIndicator(strokeWidth: 2),
        )),
    ]);
  }
}
