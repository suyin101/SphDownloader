// 注入到隐藏的抖音网页里（文档开始时运行）：只“旁听”网页自己请求到的视频详情数据，交给软件。
// 签名、Cookie 都是抖音网页自己处理的，这里不伪造任何东西。
(function () {
  if (window.__sphdlDy) return;
  window.__sphdlDy = 1;
  var want = '__ID__';
  var sent = false;
  function post(t) {
    try {
      if (window.webkit && webkit.messageHandlers && webkit.messageHandlers.sphdl) webkit.messageHandlers.sphdl.postMessage(t);
      else if (window.sphdlDouyin) window.sphdlDouyin(t);
    } catch (e) {}
  }
  function check(url, text) {
    if (sent || !url || String(url).indexOf('/aweme/v1/web/aweme/detail/') < 0) return;
    if (want && String(url).indexOf(want) < 0) return;
    if (text && text.indexOf('"aweme_detail"') >= 0 && text.indexOf('"aweme_detail":null') < 0) { sent = true; post(text); }
  }
  var open = XMLHttpRequest.prototype.open;
  XMLHttpRequest.prototype.open = function (m, u) { this.__sphdlU = String(u); return open.apply(this, arguments); };
  var send = XMLHttpRequest.prototype.send;
  XMLHttpRequest.prototype.send = function () {
    var x = this;
    x.addEventListener('load', function () {
      try {
        var t = (x.responseType === '' || x.responseType === 'text') ? x.responseText : (typeof x.response === 'string' ? x.response : JSON.stringify(x.response));
        check(x.responseURL || x.__sphdlU, t);
      } catch (e) {}
    });
    return send.apply(this, arguments);
  };
  var f = window.fetch;
  if (f) window.fetch = function (i) {
    var u = typeof i === 'string' ? i : (i && i.url) || '';
    return f.apply(this, arguments).then(function (r) {
      try { if (String(u).indexOf('/aweme/v1/web/aweme/detail/') >= 0) r.clone().text().then(function (t) { check(r.url || u, t); }); } catch (e) {}
      return r;
    });
  };
  // 抖音弹验证时告诉软件，把窗口显示出来让用户手动过
  // 注意：抖音每次打开都会先在后台做一次“无感验证”（看不见的 verifycenter 小框），那不算，只认真正显示出来、够大的验证框
  setInterval(function () {
    if (sent) return;
    var els = document.querySelectorAll('#captcha_container, .captcha_verify_container, #captcha-verify-image, .vc-captcha-verify, iframe[src*="verifycenter"]');
    for (var i = 0; i < els.length; i++) {
      var r = els[i].getBoundingClientRect(), st = getComputedStyle(els[i]);
      if (r.width > 200 && r.height > 150 && st.visibility !== 'hidden' && st.display !== 'none' && st.opacity !== '0') { post('{"__captcha":1}'); return; }
    }
  }, 2000);
})();
