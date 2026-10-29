/// 够用的 ASS/SSA 解析：取出每条对白的起止时间、文字和上下位置。
/// 特效、字体、坐标定位这些不还原，绘图指令（\p1）整条跳过。
class AssCue {
  AssCue(this.start, this.end, this.text, {required this.top});

  final Duration start;
  final Duration end;
  final String text;

  /// \an7/8/9 或样式对齐在上方的（常见于注释、歌词翻译）放到画面顶部。
  final bool top;
}

class AssSubtitle {
  AssSubtitle(this.cues);

  final List<AssCue> cues;

  /// 某一时刻正在显示的字幕，按出现顺序。
  List<AssCue> at(Duration position) =>
      [for (final cue in cues) if (position >= cue.start && position < cue.end) cue];

  static AssSubtitle parse(String source) {
    final cues = <AssCue>[];
    final styleTop = <String, bool>{};
    var section = '';
    List<String>? styleFormat;
    List<String>? eventFormat;

    for (final rawLine in source.split(RegExp(r'\r?\n'))) {
      final line = rawLine.trim();
      if (line.startsWith('[') && line.endsWith(']')) {
        section = line.toLowerCase();
        continue;
      }
      final colon = line.indexOf(':');
      if (colon < 0) continue;
      final key = line.substring(0, colon).trim().toLowerCase();
      final value = line.substring(colon + 1).trim();

      if (section.contains('styles')) {
        if (key == 'format') {
          styleFormat = value.split(',').map((s) => s.trim().toLowerCase()).toList();
        } else if (key == 'style' && styleFormat != null) {
          final fields = value.split(',');
          final nameIndex = styleFormat.indexOf('name');
          final alignIndex = styleFormat.indexOf('alignment');
          if (nameIndex >= 0 && alignIndex >= 0 && alignIndex < fields.length) {
            final align = int.tryParse(fields[alignIndex].trim()) ?? 2;
            // SSA 旧式对齐 5-7 在上，ASS 新式 7-9 在上
            styleTop[fields[nameIndex].trim()] = align >= 7 || (section.contains('v4 styles]') && align >= 5);
          }
        }
      } else if (section.contains('events')) {
        if (key == 'format') {
          eventFormat = value.split(',').map((s) => s.trim().toLowerCase()).toList();
        } else if (key == 'dialogue' && eventFormat != null) {
          final cue = _parseDialogue(value, eventFormat, styleTop);
          if (cue != null) cues.add(cue);
        }
      }
    }
    cues.sort((a, b) => a.start.compareTo(b.start));
    return AssSubtitle(cues);
  }

  static AssCue? _parseDialogue(String value, List<String> format, Map<String, bool> styleTop) {
    // Text 是最后一列，里面可能有逗号，只切前 n-1 刀
    final fields = <String>[];
    var rest = value;
    for (var i = 0; i < format.length - 1; i++) {
      final comma = rest.indexOf(',');
      if (comma < 0) return null;
      fields.add(rest.substring(0, comma));
      rest = rest.substring(comma + 1);
    }
    fields.add(rest);
    String field(String name) {
      final i = format.indexOf(name);
      return i >= 0 && i < fields.length ? fields[i].trim() : '';
    }

    final start = _parseTime(field('start'));
    final end = _parseTime(field('end'));
    if (start == null || end == null) return null;
    final raw = field('text');
    if (RegExp(r'\\p[1-9]').hasMatch(raw)) return null;

    final anMatch = RegExp(r'\\an(\d)').firstMatch(raw);
    final top = anMatch != null ? int.parse(anMatch.group(1)!) >= 7 : (styleTop[field('style')] ?? false);
    final text = raw
        .replaceAll(RegExp(r'\{[^}]*\}'), '')
        .replaceAll(RegExp(r'\\[Nn]'), '\n')
        .replaceAll(r'\h', ' ')
        .trim();
    if (text.isEmpty) return null;
    return AssCue(start, end, text, top: top);
  }

  static Duration? _parseTime(String value) {
    final match = RegExp(r'^(\d+):(\d{1,2}):(\d{1,2})[.:](\d{1,3})$').firstMatch(value);
    if (match == null) return null;
    final fraction = match.group(4)!;
    final millis = int.parse(fraction.padRight(3, '0').substring(0, 3));
    return Duration(
      hours: int.parse(match.group(1)!),
      minutes: int.parse(match.group(2)!),
      seconds: int.parse(match.group(3)!),
      milliseconds: millis,
    );
  }
}
