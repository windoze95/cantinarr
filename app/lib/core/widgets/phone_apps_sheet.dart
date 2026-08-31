import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:url_launcher/url_launcher.dart';

import '../theme/app_theme.dart';
import 'app_sheet.dart';

const _iosBetaUrl = 'https://testflight.apple.com/join/bCPDwCsD';
const _androidBetaUrl = 'https://cantinarr.com/#android-beta';

const _promptShownKey = 'phone_apps_prompt_shown';

/// Whether this build advertises the phone apps at all. The iOS/Android store
/// binaries ARE those apps, so they never do; web and desktop builds do.
bool get phoneAppsVisible =>
    kIsWeb ||
    (defaultTargetPlatform != TargetPlatform.iOS &&
        defaultTargetPlatform != TargetPlatform.android);

/// Shows [PhoneAppsSheet] once per device, ever. Called after a successful
/// request submission — the moment the phone app has something concrete to
/// offer (a push notification when that request is ready). The showing itself
/// is what's recorded, so dismissing the sheet any way still counts: the
/// permanent home for these links is the Settings tile, not a repeat prompt.
Future<void> maybeShowPhoneAppsPrompt(BuildContext context) async {
  if (!phoneAppsVisible) return;
  final prefs = await SharedPreferences.getInstance();
  if (prefs.getBool(_promptShownKey) ?? false) return;
  await prefs.setBool(_promptShownKey, true);
  if (!context.mounted) return;
  await showAppSheet(context, builder: (_) => const PhoneAppsSheet());
}

/// Points the user at the iPhone/Android companion apps. Opened one-time after
/// a first request (see [maybeShowPhoneAppsPrompt]) and any time from the
/// Settings "Get the phone app" tile.
class PhoneAppsSheet extends StatelessWidget {
  const PhoneAppsSheet({super.key});

  @override
  Widget build(BuildContext context) {
    return const AppSheet(
      padding: EdgeInsets.fromLTRB(
        AppTheme.spaceXl,
        0,
        AppTheme.spaceXl,
        AppTheme.spaceXl,
      ),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          Icon(Icons.smartphone, color: AppTheme.accent, size: 36),
          SizedBox(height: AppTheme.spaceMd),
          Text(
            'Get the Cantinarr app',
            style: TextStyle(
              color: AppTheme.textPrimary,
              fontSize: 20,
              fontWeight: FontWeight.bold,
            ),
          ),
          SizedBox(height: AppTheme.spaceXs),
          Text(
            'Browse and request from your phone, and get a push notification '
            'the moment something is ready.',
            textAlign: TextAlign.center,
            style: TextStyle(color: AppTheme.textSecondary, fontSize: 14),
          ),
          SizedBox(height: AppTheme.spaceLg),
          _AppLinkTile(
            icon: Icons.phone_iphone,
            title: 'iPhone',
            subtitle: 'Join the beta on TestFlight',
            url: _iosBetaUrl,
          ),
          SizedBox(height: AppTheme.spaceSm),
          _AppLinkTile(
            icon: Icons.android,
            title: 'Android',
            subtitle: 'Join the beta at cantinarr.com',
            url: _androidBetaUrl,
          ),
        ],
      ),
    );
  }
}

class _AppLinkTile extends StatelessWidget {
  final IconData icon;
  final String title;
  final String subtitle;
  final String url;

  const _AppLinkTile({
    required this.icon,
    required this.title,
    required this.subtitle,
    required this.url,
  });

  @override
  Widget build(BuildContext context) {
    return Material(
      color: AppTheme.surface,
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(AppTheme.radiusLg),
        side: const BorderSide(color: AppTheme.border),
      ),
      clipBehavior: Clip.antiAlias,
      child: ListTile(
        leading: Icon(icon, color: AppTheme.accent),
        title: Text(title,
            style: const TextStyle(
                color: AppTheme.textPrimary, fontWeight: FontWeight.w500)),
        subtitle: Text(subtitle,
            style:
                const TextStyle(color: AppTheme.textSecondary, fontSize: 13)),
        trailing: const Icon(Icons.open_in_new,
            size: 18, color: AppTheme.textSecondary),
        onTap: () {
          Navigator.of(context).pop();
          launchUrl(Uri.parse(url), mode: LaunchMode.externalApplication);
        },
      ),
    );
  }
}
