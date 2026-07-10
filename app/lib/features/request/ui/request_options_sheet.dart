import 'package:flutter/material.dart';
import '../../../core/theme/app_theme.dart';
import '../data/request_service.dart';

/// The user's picks from [RequestOptionsSheet]. Null fields mean "use the
/// server default".
class RequestOptionsResult {
  final String? seasonScope;
  final int? qualityProfileId;
  const RequestOptionsResult({this.seasonScope, this.qualityProfileId});
}

/// Pre-submit sheet that lets a permitted user choose request options
/// (TV season scope and/or quality profile). Only shown when there is
/// something to choose; popping with a result submits, popping with null
/// cancels.
class RequestOptionsSheet extends StatefulWidget {
  final RequestOptions options;
  const RequestOptionsSheet({super.key, required this.options});

  @override
  State<RequestOptionsSheet> createState() => _RequestOptionsSheetState();
}

class _RequestOptionsSheetState extends State<RequestOptionsSheet> {
  late String _seasonScope;
  int? _qualityProfileId;

  @override
  void initState() {
    super.initState();
    _seasonScope = widget.options.defaultSeasonScope;
  }

  @override
  Widget build(BuildContext context) {
    final o = widget.options;
    return Container(
      padding: EdgeInsets.only(
        left: 24,
        right: 24,
        top: 16,
        bottom: 24 + MediaQuery.of(context).viewInsets.bottom,
      ),
      decoration: const BoxDecoration(
        color: AppTheme.surface,
        borderRadius: BorderRadius.vertical(top: Radius.circular(20)),
      ),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Center(
            child: Container(
              width: 40,
              height: 4,
              decoration: BoxDecoration(
                color: AppTheme.textSecondary,
                borderRadius: BorderRadius.circular(2),
              ),
            ),
          ),
          const SizedBox(height: 20),
          const Text(
            'Request options',
            style: TextStyle(
              color: AppTheme.textPrimary,
              fontSize: 20,
              fontWeight: FontWeight.bold,
            ),
          ),
          const SizedBox(height: 16),
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
