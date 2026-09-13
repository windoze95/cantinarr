import 'dart:convert';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/theme/app_theme.dart';
import '../logic/request_quota_provider.dart';
import 'request_allowance_screen.dart';

/// An advisory price beside the actual selection. Availability/playback and
/// request controls stay usable; submission always checks fresh server state.
class RequestCost extends ConsumerWidget {
  final Map<String, dynamic> selection;
  const RequestCost({super.key, required this.selection});
  @override
  Widget build(BuildContext context, WidgetRef ref) {
    if (!ref.watch(requestQuotasSupportedProvider)) return const SizedBox.shrink();
    final preview = ref.watch(requestQuotaPreviewProvider(jsonEncode(selection)));
    return Padding(padding: const EdgeInsets.symmetric(vertical: 8), child: preview.when(
      loading: () => const Text('Checking allowance…', style: TextStyle(fontSize: 12)),
      error: (e, s) => const Text('Could not preview the allowance. It will be checked when you request.',
          style: TextStyle(fontSize: 12, color: AppTheme.textSecondary)),
      data: (p) {
        if (p.exempt) return const Text('Request allowance: unlimited for admins', style: TextStyle(fontSize: 12));
        final relevant = p.allowances.where((a) => a.mediaType == selection['media_type'] &&
            (a.mediaType != 'book' || selection['book_format'] == null ||
                selection['book_format'] == 'both' || a.bookFormat == selection['book_format']));
        return Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
          for (final a in relevant) Text('${a.label}: ${a.requestedUnits} ${a.requestedUnits == 1 ? 'unit' : 'units'} for this selection · ${a.remaining == null ? 'Unlimited' : '${a.remaining} remaining'}',
              style: TextStyle(fontSize: 12, color: p.fits ? AppTheme.textSecondary : AppTheme.error)),
          if (!p.fits) Text(p.reduceSelection ? 'Reduce the selection to fit the configured limit.' :
              p.earliestFitsAt != null ? 'This selection can fit ${allowanceDate(context, p.earliestFitsAt!)}.' :
              'Reduce the selection or wait for allowance to return.',
              style: const TextStyle(fontSize: 12, color: AppTheme.error)),
        ]);
      },
    ));
  }
}
