import 'package:flutter/material.dart';
import '../../../core/theme/app_theme.dart';
import '../../../core/widgets/app_sheet.dart';
import '../data/request_service.dart';

/// The user's picks from [RequestOptionsSheet]. Null fields mean "use the
/// server default".
class RequestOptionsResult {
  final String? seasonScope;
  final int? qualityProfileId;
  final String? instanceId;
  const RequestOptionsResult({
    this.seasonScope,
    this.qualityProfileId,
    this.instanceId,
  });
}

/// One library the requester may aim this request at, labeled by the
/// admin-chosen instance name (never a host or URL).
class LibraryChoice {
  final String id;
  final String name;
  const LibraryChoice({required this.id, required this.name});
}

/// Pre-submit sheet that lets a permitted user choose request options
/// (target library, TV season scope, and/or quality profile). Only shown when
/// there is something to choose; popping with a result submits, popping with
/// null cancels.
class RequestOptionsSheet extends StatefulWidget {
  final RequestOptions options;

  /// The libraries this user may choose between; the section renders only
  /// when there is more than one.
  final List<LibraryChoice> libraries;

  /// The library preselected when the sheet opens (defaults to the first).
  final String? selectedLibraryId;

  /// Refetches the option set for a newly selected library, since quality
  /// profiles live inside an instance and a sibling's ids are meaningless.
  /// Null keeps the initial options for every library.
  final Future<RequestOptions?> Function(String libraryId)? onLibraryOptions;

  const RequestOptionsSheet({
    super.key,
    required this.options,
    this.libraries = const [],
    this.selectedLibraryId,
    this.onLibraryOptions,
  });

  @override
  State<RequestOptionsSheet> createState() => _RequestOptionsSheetState();
}

class _RequestOptionsSheetState extends State<RequestOptionsSheet> {
  late String _seasonScope;
  int? _qualityProfileId;
  late RequestOptions _options;
  String? _libraryId;

  @override
  void initState() {
    super.initState();
    _options = widget.options;
    _seasonScope = widget.options.defaultSeasonScope;
    _libraryId = widget.selectedLibraryId ??
        (widget.libraries.isNotEmpty ? widget.libraries.first.id : null);
  }

  Future<void> _selectLibrary(String libraryId) async {
    if (_libraryId == libraryId) return;
    setState(() {
      _libraryId = libraryId;
      // Profile ids are per-library; a kept selection would silently name a
      // different profile (or nothing) on the new library.
      _qualityProfileId = null;
    });
    final refetch = widget.onLibraryOptions;
    if (refetch == null) return;
    final refreshed = await refetch(libraryId);
    if (!mounted || refreshed == null || _libraryId != libraryId) return;
    setState(() => _options = refreshed);
  }

