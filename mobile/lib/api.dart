import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:http/http.dart' as http;

import 'models.dart';

/// 后端统一返回 `{ok, data}` / `{ok:false, error:{code,message}}`，失败时抛这个。
class ApiException implements Exception {
  ApiException(this.message, [this.code]);

  final String message;
  final String? code;

  bool get isAuthInvalid => code == 'AUTH_INVALID';

  @override
  String toString() => message;
}

/// 规范化用户填的服务器地址：补协议、去掉末尾斜杠。
String normalizeServerUrl(String input) {
  var value = input.trim();
  if (value.isEmpty) return value;
  if (!value.startsWith(RegExp(r'https?://', caseSensitive: false))) {
    value = 'http://$value';
  }
  return value.replaceAll(RegExp(r'/+$'), '');
}

class ApiClient {
  ApiClient({required this.baseUrl, required this.token, http.Client? client})
      : _client = client ?? http.Client();

  final String baseUrl;
  final String token;
  final http.Client _client;

  static const _defaultTimeout = Duration(seconds: 15);

  Uri _uri(String path, [Map<String, String>? query]) {
    final uri = Uri.parse('$baseUrl$path');
    return query == null ? uri : uri.replace(queryParameters: query);
  }

  /// 播放流、HLS 分片、字幕、缩略图这几类地址后端认 `?token=`，播放器发不了自定义请求头时用它。
  String withToken(String path) {
    final separator = path.contains('?') ? '&' : '?';
    return '$baseUrl$path${separator}token=${Uri.encodeQueryComponent(token)}';
  }

  /// 下载任务的封面：`/api/...` 开头的是后端生成的缩略图，要带口令。
  String? resolveImage(String? url) {
    if (url == null || url.isEmpty) return null;
    if (url.startsWith('/api/')) return withToken(url);
    if (url.startsWith('//')) return 'https:$url';
    return url;
  }

  Future<dynamic> _request(
    String method,
    String path, {
    Map<String, String>? query,
    Object? body,
    Duration timeout = _defaultTimeout,
  }) async {
    final request = http.Request(method, _uri(path, query));
    request.headers['Content-Type'] = 'application/json';
    request.headers['Accept'] = 'application/json';
    if (token.isNotEmpty) request.headers['Authorization'] = 'Bearer $token';
    if (body != null) request.body = jsonEncode(body);

    http.Response response;
    try {
      final streamed = await _client.send(request).timeout(timeout);
      response = await http.Response.fromStream(streamed).timeout(timeout);
    } on TimeoutException {
      throw ApiException('请求超时，请检查服务器地址和网络', 'REQUEST_TIMEOUT');
    } on SocketException {
      throw ApiException('连不上服务器，请检查地址是否正确、后端是否在运行', 'NETWORK_ERROR');
    } on http.ClientException {
      throw ApiException('连不上服务器，请检查地址是否正确、后端是否在运行', 'NETWORK_ERROR');
    } on HandshakeException {
      throw ApiException('HTTPS 握手失败，请确认服务器证书', 'NETWORK_ERROR');
    } on ArgumentError {
      throw ApiException('服务器地址格式不对', 'INVALID_URL');
    }

    dynamic payload;
    try {
      payload = jsonDecode(utf8.decode(response.bodyBytes));
    } catch (_) {
      throw ApiException('服务器返回了无法识别的内容（HTTP ${response.statusCode}），地址是不是填错了？', 'INVALID_RESPONSE');
    }
    if (payload is! Map || payload['ok'] != true) {
      final error = payload is Map ? payload['error'] : null;
      final code = error is Map ? error['code']?.toString() : null;
      final message = error is Map ? error['message']?.toString() : null;
      throw ApiException(_friendlyMessage(code, message, response.statusCode), code);
    }
    return payload['data'];
  }

  static String _friendlyMessage(String? code, String? message, int status) {
    if (code == 'AUTH_INVALID') return '访问口令不对，请到服务器的「设置 → 访问口令」里核对后重新填写';
    if (message != null && message.isNotEmpty) return message;
    return '请求失败（HTTP $status）';
  }

  // ---- 鉴权 ----

  Future<void> health() async {
    await _request('GET', '/api/health', timeout: const Duration(seconds: 8));
  }

  Future<void> tokenLogin() async {
    await _request('POST', '/api/auth/token-login', body: {'token': token});
  }

  // ---- 发现 ----

  Future<List<Anime>> homepage() async {
    final data = await _request('GET', '/api/discovery/home', timeout: const Duration(seconds: 30));
    return (data as List).map((e) => Anime.fromJson(e as Map<String, dynamic>)).toList();
  }

