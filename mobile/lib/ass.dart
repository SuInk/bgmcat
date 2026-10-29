import 'dart:ui' show Color, Offset;

/// 够用的 ASS/SSA 解析：取出每条对白的起止时间、文字、对齐、`\pos` 定位、字号和颜色。
/// 动画、旋转、自定义字体这些不还原，绘图指令（\p1）整条跳过。
class AssCue {
  AssCue(
    this.start,
    this.end,
    this.text, {
    required this.align,
    required this.fontSize,
    required this.color,
    required this.outline,
    this.pos,
  });

  final Duration start;
  final Duration end;
  final String text;

  /// 小键盘方位 1-9：1-3 底部、4-6 居中、7-9 顶部。
  final int align;

  /// 字号与定位都是 PlayRes 坐标系下的值，渲染时按画面高度缩放。
  final double fontSize;
  final Offset? pos;
  final Color color;
  final bool outline;

  bool get top => align >= 7;
}

class _Style {
  _Style({required this.align, required this.fontSize, required this.color, required this.outline});

  final int align;
  final double fontSize;
  final Color color;
  final bool outline;
}

class AssSubtitle {
  AssSubtitle(this.cues, {required this.playResX, required this.playResY});

  final List<AssCue> cues;
  final double playResX;
  final double playResY;

  /// 某一时刻正在显示的字幕，按出现顺序。
  List<AssCue> at(Duration position) =>
      [for (final cue in cues) if (position >= cue.start && position < cue.end) cue];

  static AssSubtitle parse(String source) {
    final cues = <AssCue>[];
    final styles = <String, _Style>{};
    var section = '';
    var playResX = 0.0;
    var playResY = 0.0;
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

      if (section == '[script info]') {
        if (key == 'playresx') playResX = double.tryParse(value) ?? 0;
        if (key == 'playresy') playResY = double.tryParse(value) ?? 0;
      } else if (section.contains('styles')) {
        if (key == 'format') {
          styleFormat = value.split(',').map((s) => s.trim().toLowerCase()).toList();
        } else if (key == 'style' && styleFormat != null) {
          final fields = value.split(',');
          String field(String name) {
            final i = styleFormat!.indexOf(name);
            return i >= 0 && i < fields.length ? fields[i].trim() : '';
          }

          var align = int.tryParse(field('alignment')) ?? 2;
          // SSA 旧式对齐（v4 styles）：1-3 底、5-7 顶、9-11 中，换成小键盘方位
          if (section == '[v4 styles]') {
            align = switch (align) { 5 || 6 || 7 => align + 2, 9 || 10 || 11 => align - 5, _ => align };
          }
          styles[field('name')] = _Style(
            align: align,
            fontSize: double.tryParse(field('fontsize')) ?? 0,
            color: _parseColor(field('primarycolour')) ?? const Color(0xFFFFFFFF),
            outline: (double.tryParse(field('outline')) ?? 1) > 0,
          );
        }
      } else if (section.contains('events')) {
        if (key == 'format') {
          eventFormat = value.split(',').map((s) => s.trim().toLowerCase()).toList();
        } else if (key == 'dialogue' && eventFormat != null) {
          final cue = _parseDialogue(value, eventFormat, styles);
          if (cue != null) cues.add(cue);
        }
      }
    }
    // 没写 PlayRes 的老字幕按 VSFilter 的默认值 384x288
    if (playResY <= 0) playResY = playResX > 0 ? playResX * 3 / 4 : 288;
    if (playResX <= 0) playResX = playResY * 4 / 3;
    cues.sort((a, b) => a.start.compareTo(b.start));
    return AssSubtitle(cues, playResX: playResX, playResY: playResY);
  }

  static AssCue? _parseDialogue(String value, List<String> format, Map<String, _Style> styles) {
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

    final style = styles[field('style')] ?? styles['Default'];
    final tags = RegExp(r'\{([^}]*)\}').allMatches(raw).map((m) => m.group(1)!).join();
    final an = RegExp(r'\\an(\d)').firstMatch(tags);
    final fs = RegExp(r'\\fs(\d+(?:\.\d+)?)').firstMatch(tags);
    final pos = RegExp(r'\\(?:pos|move)\(\s*(-?[\d.]+)\s*,\s*(-?[\d.]+)').firstMatch(tags);
    final color = RegExp(r'\\1?c(&H[0-9A-Fa-f]+&?)').firstMatch(tags);

    final text = raw
        .replaceAll(RegExp(r'\{[^}]*\}'), '')
        .replaceAll(RegExp(r'\\[Nn]'), '\n')
        .replaceAll(r'\h', ' ')
        .trim();
    if (text.isEmpty) return null;
    return AssCue(
      start,
      end,
      text,
      align: an != null ? int.parse(an.group(1)!) : (style?.align ?? 2),
      fontSize: fs != null ? double.parse(fs.group(1)!) : (style?.fontSize ?? 0),
      pos: pos == null ? null : Offset(double.parse(pos.group(1)!), double.parse(pos.group(2)!)),
      color: (color == null ? null : _parseColor(color.group(1)!)) ?? style?.color ?? const Color(0xFFFFFFFF),
      outline: style?.outline ?? true,
    );
  }

  /// ASS 颜色是 &HAABBGGRR（alpha 0 表示不透明）。
  static Color? _parseColor(String value) {
    final hex = value.replaceAll(RegExp(r'[&Hh]'), '');
    final n = int.tryParse(hex, radix: 16);
    if (n == null) return null;
    final r = n & 0xFF;
    final g = (n >> 8) & 0xFF;
    final b = (n >> 16) & 0xFF;
    final a = 0xFF - ((n >> 24) & 0xFF);
    return Color.fromARGB(a, r, g, b);
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
