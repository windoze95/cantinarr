import 'package:flutter/material.dart';

import '../../../core/theme/app_theme.dart';

/// One write-only credential block against the server's credentials registry:
/// title, a Configured / Built-in key / Not set status chip, per-key delete,
/// and a field that only ever sends a new value — the server never returns
/// stored secrets. Shared by Providers & Credentials (AI keys) and Discover
/// (TMDB, Trakt).
class CredentialSection extends StatelessWidget {
  final String title;
  final String description;
  final bool isConfigured;

  /// True when the integration is running on a built-in public key instead of
  /// an admin credential — working, but nothing stored to delete.
  final bool builtinActive;
  final TextEditingController controller;
  final String hint;
  final VoidCallback onDelete;

  const CredentialSection({
    super.key,
    required this.title,
    required this.description,
    required this.isConfigured,
    this.builtinActive = false,
    required this.controller,
    required this.hint,
    required this.onDelete,
  });

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Row(
          children: [
            Expanded(
              child: Text(
                title,
                style: const TextStyle(
                  color: AppTheme.textPrimary,
                  fontSize: 16,
                  fontWeight: FontWeight.w600,
                ),
              ),
            ),
            Container(
              padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
              decoration: BoxDecoration(
                color: isConfigured || builtinActive
                    ? AppTheme.available.withValues(alpha: 0.15)
                    : AppTheme.unavailable.withValues(alpha: 0.15),
                borderRadius: BorderRadius.circular(4),
              ),
              child: Text(
                isConfigured
                    ? 'Configured'
                    : (builtinActive ? 'Built-in key' : 'Not set'),
                style: TextStyle(
                  color: isConfigured || builtinActive
                      ? AppTheme.available
                      : AppTheme.unavailable,
                  fontSize: 12,
                  fontWeight: FontWeight.w500,
                ),
              ),
            ),
            if (isConfigured) ...[
              const SizedBox(width: 8),
              GestureDetector(
                onTap: onDelete,
                child: const Icon(Icons.close,
                    size: 18, color: AppTheme.textSecondary),
              ),
            ],
          ],
        ),
        const SizedBox(height: 4),
        Text(description,
            style:
                const TextStyle(color: AppTheme.textSecondary, fontSize: 13)),
        const SizedBox(height: 8),
        TextField(
          controller: controller,
          obscureText: true,
          decoration: InputDecoration(
            hintText: isConfigured ? 'Enter new value to replace' : hint,
            isDense: true,
          ),
        ),
      ],
    );
  }
}
