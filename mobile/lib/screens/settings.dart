import 'package:flutter/material.dart';

import '../api.dart';
import '../main.dart';
import '../theme.dart';
import '../widgets.dart';

class SettingsScreen extends StatelessWidget {
  const SettingsScreen({super.key});

  Future<void> _changeServer(BuildContext context) async {
    final session = SessionScope.read(context);
    final api = session.api!;
    final server = TextEditingController(text: api.baseUrl);
    final token = TextEditingController(text: api.token);
    final result = await showDialog<bool>(
      context: context,
      builder: (dialogContext) => _ServerDialog(server: server, token: token),
    );
    server.dispose();
    token.dispose();
    if (result == true && context.mounted) showToast(context, '已切换服务器');
  }

  Future<void> _logout(BuildContext context) async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('退出登录？'),
        content: const Text('会清除本机保存的访问口令，服务器上的数据不受影响。'),
        actions: [
          TextButton(onPressed: () => Navigator.pop(context, false), child: const Text('取消')),
          FilledButton(
            style: FilledButton.styleFrom(backgroundColor: AppColors.err, foregroundColor: Colors.white),
            onPressed: () => Navigator.pop(context, true),
            child: const Text('退出登录'),
          ),
        ],
      ),
    );
    if (confirmed == true && context.mounted) await SessionScope.read(context).logout();
  }

  @override
  Widget build(BuildContext context) {
    final api = SessionScope.of(context).api;
    return Scaffold(
      appBar: AppBar(title: const Text('设置')),
      body: ListView(
        children: [
          const _GroupLabel('服务器'),
          ListTile(
            leading: const Icon(Icons.dns_outlined),
            title: const Text('服务器地址'),
            subtitle: Text(api?.baseUrl ?? '', style: const TextStyle(color: AppColors.muted)),
            trailing: const Icon(Icons.chevron_right),
            onTap: api == null ? null : () => _changeServer(context),
          ),
          const _GroupLabel('账号'),
          ListTile(
            leading: const Icon(Icons.logout, color: AppColors.err),
            title: const Text('退出登录', style: TextStyle(color: AppColors.err)),
            onTap: () => _logout(context),
          ),
          const _GroupLabel('关于'),
          const ListTile(
            leading: Icon(Icons.info_outline),
            title: Text('猫追番'),
            subtitle: Text('版本 0.1.0 · 数据都在你自己的服务器上', style: TextStyle(color: AppColors.muted)),
          ),
        ],
      ),
    );
  }
}

class _GroupLabel extends StatelessWidget {
  const _GroupLabel(this.text);

  final String text;

  @override
  Widget build(BuildContext context) => Padding(
        padding: const EdgeInsets.fromLTRB(16, 20, 16, 6),
        child: Text(text, style: const TextStyle(fontSize: 13, color: AppColors.muted)),
      );
}

class _ServerDialog extends StatefulWidget {
  const _ServerDialog({required this.server, required this.token});

  final TextEditingController server;
  final TextEditingController token;

  @override
  State<_ServerDialog> createState() => _ServerDialogState();
}

class _ServerDialogState extends State<_ServerDialog> {
  bool _busy = false;
  String? _error;

  Future<void> _save() async {
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      await SessionScope.read(context).login(widget.server.text, widget.token.text);
      if (mounted) Navigator.pop(context, true);
    } on ApiException catch (e) {
      if (mounted) setState(() => _error = e.message);
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: const Text('服务器'),
      content: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          TextField(
            controller: widget.server,
            keyboardType: TextInputType.url,
            autocorrect: false,
            decoration: const InputDecoration(labelText: '服务器地址'),
          ),
          const SizedBox(height: 12),
          TextField(
            controller: widget.token,
            obscureText: true,
            autocorrect: false,
            decoration: const InputDecoration(labelText: '访问口令'),
          ),
          if (_error != null)
            Padding(
              padding: const EdgeInsets.only(top: 12),
              child: Text(_error!, style: const TextStyle(color: AppColors.err, fontSize: 13)),
            ),
        ],
      ),
      actions: [
        TextButton(onPressed: _busy ? null : () => Navigator.pop(context, false), child: const Text('取消')),
        FilledButton(
          onPressed: _busy ? null : _save,
          child: _busy
              ? const SizedBox(width: 16, height: 16, child: CircularProgressIndicator(strokeWidth: 2))
              : const Text('保存并连接'),
        ),
      ],
    );
  }
}
