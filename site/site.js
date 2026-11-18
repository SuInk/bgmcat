(function () {
  var REPO = "SuInk/bgmcat";
  var DEFAULT_VER = "0.2.0";

  var DOC_PAGES = [
    {
      file: "guide.html",
      slug: "guide",
      title: "快速上手",
      sections: [
        ["workflow", "多端协同工作流", "电脑 桌面 网盘 手机 网页 自动下载 播放"],
        ["desktop", "安装电脑桌面端", "macOS Windows Linux DMG EXE AppImage 内置引擎"],
        ["discovery", "番剧发现与全库筛选", "本季 全库 类型 地区 年份 季度 风格 评分"],
        ["subscribe", "RSS 自动追番规则", "蜜柑 Mikan 字幕组 1080p 合集 自动下载"]
      ]
    },
    {
      file: "cloud.html",
      slug: "cloud",
      title: "网盘归档",
      sections: [
        ["oauth", "连接 Google Drive", "OAuth 客户端 回调 文件夹 授权"],
        ["archive", "分块上传与空间释放", "8MiB 断点续传 MD5 校验 自动删除 本地空间"],
        ["stream", "云端直连按需播放", "Range 流式播放 转码 字幕 HLS FFmpeg"],
        ["direct-ip", "Drive IP 直连探测", "SNI TLS 测速 优选 IP"]
      ]
    },
    {
      file: "mobile.html",
      slug: "mobile",
      title: "手机与网页播放",
      sections: [
        ["lan", "开启局域网手机直连", "Wi-Fi Tailscale 局域网 端口 39123"],
        ["tunnel", "Cloudflare Tunnel 穿透", "外网 远程 公网 trycloudflare 命名隧道"],
        ["android", "Android 客户端使用", "APK 主备地址 自动切换 横屏全屏 常亮"],
        ["pwa", "iOS 与网页端 PWA", "Safari Chrome 添加到主屏幕 全屏应用"]
      ]
    },
    {
      file: "deploy.html",
      slug: "deploy",
      title: "服务端与运维",
      sections: [
        ["installer", "命令行一键安装", "Linux macOS Windows install.sh install.ps1 SHA256"],
        ["docker", "Docker 容器部署", "docker compose 持久化卷 ghcr"],
        ["security", "账号密码与会话安全", "PBKDF2 reset-password 登录 踢下线"],
        ["mcp", "Diana MCP 与发布流水线", "MCP stdio GitHub Actions Release 签名"]
      ]
    }
  ];

  function currentPageKey() {
    var name = (window.location.pathname.split("/").pop() || "index.html").replace(/\.html$/, "");
    return name || "index";
  }

  function themeLabel(mode) {
    return mode === "dark" ? "深色" : mode === "light" ? "浅色" : "自动";
  }

  function renderNav() {
    var mount = document.querySelector("[data-site-nav]");
    if (!mount) return;
    var key = currentPageKey();
    var isDocs = DOC_PAGES.some(function (p) { return p.slug === key; });
    mount.innerHTML =
      '<nav class="site-nav">' +
      '  <div class="wrap nav-inner">' +
      '    <a class="brand" href="./index.html">' +
      '      <img src="./assets/icon-192.png" alt="" width="28" height="28" />' +
      '      <span>猫追番 · Bangumi Cat</span>' +
      '      <small data-version-badge>v' + DEFAULT_VER + '</small>' +
      '    </a>' +
      '    <div class="nav-links">' +
      '      <a class="nav-link" href="./download.html"' + (key === "download" ? ' aria-current="page"' : '') + '>下载客户端</a>' +
      '      <a class="nav-link" href="./guide.html"' + (isDocs ? ' aria-current="page"' : '') + '>使用文档</a>' +
      '      <a class="nav-link" href="./demo/"><span class="nav-label-hide-sm">在线</span>演示</a>' +
      '      <a class="nav-link" href="https://github.com/' + REPO + '" target="_blank" rel="noopener">GitHub</a>' +
      '      <button class="nav-link" type="button" data-theme-btn title="切换外观主题">' +
      '        <span data-theme-label>' + themeLabel(window.BgmcatTheme ? window.BgmcatTheme.getMode() : "system") + '</span>' +
      '      </button>' +
      '    </div>' +
      '  </div>' +
      '</nav>';

    var btn = mount.querySelector("[data-theme-btn]");
    var labelEl = mount.querySelector("[data-theme-label]");
    if (btn && window.BgmcatTheme) {
      btn.addEventListener("click", function () {
        var next = window.BgmcatTheme.cycle();
        if (labelEl) labelEl.textContent = themeLabel(next);
      });
    }
  }

  function renderFooter() {
    var mount = document.querySelector("[data-site-footer]");
    if (!mount) return;
    mount.innerHTML =
      '<footer class="site-footer">' +
      '  <div class="wrap footer-inner">' +
      '    <span>猫追番 · Bangumi Cat — 电脑自动追番归档网盘，手机与网页随时点播（Limited Redistribution License · SuInk）</span>' +
      '    <div class="footer-links">' +
      '      <a href="./download.html">多端下载</a>' +
      '      <a href="./guide.html">文档中心</a>' +
      '      <a href="./demo/">在线演示</a>' +
      '      <a href="https://github.com/' + REPO + '/releases" target="_blank" rel="noopener">Releases</a>' +
      '      <a href="https://github.com/' + REPO + '" target="_blank" rel="noopener">GitHub</a>' +
      '    </div>' +
      '  </div>' +
      '</footer>';
  }

  function renderDocsSidebar() {
    var mount = document.querySelector("[data-docs-sidebar]");
    if (!mount) return;
    var key = currentPageKey();
    var html =
      '<div class="docs-search">' +
      '  <input type="search" placeholder="搜索文档章节（如 网盘、APK、穿透）…" aria-label="搜索文档章节" data-docs-search />' +
      '  <p class="docs-search-status" data-docs-search-status></p>' +
      '</div>';

    DOC_PAGES.forEach(function (page) {
      var activePage = page.slug === key;
      html += '<div class="docs-nav-group">';
      html += '<p class="docs-nav-title">' + page.title + '</p>';
      page.sections.forEach(function (sec, idx) {
        var isFirst = activePage && idx === 0 && !window.location.hash;
        var href = "./" + page.file + "#" + sec[0];
        var searchText = (page.title + " " + sec[1] + " " + sec[2]).toLowerCase();
        html +=
          '<a class="docs-nav-link' + (isFirst ? " is-active" : "") + '" href="' + href + '" data-search="' + searchText + '">' +
          sec[1] +
          '</a>';
      });
      html += '</div>';
    });

    mount.innerHTML = html;

    var input = mount.querySelector("[data-docs-search]");
    var status = mount.querySelector("[data-docs-search-status]");
    var links = Array.prototype.slice.call(mount.querySelectorAll(".docs-nav-link"));

    function syncActiveHash() {
      var hash = window.location.hash;
      links.forEach(function (a, idx) {
        var href = a.getAttribute("href") || "";
        var samePage = href.indexOf("./" + key + ".html#") === 0;
        var active = samePage && (hash ? href.slice(href.indexOf("#")) === hash : idx === links.findIndex(function (x) {
          return (x.getAttribute("href") || "").indexOf("./" + key + ".html#") === 0;
        }));
        a.classList.toggle("is-active", active);
      });
    }
    syncActiveHash();
    window.addEventListener("hashchange", syncActiveHash);

    if (input) {
      input.addEventListener("input", function () {
        var q = input.value.trim().toLowerCase();
        var count = 0;
        links.forEach(function (a) {
          var match = !q || (a.getAttribute("data-search") || "").indexOf(q) !== -1;
          a.style.display = match ? "block" : "none";
          if (match) count++;
        });
        if (status) status.textContent = q ? "匹配 " + count + " 个章节" : "";
      });
    }
  }

  function bindTabsAndCopy() {
    document.querySelectorAll("[data-code-tabs]").forEach(function (root) {
      var tabs = root.querySelectorAll("[data-tab]");
      var panels = root.querySelectorAll("[data-panel]");
      tabs.forEach(function (tab) {
        tab.addEventListener("click", function () {
          var target = tab.getAttribute("data-tab");
          tabs.forEach(function (t) {
            t.setAttribute("aria-selected", t === tab ? "true" : "false");
          });
          panels.forEach(function (p) {
            p.hidden = p.getAttribute("data-panel") !== target;
          });
        });
      });
    });

    document.querySelectorAll("[data-copy-btn]").forEach(function (btn) {
      btn.addEventListener("click", function () {
        var card = btn.closest(".code-card");
        if (!card) return;
        var pre = card.querySelector("pre:not([hidden])") || card.querySelector("pre");
        if (!pre) return;
        var text = pre.innerText.trim();
        if (navigator.clipboard && navigator.clipboard.writeText) {
          navigator.clipboard.writeText(text).then(function () {
            var old = btn.textContent;
            btn.textContent = "已复制";
            setTimeout(function () { btn.textContent = old; }, 1400);
          });
        }
      });
    });
  }

  function detectVisitorPlatform() {
    var ua = navigator.userAgent || "";
    var plat = (navigator.platform || "").toLowerCase();
    if (/android/i.test(ua)) {
      return { key: "android", label: "Android 手机客户端 (.apk)", anchor: "#dl-mobile" };
    }
    if (/iphone|ipad|ipod/i.test(ua)) {
      return { key: "ios", label: "iOS 网页端 / 添加到主屏幕 (PWA)", anchor: "#dl-mobile" };
    }
    if (/win/i.test(plat) || /windows/i.test(ua)) {
      return { key: "win-x64", label: "Windows 桌面版 (.exe)", anchor: "#dl-desktop" };
    }
    if (/mac/i.test(plat) || /macintosh/i.test(ua)) {
      return { key: "mac-arm64", label: "macOS 桌面版 (.dmg)", anchor: "#dl-desktop" };
    }
    if (/linux/i.test(plat) || /linux/i.test(ua)) {
      return { key: "linux-x64", label: "Linux 桌面版 (.AppImage / .deb)", anchor: "#dl-desktop" };
    }
    return { key: "mac-arm64", label: "多平台客户端下载", anchor: "#dl-desktop" };
  }

  function enhanceDownloadWidgets() {
    var detected = detectVisitorPlatform();
    var osHint = document.querySelector("[data-detected-os]");
    if (osHint) {
      osHint.textContent = "已识别当前设备推荐：" + detected.label;
    }
    var recTitle = document.querySelector("[data-recommended-title]");
    if (recTitle) {
      recTitle.textContent = detected.label;
    }
    if (detected.key === "android" || detected.key === "ios") {
      var mobileCard = document.getElementById("dl-mobile");
      if (mobileCard) mobileCard.classList.add("is-recommended");
    } else {
      var desktopCard = document.getElementById("dl-desktop");
      if (desktopCard) desktopCard.classList.add("is-recommended");
    }

    // Query GitHub latest release API: automatically upgrades buttons to direct asset URLs once public!
    fetch("https://api.github.com/repos/" + REPO + "/releases/latest", { headers: { Accept: "application/vnd.github+json" } })
      .then(function (res) {
        if (!res.ok) throw new Error("private_or_unreleased");
        return res.json();
      })
      .then(function (release) {
        if (!release || !release.tag_name) return;
        var ver = String(release.tag_name).replace(/^v/, "");
        document.querySelectorAll("[data-version-badge]").forEach(function (el) {
          el.textContent = "v" + ver;
        });
        var stateEl = document.querySelector("[data-release-state]");
        if (stateEl) {
          stateEl.className = "status-chip ok";
          stateEl.textContent = "最新正式版 " + release.tag_name + " · 支持一键直链下载";
        }
        var assets = Array.isArray(release.assets) ? release.assets : [];
        document.querySelectorAll("[data-asset-suffix]").forEach(function (link) {
          var suffix = link.getAttribute("data-asset-suffix");
          var hit = assets.find(function (a) {
            return a && typeof a.name === "string" && a.name.indexOf(suffix) !== -1;
          });
          if (hit && hit.browser_download_url) {
            link.setAttribute("href", hit.browser_download_url);
            link.textContent = "直接下载";
          }
        });
      })
      .catch(function () {
        var stateEl = document.querySelector("[data-release-state]");
        if (stateEl) {
          stateEl.className = "status-chip warn";
          stateEl.textContent = "v" + DEFAULT_VER + " · 可前往 GitHub Releases 获取安装包或使用下方一键脚本安装";
        }
      });
  }

  renderNav();
  renderDocsSidebar();
  renderFooter();
  bindTabsAndCopy();
  enhanceDownloadWidgets();
})();
