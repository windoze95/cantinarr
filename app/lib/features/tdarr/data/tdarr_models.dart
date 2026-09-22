class TdarrWorker {
  final String id, file, kind, compute, status, eta;
  final double? progress, fps;
  final bool flow;

  TdarrWorker.fromJson(Map<String, dynamic> json)
      : id = json['id'] as String,
        file = json['file'] as String,
        kind = json['kind'] as String,
        compute = json['compute'] as String,
        status = json['status'] as String,
        eta = json['eta'] as String,
        progress = (json['progress_percent'] as num?)?.toDouble(),
        fps = (json['fps'] as num?)?.toDouble(),
        flow = json['flow'] == true;

  String get filename => file.replaceAll('\\', '/').split('/').last;
}

class TdarrNode {
  final String id, name;
  final bool paused;
  final List<TdarrWorker> workers;

  TdarrNode.fromJson(Map<String, dynamic> json)
      : id = json['id'] as String,
        name = json['name'] as String,
        paused = json['paused'] == true,
        workers = (json['workers'] as List)
            .map((e) => TdarrWorker.fromJson(e as Map<String, dynamic>)).toList();
}

class TdarrActivity {
  final DateTime observedAt;
  final List<TdarrNode> nodes;

  TdarrActivity.fromJson(Map<String, dynamic> json)
      : observedAt = DateTime.parse(json['observed_at'] as String),
        nodes = (json['nodes'] as List)
            .map((e) => TdarrNode.fromJson(e as Map<String, dynamic>)).toList();

  int get activeWorkers => nodes.fold(0, (sum, node) => sum + node.workers.length);
}

class TdarrLibrary {
  final String id, name;
  TdarrLibrary.fromJson(Map<String, dynamic> json)
      : id = json['id'] as String, name = json['name'] as String;
}

class TdarrLibraries {
  final DateTime observedAt;
  final List<TdarrLibrary> items;
  TdarrLibraries.fromJson(Map<String, dynamic> json)
      : observedAt = DateTime.parse(json['observed_at'] as String),
        items = (json['items'] as List)
            .map((e) => TdarrLibrary.fromJson(e as Map<String, dynamic>)).toList();
}

class TdarrCount {
  final String label;
  final int? value;
  TdarrCount.fromJson(Map<String, dynamic> json)
      : label = json['label'] as String, value = (json['value'] as num?)?.toInt();
}

class TdarrStats {
  final DateTime observedAt;
  final String libraryId, note;
  final int totalFiles;
  final List<TdarrCount> transcodes, healthChecks;
  TdarrStats.fromJson(Map<String, dynamic> json)
      : observedAt = DateTime.parse(json['observed_at'] as String),
        libraryId = json['library_id'] as String,
        note = json['note'] as String? ?? '',
        totalFiles = (json['total_files'] as num).toInt(),
        transcodes = (json['transcodes'] as List)
            .map((e) => TdarrCount.fromJson(e as Map<String, dynamic>)).toList(),
        healthChecks = (json['health_checks'] as List)
            .map((e) => TdarrCount.fromJson(e as Map<String, dynamic>)).toList();
}
