import 'package:flutter/material.dart';
import '../models/backend_connection.dart';
import '../theme/app_theme.dart';

/// Dropdown for switching between service instances.
/// Hidden when only one instance exists.
class InstanceDropdown extends StatelessWidget {
  final List<ServiceInstance> instances;
  final String? activeInstanceId;
  final ValueChanged<String> onChanged;

  /// Optional aggregate entry (e.g. the downloads "All" view) listed above
  /// the instances; selecting it reports its id through [onChanged].
  final ({String id, String label})? aggregateOption;

  const InstanceDropdown({
    super.key,
    required this.instances,
    required this.activeInstanceId,
    required this.onChanged,
    this.aggregateOption,
  });

  @override
  Widget build(BuildContext context) {
    if (instances.length <= 1) return const SizedBox.shrink();
    final aggregate = aggregateOption;

    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 4),
      decoration: BoxDecoration(
        color: AppTheme.surface,
        borderRadius: BorderRadius.circular(8),
        border: Border.all(color: AppTheme.border),
      ),
      child: DropdownButtonHideUnderline(
        child: DropdownButton<String>(
          value: activeInstanceId,
          isDense: true,
          dropdownColor: AppTheme.surface,
          style: const TextStyle(
            color: AppTheme.textPrimary,
            fontSize: 14,
            fontWeight: FontWeight.w500,
          ),
          icon: const Icon(Icons.arrow_drop_down,
              color: AppTheme.textSecondary, size: 20),
          items: [
            if (aggregate != null)
              DropdownMenuItem(
                value: aggregate.id,
                child: Text(aggregate.label),
              ),
            ...instances.map((inst) => DropdownMenuItem(
                  value: inst.id,
                  child: Text(inst.name),
                )),
          ],
          onChanged: (id) {
            if (id != null) onChanged(id);
          },
        ),
      ),
    );
  }
}