  @override
  Widget build(BuildContext context) {
    final o = _options;
    return AppSheet(
      padding: const EdgeInsets.fromLTRB(
        AppTheme.spaceXl,
        0,
        AppTheme.spaceXl,
        AppTheme.spaceXl,
      ),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const Text(
            'Request options',
            style: TextStyle(
              color: AppTheme.textPrimary,
              fontSize: 20,
              fontWeight: FontWeight.bold,
            ),
          ),
          const SizedBox(height: 16),
          if (widget.libraries.length > 1) ...[
            const _SectionLabel('Library'),
            const SizedBox(height: 8),
            Wrap(
              spacing: 8,
              runSpacing: 8,
              children: widget.libraries
                  .map((library) => ChoiceChip(
                        label: Text(library.name),
                        selected: _libraryId == library.id,
                        onSelected: (_) => _selectLibrary(library.id),
                        showCheckmark: false,
                        selectedColor: AppTheme.accent,
                        backgroundColor: AppTheme.surfaceVariant,
                        labelStyle: TextStyle(
                          color: _libraryId == library.id
                              ? AppTheme.onAccent
                              : AppTheme.textPrimary,
                          fontSize: 13,
                        ),
                        side: const BorderSide(color: AppTheme.border),
                      ))
                  .toList(),
            ),
            const SizedBox(height: 16),
          ],
          if (o.canChooseSeason) ...[
            const _SectionLabel('Seasons'),
            const SizedBox(height: 8),
            Wrap(
              spacing: 8,
              runSpacing: 8,
              children: SeasonScope.choices
                  .map((c) => ChoiceChip(
                        label: Text(c.label),
                        selected: _seasonScope == c.value,
                        onSelected: (_) =>
                            setState(() => _seasonScope = c.value),
                        showCheckmark: false,
                        selectedColor: AppTheme.accent,
                        backgroundColor: AppTheme.surfaceVariant,
                        labelStyle: TextStyle(
                          color: _seasonScope == c.value
                              ? AppTheme.onAccent
                              : AppTheme.textPrimary,
                          fontSize: 13,
                        ),
                        side: const BorderSide(color: AppTheme.border),
                      ))
                  .toList(),
            ),
            const SizedBox(height: 16),
          ],
          if (o.canChooseQuality && o.qualityProfiles.isNotEmpty) ...[
            const _SectionLabel('Quality'),
            const SizedBox(height: 8),
            DropdownButtonFormField<int?>(
              // Re-key per library: the form field caches its own selection,
              // and a kept value could name a profile the new library's item
              // list no longer contains.
              key: ValueKey('quality-${_libraryId ?? 'default'}'),
              initialValue: _qualityProfileId,
              isExpanded: true,
              dropdownColor: AppTheme.surfaceVariant,
              decoration: InputDecoration(
                contentPadding:
                    const EdgeInsets.symmetric(horizontal: 12, vertical: 4),
                border: OutlineInputBorder(
                  borderRadius: BorderRadius.circular(12),
                ),
              ),
              items: [
                const DropdownMenuItem<int?>(
                  value: null,
                  child: Text('Default'),
                ),
                ...o.qualityProfiles.map(
                  (p) => DropdownMenuItem<int?>(
                    value: p.id,
                    child: Text(p.name),
                  ),
                ),
              ],
              onChanged: (v) => setState(() => _qualityProfileId = v),
            ),
            const SizedBox(height: 16),
          ],
          const SizedBox(height: 8),
          Row(
            children: [
              Expanded(
                child: OutlinedButton(
                  onPressed: () => Navigator.of(context).pop(),
                  style: OutlinedButton.styleFrom(
                    foregroundColor: AppTheme.textPrimary,
                    side: const BorderSide(color: AppTheme.border),
                    shape: RoundedRectangleBorder(
                      borderRadius: BorderRadius.circular(12),
                    ),
                  ),
                  child: const Text('Cancel'),
                ),
              ),
              const SizedBox(width: 12),
              Expanded(
                child: ElevatedButton(
                  onPressed: () => Navigator.of(context).pop(
                    RequestOptionsResult(
                      seasonScope: o.canChooseSeason ? _seasonScope : null,
                      qualityProfileId: _qualityProfileId,
                      instanceId:
                          widget.libraries.length > 1 ? _libraryId : null,
                    ),
                  ),
                  style: ElevatedButton.styleFrom(
                    backgroundColor: AppTheme.accent,
                    foregroundColor: AppTheme.onAccent,
                    shape: RoundedRectangleBorder(
                      borderRadius: BorderRadius.circular(12),
                    ),
                  ),
                  child: const Text('Request'),
                ),
              ),
            ],
          ),
        ],
      ),
    );
  }
}

class _SectionLabel extends StatelessWidget {
  final String text;
  const _SectionLabel(this.text);

  @override
  Widget build(BuildContext context) => Text(
        text,
        style: const TextStyle(
          color: AppTheme.textSecondary,
          fontSize: 13,
          fontWeight: FontWeight.w600,
        ),
      );
}
