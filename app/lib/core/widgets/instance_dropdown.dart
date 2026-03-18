import 'package:flutter/material.dart';
import '../models/backend_connection.dart';
import '../theme/app_theme.dart';

/// Dropdown for switching between service instances.
/// Hidden when only one instance exists.
class InstanceDropdown extends StatelessWidget {
  final List<ServiceInstance> instances;
  final String? activeInstanceId;
  final ValueChanged<String> onChanged;

  const InstanceDropdown({
    super.key,
    required this.instances,
    required this.activeInstanceId,
    required this.onChanged,
  });

  @override
  Widget build(BuildContext context) {
    if (instances.length <= 1) return const SizedBox.shrink();

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
          items: instances
              .map((inst) => DropdownMenuItem(
                    value: inst.id,
                    child: Text(inst.name),
                  ))
              .toList(),
          onChanged: (id) {
            if (id != null) onChanged(id);
          },
        ),
      ),
    );
  }
}
