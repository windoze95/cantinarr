import 'package:flutter/material.dart';
import '../../../l10n/l10n.dart';

/// Saved request intent for reviewers; never reads current library coverage.
class RequestIntentSummary extends StatelessWidget {
  final String requestMode;
  final List<int> sourceSeasons;
  const RequestIntentSummary(
      {super.key, required this.requestMode, required this.sourceSeasons});

  @override
  Widget build(BuildContext context) {
    final l = context.l10n;
    final intent = switch (requestMode) {
      'library_copy' => l.availabilityRequestLibraryCopy,
      'missing' => l.availabilityRequestMissingIntent,
      _ => '',
    };
    if (intent.isEmpty && sourceSeasons.isEmpty) return const SizedBox.shrink();
    return Padding(
        padding: const EdgeInsets.only(top: 4),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            if (intent.isNotEmpty)
              Text(intent, style: const TextStyle(fontSize: 12)),
            if (sourceSeasons.isNotEmpty)
              Text(l.availabilitySelectedSeasons(sourceSeasons.join(', ')),
                  style: const TextStyle(fontSize: 12)),
          ],
        ));
  }
}
