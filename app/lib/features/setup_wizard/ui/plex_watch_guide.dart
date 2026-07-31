import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import '../../../core/layout/adaptive.dart';
import '../../../core/storage/preferences.dart';
import '../../../core/theme/app_theme.dart';
import '../../auth/logic/auth_provider.dart';

/// Requester-focused guide: install Plex, request a server invite with your
/// Plex email, and start watching. Hideable via [plexGuideEnabledProvider].
class PlexWatchGuide extends ConsumerStatefulWidget {
  const PlexWatchGuide({super.key});

  @override
  ConsumerState<PlexWatchGuide> createState() => _PlexWatchGuideState();
}

class _PlexWatchGuideState extends ConsumerState<PlexWatchGuide> {
  @override
  void initState() {
    super.initState();
    // The Plex email may have been shared from another device; re-fetch so the
    // invite section reflects it.
    WidgetsBinding.instance.addPostFrameCallback((_) {
      ref.read(authProvider.notifier).refreshUser();
    });
  }

  @override
  Widget build(BuildContext context) {
    final user = ref.watch(authProvider).valueOrNull?.user;
    final plexEmail = user?.plexEmail ?? '';
    final inviteSent = user?.plexInvitedAt != null;

    return Scaffold(
      appBar: AppBar(title: const Text('Watch on Plex')),
      body: CenteredContent(
          child: ListView(
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
            ],
          ),
          const SizedBox(height: 24),
          _buildInviteSection(plexEmail, inviteSent),
          const SizedBox(height: 24),
          const _GuideSection(
            number: 4,
            title: 'Accept your invite',
            steps: [
              'Your server admin sends the invite to that email — open it and tap Accept',
              'You can also find pending invites under the bell icon at app.plex.tv',
              'This is a one-time step: the shared libraries stay linked to your account',
            ],
          ),
          const SizedBox(height: 24),
          const _GuideSection(
            number: 5,
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
      )),
    );
  }

  /// Step 3: the interactive part. Shares the user's Plex email with the
  /// server; depending on the server's setup the invite goes out
  /// automatically or an admin sends it, and the card reflects which state
  /// this user is in (nothing shared / waiting / invite sent).
  Widget _buildInviteSection(String plexEmail, bool inviteSent) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        const _SectionHeader(number: 3, title: 'Request your invite'),
        const SizedBox(height: 12),
        Padding(
          padding: const EdgeInsets.only(left: 44),
          child: Container(
            width: double.infinity,
            padding: const EdgeInsets.all(16),
            decoration: BoxDecoration(
              color: AppTheme.accent.withValues(alpha: 0.08),
              borderRadius: BorderRadius.circular(12),
              border: Border.all(color: AppTheme.accent.withValues(alpha: 0.2)),
            ),
            child: plexEmail.isEmpty
                ? Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      const Text(
                        'Tell your server admin where to send your Plex '
                        'invite. They\'ll get a notification with your email.',
                        style: TextStyle(
                          color: AppTheme.textSecondary,
                          fontSize: 14,
                          height: 1.4,
                        ),
                      ),
                      const SizedBox(height: 12),
                      ElevatedButton.icon(
                        onPressed: () => _showEmailDialog(current: plexEmail),
                        icon: const Icon(Icons.mail_outline, size: 18),
                        label: const Text('Share my Plex email'),
                      ),
                    ],
                  )
                : Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Row(
                        children: [
                          Icon(
                            inviteSent
                                ? Icons.mark_email_read_outlined
                                : Icons.check_circle,
                            color: AppTheme.available,
                            size: 18,
                          ),
                          const SizedBox(width: 8),
                          Expanded(
                            child: Text(
                              plexEmail,
                              style: const TextStyle(
                                color: AppTheme.textPrimary,
                                fontWeight: FontWeight.w500,
                              ),
                            ),
                          ),
                        ],
                      ),
                      const SizedBox(height: 8),
                      Text(
                        inviteSent
                            ? 'Your invite has been sent! Check your inbox '
                                '(and spam) for an email from Plex, then '
                                'continue with step 4.'
                            : 'Your admin has been notified — the invite '
                                'will arrive at this address.',
                        style: const TextStyle(
                          color: AppTheme.textSecondary,
                          fontSize: 13,
                          height: 1.4,
                        ),
                      ),
                      TextButton(
                        onPressed: () => _showEmailDialog(current: plexEmail),
                        style: TextButton.styleFrom(
                          padding: EdgeInsets.zero,
                          minimumSize: const Size(0, 32),
                          tapTargetSize: MaterialTapTargetSize.shrinkWrap,
                        ),
                        child: const Text('Change email'),
                      ),
                    ],
                  ),
          ),
        ),
      ],
    );
  }

  void _showEmailDialog({required String current}) {
    final controller = TextEditingController(text: current);
    String? errorText;
    bool saving = false;

    // Mirrors the server's shape check: something@something, no whitespace.
    bool looksLikeEmail(String email) {
      if (email.isEmpty ||
          email.length > 254 ||
          email.contains(RegExp(r'\s'))) {
        return false;
      }
      final at = email.indexOf('@');
      return at > 0 && at < email.length - 1;
    }

    showDialog(
      context: context,
      builder: (dialogContext) => StatefulBuilder(
        builder: (context, setDialogState) {
          Future<void> submit() async {
            final email = controller.text.trim();
            if (!looksLikeEmail(email)) {
              setDialogState(() => errorText = 'Enter a valid email address');
              return;
            }
            setDialogState(() {
              saving = true;
              errorText = null;
            });
            try {
              await ref.read(authProvider.notifier).setPlexEmail(email);
              if (dialogContext.mounted) {
                Navigator.of(dialogContext).pop();
              }
              if (mounted) {
                ScaffoldMessenger.of(this.context).showSnackBar(const SnackBar(
                  content: Text('Thanks! Your admin has been notified.'),
                ));
                // If the server auto-invites, the invite lands within a few
                // seconds — re-fetch so the card flips to "check your inbox".
                Future.delayed(const Duration(seconds: 3), () {
                  if (mounted) {
                    ref.read(authProvider.notifier).refreshUser();
                  }
                });
              }
            } catch (_) {
              setDialogState(() {
                saving = false;
                errorText = 'Couldn\'t send — check your connection';
              });
            }
          }

          return AlertDialog(
            title: const Text('Your Plex email'),
            content: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                const Text(
                  'Enter the email of your Plex account. Your server admin '
                  'sends the invite there.',
                  style: TextStyle(color: AppTheme.textSecondary, fontSize: 13),
                ),
                const SizedBox(height: 12),
                TextField(
                  controller: controller,
                  enabled: !saving,
                  autofocus: true,
                  keyboardType: TextInputType.emailAddress,
                  autocorrect: false,
                  textInputAction: TextInputAction.done,
                  decoration: InputDecoration(
                    labelText: 'Email',
                    hintText: 'you@example.com',
                    prefixIcon: const Icon(Icons.mail_outline),
                    errorText: errorText,
                  ),
                  onSubmitted: (_) => submit(),
                ),
              ],
            ),
            actions: [
              TextButton(
                onPressed:
                    saving ? null : () => Navigator.of(dialogContext).pop(),
                child: const Text('Cancel'),
              ),
              ElevatedButton(
                onPressed: saving ? null : submit,
                child: saving
                    ? const SizedBox(
                        width: 18,
                        height: 18,
                        child: CircularProgressIndicator(strokeWidth: 2),
                      )
                    : const Text('Send'),
              ),
            ],
          );
        },
      ),
    );
  }
}

/// Numbered section header: the accent number bubble plus the section title.
class _SectionHeader extends StatelessWidget {
  final int number;
  final String title;

  const _SectionHeader({required this.number, required this.title});

  @override
  Widget build(BuildContext context) {
    return Row(
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
        _SectionHeader(number: number, title: title),
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
          const Icon(Icons.lightbulb_outline, color: AppTheme.accent, size: 20),
          const SizedBox(width: 12),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(title,
                    style: const TextStyle(
                        color: AppTheme.accent, fontWeight: FontWeight.w600)),
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
