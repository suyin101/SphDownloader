// 注入到视频号页面里：把页面拿到的视频信息（地址 + 解密钥匙）交给本机下载器。
(function () {
  if (window.__sphdl) return;
  var SPHDL_V = "__SPHDL_V__";
  var MAX_DEPTH = 10;

  function collect(o, out, depth, seen) {
    if (!o || depth > MAX_DEPTH) return;
    if (typeof o === "string") {
      if (o.length > 100 && o.indexOf("decodeKey") >= 0 && /^[\[{]/.test(o)) {
        try { collect(JSON.parse(o), out, depth + 1, seen); } catch (e) {}
      }
      return;
    }
    if (typeof o !== "object" || seen.has(o)) return;
    seen.add(o);
    if (o.objectDesc && o.objectDesc.media) { out.push(o); return; }
    var keys;
    try { keys = Object.keys(o); } catch (e) { return; }
    if (keys.length > 2000) return;
    for (var i = 0; i < keys.length; i++) {
      var v;
      try { v = o[keys[i]]; } catch (e) { continue; }
      if (v && (typeof v === "object" || typeof v === "string")) collect(v, out, depth + 1, seen);
    }
  }

  function simplify(feed) {
    var d = feed.objectDesc || {};
    var m = (d.media || [])[0];
    if (!m || !m.url) return null;
    if (d.mediaType && d.mediaType !== 4) return null; // 4 = 视频；图片和直播先不管
    var contact = feed.contact || {};
    return {
      id: String(feed.id || feed.objectId || feed.exportId || m.url),
      title: d.description || "",
      nickname: contact.nickname || feed.nickname || "",
      url: m.url + (m.urlToken || ""),
      decodeKey: m.decodeKey == null ? "" : String(m.decodeKey),
      cover: m.fullCoverUrl || m.coverUrl || m.fullThumbUrl || m.thumbUrl || "",
      width: Number(m.width || 0),
      height: Number(m.height || 0),
      duration: m.videoPlayLen || 0,
      size: Number(m.fileSize || 0),
      createtime: feed.createtime || 0,
    };
  }

  function send(payload) {
    try {
      fetch("/__sphdl/report", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload),
        keepalive: true,
      }).catch(function () {});
    } catch (e) {}
  }

  // kind: current = 正在看的这条（会自动下载）；list = 列表里预加载的（只显示，不自动下）
  window.__sphdl = function (kind, data) {
    try {
      var feeds = [];
      collect(data, feeds, 0, new WeakSet());
      var items = [];
      for (var i = 0; i < feeds.length; i++) {
        var it = simplify(feeds[i]);
        if (it) items.push(it);
      }
      if (items.length) send({ kind: kind, items: items, href: location.href });
    } catch (e) {
      send({ kind: "error", msg: String(e), href: location.href });
    }
  };

  // 兜底：页面解析任何带 decodeKey 的数据时也收一份（当作列表）
  var rawParse = JSON.parse;
  JSON.parse = function (text) {
    var r = rawParse.apply(this, arguments);
    try {
      if (typeof text === "string" && text.length > 200 && text.indexOf("decodeKey") >= 0) {
        setTimeout(function () { window.__sphdl("list", r); }, 0);
      }
    } catch (e) {}
    return r;
  };

  send({ kind: "hello", href: location.href, ua: navigator.userAgent });

  // 分享卡片的预览页（/finder-preview/）会用 openUrlWithExtraWebview(type:FinderFeed) 打开视频页。
  // 部分 Windows 微信（同版本、被灰度）收到这个调用会改用微信自带播放器（liteapp），
  // 不再加载网页，下载器就拿不到视频地址和解密钥匙。这里改成在当前网页里直接打开视频页。
  var isPreview = location.pathname.indexOf("/finder-preview/") === 0;
  function patchBridge() {
    var b = window.WeixinJSBridge;
    if (!isPreview || !b || !b.invoke || b.__sphdlPatched) return;
    var raw = b.invoke;
    try {
      b.invoke = function (name, args) {
        if (name === "openUrlWithExtraWebview" && args && /\/web\/pages\/feed/.test(String(args.url || ""))) {
          send({ kind: "log", msg: "预览页改为直接打开视频网页", href: location.href });
          location.replace(args.url);
          return;
        }
        return raw.apply(this, arguments);
      };
      b.__sphdlPatched = true;
    } catch (e) {}
  }
  if (isPreview) {
    patchBridge();
    document.addEventListener("WeixinJSBridgeReady", patchBridge);
    var tries = 0;
    var timer = setInterval(function () {
      patchBridge();
      if (++tries > 200) clearInterval(timer);
    }, 50);
  }

  // “链接下载”：下载器把要打开的视频地址交给页面，页面让微信打开它
  function openFeed(u) {
    var b = window.WeixinJSBridge;
    var fallback = function () { location.href = u; };
    if (isPreview) { fallback(); return; } // 预览页直接在当前网页打开，原因见上
    if (b && b.invoke) {
      try {
        b.invoke("openUrlWithExtraWebview", { url: u, type: "FinderFeed", id: "FinderFeed" }, function (res) {
          var m = (res && (res.err_msg || res.errMsg)) || "";
          send({ kind: "log", msg: "openUrlWithExtraWebview " + m, href: location.href });
          if (m.indexOf(":ok") < 0) fallback();
        });
        return;
      } catch (e) {}
    }
    fallback();
  }
  if (window.top === window) {
    setInterval(function () {
      fetch("/__sphdl/task?v=" + SPHDL_V).then(function (r) { return r.json(); }).then(function (t) {
        if (t && t.reload) { location.reload(); return; } // 下载器升级了，刷新页面换新脚本
        if (t && t.url) openFeed(t.url);
      }).catch(function () {});
    }, 2000);
  }
})();
