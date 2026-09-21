class DownloadsActivity {
  final int? count;
  final bool complete;
  final bool stale;
  final String scope;
  final String userScope;
  final List<DownloadContentGroup> groups;
  final Map<String, DownloadActivityJob> jobs;
  final List<String> unavailableSources;

  DownloadsActivity.fromJson(Map<String, dynamic> json)
      : count = json['count'] as int?,
        complete = json['complete'] == true,
        stale = json['stale'] == true,
        scope = json['scope'] as String? ?? 'all',
        userScope = json['user_scope'] as String? ?? 'mine',
        groups = [for (final g in json['groups'] as List? ?? [])
          DownloadContentGroup.fromJson(g as Map<String, dynamic>)],
        jobs = {for (final j in json['jobs'] as List? ?? [])
          j['id'] as String: DownloadActivityJob.fromJson(j as Map<String, dynamic>)},
        unavailableSources = [for (final s in json['sources'] as List? ?? [])
          if (s['available'] != true) s['name'] as String];
}

class DownloadContentGroup {
  final String id, title, mediaType, instanceName, creator, format, artwork;
  final int year;
  final double progress;
  final bool detailsKnown;
  final List<String> jobIds;
  final List<DownloadContentChild> children;

  DownloadContentGroup.fromJson(Map<String, dynamic> j)
      : id = j['id'] as String,
        title = j['title'] as String,
        mediaType = j['media_type'] as String,
        instanceName = j['instance_name'] as String? ?? '',
        creator = j['creator'] as String? ?? '',
        format = j['format'] as String? ?? '',
        artwork = j['artwork'] as String? ?? '',
        year = j['year'] as int? ?? 0,
        progress = (j['progress'] as num? ?? 0).toDouble(),
        detailsKnown = j['details_known'] == true,
        jobIds = (j['job_ids'] as List? ?? []).cast<String>(),
        children = [for (final c in j['children'] as List? ?? [])
          DownloadContentChild.fromJson(c as Map<String, dynamic>)];
}

class DownloadContentChild {
  final String id, title, track;
  final int? season;
  final int episode, disc;
  final List<String> jobIds;

  DownloadContentChild.fromJson(Map<String, dynamic> j)
      : id = j['id'] as String,
        title = j['title'] as String,
        track = j['track'] as String? ?? '',
        season = j['season'] as int?,
        episode = j['episode'] as int? ?? 0,
        disc = j['disc'] as int? ?? 0,
        jobIds = (j['job_ids'] as List? ?? []).cast<String>();

  String get label => season != null
      ? 'E${episode.toString().padLeft(2, '0')} · $title'
      : '${disc > 0 ? 'Disc $disc · ' : ''}${track.isEmpty ? '' : '$track · '}$title';
}

class DownloadActivityJob {
  final String id, status, name;
  final double progress;
  final int sizeBytes, sizeLeftBytes, speedBps;
  final DownloadJobControl? control;

  DownloadActivityJob.fromJson(Map<String, dynamic> j)
      : id = j['id'] as String,
        status = j['status'] as String? ?? 'queued',
        name = j['name'] as String? ?? '',
        progress = (j['progress'] as num? ?? 0).toDouble(),
        sizeBytes = (j['size_bytes'] as num? ?? 0).toInt(),
        sizeLeftBytes = (j['size_left_bytes'] as num? ?? 0).toInt(),
        speedBps = (j['speed_bps'] as num? ?? 0).toInt(),
        control = j['control'] == null ? null
            : DownloadJobControl.fromJson(j['control'] as Map<String, dynamic>);
}

class DownloadJobControl {
  final String instanceId, itemId, serviceType, clientName;
  DownloadJobControl.fromJson(Map<String, dynamic> j)
      : instanceId = j['instance_id'] as String,
        itemId = j['item_id'] as String,
        serviceType = j['service_type'] as String,
        clientName = j['client_name'] as String;
}
