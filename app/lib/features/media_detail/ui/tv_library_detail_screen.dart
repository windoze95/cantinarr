import 'package:cantinarr/l10n/l10n.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../../core/network/backend_client.dart';
import '../../../core/storage/metadata_preferences.dart';
import '../../auth/logic/auth_provider.dart';
import '../../discover/data/tmdb_models.dart';
import '../data/tv_library_service.dart';
import 'media_detail_screen.dart';

/// Resolves native identity before constructing the regular title page. No
/// title chooser, admin screen, or route-stack replacement is involved.
class TVLibraryDetailScreen extends ConsumerStatefulWidget {
  const TVLibraryDetailScreen(
      {super.key, required this.instanceId, required this.seriesId});
  final String instanceId;
  final int seriesId;

  @override
  ConsumerState<TVLibraryDetailScreen> createState() =>
      _TVLibraryDetailScreenState();
}

class _TVLibraryDetailScreenState extends ConsumerState<TVLibraryDetailScreen> {
  late TVLibraryService _service;
  late Future<TVLibraryDetail> _load;
  late final (int?, String?) _session;
  CancelToken? _cancel;
  String? _metadataLanguage;

  (int?, String?) _identity(AuthState? auth) =>
      (auth?.user?.id, auth?.connection?.serverUrl);

  @override
  void initState() {
    super.initState();
    _session = _identity(ref.read(authProvider).valueOrNull);
    _reloadService();
  }

  void _reloadService() {
    _cancel?.cancel();
    _cancel = CancelToken();
    _metadataLanguage = ref.read(effectiveMetadataLanguageProvider);
    _service = TVLibraryService(
        dio: ref.read(backendClientProvider),
        libraryId: widget.instanceId,
        seriesId: widget.seriesId,
        metadataLanguage: _metadataLanguage);
    _load = _service.load(cancelToken: _cancel);
  }

  @override
  void dispose() {
    _cancel?.cancel();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    ref.listen(effectiveMetadataLanguageProvider, (previous, next) {
      if (previous != next) setState(_reloadService);
    });
    if (_identity(ref.watch(authProvider).valueOrNull) != _session) {
      return Scaffold(
          appBar: AppBar(),
          body: Center(
              child:
                  Text(context.l10n.browseYourSessionChangedReopenThisTitle)));
    }
    return FutureBuilder<TVLibraryDetail>(
      future: _load,
      builder: (context, snapshot) {
        final detail = snapshot.connectionState == ConnectionState.done
            ? snapshot.data
            : null;
        if (detail != null) {
          return MediaDetailScreen(
              key: ValueKey(
                  (widget.instanceId, widget.seriesId, _metadataLanguage)),
              id: detail.catalogId,
              mediaType: MediaType.tv,
              instanceId: widget.instanceId,
              librarySeries: detail.catalogId > 0 ? null : _service);
        }
        final error = snapshot.error;
        final code = error is DioException ? error.response?.statusCode : null;
        final unavailable = code == 400 || code == 403;
        return Scaffold(
          appBar: AppBar(
              title: Text(unavailable
                  ? context.l10n.browseLibraryUnavailable
                  : context.l10n.browseTVShow)),
          body: Center(
              child: Padding(
            padding: const EdgeInsets.all(24),
            child: error == null
                ? const CircularProgressIndicator()
                : Column(
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      Text(
                          unavailable
                              ? context.l10n
                                  .browseThisLibraryWasRemovedOrYouNoLongerHaveAccessToIt
                              : code == 404
                                  ? context.l10n.browseThisTitleIsNotAvailable
                                  : context.l10n
                                      .browseCouldNotLoadThisSeriesRetryOrAskAnAdminToCheckTVMatches,
                          textAlign: TextAlign.center),
                      if (!unavailable && code != 404)
                        TextButton(
                            onPressed: () => setState(() {
                                  _reloadService();
                                }),
                            child: Text(context.l10n.browseRetry)),
                    ],
                  ),
          )),
        );
      },
    );
  }
}