  Future<List<Anime>> search(String keyword) async {
    final data = await _request('GET', '/api/discovery/search',
        query: {'keyword': keyword}, timeout: const Duration(seconds: 25));
    final items = (data as Map)['items'] as List? ?? const [];
    return items.map((e) => Anime.fromJson(e as Map<String, dynamic>)).toList();
  }

  Future<AnimeDetail> animeDetail(String animeId) async {
    final data = await _request('GET', '/api/discovery/anime/${Uri.encodeComponent(animeId)}',
        timeout: const Duration(seconds: 25));
    return AnimeDetail.fromJson(data as Map<String, dynamic>);
  }

  // ---- 收藏与订阅 ----

  Future<List<FavoriteAnime>> favorites() async {
    final data = await _request('GET', '/api/favorites');
    return (data as List).map((e) => FavoriteAnime.fromJson(e as Map<String, dynamic>)).toList();
  }

  Future<void> addFavorite(Anime anime) async {
    await _request('POST', '/api/favorites',
        body: {'anime': anime.raw, 'mikanKeyword': anime.searchKeyword ?? anime.title});
  }

  Future<void> removeFavorite(String animeId) async {
    await _request('DELETE', '/api/favorites/${Uri.encodeComponent(animeId)}');
  }

  /// 收藏并订阅（后端幂等），新集出来自动下载。
  Future<void> subscribe(Anime anime, {String? keyword}) async {
    await _request('POST', '/api/favorites/subscribe',
        body: {'anime': anime.raw, 'mikanKeyword': keyword ?? anime.searchKeyword ?? anime.title},
        timeout: const Duration(seconds: 30));
  }

  Future<void> deleteSubscription(int id) async {
    await _request('DELETE', '/api/subscriptions/$id');
  }

  // ---- 下载 ----

  Future<List<DownloadJob>> downloads() async {
    final data = await _request('GET', '/api/downloads');
    return (data as List).map((e) => DownloadJob.fromJson(e as Map<String, dynamic>)).toList();
  }

  Future<void> createDownload(ResourceItem item, Anime anime) async {
    final url = item.torrentUrl ?? item.magnetUrl;
    if (url == null) throw ApiException('这条资源没有可用的下载地址');
    await _request('POST', '/api/downloads', body: {
      'url': url,
      'title': item.title,
      'imageUrl': anime.imageUrl,
      'animeId': anime.animeId,
      if (item.pageUrl != null) 'pageUrl': item.pageUrl,
    }, timeout: const Duration(seconds: 30));
  }

  Future<void> pauseDownload(String gid) =>
      _request('POST', '/api/downloads/${Uri.encodeComponent(gid)}/pause');

  /// 开始边下边播：让后端把这个任务改成按顺序下载。
  Future<void> makeSequential(String gid) =>
      _request('POST', '/api/downloads/${Uri.encodeComponent(gid)}/sequential');

  Future<void> resumeDownload(String gid) =>
      _request('POST', '/api/downloads/${Uri.encodeComponent(gid)}/resume');

  Future<void> removeDownload(String gid, {bool deleteLocalFiles = false}) => _request(
      'DELETE', '/api/downloads/${Uri.encodeComponent(gid)}',
      body: {'deleteLocalFiles': deleteLocalFiles});

  // ---- 播放 ----

  Future<PlaybackInfo> playbackInfo(String gid) async {
    final data = await _request('GET', '/api/downloads/${Uri.encodeComponent(gid)}/playback',
        timeout: const Duration(seconds: 30));
    return PlaybackInfo.fromJson(data as Map<String, dynamic>);
  }

  String streamUrl(String gid) => withToken('/api/downloads/${Uri.encodeComponent(gid)}/stream');

  String hlsUrl(String gid) => withToken('/api/downloads/${Uri.encodeComponent(gid)}/hls/master.m3u8');

  /// ASS 轨原样取回，自己解析渲染；转成 WebVTT 会丢掉样式和定位。
  Future<String> assSubtitle(String gid, String trackId) async {
    final uri = Uri.parse(withToken(
        '/api/downloads/${Uri.encodeComponent(gid)}/subtitles/${Uri.encodeComponent(trackId)}.ass'));
    final response = await _client.get(uri).timeout(const Duration(minutes: 2));
    if (response.statusCode != 200) throw ApiException('字幕提取失败');
    return utf8.decode(response.bodyBytes, allowMalformed: true);
  }
}
