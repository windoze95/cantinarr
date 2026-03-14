import 'package:flutter/material.dart';
import '../../../core/theme/app_theme.dart';
import '../data/request_service.dart';

/// A bottom sheet showing detailed request status in human-friendly language.
class RequestStatusSheet extends StatelessWidget {
  final String title;
  final RequestStatus status;
  final String? additionalInfo;

  const RequestStatusSheet({
    super.key,
    required this.title,
    required this.status,
    this.additionalInfo,
  });

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(24),
      decoration: const BoxDecoration(
        color: AppTheme.surface,
        borderRadius: BorderRadius.vertical(top: Radius.circular(20)),
      ),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          // Handle
          Container(
            width: 40,
            height: 4,
            decoration: BoxDecoration(
              color: AppTheme.textSecondary,
              borderRadius: BorderRadius.circular(2),
            ),
          ),
          const SizedBox(height: 24),

          // Status icon
          _StatusIcon(status: status),
          const SizedBox(height: 16),

          // Title
          Text(
            title,
            style: const TextStyle(
              color: AppTheme.textPrimary,
              fontSize: 20,
              fontWeight: FontWeight.bold,
            ),
            textAlign: TextAlign.center,
          ),
          const SizedBox(height: 8),

          // Status message
          Text(
            _statusMessage,
            style: const TextStyle(
                color: AppTheme.textSecondary, fontSize: 15),
            textAlign: TextAlign.center,
          ),

          if (additionalInfo != null) ...[
            const SizedBox(height: 12),
            Text(
              additionalInfo!,
              style: const TextStyle(
                  color: AppTheme.textSecondary, fontSize: 13),
              textAlign: TextAlign.center,
            ),
          ],

          const SizedBox(height: 24),
        ],
      ),
    );
  }

  String get _statusMessage => switch (status) {
        RequestStatus.unavailable =>
          'This title is not yet on the server. Tap Request to add it!',
        RequestStatus.requested =>
          'Your request has been received! The server is searching for this title and will start downloading it soon.',
        RequestStatus.downloading =>
          'Great news! This title is currently downloading and should be available shortly.',
        RequestStatus.available =>
          'This title is ready to watch! Open Plex or Infuse to start streaming.',
        RequestStatus.partial =>
          'Some episodes are available. You can request the remaining ones.',
      };
}

class _StatusIcon extends StatelessWidget {
  final RequestStatus status;
  const _StatusIcon({required this.status});

  @override
  Widget build(BuildContext context) {
    final (icon, color) = switch (status) {
      RequestStatus.unavailable => (Icons.add_circle_outline, AppTheme.accent),
      RequestStatus.requested => (Icons.hourglass_top, AppTheme.requested),
      RequestStatus.downloading => (Icons.downloading, AppTheme.downloading),
      RequestStatus.available => (Icons.check_circle, AppTheme.available),
      RequestStatus.partial => (
          Icons.pie_chart,
          AppTheme.requested
        ),
    };

    return Container(
      width: 64,
      height: 64,
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.15),
        shape: BoxShape.circle,
      ),
      child: Icon(icon, color: color, size: 32),
    );
  }
}
