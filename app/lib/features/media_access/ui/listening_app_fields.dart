import 'package:flutter/material.dart';

import '../data/listening_apps.dart';

/// Shared by the administrator's instance defaults and personal overrides.
class ListeningAppFields extends StatelessWidget {
  const ListeningAppFields({
    super.key,
    required this.value,
    required this.inheritDefaults,
    required this.onChanged,
  });

  final ListeningApps value;
  final bool inheritDefaults;
  final ValueChanged<ListeningApps>? onChanged;

  @override
  Widget build(BuildContext context) => Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          _field(
            label: 'iPhone and iPad',
            selected: value.ios,
            apps: ListeningApp.ios,
            change: (id) => ListeningApps(ios: id, android: value.android),
          ),
          const SizedBox(height: 16),
          _field(
            label: 'Android',
            selected: value.android,
            apps: ListeningApp.android,
            change: (id) => ListeningApps(ios: value.ios, android: id),
          ),
        ],
      );

  Widget _field({
    required String label,
    required String selected,
    required List<ListeningApp> apps,
    required ListeningApps Function(String) change,
  }) {
    final ids = apps.map((app) => app.id).toSet();
    final selectedId = ids.contains(selected)
        ? selected
        : inheritDefaults
            ? ''
            : ListeningApp.browser.id;
    return DropdownButtonFormField<String>(
      key: ValueKey('$label:$selectedId'),
      initialValue: selectedId,
      isExpanded: true,
      decoration: InputDecoration(labelText: label),
      items: [
        if (inheritDefaults)
          const DropdownMenuItem(
              value: '',
              child:
                  Text('Use admin default', overflow: TextOverflow.ellipsis)),
        for (final app in apps)
          DropdownMenuItem(
              value: app.id,
              child: Text(app.label, overflow: TextOverflow.ellipsis)),
      ],
      onChanged: onChanged == null
          ? null
          : (id) {
              if (id != null) onChanged!(change(id));
            },
    );
  }
}
