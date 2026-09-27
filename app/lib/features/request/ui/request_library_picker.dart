import 'package:flutter/material.dart';

import '../../../core/models/backend_connection.dart';
import '../../../core/widgets/app_sheet.dart';
import '../data/request_service.dart';
import 'request_options_sheet.dart';

/// Administrator navigation may include instances outside personal assignments.
List<LibraryChoice> requestLibraries(
        BackendConnection? connection, String serviceType) =>
    [
      for (final instance in connection?.instances ?? const <ServiceInstance>[])
        if (instance.serviceType == serviceType && instance.assigned != false)
          LibraryChoice(id: instance.id, name: instance.name),
    ];

String? defaultRequestLibrary(
    BackendConnection? connection, String serviceType) {
  final instances = connection?.instances
          .where((i) => i.serviceType == serviceType && i.assigned != false)
          .toList() ??
      <ServiceInstance>[];
  if (instances.isEmpty) return null;
  return instances
      .firstWhere((i) => i.requestDefault ?? i.isDefault,
          orElse: () => instances.first)
      .id;
}

/// Null means cancel or no personal request destination. One assigned library
/// needs no extra confirmation; multiple assignments always require a choice.
Future<String?> confirmRequestLibrary(
  BuildContext context, {
  required List<LibraryChoice> libraries,
  String? defaultLibraryId,
  bool confirmSingle = false,
  String? libraryNote,
}) async {
  if (libraries.isEmpty) {
    ScaffoldMessenger.of(context).showSnackBar(const SnackBar(
        content: Text(
            'No request library is assigned. An administrator can update assignments in Settings > Users.')));
    return null;
  }
  if (libraries.length == 1 && !confirmSingle) return libraries.single.id;
  final result = await showAppSheet<RequestOptionsResult>(context,
      builder: (_) => RequestOptionsSheet(
            options: const RequestOptions(
                canChooseSeason: false,
                canChooseQuality: false,
                defaultSeasonScope: SeasonScope.all,
                qualityProfiles: []),
            libraries: libraries,
            selectedLibraryId: defaultLibraryId,
            showLibrary: true,
            libraryNote: libraryNote,
          ));
  return result?.instanceId;
}

String requestLibraryName(List<LibraryChoice> libraries, String? id) =>
    libraries.where((library) => library.id == id).firstOrNull?.name ??
    'Selected library';
