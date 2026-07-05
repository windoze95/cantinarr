import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/models/app_module.dart';
import '../../../core/providers/instance_provider.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/instance_dropdown.dart';
import '../../../core/widgets/module_scaffold.dart';

/// Downloads module shell: Queue | History.
/// Pages render as a bottom nav on mobile and sidebar items on desktop.
/// Shows instance dropdown in the header when 2+ download clients exist.
class DownloadsModuleShell extends ConsumerWidget {
  final int currentIndex;
  final ValueChanged<int> onTabChanged;
  final Widget child;

  const DownloadsModuleShell({
    super.key,
    required this.currentIndex,
    required this.onTabChanged,
    required this.child,
  });

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final instanceState = ref.watch(instanceProvider);

    return ModuleScaffold(
      appBar: instanceState.downloadInstances.length > 1
          ? AppBar(
              title: InstanceDropdown(
                instances: instanceState.downloadInstances,
                activeInstanceId: instanceState.activeDownloadInstanceId,
                onChanged: (id) => ref
                    .read(instanceProvider.notifier)
                    .setActiveDownloadInstance(id),
              ),
              backgroundColor: AppTheme.background,
              elevation: 0,
            )
          : null,
      pages: modulePagesFor(ModuleType.downloads),
      currentIndex: currentIndex,
      onTabChanged: onTabChanged,
      child: child,
    );
  }
}
