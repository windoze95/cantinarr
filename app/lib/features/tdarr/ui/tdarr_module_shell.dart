import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../core/models/app_module.dart';
import '../../../core/providers/instance_provider.dart';
import '../../../core/widgets/instance_dropdown.dart';
import '../../../core/widgets/module_scaffold.dart';

class TdarrModuleShell extends ConsumerWidget {
  final int currentIndex;
  final ValueChanged<int> onTabChanged;
  final Widget child;

  const TdarrModuleShell({super.key, required this.currentIndex,
    required this.onTabChanged, required this.child});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final state = ref.watch(instanceProvider);
    return ModuleScaffold(
      appBar: state.tdarrInstances.length > 1 ? AppBar(
        title: InstanceDropdown(instances: state.tdarrInstances,
          activeInstanceId: state.activeTdarrInstance?.id,
          onChanged: ref.read(instanceProvider.notifier).setActiveTdarrInstance),
      ) : null,
      pages: modulePagesFor(ModuleType.tdarr),
      currentIndex: currentIndex, onTabChanged: onTabChanged, child: child,
    );
  }
}
