import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/theme/app_theme.dart';
import '../logic/downloads_activity_provider.dart';

class DownloadsMenuBadge extends ConsumerWidget {
  const DownloadsMenuBadge({super.key});
  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final summary = ref.watch(downloadsSummaryProvider);
    // A dependency reload carries the prior value, so a normal poll does not
    // make the badge flash unavailable. A first load or authorization reset
    // has no prior value and stays hidden until the server answers.
    if (summary.isLoading && !summary.hasValue && !summary.hasError) {
      return const SizedBox.shrink();
    }
    final value = summary.hasError ? null : summary.valueOrNull;
    if (!summary.hasError && value == null) return const SizedBox.shrink();
    final count = value?.complete == true && value?.stale == false ? value?.count : null;
    if (count == 0) return const SizedBox.shrink();
    return Tooltip(
      message: count == null ? 'Download count unavailable' : '$count active download jobs',
      child: Container(
        key: const Key('downloads-menu-count'),
        margin: const EdgeInsets.only(right: 3),
        constraints: const BoxConstraints(maxWidth: 30),
        padding: const EdgeInsets.symmetric(horizontal: 4, vertical: 2),
        decoration: BoxDecoration(color: AppTheme.accent.withValues(alpha: 0.16),
            borderRadius: BorderRadius.circular(12)),
        child: FittedBox(fit: BoxFit.scaleDown, child: Text(
            count == null ? '?' : count > 99 ? '99+' : '$count',
            style: const TextStyle(color: AppTheme.accent, fontSize: 12, fontWeight: FontWeight.w600))),
      ),
    );
  }
}
