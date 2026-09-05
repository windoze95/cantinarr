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
                ? 'Test sign-in succeeded. No account was created or linked.'
                : 'Your single sign-on identity is linked.')));
      }
    } catch (e) {
      if (mounted) {
        setState(() =>
            _error = ref.read(authProvider).valueOrNull?.error ?? e.toString());
      }
    }
  }

  @override
  Widget build(BuildContext context) => Scaffold(
        appBar: AppBar(title: const Text('Single sign-on')),
        body: Center(
            child: Padding(
                padding: const EdgeInsets.all(24),
                child: Column(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    if (_error == null) ...[
                      const CircularProgressIndicator(),
                      const SizedBox(height: 20),
                      const Text('Completing sign-in…'),
                    ] else ...[
                      Text(_error!, textAlign: TextAlign.center),
                      const SizedBox(height: 20),
                      FilledButton(
                          onPressed: () => context.go(ref
                                      .read(authProvider)
                                      .valueOrNull
                                      ?.isAuthenticated ==
                                  true
                              ? '/settings'
                              : '/login'),
                          child: const Text('Back to Cantinarr')),
                    ],
                  ],
                ))),
      );
}
