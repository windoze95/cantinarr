import 'package:flutter/material.dart';
import '../theme/app_theme.dart';

/// An animated search bar with focus handling, clear button,
/// and optional AI mode (sparkle hint, multiline input, glow effect).
class CantinarrSearchBar extends StatelessWidget {
  final TextEditingController controller;
  final FocusNode focusNode;
  final String hintText;
  final ValueChanged<String>? onChanged;
  final VoidCallback? onSubmitted;
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
    this.onSubmitted,
    this.onClear,
    this.aiEnabled = false,
    this.multiline = false,
    this.onSend,
    this.maxLines,
    this.glowIntensity = 0.0,
  });

  @override
  Widget build(BuildContext context) {
    final submitAction = multiline ? onSend : onSubmitted;

    return ListenableBuilder(
      listenable: Listenable.merge([controller, focusNode]),
      builder: (context, _) {
        final focused = focusNode.hasFocus;
        final glow = focused ? 1.0 : glowIntensity;
        final signalColor = aiEnabled ? AppTheme.signal : AppTheme.accent;
        final borderColor = Color.lerp(
          AppTheme.border,
          signalColor,
          glow.clamp(0, 1),
        )!;

        return AnimatedContainer(
          duration: MediaQuery.disableAnimationsOf(context)
              ? Duration.zero
              : AppTheme.motionMedium,
          curve: Curves.easeOutCubic,
          decoration: BoxDecoration(
            gradient: LinearGradient(
              begin: Alignment.topLeft,
              end: Alignment.bottomRight,
              colors: [
                focused ? AppTheme.surfaceRaised : AppTheme.surfaceVariant,
                AppTheme.surface,
              ],
            ),
            borderRadius: BorderRadius.circular(AppTheme.radiusLarge),
            border: Border.all(
              color: borderColor,
              width: focused || glowIntensity > 0 ? 1.35 : 1,
            ),
            boxShadow: [
              BoxShadow(
                color: Colors.black.withValues(alpha: focused ? 0.34 : 0.22),
                blurRadius: focused ? 24 : 14,
                offset: const Offset(0, 8),
              ),
              if (glow > 0)
                BoxShadow(
                  color: signalColor.withValues(alpha: glow * 0.12),
                  blurRadius: 28,
                  spreadRadius: 1,
                ),
            ],
          ),
          child: Semantics(
            identifier: 'global-search',
            child: TextField(
              controller: controller,
              focusNode: focusNode,
              keyboardType: TextInputType.text,
              onChanged: onChanged,
              onSubmitted: submitAction == null ? null : (_) => submitAction(),
              onTapOutside: (_) => focusNode.unfocus(),
              textInputAction: multiline
                  ? (onSend == null
                      ? TextInputAction.newline
                      : TextInputAction.send)
                  : TextInputAction.search,
              maxLines: maxLines ?? (multiline ? 3 : 1),
              minLines: multiline ? 2 : 1,
              style: Theme.of(context).textTheme.bodyLarge?.copyWith(
                    color: AppTheme.textPrimary,
                    fontWeight: FontWeight.w500,
                  ),
              decoration: InputDecoration(
                hintText: hintText,
                hintStyle: Theme.of(context).textTheme.bodyLarge?.copyWith(
                      color: AppTheme.textMuted,
                      fontWeight: FontWeight.w400,
                    ),
                prefixIcon: _buildPrefixIcon(focused),
                suffixIcon: _buildSuffixIcon(),
                filled: false,
                border: InputBorder.none,
                enabledBorder: InputBorder.none,
                focusedBorder: InputBorder.none,
                contentPadding: EdgeInsets.symmetric(
                  horizontal: 16,
                  vertical: multiline ? 15 : 16,
                ),
              ),
            ),
          ),
        );
      },
    );
  }

  Widget _buildPrefixIcon(bool focused) {
    final color = aiEnabled
        ? AppTheme.signal
        : (focused ? AppTheme.accent : AppTheme.textSecondary);
    return Padding(
      padding: const EdgeInsets.all(10),
      child: AnimatedContainer(
        duration: AppTheme.motionFast,
        width: 34,
        height: 34,
        decoration: BoxDecoration(
          color: color.withValues(alpha: focused ? 0.16 : 0.09),
          borderRadius: BorderRadius.circular(AppTheme.radiusMedium),
          border: Border.all(color: color.withValues(alpha: 0.18)),
        ),
        child: Icon(
          aiEnabled ? Icons.auto_awesome_rounded : Icons.search_rounded,
          size: 18,
          color: color,
        ),
      ),
    );
  }

  Widget? _buildSuffixIcon() {
    final hasSend = onSend != null;

    if (hasSend && controller.text.isNotEmpty) {
      return Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          IconButton(
            tooltip: 'Clear message',
            icon: const Icon(Icons.close,
                size: 20, color: AppTheme.textSecondary),
            onPressed: () {
              controller.clear();
              onClear?.call();
            },
          ),
          IconButton(
            tooltip: 'Send',
            icon: Icon(
              Icons.send_rounded,
              color: aiEnabled ? AppTheme.signal : AppTheme.accent,
            ),
            onPressed: onSend,
          ),
        ],
      );
    }

    if (controller.text.isNotEmpty) {
      return IconButton(
        tooltip: 'Clear search',
        icon: const Icon(Icons.close_rounded, color: AppTheme.textSecondary),
        onPressed: () {
          controller.clear();
          onClear?.call();
        },
      );
    }

    if (hasSend) {
      return IconButton(
        tooltip: 'Send',
        icon: Icon(Icons.send_rounded,
            color: AppTheme.textSecondary.withValues(alpha: 0.5)),
        onPressed: null,
      );
    }

    return null;
  }
}
