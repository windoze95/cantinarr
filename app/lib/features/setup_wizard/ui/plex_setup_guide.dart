import 'package:flutter/material.dart';
import '../../../core/theme/app_theme.dart';

/// Interactive guide for setting up Plex with Cantinarr.
class PlexSetupGuide extends StatelessWidget {
  const PlexSetupGuide({super.key});

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('Plex Setup')),
      body: ListView(
        padding: const EdgeInsets.all(24),
        children: const [
          _GuideSection(
            number: 1,
            title: 'Plex Media Server',
            steps: [
              'Ensure Plex Media Server is running on your network or VPS',
              'Your media folders should be the same paths Radarr/Sonarr download to',
              'Plex will automatically detect new files once a library scan runs',
            ],
          ),
          SizedBox(height: 24),
          _GuideSection(
            number: 2,
            title: 'Library Setup',
            steps: [
              'Add a "Movies" library pointing to your Radarr root folder',
              'Add a "TV Shows" library pointing to your Sonarr root folder',
              'Enable "Automatically update my library" in Settings > Library',
            ],
          ),
          SizedBox(height: 24),
          _GuideSection(
            number: 3,
            title: 'Remote Access',
            steps: [
              'Go to Settings > Remote Access in Plex',
              'Enable remote access and verify the port is open',
              'If behind a reverse proxy, ensure WebSocket support is enabled',
            ],
          ),
          SizedBox(height: 24),
          _GuideSection(
            number: 4,
            title: 'Connect Your Device',
            steps: [
              'Download the Plex app on your iPhone/iPad/Apple TV',
              'Sign in with your Plex account',
              'Your server should appear automatically',
              'You can also use Infuse for a premium playback experience',
            ],
          ),
          SizedBox(height: 24),
          _TipCard(
            title: 'Pro Tip',
            message:
                'For the best experience, use Infuse on Apple TV. It supports direct play for virtually every format, avoiding any transcoding on your server.',
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
            Text(
              title,
              style: const TextStyle(
                color: AppTheme.textPrimary,
                fontSize: 18,
                fontWeight: FontWeight.w600,
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
