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

  function num(x) { var n = Math.round(Number(x)); return isFinite(n) ? n : 0; } // 下载器要整数，小数/文字会让整条上报被拒
  function simplify(feed) {
    var d = feed.objectDesc || {};
    var m = (d.media || [])[0];
    if (!m || !m.url) return null;
    if (d.mediaType && d.mediaType !== 4) return null; // 4 = 视频；图片和直播先不管
    var contact = feed.contact || {};
    return {
      id: String(feed.id || feed.objectId || feed.exportId || m.url),
      title: String(d.description || ""),
      nickname: String(contact.nickname || feed.nickname || ""),
      url: String(m.url) + (m.urlToken || ""),
      decodeKey: m.decodeKey == null ? "" : String(m.decodeKey),
      cover: String(m.fullCoverUrl || m.coverUrl || m.fullThumbUrl || m.thumbUrl || ""),
      width: num(m.width),
      height: num(m.height),
      duration: num(m.videoPlayLen),
      size: num(m.fileSize),
      specs: (m.spec || feed.spec || []).map(function (x) {
        return {
          fileFormat: String(x.fileFormat || ""),
          width: num(x.width),
          height: num(x.height),
          levelOrder: num(x.levelOrder),
          bitrate: num(x.videoBitrate || x.bitRate),
        };
      }).filter(function (x) { return x.fileFormat; }),
      createtime: num(feed.createtime),
    };
  }

  // 不能一律用 keepalive：浏览器给所有 keepalive 请求合计只留 64KB，整批列表还在路上时，
  // 后面的“正在看”会被直接拒发（不报错），表现就是认出了但没跳到第一行。只有小的才用它。
  function send(payload) {
    try {
      var body = JSON.stringify(payload);
      var opt = { method: "POST", headers: { "Content-Type": "application/json" }, body: body };
      if (body.length < 8000) opt.keepalive = true;
      fetch("/__sphdl/report", opt).catch(function () {
        if (opt.keepalive) { delete opt.keepalive; fetch("/__sphdl/report", opt).catch(function () {}); }
      });
    } catch (e) {}
  }
  // 正在看：除了完整数据，再单独发一条很小的（编号 + 地址），下载器按它把列表里已有的那条排第一
  function sendPlaying(f, why) {
    try {
      var m = f.objectDesc && f.objectDesc.media && f.objectDesc.media[0];
      send({ kind: "playing", id: String(f.id || ""), msg: m && m.url ? String(m.url) : "", via: why, href: location.href });
    } catch (e) {}
  }

  // 页面自己记住看过的视频：下载器重启 / 嗅探关了又开的那段时间，报上去的会丢，
  // 重新连上后（或下载器说要）再整批补报一遍，不用用户重开视频号窗口。
  var memo = {}, memoOrder = [], lastCurrent = null;
  function remember(kind, items) {
    for (var i = 0; i < items.length; i++) {
      var id = items[i].id;
      if (!memo[id]) memoOrder.push(id);
      memo[id] = items[i];
    }
    while (memoOrder.length > 300) delete memo[memoOrder.shift()];
    if (kind === "current") lastCurrent = items;
  }
  var lastResend = 0;
  function resend(why) {
    var now = Date.now();
    if (now - lastResend < 8000) return;
    lastResend = now;
    var all = [];
    for (var i = 0; i < memoOrder.length; i++) if (memo[memoOrder[i]]) all.push(memo[memoOrder[i]]);
    if (all.length) send({ kind: "list", items: all, href: location.href, via: "resend:" + why });
    if (lastCurrent) send({ kind: "current", items: lastCurrent, href: location.href, via: "resend:" + why });
  }

  // kind: current = 正在看的这条（会自动下载）；list = 列表里预加载的（只显示，不自动下）
  window.__sphdl = function (kind, data, via) {
    try {
      var feeds = [];
      collect(data, feeds, 0, new WeakSet());
      var items = [];
      for (var i = 0; i < feeds.length; i++) {
        var it = simplify(feeds[i]);
        if (it) items.push(it);
      }
      if (items.length) {
        remember(kind, items);
        send({ kind: kind, items: items, href: location.href, via: via || "" });
      }
      else if (kind === "current") send({ kind: "log", msg: "正在看的视频没取到信息（" + (via || "?") + "）", href: location.href });
    } catch (e) {
      send({ kind: "error", msg: String(e), href: location.href });
    }
  };

  // 正在看：直接盯页面里的播放器。哪个 <video> 开始播放，就沿着 Vue 组件往上找这条视频的数据，立即报 current。
  // 不依赖 goToNextFlowFeed 之类的函数名（作者主页、详情页、点开的播放流里那些钩子不一定触发）。
  // 正式版 Vue 不在画面元素上挂组件信息，只能从根（#app._vnode / __vue_app__）往下走，
  // 一路只进“画面包含这个播放器”的分支，记下路上最后（最近）一个属性里带视频数据的组件。
  function vueRoot() {
    var c = document.querySelector("#app");
    if (c && c._vnode) return c._vnode;
    var all = document.querySelectorAll("body > *");
    for (var i = 0; i < all.length; i++) if (all[i]._vnode) return all[i]._vnode;
    return null;
  }
  // 沿着包含这个播放器的那一支往下走，收集路上的视频数据：
  //   lists = 带 feeds + currentFeedIndex 的播放流（它的 currentFeed 最准）；feeds = 直接带视频数据的组件属性（越深越近）。
  function scanPath(el) {
    var root = vueRoot(), lists = [], feeds = [], visited = 0;
    if (!root) return { lists: lists, feeds: feeds, why: "没找到 Vue 根" };
    function look(inst) {
      var srcs = [inst.props, inst.setupState];
      for (var j = 0; j < srcs.length; j++) {
        var o = srcs[j];
        if (!o) continue;
        var keys;
        try { keys = Object.keys(o); } catch (e) { continue; }
        for (var k = 0; k < keys.length; k++) {
          var v;
          try { v = o[keys[k]]; if (v && v.__v_isRef) v = v.value; } catch (e) { continue; }
          if (!v || typeof v !== "object") continue;
          if (v.objectDesc && v.objectDesc.media) feeds.push(v);
          else if (Array.isArray(v.feeds) && typeof v.currentFeedIndex === "number") lists.push(v);
        }
      }
    }
    function walk(vn) {
      if (!vn || typeof vn !== "object" || ++visited > 30000) return;
      var e = vn.el;
      if (e && e.nodeType === 1 && e !== el && !e.contains(el)) return; // 这一支不包含播放器
      if (vn.component) { look(vn.component); walk(vn.component.subTree); return; }
      if (vn.suspense && vn.suspense.activeBranch) walk(vn.suspense.activeBranch);
      if (Array.isArray(vn.children)) for (var i = 0; i < vn.children.length; i++) walk(vn.children[i]);
    }
    try { walk(root); } catch (e) {}
    return { lists: lists, feeds: feeds, visited: visited };
  }
  function ekOf(u) { var m = /[?&]encfilekey=([^&]+)/.exec(u || ""); return m ? m[1] : ""; }
  function titleOf(f) { return ((f && f.objectDesc && f.objectDesc.description) || (f && f.id) || "?").slice(0, 14); }
  // 画面上正在播、最大的那个 <video>
  function playingVideo() {
    var vs = document.querySelectorAll("video"), best = null, area = 0;
    for (var i = 0; i < vs.length; i++) {
      var v = vs[i];
      if (v.paused || v.ended) continue;
      var r = v.getBoundingClientRect(), a = r.width * r.height;
      if (r.width < 200 || r.height < 150 || r.bottom < 0 || r.top > innerHeight) continue;
      if (a > area) { area = a; best = v; }
    }
    return best;
  }
  var lastPlayed = "", lastSrc = "", lastDiag = "";
  function checkPlaying(why) {
    try {
      var el = playingVideo();
      if (!el) return;
      var src = el.currentSrc || el.src || "";
      var p = scanPath(el), f = null, how = "";
      // ① 播放地址里有 encfilekey：路上哪条视频的地址对得上就是它
      var ek = ekOf(src);
      if (ek) {
        var all = p.feeds.slice();
        for (var i = 0; i < p.lists.length; i++) all = all.concat(p.lists[i].feeds || []);
        for (var j = 0; j < all.length && !f; j++) {
          var m = all[j] && all[j].objectDesc && all[j].objectDesc.media && all[j].objectDesc.media[0];
          if (m && ekOf(m.url) === ek) { f = all[j]; how = "地址"; }
        }
      }
      // ② 最近的那个播放流的当前条
      if (!f && p.lists.length) { f = p.lists[p.lists.length - 1].currentFeed; how = "播放流"; }
      // ③ 最近的组件属性
      if (!f && p.feeds.length) { f = p.feeds[p.feeds.length - 1]; how = "组件"; }
      var diag = how + "｜" + (f ? titleOf(f) : "无") + "｜流" + p.lists.length + " 组件" + p.feeds.length + "｜" + (ek ? "地址有钥匙" : src.slice(0, 20));
      if (!f) {
        if (src && src !== lastSrc) { lastSrc = src; send({ kind: "playing", msg: src, href: location.href }); }
        if (diag !== lastDiag) { lastDiag = diag; send({ kind: "log", msg: "正在播（" + why + "）没认出：" + diag + (p.why ? " " + p.why : ""), href: location.href }); }
        return;
      }
      var id = String(f.id || "");
      if (!id || id === lastPlayed) return;
      lastPlayed = id;
      send({ kind: "log", msg: "正在播（" + why + "）：" + diag, href: location.href });
      sendPlaying(f, "video-" + why);
      window.__sphdl("current", f, "video-" + why);
    } catch (e) {
      send({ kind: "error", msg: "checkPlaying " + String(e), href: location.href });
    }
  }
  function onPlay(ev) {
    if (ev.target && ev.target.tagName === "VIDEO") setTimeout(function () { checkPlaying("play"); }, 300);
  }
  document.addEventListener("play", onPlay, true);
  document.addEventListener("playing", onPlay, true);
  setInterval(function () { checkPlaying("poll"); }, 1000); // 播放器复用同一个 <video> 换地址时不一定有 play 事件

  // 兜底：页面解析任何带 decodeKey 的数据时也收一份（当作列表）
  var rawParse = JSON.parse;
  JSON.parse = function (text) {
    var r = rawParse.apply(this, arguments);
    try {
      if (typeof text === "string" && text.length > 200 && text.indexOf("decodeKey") >= 0) {
        setTimeout(function () { window.__sphdl("list", r, "JSON.parse"); }, 0);
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
  function openFeed(u, inPlace) {
    var b = window.WeixinJSBridge;
    var fallback = function () { location.href = u; };
    // 预览页、或下载器发现新窗口会被改走自带播放器时，直接在当前网页打开，原因见上
    if (isPreview || inPlace) { fallback(); return; }
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
  var lost = false; // 上次轮询没连上下载器（下载器关了 / 重启中）
  if (window.top === window) setInterval(function () {
    fetch("/__sphdl/task?v=" + SPHDL_V).then(function (r) { return r.json(); }).then(function (t) {
      if (t && t.reload) { location.reload(); return; } // 下载器升级了，刷新页面换新脚本
      if (lost) { lost = false; send({ kind: "hello", href: location.href, ua: navigator.userAgent }); resend("reconnect"); }
      else if (t && t.resend) resend("ask");
      if (t && t.url) openFeed(t.url, !!t.inPlace);
    }).catch(function () { lost = true; });
  }, 2000);
})();
