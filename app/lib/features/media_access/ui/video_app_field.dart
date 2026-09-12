import 'package:flutter/material.dart';

import '../data/media_access_service.dart';
import '../data/video_apps.dart';

/// Shared by per-instance administrator defaults and personal preferences.
class VideoAppField extends StatelessWidget {
  const VideoAppField({
    super.key,
    required this.serviceType,
    required this.value,
    required this.inheritDefaults,
    required this.onChanged,
  });

  final String serviceType;
  final VideoApps value;
  final bool inheritDefaults;
  final ValueChanged<VideoApps>? onChanged;

  @override
  Widget build(BuildContext context) {
    final selected = VideoApp.values.any((app) => app.id == value.ios)
        ? value.ios
        : inheritDefaults ? '' : VideoApp.service.id;
    return DropdownButtonFormField<String>(
      key: ValueKey('$serviceType:$selected'),
      initialValue: selected,
      isExpanded: true,
      decoration: const InputDecoration(labelText: 'iPhone and iPad'),
      items: [
        if (inheritDefaults)
          const DropdownMenuItem(
            value: '',
            child: Text('Use admin default', overflow: TextOverflow.ellipsis),
          ),
        DropdownMenuItem(
          value: VideoApp.service.id,
          child: Text(mediaServerTypeLabel(serviceType)),
        ),
        const DropdownMenuItem(value: 'infuse', child: Text('Infuse')),
        const DropdownMenuItem(value: 'browser', child: Text('Browser')),
      ],
      onChanged: onChanged == null ? null : (id) {
        if (id != null) onChanged!(VideoApps(ios: id));
      },
    );
  }
}
