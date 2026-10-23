import 'package:cantinarr/l10n/auth_messages.dart';
import 'package:cantinarr/l10n/l10n.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import '../logic/auth_provider.dart';

class OIDCReturnScreen extends ConsumerStatefulWidget {
  final Uri uri;
  final bool start;
  const OIDCReturnScreen({super.key, required this.uri, this.start = false});
  @override
  ConsumerState<OIDCReturnScreen> createState() => _OIDCReturnScreenState();
}

class _OIDCReturnScreenState extends ConsumerState<OIDCReturnScreen> {
  String? _error;
  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) => _complete());
  }

  Future<void> _complete() async {
    try {
      await ref.read(authProvider.future);
      if (!mounted) return;
      final notifier = ref.read(authProvider.notifier);
      if (widget.start) {
        await notifier.startSSO(Uri.base.origin,
            invitation: widget.uri.queryParameters['invitation']);
        return;
      }
      final purpose = await notifier.finishSSO(widget.uri);
      if (!mounted || purpose.isEmpty) return;
      context.go(purpose == 'test'
          ? '/settings/oidc'
          : purpose == 'link'
              ? '/settings/sso-account'
              : '/dashboard/movies');
      if (purpose != 'login') {
        ScaffoldMessenger.of(context).showSnackBar(SnackBar(
            content: Text(purpose == 'test'
                ? context.l10n.authOidcTestSuccess
                : context.l10n.authSsoLinked)));
      }
    } catch (e) {
      if (mounted) {
        setState(() =>
            _error = ref.read(authProvider).valueOrNull?.error ?? e.toString());
        // Keep the router in sync with the browser URL cleanup. Otherwise an
        // auth refresh restores the consumed handoff query on this error page.
        if (!widget.start && widget.uri.hasQuery) {
          context.replace('/oidc/return');
        }
      }
    }
  }

  @override
  Widget build(BuildContext context) => Scaffold(
        appBar: AppBar(title: Text(context.l10n.authSingleSignOn)),
        body: Center(
            child: Padding(
                padding: const EdgeInsets.all(24),
                child: Column(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    if (_error == null) ...[
                      const CircularProgressIndicator(),
                      const SizedBox(height: 20),
                      Text(context.l10n.authCompletingSignIn),
                    ] else ...[
                      Text(localizeAuthMessage(context, _error!),
                          textAlign: TextAlign.center),
                      const SizedBox(height: 20),
                      FilledButton(
                          onPressed: () => context.go(ref
                                      .read(authProvider)
                                      .valueOrNull
                                      ?.isAuthenticated ==
                                  true
                              ? '/settings'
                              : '/login'),
                          child: Text(context.l10n.authBackCantinarr)),
                    ],
                  ],
                ))),
      );
}
