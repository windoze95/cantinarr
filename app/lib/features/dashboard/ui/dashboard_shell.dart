import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/models/app_module.dart';
import '../../../core/widgets/module_scaffold.dart';
import '../../auth/logic/auth_provider.dart';

/// Dashboard module shell: Movies | TV Shows | Releases, plus a Books tab when
/// the user has Chaptarr access (services.chaptarr). Books is the LAST branch
/// so showing/hiding it never shifts the other tabs' indices. Pages render as
/// a bottom nav on mobile and sidebar items on desktop.
class DashboardShell extends ConsumerWidget {
  final int currentIndex;
  final ValueChanged<int> onTabChanged;
  final Widget child;

  const DashboardShell({
    super.key,
    required this.currentIndex,
    required this.onTabChanged,
    required this.child,
  });

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final showBooks = ref.watch(
      authProvider.select(
        (a) => a.valueOrNull?.connection?.services.chaptarr ?? false,
      ),
    );

    return ModuleScaffold(
      pages: modulePagesFor(ModuleType.dashboard, includeBooks: showBooks),
      currentIndex: currentIndex,
      onTabChanged: onTabChanged,
      child: child,
    );
  }
}
