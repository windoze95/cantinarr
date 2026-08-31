import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:url_launcher/url_launcher.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/app_sheet.dart';
import '../data/media_access_service.dart';

/// Opens the Plex sign-in sheet: plex.tv opens in the browser for the person
/// to approve with their own account, the sheet polls until they have, and
/// resolves to what the sign-in led to. Null means dismissed.
Future<PlexSignInState?> showPlexSignInSheet(BuildContext context) =>
    showAppSheet<PlexSignInState>(
      context,
      builder: (_) => const PlexSignInSheet(),
    );

/// The plex.tv PIN flow from the person's side, the same shape the instance
/// editor uses for the admin's account: begin on open, open plex.tv, poll
/// every few seconds, and offer a check-now button for when the approval
/// happened in another app. The token the approval yields never reaches the
/// app: the server reads the account's email with it and signs it out.
class PlexSignInSheet extends ConsumerStatefulWidget {
  const PlexSignInSheet({super.key});

  @override
  ConsumerState<PlexSignInSheet> createState() => _PlexSignInSheetState();
}

class _PlexSignInSheetState extends ConsumerState<PlexSignInSheet> {
  int? _pinId;
  String? _url;
  bool _starting = true;
  bool _checking = false;
  String? _error;
  String? _hint;
  Timer? _timer;

  @override
  void initState() {
    super.initState();
    _begin();
  }

  @override
  void dispose() {
    _timer?.cancel();
    super.dispose();
  }

  Future<void> _begin() async {
    setState(() {
      _starting = true;
      _error = null;
      _hint = null;
      _pinId = null;
      _url = null;
    });
    try {
      final start =
          await ref.read(mediaAccessServiceProvider).beginPlexSignIn();
      if (!mounted) return;
      setState(() {
        _starting = false;
        _pinId = start.pinId;
        _url = start.url;
      });
      // Poll while the person approves in the browser; the sign-in expires
      // server-side, so a forgotten sheet just says so.
      _timer?.cancel();
      _timer = Timer.periodic(
        const Duration(seconds: 3),
        (_) => _check(silent: true),
      );
    } on MediaAccessException catch (e) {
      if (!mounted) return;
      setState(() {
        _starting = false;
        _error = _beginFailure(e);
      });
      return;
    } catch (_) {
      if (!mounted) return;
      setState(() {
        _starting = false;
        _error = "Couldn't reach plex.tv. Try again.";
      });
      return;
    }
    // A browser that will not open is not a failed sign-in: the Reopen
    // button stays, and the poll is already running.
    await _open();
  }

  /// Says which side failed: the server without this route (a build from
  /// before the Plex sign-in), the server unable to reach plex.tv, or no
  /// answer from the server at all.
  static String _beginFailure(MediaAccessException e) {
    if (e.isTransport) {
      return "Couldn't reach the server. Check your connection and try again.";
    }
    switch (e.status) {
      case 404:
        return "This server doesn't support signing in with Plex yet. Ask "
            'your admin to update it.';
      case 502:
        return "Your server couldn't reach plex.tv. Try again in a moment.";
      case 429:
        return 'Too many attempts. Wait a minute and try again.';
      default:
        return "Couldn't reach plex.tv. Try again.";
    }
  }

  Future<void> _open() async {
    final url = _url;
    if (url == null) return;
    try {
      await launchUrl(Uri.parse(url), mode: LaunchMode.externalApplication);
    } catch (_) {}
  }

  Future<void> _check({bool silent = false}) async {
    final pinId = _pinId;
    if (pinId == null || _checking) return;
    _checking = true;
    try {
      final state =
          await ref.read(mediaAccessServiceProvider).checkPlexSignIn(pinId);
      if (!mounted) return;
      if (!state.linked) {
        if (!silent) {
          setState(
              () => _hint = 'Not approved yet. Finish signing in on plex.tv.');
        }
        return;
      }
      _timer?.cancel();
      Navigator.of(context).pop(state);
    } on MediaAccessException catch (e) {
      if (!mounted) return;
      if (e.code == 'pin_expired') {
        _timer?.cancel();
        setState(() {
          _pinId = null;
          _error = 'The sign-in expired. Start again.';
        });
      } else if (!silent) {
        setState(() => _hint = e.isTransport
            ? "Couldn't reach the server. Check your connection."
            : "Couldn't reach plex.tv. Try again.");
      }
    } catch (_) {
      if (!silent && mounted) {
        setState(() => _hint = "Couldn't reach plex.tv. Try again.");
      }
    } finally {
      _checking = false;
    }
  }

  @override
  Widget build(BuildContext context) {
    return AppSheet(
      padding: const EdgeInsets.fromLTRB(
        AppTheme.spaceXl,
        0,
        AppTheme.spaceXl,
        AppTheme.spaceXl,
      ),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const Text(
            'Sign in with Plex',
            style: TextStyle(
              color: AppTheme.textPrimary,
              fontSize: 20,
              fontWeight: FontWeight.bold,
            ),
          ),
          const SizedBox(height: AppTheme.spaceSm),
          const Text(
            'Approve the sign-in on the plex.tv page that just opened, with '
            'the Plex account you watch with. Cantinarr reads the email of '
            'that account, signs itself out again, and keeps no token.',
            style: TextStyle(
              color: AppTheme.textSecondary,
              fontSize: 14,
              height: 1.4,
            ),
          ),
          const SizedBox(height: AppTheme.spaceLg),
          if (_error != null) ...[
            Text(
              _error!,
              style: const TextStyle(
                color: AppTheme.error,
                fontSize: 13,
                height: 1.4,
              ),
            ),
            const SizedBox(height: AppTheme.spaceMd),
            Wrap(
              spacing: 12,
              runSpacing: 8,
              children: [
                OutlinedButton(
                  onPressed: _begin,
                  child: const Text('Start again'),
                ),
                TextButton(
                  onPressed: () => Navigator.of(context).pop(),
                  child: const Text('Cancel'),
                ),
              ],
            ),
          ] else ...[
            Row(
              children: [
                const SizedBox(
                  width: 18,
                  height: 18,
                  child: CircularProgressIndicator(
                      strokeWidth: 2, color: AppTheme.accent),
                ),
                const SizedBox(width: 12),
                Expanded(
                  child: Text(
                    _starting ? 'Opening plex.tv.' : 'Waiting for approval.',
                    style: const TextStyle(
                        color: AppTheme.textSecondary, fontSize: 13),
                  ),
                ),
              ],
            ),
            if (_hint != null) ...[
              const SizedBox(height: 8),
              Text(
                _hint!,
                style: const TextStyle(
                  color: AppTheme.warning,
                  fontSize: 13,
                  height: 1.4,
                ),
              ),
            ],
            const SizedBox(height: AppTheme.spaceMd),
            Wrap(
              spacing: 12,
              runSpacing: 8,
              children: [
                OutlinedButton(
                  onPressed: _pinId == null ? null : () => _check(),
                  child: const Text("I've approved, check now"),
                ),
                if (_url != null)
                  OutlinedButton(
                    onPressed: _open,
                    child: const Text('Reopen plex.tv'),
                  ),
                TextButton(
                  onPressed: () => Navigator.of(context).pop(),
                  child: const Text('Cancel'),
                ),
              ],
            ),
          ],
        ],
      ),
    );
  }
}
