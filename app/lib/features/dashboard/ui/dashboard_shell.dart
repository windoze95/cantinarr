import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import '../../../core/widgets/module_scaffold.dart';
import '../../discover/logic/discovery_access.dart';
import '../../discover/ui/catalog_prefetch.dart';
import '../../discover/ui/catalog_setup_footer.dart';

/// Server visibility applies to everyone; requesters also need book/music grants. Visible tabs
/// map onto fixed router branches, including when only Music is granted.
/// Pages render as bottom navigation on mobile and sidebar items on desktop.
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
    final access = ref.watch(discoveryAccessProvider);
    final indices = access.visibleBranches;
    if (indices.isEmpty) return const EmptyDiscoverScreen();
    final catalog =
        discoverCatalogs.where((tab) => tab.branch == currentIndex).firstOrNull;
    return CatalogWarmup(
        child: ModuleScaffold(
      pages: access.pages,
      currentIndex: indices.indexOf(currentIndex).clamp(0, indices.length - 1),
      onTabChanged: (index) => onTabChanged(indices[index]),
      child: Column(children: [
        Expanded(child: child),
        if (catalog != null && access.needsSetup(catalog.serviceType))
          CatalogSetupFooter(
              key: ValueKey(catalog.mediaType), mediaType: catalog.mediaType),
      ]),
    ));
  }
}

/// A stable landing when every catalog is hidden, with a way for admins to
/// restore tabs without reopening one of the hidden routes.
class EmptyDiscoverScreen extends ConsumerWidget {
  const EmptyDiscoverScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final isAdmin = ref.watch(discoveryAccessProvider).isAdmin;
    final theme = Theme.of(context);
    return Scaffold(
      backgroundColor: Colors.transparent,
      body: SafeArea(
        child: Center(
          child: SingleChildScrollView(
            padding: const EdgeInsets.all(24),
            child: ConstrainedBox(
              constraints: const BoxConstraints(maxWidth: 420),
              child: Column(
                mainAxisSize: MainAxisSize.min,
                children: [
                  Icon(Icons.explore_outlined,
                      size: 48, color: theme.colorScheme.onSurfaceVariant),
                  const SizedBox(height: 16),
                  Text('No Discover tabs to show',
                      style: theme.textTheme.titleLarge,
                      textAlign: TextAlign.center),
                  const SizedBox(height: 8),
                  Text(
                    isAdmin
                        ? 'Show a tab in Discover settings or connect a service to start browsing.'
                        : 'No Discover tabs are available for your account.',
                    textAlign: TextAlign.center,
                  ),
                  if (isAdmin) ...[
                    const SizedBox(height: 16),
                    FilledButton(
                      onPressed: () => context.go('/settings/discovery'),
                      child: const Text('Discover settings'),
                    ),
                  ],
                ],
              ),
            ),
          ),
        ),
      ),
    );
  }
}

/// Stateful router branches stay alive after visiting another tab. Unmount a
/// hidden catalog so its rows and preload subscriptions stop with the tab.
class DiscoverTabContent extends ConsumerWidget {
  final String mediaType;
  final Widget child;
  const DiscoverTabContent(
      {super.key, required this.mediaType, required this.child});

  @override
  Widget build(BuildContext context, WidgetRef ref) =>
      ref.watch(discoveryAccessProvider).isVisible(mediaType)
          ? child
          : const SizedBox.shrink();
}
