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
          height: 48,
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
          Icons.play_circle_fill,
          false
        ),
      RequestStatus.partial => (AppTheme.accent, Icons.add, true),
    };

    return AnimatedContainer(
      duration: const Duration(milliseconds: 300),
      child: ElevatedButton.icon(
        onPressed: enabled && !isRequesting ? onRequest : null,
        icon: isRequesting
            ? const SizedBox(
                width: 18,
                height: 18,
                child: CircularProgressIndicator(
                    strokeWidth: 2, color: Colors.white),
              )
            : Icon(icon, size: 20),
        label: Text(
          isRequesting ? 'Requesting...' : status.buttonLabel,
          style: const TextStyle(fontSize: 16, fontWeight: FontWeight.w600),
        ),
        style: ElevatedButton.styleFrom(
          backgroundColor: enabled ? color : color.withValues(alpha: 0.3),
          foregroundColor: Colors.white,
          disabledBackgroundColor: color.withValues(alpha: 0.3),
          disabledForegroundColor: Colors.white70,
          shape: RoundedRectangleBorder(
            borderRadius: BorderRadius.circular(12),
          ),
        ),
      ),
    );
  }
}
