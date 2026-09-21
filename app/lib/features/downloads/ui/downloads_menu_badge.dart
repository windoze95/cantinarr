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
    // The badge is a number or nothing. When the server cannot produce an
    // exact count (a source could not be read), the Content view says so;
    // a placeholder here would only read as a help button.
    final count = value?.complete == true && value?.stale == false ? value?.count : null;
    if (count == null || count == 0) return const SizedBox.shrink();
    return Tooltip(
      message: '$count active downloads',
      child: Container(
        key: const Key('downloads-menu-count'),
        margin: const EdgeInsets.only(right: 3),
        constraints: const BoxConstraints(maxWidth: 30),
        padding: const EdgeInsets.symmetric(horizontal: 4, vertical: 2),
        decoration: BoxDecoration(color: AppTheme.accent.withValues(alpha: 0.16),
            borderRadius: BorderRadius.circular(12)),
        child: FittedBox(fit: BoxFit.scaleDown, child: Text(count > 99 ? '99+' : '$count',
            style: const TextStyle(color: AppTheme.accent, fontSize: 12, fontWeight: FontWeight.w600))),
      ),
    );
  }
}
