import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/app_sheet.dart';
import '../../auth/logic/auth_provider.dart';
import '../logic/app_version_provider.dart';

class AboutSheet extends ConsumerWidget {
  const AboutSheet({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final serverVersion =
        ref.watch(authProvider).valueOrNull?.connection?.serverVersion;
    final appVersion = ref.watch(appVersionProvider).valueOrNull;
    return AppSheet(
      padding: const EdgeInsets.fromLTRB(
        AppTheme.spaceXl,
        0,
        AppTheme.spaceXl,
        AppTheme.spaceXl,
      ),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          // Greedo image. Capped in height as well as width so the version
          // lines below it stay on screen on a short or landscape viewport.
          ConstrainedBox(
            constraints: const BoxConstraints(maxHeight: 250),
            child: ClipRRect(
              borderRadius: BorderRadius.circular(AppTheme.radiusLg),
              child: Image.asset(
                'assets/greedo.png',
                width: 200,
                fit: BoxFit.contain,
              ),
            ),
          ),
          const SizedBox(height: AppTheme.spaceMd),

          // GREEDO <3
          const Text(
            'GREEDO <3',
            style: TextStyle(
              color: AppTheme.accent,
              fontSize: 13,
              fontWeight: FontWeight.bold,
              letterSpacing: 1.2,
            ),
          ),
          const SizedBox(height: AppTheme.spaceXl),

          // App name and version
          const Text(
            'Cantinarr',
            style: TextStyle(
              color: AppTheme.textPrimary,
              fontSize: 20,
              fontWeight: FontWeight.bold,
            ),
          ),
          const SizedBox(height: AppTheme.spaceXs),
          Text(
            appVersion?.label ?? '',
            style: const TextStyle(
              color: AppTheme.textSecondary,
              fontSize: 14,
            ),
          ),
          if (serverVersion != null && serverVersion.isNotEmpty) ...[
            const SizedBox(height: 2),
            Text(
              'Server $serverVersion',
              style: const TextStyle(
                color: AppTheme.textSecondary,
                fontSize: 12,
              ),
            ),
          ],
          const SizedBox(height: AppTheme.spaceLg),
          // Required by TMDB's API terms of use.
          const Text(
            'This product uses the TMDB API but is not endorsed '
            'or certified by TMDB.',
            textAlign: TextAlign.center,
            style: TextStyle(
              color: AppTheme.textSecondary,
              fontSize: 11,
            ),
          ),
        ],
      ),
    );
  }
}
