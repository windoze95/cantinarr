import 'package:flutter/material.dart';

import '../../../core/theme/app_theme.dart';

/// Non-blocking warning shown when an issue screen still has a usable cached
/// snapshot but its latest refresh failed.
///
/// Keeping the old data is preferable to blanking the screen, but it must never
/// look authoritative: the fixed warning and explicit retry make the stale
/// state visible without rendering any server-supplied text as a control.
class IssueRefreshBanner extends StatelessWidget {
  final String message;
  final VoidCallback onRetry;

  const IssueRefreshBanner({
    super.key,
    required this.message,
    required this.onRetry,
  });

  @override
  Widget build(BuildContext context) {
    return Container(
      width: double.infinity,
      padding: const EdgeInsets.fromLTRB(12, 8, 8, 8),
      decoration: BoxDecoration(
        color: AppTheme.requested.withValues(alpha: 0.12),
        borderRadius: BorderRadius.circular(8),
        border: Border.all(
          color: AppTheme.requested.withValues(alpha: 0.45),
        ),
      ),
      child: Row(
        children: [
          const Icon(
            Icons.sync_problem_outlined,
            size: 18,
            color: AppTheme.requested,
          ),
          const SizedBox(width: 8),
          Expanded(
            child: Text(
              message,
              style: const TextStyle(
                color: AppTheme.textSecondary,
                fontSize: 12,
                height: 1.3,
              ),
            ),
          ),
          TextButton(
            onPressed: onRetry,
            style: TextButton.styleFrom(
              foregroundColor: AppTheme.requested,
              padding: const EdgeInsets.symmetric(horizontal: 8),
              minimumSize: const Size(0, 36),
              tapTargetSize: MaterialTapTargetSize.shrinkWrap,
            ),
            child: const Text('Retry'),
          ),
        ],
      ),
    );
  }
}
