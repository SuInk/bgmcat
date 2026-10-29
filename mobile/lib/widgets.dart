import 'package:flutter/material.dart';

import 'theme.dart';

void showToast(BuildContext context, String message, {bool error = false}) {
  final messenger = ScaffoldMessenger.maybeOf(context);
  messenger?.hideCurrentSnackBar();
  messenger?.showSnackBar(SnackBar(
    content: Text(message),
    backgroundColor: error ? AppColors.err : AppColors.panel,
    duration: const Duration(seconds: 3),
  ));
}

/// 圆角封面图，加载失败或没有图时显示占位。
class CoverImage extends StatelessWidget {
  const CoverImage({super.key, required this.url, this.radius = 10, this.aspectRatio, this.fit = BoxFit.cover});

  final String? url;
  final double radius;
  final double? aspectRatio;
  final BoxFit fit;

  @override
  Widget build(BuildContext context) {
    Widget placeholder() => Container(
          color: AppColors.panel,
          alignment: Alignment.center,
          child: const Icon(Icons.movie_outlined, color: AppColors.muted, size: 32),
        );
    Widget child = url == null
        ? placeholder()
        : Image.network(
            url!,
            fit: fit,
            width: double.infinity,
            height: double.infinity,
            errorBuilder: (_, _, _) => placeholder(),
            loadingBuilder: (context, child, progress) => progress == null ? child : Container(color: AppColors.panel),
          );
    child = ClipRRect(borderRadius: BorderRadius.circular(radius), child: child);
    return aspectRatio == null ? child : AspectRatio(aspectRatio: aspectRatio!, child: child);
  }
}

/// 加载失败的整页提示，带重试。
class ErrorView extends StatelessWidget {
  const ErrorView({super.key, required this.message, required this.onRetry});

  final String message;
  final VoidCallback onRetry;

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(32),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            const Icon(Icons.cloud_off_outlined, size: 40, color: AppColors.muted),
            const SizedBox(height: 12),
            Text(message, textAlign: TextAlign.center, style: const TextStyle(color: AppColors.inkSecondary)),
            const SizedBox(height: 16),
            FilledButton.tonal(onPressed: onRetry, child: const Text('重试')),
          ],
        ),
      ),
    );
  }
}

class SectionTitle extends StatelessWidget {
  const SectionTitle(this.text, {super.key, this.trailing});

  final String text;
  final Widget? trailing;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 20, 16, 10),
      child: Row(
        children: [
          Expanded(child: Text(text, style: const TextStyle(fontSize: 17, fontWeight: FontWeight.w700))),
          ?trailing,
        ],
      ),
    );
  }
}
