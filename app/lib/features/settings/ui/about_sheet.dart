import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:package_info_plus/package_info_plus.dart';
import '../../../core/theme/app_theme.dart';
import '../../auth/logic/auth_provider.dart';

class AboutSheet extends ConsumerWidget {
  const AboutSheet({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final serverVersion =
        ref.watch(authProvider).valueOrNull?.connection?.serverVersion;
    return Container(
      padding: const EdgeInsets.all(24),
      decoration: const BoxDecoration(
        color: AppTheme.surface,
        borderRadius: BorderRadius.vertical(top: Radius.circular(20)),
      ),
      child: FutureBuilder<PackageInfo>(
        future: PackageInfo.fromPlatform(),
        builder: (context, snapshot) {
          final info = snapshot.data;
          final version = info?.version ?? '';
          final build = info?.buildNumber ?? '';
          return Column(
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

              // Greedo image
              ClipRRect(
                borderRadius: BorderRadius.circular(16),
                child: Image.asset(
                  'assets/greedo.png',
                  width: 200,
                  fit: BoxFit.contain,
                ),
              ),
              const SizedBox(height: 12),

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
              const SizedBox(height: 20),

              // App name and version
              const Text(
                'Cantinarr',
                style: TextStyle(
                  color: AppTheme.textPrimary,
                  fontSize: 20,
                  fontWeight: FontWeight.bold,
                ),
              ),
              const SizedBox(height: 4),
              Text(
                build.isNotEmpty
                    ? 'Version $version ($build)'
                    : 'Version $version',
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

              const SizedBox(height: 24),
            ],
          );
        },
      ),
    );
  }
}
