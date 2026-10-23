import 'package:cantinarr/l10n/l10n.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/app_sheet.dart';
import '../data/media_access_service.dart';

/// How the sign-in sheet ended: the account is linked, or the server said
/// one already was (the guide refreshes either way). Null means dismissed.
enum MediaServerSignInSheetOutcome { linked, accountExists }

/// Opens the link-my-existing-account sheet for [server]. The password lives
/// in the sheet's controller and travels once, in the link request, where
/// the server checks it with the media server and keeps nothing.
Future<MediaServerSignInSheetOutcome?> showMediaServerSignInSheet(
  BuildContext context, {
  required MediaServerAccess server,
  required String username,
}) =>
    showAppSheet<MediaServerSignInSheetOutcome>(
      context,
      builder: (_) => MediaServerSignInSheet(
        server: server,
        username: username,
      ),
    );

/// Asks for the username and password of an account the person already has
/// on a media server, so Cantinarr can prove it is theirs and link it. The
/// username is prefilled with the Cantinarr one, which is the usual case.
class MediaServerSignInSheet extends ConsumerStatefulWidget {
  final MediaServerAccess server;
  final String username;

  const MediaServerSignInSheet({
    super.key,
    required this.server,
    required this.username,
  });

  @override
  ConsumerState<MediaServerSignInSheet> createState() =>
      _MediaServerSignInSheetState();
}

class _MediaServerSignInSheetState
    extends ConsumerState<MediaServerSignInSheet> {
  late final _usernameController = TextEditingController(text: widget.username);
  final _passwordController = TextEditingController();
  bool _obscure = true;
  bool _busy = false;
  String? _error;

  @override
  void dispose() {
    _usernameController.dispose();
    _passwordController.dispose();
    super.dispose();
  }

  Future<void> _submit() async {
    if (_busy) return;
    final username = _usernameController.text.trim();
    final password = _passwordController.text;
    if (username.isEmpty) {
      setState(() => _error = context.l10n.mediaEnterYourUsername);
      return;
    }
    if (password.isEmpty) {
      setState(() => _error = context.l10n.mediaEnterYourPassword);
      return;
    }
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      await ref.read(mediaAccessServiceProvider).linkOwnAccount(
            widget.server.instanceId,
            username: username,
            password: password,
          );
      if (!mounted) return;
      Navigator.of(context).pop(MediaServerSignInSheetOutcome.linked);
    } on MediaAccessException catch (e) {
      if (!mounted) return;
      if (e.code == 'account_exists') {
        // Not an error to sit on: the guide re-reads and shows the account.
        Navigator.of(context).pop(MediaServerSignInSheetOutcome.accountExists);
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

  String get _genericFailure =>
      context.l10n.mediaCouldnTLinkTheAccountTryAgainInAMomentOrAskYourAdmin;

  /// Requester-voice copy per refusal: what happened and what to do next.
  String _describe(MediaAccessException e) {
    final name = widget.server.name;
    if (e.isTransport) {
      return context
          .l10n.mediaCouldnTReachTheServerCheckYourConnectionAndTryAgain;
    }
    switch (e.code) {
      case 'bad_credentials':
        return context.l10n
            .mediaWrongUsernameOrPasswordForValue0((name).toString());
      case 'account_refused':
        return context.l10n
            .mediaValue0WonTLetThatAccountSignInRightNowItMayBeTurnedOffAskYourAdmin(
                (name).toString());
      case 'remote_already_linked':
        return context.l10n
            .mediaThatAccountIsAlreadyLinkedToAnotherCantinarrUserAskYourAdmin;
    }
    switch (e.status) {
      case 403:
        return context.l10n.mediaThatServerIsNotAvailableToYou;
      case 404:
        // A server from before this route exists; nothing else answers 404.
        return context.l10n
            .mediaThisServerDoesnTSupportLinkingAnExistingAccountYetAskYourAdminToUpdateIt;
      case 429:
        return context.l10n.mediaTooManyAttemptsWaitAMinuteAndTryAgain;
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
        child: AutofillGroup(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(
                context.l10n.mediaLinkYourValue0Account((product).toString()),
                style: const TextStyle(
                  color: AppTheme.textPrimary,
                  fontSize: 20,
                  fontWeight: FontWeight.bold,
                ),
              ),
              const SizedBox(height: AppTheme.spaceSm),
              Text(
                context.l10n
                    .mediaEnterTheUsernameAndPasswordOfYourExistingAccountOnValue0CantinarrChecksThemWithValue1OnceA(
                        (name).toString(), (name).toString()),
                style: const TextStyle(
                  color: AppTheme.textSecondary,
                  fontSize: 14,
                  height: 1.4,
                ),
              ),
              const SizedBox(height: AppTheme.spaceLg),
              TextField(
                controller: _usernameController,
                enabled: !_busy,
                autofocus: true,
                autocorrect: false,
                enableSuggestions: false,
                autofillHints: const [AutofillHints.username],
                textInputAction: TextInputAction.next,
                decoration: InputDecoration(
                  labelText: context.l10n.mediaUsername,
                  prefixIcon: Icon(Icons.person_outline),
                ),
              ),
              const SizedBox(height: AppTheme.spaceLg),
              TextField(
                controller: _passwordController,
                enabled: !_busy,
                obscureText: _obscure,
                autocorrect: false,
                enableSuggestions: false,
                autofillHints: const [AutofillHints.password],
                textInputAction: TextInputAction.done,
                onSubmitted: (_) => _submit(),
                decoration: InputDecoration(
                  labelText: context.l10n.mediaPassword,
                  prefixIcon: const Icon(Icons.lock_outline),
                  suffixIcon: IconButton(
                    tooltip: _obscure
                        ? context.l10n.mediaShowPassword
                        : context.l10n.mediaHidePassword,
                    icon: Icon(
                        _obscure ? Icons.visibility : Icons.visibility_off),
                    onPressed: () => setState(() => _obscure = !_obscure),
                  ),
                ),
              ),
              if (_error != null) ...[
                const SizedBox(height: AppTheme.spaceMd),
                Text(
                  _error!,
                  style: const TextStyle(
                    color: AppTheme.error,
                    fontSize: 13,
                    height: 1.4,
                  ),
                ),
              ],
              const SizedBox(height: AppTheme.spaceXl),
              ElevatedButton(
                onPressed: _busy ? null : _submit,
                style: ElevatedButton.styleFrom(
                  backgroundColor: AppTheme.accent,
                  foregroundColor: Colors.black,
                  padding: const EdgeInsets.symmetric(vertical: 16),
                  shape: RoundedRectangleBorder(
                    borderRadius: BorderRadius.circular(12),
                  ),
                ),
                child: _busy
                    ? const SizedBox(
                        width: 20,
                        height: 20,
                        child: CircularProgressIndicator(
                            strokeWidth: 2, color: Colors.black),
                      )
                    : Text(context.l10n.mediaLinkAccount),
              ),
            ],
          ),
        ),
      ),
    );
  }
}
