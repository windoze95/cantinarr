import 'package:cantinarr/l10n/l10n.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../auth/logic/auth_provider.dart';

/// Opens the existing setup form without replacing the current catalog route.
class CatalogSetupButton extends ConsumerWidget {
  final String serviceType;
  final String? label;
  const CatalogSetupButton({super.key, required this.serviceType, this.label});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    if (!(ref.watch(authProvider).valueOrNull?.user?.isAdmin ?? false)) {
      return const SizedBox.shrink();
    }
    final buttonLabel = label ??
        switch (serviceType) {
          'radarr' => context.l10n.browseConnectRadarrToRequestMovies,
          'sonarr' => context.l10n.browseConnectSonarrToRequestTVShows,
          'chaptarr' => context.l10n.browseConnectChaptarrToRequestBooks,
          'lidarr' => context.l10n.browseConnectLidarrToRequestMusic,
          _ => throw ArgumentError.value(serviceType),
        };
    return OutlinedButton.icon(
      icon: const Icon(Icons.add_link),
      label: Text(buttonLabel, textAlign: TextAlign.center),
      onPressed: () async {
        await context.push<bool>('/settings/instance/new', extra: {
          'service_type': serviceType,
          'refresh_config_after_return': true,
        });
        // The editor refreshes after every close, including cancellation.
      },
    );
  }
}
