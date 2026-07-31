import 'package:flutter/material.dart';
import '../../../core/theme/app_theme.dart';
import '../data/request_service.dart';

/// A smart one-tap request button that adapts its appearance based on status.
///
/// This is the core of the frictionless UX – users see one button that
/// changes from "Request" → "Requested" → "Downloading" → "Available".
class RequestButton extends StatelessWidget {
  final RequestStatus status;
  final bool isRequesting;
  final VoidCallback? onRequest;
  final String? error;

  const RequestButton({
    super.key,
    required this.status,
    this.isRequesting = false,
    this.onRequest,
    this.error,
  });

  @override
  Widget build(BuildContext context) {
    return Column(
      mainAxisSize: MainAxisSize.min,
      children: [
        SizedBox(
          width: double.infinity,
          height: 54,
          child: _buildButton(),
        ),
        if (error != null)
          Padding(
            padding: const EdgeInsets.only(top: 6),
            child: Text(
              error!,
              style: const TextStyle(color: AppTheme.error, fontSize: 12),
            ),
          ),
      ],
    );
  }

  Widget _buildButton() {
    final (color, icon, enabled) = switch (status) {
      RequestStatus.unavailable => (AppTheme.accent, Icons.add, true),
      RequestStatus.pending => (
          AppTheme.requested,
          Icons.hourglass_empty,
          false
        ),
      RequestStatus.denied => (AppTheme.accent, Icons.add, true),
      RequestStatus.requested => (
          AppTheme.requested,
          Icons.hourglass_top,
          false
        ),
      RequestStatus.downloading => (
          AppTheme.downloading,
          Icons.downloading,
          false
        ),
      RequestStatus.available => (
          AppTheme.available,
          Icons.check_circle_rounded,
          false
        ),
      RequestStatus.partial => (AppTheme.accent, Icons.add, true),
    };

    final foreground = color.computeLuminance() > 0.42
        ? AppTheme.background
        : AppTheme.textPrimary;

    return AnimatedContainer(
      duration: AppTheme.motionMedium,
      child: ElevatedButton.icon(
        onPressed: enabled && !isRequesting ? onRequest : null,
        icon: isRequesting
            ? const SizedBox(
                width: 18,
                height: 18,
                child: CircularProgressIndicator(
                  strokeWidth: 2,
                  color: AppTheme.onAccent,
                ),
              )
            : Icon(icon, size: 20),
        label: Text(
          isRequesting ? 'Requesting...' : status.buttonLabel,
          style: const TextStyle(fontSize: 15, fontWeight: FontWeight.w800),
        ),
        style: ElevatedButton.styleFrom(
          backgroundColor: color,
          foregroundColor: foreground,
          disabledBackgroundColor: color.withValues(alpha: 0.13),
          disabledForegroundColor: color,
          side: BorderSide(
            color: color.withValues(alpha: enabled ? 0.52 : 0.3),
          ),
          shape: RoundedRectangleBorder(
            borderRadius: BorderRadius.circular(AppTheme.radiusLarge),
          ),
        ),
      ),
    );
  }
}
