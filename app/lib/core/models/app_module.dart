import 'package:flutter/material.dart';

/// Types of modules available in the app.
enum ModuleType { dashboard, radarr, sonarr, assistant }

/// Represents a navigable module in the app (shown in drawer).
class AppModule {
  final ModuleType type;
  final String label;
  final IconData icon;
  final String? instanceId;
  final String? instanceName;

  const AppModule({
    required this.type,
    required this.label,
    required this.icon,
    this.instanceId,
    this.instanceName,
  });
}
