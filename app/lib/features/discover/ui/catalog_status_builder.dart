import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../data/tmdb_models.dart';
import '../logic/search_library_status.dart';
import '../logic/tv_card_status_provider.dart';
import '../logic/media_card_status_provider.dart';
import '../../auth/logic/auth_provider.dart';

/// Keeps catalog status out of generic poster widgets and arr-native rows.
class CatalogStatusBuilder extends StatelessWidget {
  final MediaItem item;
  final LibraryStatus? legacyStatus;
  final bool resolveTVStatus;
  final Widget Function(LibraryStatus?) builder;

  const CatalogStatusBuilder(
      {super.key,
      required this.item,
      required this.builder,
      this.legacyStatus,
      this.resolveTVStatus = true});

  @override
  Widget build(BuildContext context) {
    if (!resolveTVStatus ||
        (item.mediaType != MediaType.movie && item.mediaType != MediaType.tv)) {
      return builder(legacyStatus);
    }
    return _MediaStatusBuilder(
        item: item, legacyStatus: legacyStatus, builder: builder);
  }
}

class _MediaStatusBuilder extends ConsumerWidget {
  final MediaItem item;
  final LibraryStatus? legacyStatus;
  final Widget Function(LibraryStatus?) builder;
  const _MediaStatusBuilder(
      {required this.item, required this.legacyStatus, required this.builder});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final connection = ref.watch(authProvider).valueOrNull?.connection;
    if (connection?.mediaLibraryAvailability == true) {
      final instanceId = item.mediaType == MediaType.movie
          ? connection?.defaultRadarrInstance?.id
          : connection?.defaultSonarrInstance?.id;
      if (instanceId == null ||
          !TickerMode.valuesOf(context).enabled ||
          ModalRoute.of(context)?.isCurrent == false) return builder(null);
      final status =
          ref.watch(mediaCardStatusProvider((item.mediaType, item.id)));
      return builder(
          status.hasError ? unknownTVLibraryStatus : status.valueOrNull);
    }
    return item.mediaType == MediaType.tv
        ? _TVStatusBuilder(
            tmdbId: item.id, legacyStatus: legacyStatus, builder: builder)
        : builder(legacyStatus);
  }
}

class _TVStatusBuilder extends ConsumerWidget {
  final int tmdbId;
  final LibraryStatus? legacyStatus;
  final Widget Function(LibraryStatus?) builder;
  const _TVStatusBuilder(
      {required this.tmdbId,
      required this.legacyStatus,
      required this.builder});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final scope = ref.watch(tvCardContextProvider);
    if (!scope.supported) return builder(legacyStatus);
    // Covered routes and inactive tabs do not poll. A capable server never
    // falls back to parent totals, even while loading or without a grant.
    if (scope.instanceId == null ||
        !TickerMode.valuesOf(context).enabled ||
        ModalRoute.of(context)?.isCurrent == false) {
      return builder(null);
    }
    final status = ref.watch(tvCardStatusProvider(tmdbId));
    return builder(
        status.hasError ? unknownTVLibraryStatus : status.valueOrNull);
  }
}
