import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/network/backend_client.dart';
import '../../auth/logic/auth_provider.dart';
import '../../discover/data/tmdb_models.dart';
import '../data/tv_library_service.dart';
import 'media_detail_screen.dart';

/// Resolves native identity before constructing the regular title page. No
/// title chooser, admin screen, or route-stack replacement is involved.
class TVLibraryDetailScreen extends ConsumerStatefulWidget {
  const TVLibraryDetailScreen({super.key, required this.instanceId,
    required this.seriesId});
  final String instanceId;
  final int seriesId;

  @override
  ConsumerState<TVLibraryDetailScreen> createState() => _TVLibraryDetailScreenState();
}

class _TVLibraryDetailScreenState extends ConsumerState<TVLibraryDetailScreen> {
  late final TVLibraryService _service;
  late Future<TVLibraryDetail> _load;
  late final (int?, String?) _session;

  (int?, String?) _identity(AuthState? auth) =>
      (auth?.user?.id, auth?.connection?.serverUrl);

  @override
  void initState() {
    super.initState();
    _session = _identity(ref.read(authProvider).valueOrNull);
    _service = TVLibraryService(dio: ref.read(backendClientProvider),
        libraryId: widget.instanceId, seriesId: widget.seriesId);
    _load = _service.load();
  }

  @override
  Widget build(BuildContext context) {
    if (_identity(ref.watch(authProvider).valueOrNull) != _session) {
      return Scaffold(appBar: AppBar(), body: const Center(
          child: Text('Your session changed. Reopen this title.')));
    }
    return FutureBuilder<TVLibraryDetail>(
    future: _load,
    builder: (context, snapshot) {
      final detail = snapshot.data;
      if (detail != null) {
        return MediaDetailScreen(id: detail.catalogId, mediaType: MediaType.tv,
          instanceId: widget.instanceId,
          librarySeries: detail.catalogId > 0 ? null : _service);
      }
      final error = snapshot.error;
      final code = error is DioException ? error.response?.statusCode : null;
      final unavailable = code == 400 || code == 403;
      return Scaffold(
        appBar: AppBar(title: Text(unavailable ? 'Library unavailable' : 'TV show')),
        body: Center(child: Padding(padding: const EdgeInsets.all(24),
          child: error == null ? const CircularProgressIndicator() : Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              Text(unavailable
                ? 'This library was removed or you no longer have access to it.'
                : code == 404 ? 'This title is not available.'
                : 'Could not load this series. Retry, or ask an admin to check TV Matches.',
                textAlign: TextAlign.center),
              if (!unavailable && code != 404)
                TextButton(onPressed: () => setState(() { _load = _service.load(); }),
                  child: const Text('Retry')),
            ],
          ),
        )),
      );
    },
  );
  }
}
