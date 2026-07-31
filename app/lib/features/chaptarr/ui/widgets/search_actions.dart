import 'package:flutter/material.dart';
import '../../../../core/theme/app_theme.dart';

/// The two ways an admin can look for a book download. At large text sizes the
/// buttons stack so the plain-language labels never collapse or overflow.
class ChaptarrSearchActions extends StatelessWidget {
  final VoidCallback onFindAutomatically;
  final VoidCallback onChooseDownload;

  const ChaptarrSearchActions({
    super.key,
    required this.onFindAutomatically,
    required this.onChooseDownload,
  });

  @override
  Widget build(BuildContext context) {
    return LayoutBuilder(builder: (context, constraints) {
      final scaledBody = MediaQuery.textScalerOf(context).scale(14);
      final stack = constraints.maxWidth < 350 || scaledBody > 20;
      final automatic = _action(
        icon: Icons.search,
        label: 'Find automatically',
        onPressed: onFindAutomatically,
      );
      final choose = _action(
        icon: Icons.manage_search,
        label: 'Choose a download',
        onPressed: onChooseDownload,
      );
      if (stack) {
        return Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [automatic, const SizedBox(height: 8), choose],
        );
      }
      return Row(
        children: [
          Expanded(child: automatic),
          const SizedBox(width: 10),
          Expanded(child: choose),
        ],
      );
    });
  }

  Widget _action({
    required IconData icon,
    required String label,
    required VoidCallback onPressed,
  }) {
    return OutlinedButton.icon(
      onPressed: onPressed,
      icon: Icon(icon, size: 18, color: AppTheme.available),
      label: Text(
        label,
        textAlign: TextAlign.center,
        style: const TextStyle(color: AppTheme.textPrimary),
      ),
      style: OutlinedButton.styleFrom(
        side: const BorderSide(color: AppTheme.border),
        shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(10)),
        minimumSize: const Size(0, 48),
        padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 12),
      ),
    );
  }
}
