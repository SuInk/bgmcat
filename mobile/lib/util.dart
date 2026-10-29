String formatBytes(num bytes) {
  if (bytes <= 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  var value = bytes.toDouble();
  var unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit++;
  }
  return '${value.toStringAsFixed(value >= 100 || unit == 0 ? 0 : 1)} ${units[unit]}';
}

String formatSpeed(num bytesPerSecond) => '${formatBytes(bytesPerSecond)}/s';

String formatDuration(Duration d) {
  final h = d.inHours;
  final m = d.inMinutes.remainder(60).toString().padLeft(2, '0');
  final s = d.inSeconds.remainder(60).toString().padLeft(2, '0');
  return h > 0 ? '$h:$m:$s' : '$m:$s';
}

const weekdayLabels = ['周日', '周一', '周二', '周三', '周四', '周五', '周六'];

final _resolutionPattern = RegExp(r'(2160|1080|720|480)[pP]|\b4K\b|3840x2160|1920x1080|1280x720', caseSensitive: false);

/// 资源的分辨率，后端没给就从标题里认。
String? resolutionOf(String title, [String? given]) {
  if (given != null && given.isNotEmpty) return given.toLowerCase();
  final match = _resolutionPattern.firstMatch(title);
  if (match == null) return null;
  final text = match.group(0)!.toLowerCase();
  if (text.contains('2160') || text == '4k') return '2160p';
  if (text.contains('1080')) return '1080p';
  if (text.contains('720')) return '720p';
  return '480p';
}

final _episodePatterns = [
  RegExp(r'\[(\d{1,4})(?:v\d)?(?:END)?\]', caseSensitive: false),
  RegExp(r'【(\d{1,4})(?:v\d)?】'),
  RegExp(r'第(\d{1,4})[话話集]'),
  RegExp(r'\s-\s(\d{1,4})(?:v\d)?(?:\s|$|\[)'),
  RegExp(r'\bE[Pp]?(\d{1,4})\b'),
];

/// 从资源 / 下载标题里认集数，认不出返回 null。
int? episodeOf(String title) {
  for (final pattern in _episodePatterns) {
    final match = pattern.firstMatch(title);
    if (match != null) {
      final n = int.tryParse(match.group(1)!);
      // 年份、分辨率之类的数字不算集数
      if (n != null && n > 0 && n < 2000) return n;
    }
  }
  return null;
}

/// 下载任务标题里的番名：去掉字幕组、集数、规格这些方括号块，挑最像名字的一段。
String animeNameOf(String title) {
  final brackets = RegExp(r'[\[【]([^\]】]+)[\]】]').allMatches(title).map((m) => m.group(1)!.trim()).toList();
  final rest = title.replaceAll(RegExp(r'[\[【][^\]】]*[\]】]'), ' ').replaceAll(RegExp(r'\s-\s\d+.*$'), '').trim();
  final candidates = [if (rest.isNotEmpty) rest, ...brackets.skip(1)];
  for (final candidate in candidates) {
    if (RegExp(r'^(\d+|v\d|\d{3,4}p|.*(HEVC|AVC|AAC|x26[45]|WebRip|BDRip|简|繁|CHS|CHT|MP4|MKV).*)$', caseSensitive: false)
        .hasMatch(candidate)) {
      continue;
    }
    return candidate.split(RegExp(r'\s/\s')).first.trim();
  }
  return title;
}
