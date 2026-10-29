import 'package:bgmcat/ass.dart';
import 'package:bgmcat/util.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('parses ASS dialogue with overrides and top alignment', () {
    const source = '''
[V4+ Styles]
Format: Name, Fontname, Fontsize, Alignment
Style: Default,Arial,20,2
Style: Note,Arial,16,8

[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
Dialogue: 0,0:00:01.50,0:00:03.00,Default,,0,0,0,,{\\b1}你好，世界\\N第二行
Dialogue: 0,0:00:02.00,0:00:04.00,Note,,0,0,0,,注释
Dialogue: 0,0:00:02.00,0:00:04.00,Default,,0,0,0,,{\\p1}m 0 0 l 1 1
''';
    final sub = AssSubtitle.parse(source);
    expect(sub.cues.length, 2);
    final cues = sub.at(const Duration(milliseconds: 2500));
    expect(cues.map((c) => c.text), ['你好，世界\n第二行', '注释']);
    expect(cues.last.top, isTrue);
    expect(sub.at(const Duration(seconds: 5)), isEmpty);
    expect(sub.playResY, 288);
  });

  test('keeps \\pos placement, font size and colour for typeset signs', () {
    const source = '''
[Script Info]
PlayResX: 1920
PlayResY: 1080

[V4+ Styles]
Format: Name, Fontname, Fontsize, PrimaryColour, Outline, Alignment
Style: Screen,Arial,70,&H00000000,0,7

[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
Dialogue: 1,0:00:11.45,0:00:14.62,Screen,,0,0,0,,{\\pos(1419.333,717)\\fs28}说实话这事我本不想写的
''';
    final cue = AssSubtitle.parse(source).cues.single;
    expect(cue.pos!.dx, closeTo(1419.333, 0.001));
    expect(cue.fontSize, 28);
    expect(cue.align, 7);
    expect(cue.outline, isFalse);
    expect(cue.color.toARGB32(), 0xFF000000);
  });

  test('recognizes episode, resolution and anime name from titles', () {
    const title = '【悠哈璃羽字幕社】[FX战士久留美 / FX Senshi Kurumi-chan][04][1080p HEVC][CHS_JP&CHT_JP][简繁日内封]';
    expect(episodeOf(title), 4);
    expect(resolutionOf(title), '1080p');
    expect(animeNameOf(title), 'FX战士久留美');
    expect(episodeOf('[Lilith-Raws] Some Show - 07 [Baha][WEB-DL][1080p]'), 7);
  });
}
