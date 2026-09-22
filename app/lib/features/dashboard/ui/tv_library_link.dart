import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../auth/logic/auth_provider.dart';

/// Native library identity stays separate from catalog identity. A calendar
/// episode and a series card both open the complete, regular title page.
class TVLibraryLink extends ConsumerWidget {
  const TVLibraryLink({super.key, required this.instanceId,
    required this.seriesId, required this.builder, this.tmdbId,
    this.seasonNumber});

  final String instanceId;
  final int seriesId;
  final int? tmdbId;
  final int? seasonNumber;
  final Widget Function(VoidCallback? onTap) builder;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final supported = ref.watch(authProvider).valueOrNull
        ?.connection?.tvLibraryNavigation == true;
    final enabled = seriesId > 0 && instanceId.isNotEmpty &&
        (supported || (tmdbId ?? 0) > 0);
    return builder(enabled ? () => context.push(
      '${supported ? '/detail/tv-library/$seriesId' : '/detail/tv/$tmdbId'}'
      '?instance_id=${Uri.encodeQueryComponent(instanceId)}',
    ) : null);
  }
}
