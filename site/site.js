(function () {
  var REPO = "SuInk/bgmcat";
  var DEFAULT_VER = "0.2.1";
  var RELEASES = "https://github.com/" + REPO + "/releases";

  // 一键安装命令只在这里写一份，页面里用 data-install-cmd="sh|ps|docker" 引用。
  var INSTALL = {
    sh: "curl -fsSL https://raw.githubusercontent.com/" + REPO + "/main/scripts/install.sh | sh -s -- --start",
    ps: "irm https://raw.githubusercontent.com/" + REPO + "/main/scripts/install.ps1 | iex",
    docker: "git clone https://github.com/" + REPO + ".git\ncd bgmcat\ndocker compose up -d --build"
  };

  // 线条图标（24×24，跟随文字颜色）。
  var ICON_PATHS = {
    download: '<path d="M12 4v11"/><path d="m7 10 5 5 5-5"/><path d="M5 20h14"/>',
    book: '<path d="M5 5.5A2.5 2.5 0 0 1 7.5 3H19v15H7.5A2.5 2.5 0 0 0 5 20.5z"/><path d="M5 20.5A2.5 2.5 0 0 1 7.5 18H19v3H7.5"/>',
    play: '<circle cx="12" cy="12" r="9"/><path d="m10 8.5 5.5 3.5-5.5 3.5z"/>',
    code: '<path d="m8 7-5 5 5 5"/><path d="m16 7 5 5-5 5"/><path d="m13.5 4-3 16"/>',
    sun: '<circle cx="12" cy="12" r="4"/><path d="M12 2.5v2M12 19.5v2M4.6 4.6l1.4 1.4M18 18l1.4 1.4M2.5 12h2M19.5 12h2M4.6 19.4 6 18M18 6l1.4-1.4"/>',
    moon: '<path d="M20 14.5A8 8 0 1 1 9.5 4a6.5 6.5 0 0 0 10.5 10.5z"/>',
    auto: '<circle cx="12" cy="12" r="9"/><path d="M12 3a9 9 0 0 0 0 18z" fill="currentColor"/>',
    laptop: '<rect x="4" y="5" width="16" height="11" rx="1.5"/><path d="M2 19.5h20"/>',
    windows: '<path d="M3.5 5.5 10.5 4.5v7h-7zM12.5 4.2 20.5 3v8.5h-8zM3.5 12.5h7v7l-7-1zM12.5 12.5h8V21l-8-1.2z" fill="currentColor" stroke="none"/>',
    terminal: '<rect x="3" y="4.5" width="18" height="15" rx="2"/><path d="m7 9.5 3 2.5-3 2.5"/><path d="M12.5 15h4.5"/>',
    android: '<path d="M5 17v-2a7 7 0 0 1 14 0v2z"/><path d="M8.5 9.3 7 6.8M15.5 9.3 17 6.8"/><circle cx="9.5" cy="13.2" r=".8" fill="currentColor"/><circle cx="14.5" cy="13.2" r=".8" fill="currentColor"/>',
    phone: '<rect x="7" y="2.5" width="10" height="19" rx="2.2"/><path d="M11 18.5h2"/>',
    server: '<rect x="3.5" y="4" width="17" height="7" rx="1.5"/><rect x="3.5" y="13" width="17" height="7" rx="1.5"/><path d="M7.5 7.5h.01M7.5 16.5h.01"/>',
    help: '<circle cx="12" cy="12" r="9"/><path d="M9.6 9.3a2.5 2.5 0 0 1 4.8.9c0 1.7-2.4 2.2-2.4 3.6"/><path d="M12 17h.01"/>',
    external: '<path d="M14 4h6v6"/><path d="M20 4 11 13"/><path d="M18 14v5a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V7a1 1 0 0 1 1-1h5"/>'
  };
  function icon(name, cls) {
    return '<svg class="icon' + (cls ? " " + cls : "") + '" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">' + (ICON_PATHS[name] || "") + "</svg>";
  }

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
    return mode === "dark" ? "深色" : mode === "light" ? "浅色" : "跟随系统";
  }
  function themeIcon(mode) {
    return icon(mode === "dark" ? "moon" : mode === "light" ? "sun" : "auto");
  }

  function renderNav() {
    var mount = document.querySelector("[data-site-nav]");
    if (!mount) return;
    var key = currentPageKey();
    var isDocs = DOC_PAGES.some(function (p) { return p.slug === key; });
    var mode = window.BgmcatTheme ? window.BgmcatTheme.getMode() : "system";
    function link(href, iconName, label, current, external) {
      return '      <a class="nav-link" href="' + href + '"' + (current ? ' aria-current="page"' : "") +
        (external ? ' target="_blank" rel="noopener"' : "") + ' title="' + label + '">' +
        icon(iconName) + '<span class="nav-label">' + label + "</span></a>";
    }
    mount.innerHTML =
      '<nav class="site-nav">' +
      '  <div class="wrap nav-inner">' +
      '    <a class="brand" href="./index.html">' +
      '      <img src="./assets/icon-192.png" alt="" width="28" height="28" />' +
      '      <span>猫追番<span class="brand-en"> · Bangumi Cat</span></span>' +
      '      <small data-version-badge>v' + DEFAULT_VER + '</small>' +
      '    </a>' +
      '    <div class="nav-links">' +
      link("./download.html", "download", "下载", key === "download") +
      link("./guide.html", "book", "文档", isDocs) +
      link("./demo/", "play", "在线演示", false) +
      link("https://github.com/" + REPO, "code", "GitHub", false, true) +
      '      <button class="nav-link nav-icon-btn" type="button" data-theme-btn title="外观：' + themeLabel(mode) + '（点击切换）" aria-label="切换外观，当前' + themeLabel(mode) + '">' +
      themeIcon(mode) +
      '      </button>' +
      '    </div>' +
      '  </div>' +
      '</nav>';

    var btn = mount.querySelector("[data-theme-btn]");
    if (btn && window.BgmcatTheme) {
      btn.addEventListener("click", function () {
        var next = window.BgmcatTheme.cycle();
        btn.innerHTML = themeIcon(next);
        btn.title = "外观：" + themeLabel(next) + "（点击切换）";
        btn.setAttribute("aria-label", "切换外观，当前" + themeLabel(next));
      });
    }
  }

  function renderInstallCommands() {
    document.querySelectorAll("[data-install-cmd]").forEach(function (el) {
      var cmd = INSTALL[el.getAttribute("data-install-cmd")];
      if (cmd) el.textContent = cmd;
    });
  }

  function renderIcons() {
    document.querySelectorAll("[data-icon]").forEach(function (el) {
      el.insertAdjacentHTML("afterbegin", icon(el.getAttribute("data-icon")));
    });
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

  // 下载页：每个按钮用 data-asset 指向一种安装包，拿到最新正式版后换成直链并显示大小。
  var ASSETS = {
    "mac-arm64": { re: /-mac-arm64\.dmg$/, label: "macOS · Apple 芯片", name: "Mac 版（Apple 芯片）", icon: "laptop", ext: ".dmg" },
    "mac-x64": { re: /-mac-x64\.dmg$/, label: "macOS · Intel 芯片", name: "Mac 版（Intel 芯片）", icon: "laptop", ext: ".dmg" },
    "win-x64": { re: /-win-x64\.exe$/, label: "Windows 10 / 11", name: "Windows 版", icon: "windows", ext: ".exe" },
    "linux-appimage": { re: /\.AppImage$/, label: "Linux", name: "Linux 版", icon: "terminal", ext: ".AppImage" },
    "linux-deb": { re: /\.deb$/, label: "Ubuntu / Debian", name: "Linux 版（.deb）", icon: "terminal", ext: ".deb" },
    "android": { re: /-android-.*\.apk$/, label: "Android 8.0+", name: "Android 版", icon: "android", ext: ".apk" }
  };

  function formatSize(bytes) {
    return bytes >= 1048576 ? Math.round(bytes / 1048576) + " MB" : Math.max(1, Math.round(bytes / 1024)) + " KB";
  }

  // 尽量判断 Mac 是 Apple 芯片还是 Intel：Chromium 有 userAgentData，Safari / Firefox 看 WebGL 显卡名。
  function detectMacArch() {
    var fromGPU = function () {
      try {
        var gl = document.createElement("canvas").getContext("webgl");
        var ext = gl && gl.getExtension("WEBGL_debug_renderer_info");
        var name = ext ? String(gl.getParameter(ext.UNMASKED_RENDERER_WEBGL)) : "";
        if (/apple (m\d|gpu)/i.test(name)) return "arm64";
        if (/intel|amd|radeon/i.test(name)) return "x64";
      } catch (_) {}
      return "";
    };
    if (navigator.userAgentData && navigator.userAgentData.getHighEntropyValues) {
      return navigator.userAgentData.getHighEntropyValues(["architecture"]).then(function (v) {
        return v.architecture === "arm" ? "arm64" : v.architecture === "x86" ? "x64" : fromGPU();
      }, fromGPU);
    }
    return Promise.resolve(fromGPU());
  }

  function detectDevice() {
    var ua = navigator.userAgent || "";
    var plat = ((navigator.userAgentData && navigator.userAgentData.platform) || navigator.platform || "").toLowerCase();
    if (/android/i.test(ua)) return Promise.resolve("android");
    if (/iphone|ipad|ipod/i.test(ua) || (/mac/.test(plat) && navigator.maxTouchPoints > 1)) return Promise.resolve("ios");
    if (/win/.test(plat) || /windows/i.test(ua)) return Promise.resolve("win-x64");
    if (/mac/.test(plat) || /macintosh/i.test(ua)) {
      return detectMacArch().then(function (arch) { return arch ? "mac-" + arch : "mac"; });
    }
    if (/linux/.test(plat) || /linux/i.test(ua)) return Promise.resolve("linux-appimage");
    return Promise.resolve("");
  }

  function renderRecommendation(device) {
    var mount = document.querySelector("[data-recommend]");
    if (!mount) return;
    function btn(key, primary) {
      var a = ASSETS[key];
      return '<a class="btn ' + (primary ? "btn-primary btn-lg" : "btn-ghost") + '" data-asset="' + key + '" href="' + RELEASES + '/latest" target="_blank" rel="noopener">' +
        icon("download") + "<span>下载 " + a.name + "</span><small data-asset-size></small></a>";
    }
    var title, body, note = "";
    if (device === "ios") {
      title = "iPhone / iPad 不用安装";
      body = '<a class="btn btn-primary btn-lg" href="./mobile.html#pwa">' + icon("phone") + "<span>看看怎么添加到主屏幕</span></a>";
      note = "在电脑上装好猫追番后，用 Safari 打开电脑端给的地址，再「添加到主屏幕」就像 App 一样用。";
    } else if (device === "mac") {
      title = "你的 Mac 是哪种芯片？";
      body = btn("mac-arm64", true) + btn("mac-x64", false);
      note = "不确定的话：点屏幕左上角苹果菜单 → 关于本机，看「芯片」一栏。写着 Apple M 开头的选第一个，写着 Intel 的选第二个。";
    } else if (ASSETS[device]) {
      title = "推荐你下载 " + ASSETS[device].name;
      body = btn(device, true);
      if (device === "mac-arm64") note = '不是 Apple 芯片？<a href="https://github.com/SuInk/bgmcat/releases/latest" data-asset="mac-x64">下载 Intel 芯片版</a>';
      if (device === "mac-x64") note = '其实是 Apple 芯片？<a href="https://github.com/SuInk/bgmcat/releases/latest" data-asset="mac-arm64">下载 Apple 芯片版</a>';
      if (device === "linux-appimage") note = 'Ubuntu / Debian 也可以用 <a href="https://github.com/SuInk/bgmcat/releases/latest" data-asset="linux-deb">.deb 安装包</a>。';
      if (device === "android") note = "装好就能用，也可以在 App 里改成连接家里电脑或服务器。";
    } else {
      title = "选择你的设备";
      body = '<a class="btn btn-primary btn-lg" href="#all">' + icon("download") + "<span>查看所有平台</span></a>";
    }
    mount.innerHTML =
      '<h1 data-recommend-title>' + title + "</h1>" +
      '<div class="dl-hero-actions">' + body + "</div>" +
      (note ? '<p class="dl-hero-note">' + note + "</p>" : "");
  }

  function applyRelease(release) {
    var assets = Array.isArray(release.assets) ? release.assets : [];
    document.querySelectorAll("[data-asset]").forEach(function (link) {
      var spec = ASSETS[link.getAttribute("data-asset")];
      var hit = spec && assets.find(function (a) { return a && spec.re.test(a.name || ""); });
      if (!hit) return;
      link.setAttribute("href", hit.browser_download_url);
      link.removeAttribute("target");
      link.setAttribute("title", hit.name);
      var size = link.querySelector("[data-asset-size]");
      if (size) size.textContent = formatSize(hit.size);
    });
  }

  function enhanceDownloadPage() {
    if (!document.querySelector("[data-download-page]")) return;
    var releaseReq = fetch("https://api.github.com/repos/" + REPO + "/releases/latest", { headers: { Accept: "application/vnd.github+json" } })
      .then(function (res) {
        if (!res.ok) throw new Error("release_unavailable");
        return res.json();
      });
    detectDevice().then(function (device) {
      renderRecommendation(device);
      var card = device && document.querySelector('[data-platform~="' + device.replace(/-.*/, "") + '"]');
      if (card) card.classList.add("is-recommended");
      return releaseReq;
    }).then(function (release) {
      if (!release || !release.tag_name) return;
      document.querySelectorAll("[data-version-badge]").forEach(function (el) { el.textContent = release.tag_name; });
      var stateEl = document.querySelector("[data-release-state]");
      if (stateEl) {
        stateEl.className = "status-chip ok";
        stateEl.textContent = "最新正式版 " + release.tag_name + " · " + new Date(release.published_at).toLocaleDateString();
      }
      applyRelease(release);
    }).catch(function () {
      var stateEl = document.querySelector("[data-release-state]");
      if (stateEl) {
        stateEl.className = "status-chip warn";
        stateEl.textContent = "暂时读不到版本信息，按钮会打开 GitHub 发布页";
      }
    });
  }

  renderNav();
  renderDocsSidebar();
  renderFooter();
  renderInstallCommands();
  renderIcons();
  bindTabsAndCopy();
  enhanceDownloadPage();
})();
