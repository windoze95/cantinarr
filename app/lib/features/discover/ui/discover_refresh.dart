import 'dart:async';

import 'package:flutter/widgets.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../core/providers/library_refresh_provider.dart';
import '../../../core/providers/realtime_provider.dart';
import '../../auth/logic/auth_provider.dart';

/// Refresh after the retained screen paints, including declarative browser
/// Back, branch/tab returns and app/browser resume. Hidden tabs stay quiet.
class DiscoverRefresh extends ConsumerStatefulWidget {
  const DiscoverRefresh({super.key, required this.path,
    required this.onRefresh, required this.child});
  final String path;
  final Future<void> Function() onRefresh;
  final Widget child;

  @override
  ConsumerState<DiscoverRefresh> createState() => _DiscoverRefreshState();
}

class _DiscoverRefreshState extends ConsumerState<DiscoverRefresh>
    with WidgetsBindingObserver {
  GoRouter? _router;
  bool _active = false;
  bool _scheduled = false;
  Timer? _debounce;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    _schedule();
  }

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    final router = GoRouter.maybeOf(context);
    if (router != _router) {
      _router?.routerDelegate.removeListener(_navigationChanged);
      _router = router;
      _router?.routerDelegate.addListener(_navigationChanged);
    }
    _active = _isActive;
  }

  bool get _isActive => _router == null || _router!.state.uri.path == widget.path;

  void _navigationChanged() {
    final active = _isActive;
    if (active && !_active) _schedule();
    _active = active;
  }

  void _schedule() {
    if (_scheduled) return;
    _scheduled = true;
    WidgetsBinding.instance.addPostFrameCallback((_) {
      _scheduled = false;
      if (mounted &&
          ref.read(authProvider).valueOrNull?.isAuthenticated == true) {
        unawaited(widget.onRefresh());
      }
    });
    WidgetsBinding.instance.ensureVisualUpdate();
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.resumed && _isActive) _schedule();
  }

  @override
  Widget build(BuildContext context) {
    ref.listen(libraryRefreshTickProvider, (_, __) {
      if (_isActive) _schedule();
    });
    ref.listen(libraryChangedEventsProvider, (_, next) {
      if (next.valueOrNull == null) return;
      _debounce?.cancel();
      if (_isActive) {
        _debounce = Timer(const Duration(seconds: 3), () {
          if (_isActive) _schedule();
        });
      }
    });
    return widget.child;
  }

  @override
  void dispose() {
    _debounce?.cancel();
    _router?.routerDelegate.removeListener(_navigationChanged);
    WidgetsBinding.instance.removeObserver(this);
    super.dispose();
  }
}
