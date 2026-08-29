import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/app_sheet.dart';
import '../data/media_access_service.dart';

/// How the email sheet ended: the invite went out (or an existing share was
/// adopted), or the server said this user already has one. Null means
/// dismissed.
enum MediaServerEmailSheetOutcome { requested, accountExists }

/// Opens the ask-for-invite sheet for an invite server (Plex). The email is
/// the identity the share goes to; the server remembers it so a grant that
/// arrives later knows where to send the invite.
Future<MediaServerEmailSheetOutcome?> showMediaServerEmailSheet(
  BuildContext context, {
  required MediaServerAccess server,
  String initialEmail = '',
}) =>
    showAppSheet<MediaServerEmailSheetOutcome>(
      context,
      builder: (_) => MediaServerEmailSheet(
        server: server,
        initialEmail: initialEmail,
      ),
    );

/// Mirrors the server's shape check: something@something, no whitespace.
bool looksLikeEmail(String email) {
  if (email.isEmpty || email.length > 254 || email.contains(RegExp(r'\s'))) {
    return false;
  }
  final at = email.indexOf('@');
  return at > 0 && at < email.length - 1;
}

/// Collects the Plex email for the user's invite to [server].
class MediaServerEmailSheet extends ConsumerStatefulWidget {
  final MediaServerAccess server;
  final String initialEmail;

  const MediaServerEmailSheet({
    super.key,
    required this.server,
    this.initialEmail = '',
  });

  @override
  ConsumerState<MediaServerEmailSheet> createState() =>
      _MediaServerEmailSheetState();
}

class _MediaServerEmailSheetState extends ConsumerState<MediaServerEmailSheet> {
  late final TextEditingController _controller;
  bool _busy = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    _controller = TextEditingController(text: widget.initialEmail);
  }

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  Future<void> _submit() async {
    if (_busy) return;
    final email = _controller.text.trim();
    if (!looksLikeEmail(email)) {
      setState(() => _error = 'Enter a valid email address.');
      return;
    }
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      await ref
          .read(mediaAccessServiceProvider)
          .requestInvite(widget.server.instanceId, email);
      if (!mounted) return;
      Navigator.of(context).pop(MediaServerEmailSheetOutcome.requested);
    } on MediaAccessException catch (e) {
      if (!mounted) return;
      if (e.code == 'account_exists') {
        Navigator.of(context).pop(MediaServerEmailSheetOutcome.accountExists);
        return;
      }
      setState(() {
        _busy = false;
        _error = _describe(e);
      });
    } catch (_) {
      if (!mounted) return;
      setState(() {
        _busy = false;
        _error = _genericFailure;
      });
    }
  }

  static const _genericFailure =
      "Couldn't send the invite. Try again in a moment, or ask your admin.";

  String _describe(MediaAccessException e) {
    final name = widget.server.name;
    if (e.isTransport) {
      return "Couldn't reach the server. Check your connection and try again.";
    }
    switch (e.code) {
      case 'name_taken':
        return 'That email already has access to $name through another '
            'account. Ask your admin.';
      case 'invalid_email':
        return 'Enter a valid email address.';
    }
    switch (e.status) {
      case 403:
        return 'That server is not available to you.';
      default:
        return _genericFailure;
    }
  }

  @override
  Widget build(BuildContext context) {
    final name = widget.server.name;
    final product = mediaServerTypeLabel(widget.server.serviceType);
    return PopScope(
      canPop: !_busy,
      child: AppSheet(
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
            Text(
              'Your $product email',
              style: const TextStyle(
                color: AppTheme.textPrimary,
                fontSize: 18,
                fontWeight: FontWeight.w600,
              ),
            ),
            const SizedBox(height: 8),
            Text(
              'Enter the email of your $product account. Your invite to $name '
              'is sent there, and you accept it from that email or in '
              '$product.',
              style: const TextStyle(
                color: AppTheme.textSecondary,
                fontSize: 13,
                height: 1.4,
              ),
            ),
            const SizedBox(height: 16),
            TextField(
              controller: _controller,
              enabled: !_busy,
              autofocus: true,
              keyboardType: TextInputType.emailAddress,
              autocorrect: false,
              textInputAction: TextInputAction.done,
              decoration: const InputDecoration(
                labelText: 'Email',
                hintText: 'you@example.com',
                prefixIcon: Icon(Icons.mail_outline),
              ),
              onSubmitted: (_) => _submit(),
            ),
            if (_error != null) ...[
              const SizedBox(height: 8),
              Text(
                _error!,
                style: const TextStyle(color: AppTheme.error, fontSize: 13),
              ),
            ],
            const SizedBox(height: 16),
            SizedBox(
              width: double.infinity,
              child: ElevatedButton(
                onPressed: _busy ? null : _submit,
                child: _busy
                    ? const SizedBox(
                        width: 18,
                        height: 18,
                        child: CircularProgressIndicator(strokeWidth: 2),
                      )
                    : const Text('Send my invite'),
              ),
            ),
          ],
        ),
      ),
    );
  }
}
