import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import '../../../core/storage/preferences.dart';
import '../../../core/theme/app_theme.dart';

/// Requester-focused guide: install Plex, connect to the shared server,
/// and start watching. Hideable via [plexGuideEnabledProvider].
class PlexWatchGuide extends ConsumerWidget {
  const PlexWatchGuide({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return Scaffold(
      appBar: AppBar(title: const Text('Watch on Plex')),
      body: ListView(
        padding: const EdgeInsets.all(24),
        children: [
          const Text(
            'Cantinarr is where you request movies and shows — Plex is where '
            'you watch them. Set up Plex once and everything you request '
            'appears there automatically.',
            style: TextStyle(
              color: AppTheme.textSecondary,
              fontSize: 14,
              height: 1.5,
            ),
          ),
          const SizedBox(height: 24),
          const _GuideSection(
            number: 1,
            title: 'Install the Plex app',
            steps: [
              'Download the free Plex app from the App Store or Google Play',
              'Plex is also on Apple TV, Roku, Fire TV, and most smart TVs — install it wherever you like to watch',
              'On a computer there\'s nothing to install — just go to app.plex.tv in your browser',
            ],
          ),
          const SizedBox(height: 24),
          const _GuideSection(
            number: 2,
            title: 'Sign in to Plex',
            steps: [
              'Open the app and create a free Plex account, or sign in if you already have one',
              'The free account is all you need — no Plex Pass subscription required',
              'Make sure your server admin knows the email you signed up with, so they can share the library with you',
            ],
          ),
          const SizedBox(height: 24),
          const _GuideSection(
            number: 3,
            title: 'Accept your invite',
            steps: [
              'Your server admin will send an invite to your email — open it and tap Accept',
              'You can also find pending invites under the bell icon at app.plex.tv',
              'This is a one-time step: the shared libraries stay linked to your account',
            ],
          ),
          const SizedBox(height: 24),
          const _GuideSection(
            number: 4,
            title: 'Start watching',
            steps: [
              'Open Plex on any device you\'re signed in to — the shared movie and TV libraries appear automatically',
              'Anything you request in Cantinarr shows up in Plex shortly after it becomes Available',
              'Missing something? Sign out and back in, or check with your server admin',
            ],
          ),
          const SizedBox(height: 24),
          const _TipCard(
            title: 'Request here, watch there',
            message:
                'When a request in Cantinarr shows as Available, it\'s ready to play in Plex — no downloads or extra setup on your end.',
          ),
          const SizedBox(height: 24),
          Center(
            child: TextButton.icon(
              onPressed: () {
                final messenger = ScaffoldMessenger.of(context);
                ref.read(plexGuideEnabledProvider.notifier).set(false);
                context.pop();
                messenger.showSnackBar(const SnackBar(
                  content: Text('Guide hidden — turn it back on in Settings.'),
                ));
              },
              icon: const Icon(Icons.visibility_off_outlined, size: 18),
              label: const Text('All set — hide this guide'),
              style: TextButton.styleFrom(
                foregroundColor: AppTheme.textSecondary,
              ),
            ),
          ),
        ],
      ),
    );
  }
}

class _GuideSection extends StatelessWidget {
  final int number;
  final String title;
  final List<String> steps;

  const _GuideSection({
    required this.number,
    required this.title,
    required this.steps,
  });

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Row(
          children: [
            Container(
              width: 32,
              height: 32,
              decoration: BoxDecoration(
                color: AppTheme.accent.withValues(alpha: 0.15),
                shape: BoxShape.circle,
              ),
              child: Center(
                child: Text(
                  '$number',
                  style: const TextStyle(
                    color: AppTheme.accent,
                    fontWeight: FontWeight.bold,
                  ),
                ),
              ),
            ),
            const SizedBox(width: 12),
            Expanded(
              child: Text(
                title,
                style: const TextStyle(
                  color: AppTheme.textPrimary,
                  fontSize: 18,
                  fontWeight: FontWeight.w600,
                ),
              ),
            ),
          ],
        ),
        const SizedBox(height: 12),
        ...steps.map((step) => Padding(
              padding: const EdgeInsets.only(left: 44, bottom: 8),
              child: Row(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  const Text('• ',
                      style: TextStyle(color: AppTheme.textSecondary)),
                  Expanded(
                    child: Text(
                      step,
                      style: const TextStyle(
                        color: AppTheme.textSecondary,
                        fontSize: 14,
                        height: 1.4,
                      ),
                    ),
                  ),
                ],
              ),
            )),
      ],
    );
  }
}

class _TipCard extends StatelessWidget {
  final String title;
  final String message;

  const _TipCard({required this.title, required this.message});

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(
        color: AppTheme.accent.withValues(alpha: 0.08),
        borderRadius: BorderRadius.circular(12),
        border: Border.all(color: AppTheme.accent.withValues(alpha: 0.2)),
      ),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const Icon(Icons.lightbulb_outline,
              color: AppTheme.accent, size: 20),
          const SizedBox(width: 12),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(title,
                    style: const TextStyle(
                        color: AppTheme.accent,
                        fontWeight: FontWeight.w600)),
                const SizedBox(height: 4),
                Text(message,
                    style: const TextStyle(
                        color: AppTheme.textSecondary,
                        fontSize: 13,
                        height: 1.4)),
              ],
            ),
          ),
        ],
      ),
    );
  }
}
