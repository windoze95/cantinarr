import 'package:flutter/material.dart';
import '../theme/app_theme.dart';

/// An animated search bar with focus handling, clear button,
/// and optional AI mode (sparkle hint, multiline input, glow effect).
class CantinarrSearchBar extends StatelessWidget {
  final TextEditingController controller;
  final FocusNode focusNode;
  final String hintText;
  final ValueChanged<String>? onChanged;
  final VoidCallback? onClear;

  /// When true, shows a sparkle icon hinting at AI capability.
  final bool aiEnabled;

  /// When true, the text field expands to 3 lines with a send button.
  final bool multiline;

  /// Called when the send button is tapped (AI multiline mode).
  final VoidCallback? onSend;

  /// Override max lines (defaults to 3 when multiline, 1 otherwise).
  final int? maxLines;

  /// Glow intensity (0.0–1.0) for the gold accent border/shadow.
  /// Driven by the parent's animation controller.
  final double glowIntensity;

  const CantinarrSearchBar({
    super.key,
    required this.controller,
    required this.focusNode,
    this.hintText = 'Search movies & TV shows...',
    this.onChanged,
    this.onClear,
    this.aiEnabled = false,
    this.multiline = false,
    this.onSend,
    this.maxLines,
    this.glowIntensity = 0.0,
  });

  @override
  Widget build(BuildContext context) {
    final hasGlow = glowIntensity > 0.0;
    final borderColor = hasGlow
        ? Color.lerp(AppTheme.border, AppTheme.accent, glowIntensity)!
        : AppTheme.border;

    return Container(
      decoration: BoxDecoration(
        color: AppTheme.surfaceVariant,
        borderRadius: BorderRadius.circular(14),
        border: Border.all(color: borderColor, width: hasGlow ? 1.5 : 1),
        boxShadow: hasGlow
            ? [
                BoxShadow(
                  color: AppTheme.accent.withValues(alpha: glowIntensity * 0.2),
                  blurRadius: 20,
                  spreadRadius: 1,
                ),
              ]
            : null,
      ),
      child: TextField(
        controller: controller,
        focusNode: focusNode,
        keyboardType: TextInputType.text,
        onChanged: onChanged,
        onSubmitted: multiline ? null : null,
        textInputAction:
            multiline ? TextInputAction.newline : TextInputAction.search,
        maxLines: maxLines ?? (multiline ? 3 : 1),
        minLines: multiline ? 2 : 1,
        style: const TextStyle(color: AppTheme.textPrimary, fontSize: 16),
        decoration: InputDecoration(
          hintText: hintText,
          hintStyle: const TextStyle(color: AppTheme.textSecondary),
          prefixIcon: _buildPrefixIcon(),
          suffixIcon: _buildSuffixIcon(),
          filled: false,
          border: InputBorder.none,
          contentPadding:
              const EdgeInsets.symmetric(horizontal: 16, vertical: 14),
        ),
      ),
    );
  }

  Widget _buildPrefixIcon() {
    if (aiEnabled) {
      return Icon(
        Icons.auto_awesome,
        size: 20,
        color: AppTheme.accent.withValues(alpha: 0.7),
      );
    }
    return const Icon(Icons.search, color: AppTheme.textSecondary);
  }

  Widget? _buildSuffixIcon() {
    final hasSend = onSend != null;

    if (hasSend && controller.text.isNotEmpty) {
      return Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          IconButton(
            icon: const Icon(Icons.close, size: 20, color: AppTheme.textSecondary),
            onPressed: () {
              controller.clear();
              onClear?.call();
            },
          ),
          IconButton(
            icon: const Icon(Icons.send_rounded, color: AppTheme.accent),
            onPressed: onSend,
          ),
        ],
      );
    }

    if (controller.text.isNotEmpty) {
      return IconButton(
        icon: const Icon(Icons.close, color: AppTheme.textSecondary),
        onPressed: () {
          controller.clear();
          onClear?.call();
        },
      );
    }

    if (hasSend) {
      return IconButton(
        icon: Icon(Icons.send_rounded,
            color: AppTheme.textSecondary.withValues(alpha: 0.5)),
        onPressed: null,
      );
    }

    return null;
  }
}
