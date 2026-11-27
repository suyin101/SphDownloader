// 端到端测试用：在假页面里跑真的 inject.js，模拟“列表先报上来，再一条条刷到”，把页面发出的每个上报原样写到 stdout（一行一个 JSON）。
// 视频数据故意带小数时长、文字时间、很大的列表，覆盖以前上报被拒收的情况。
const fs = require("fs");
const src = fs.readFileSync(process.argv[2], "utf8");
let ticks = [], handlers = {}, keepaliveBytes = 0;
function El(n) { this.nodeType = 1; this.kids = []; this.n = n; }
El.prototype.contains = function (x) { return x === this || this.kids.some((k) => k.contains(x)); };
const video = new El("video"); video.tagName = "VIDEO"; video.paused = false; video.currentSrc = "blob:https://channels.weixin.qq.com/abc";
video.getBoundingClientRect = () => ({ width: 800, height: 450, top: 0, bottom: 450 });
const player = new El("player"); player.kids.push(video);
const app = new El("app"); app.kids.push(player);
const mk = (i) => ({ id: "1400" + i, createtime: "1795740000", contact: { nickname: "作者" + i },
  objectDesc: { mediaType: 4, description: "视频" + i + " " + "很长的描述".repeat(40),
    media: [{ url: "https://finder.video.qq.com/251/20302/stodownload?encfilekey=EK" + i, urlToken: "&token=t" + i, decodeKey: 12345 + i,
      videoPlayLen: 37.5 + i, width: 1080, height: 1920.0, fileSize: "1234567", coverUrl: "https://c/" + i,
      spec: [{ fileFormat: "xWT111", width: 1080, height: 1920, levelOrder: 1, videoBitrate: 1500.7 }] }] } });
const feeds = []; for (let i = 0; i < 60; i++) feeds.push(mk(i));
const flow = { feeds, currentFeedIndex: 0, get currentFeed() { return this.feeds[this.currentFeedIndex]; } };
// 播放器的上层组件一直拿着第一条（旧数据），播放流才是准的
app._vnode = { el: app, component: { props: { feed: feeds[0] }, setupState: { flow: { __v_isRef: true, value: flow } },
  subTree: { el: app, children: [{ el: player, component: { props: {}, subTree: { el: player, children: [{ el: video }] } } }] } } };
global.window = global; global.top = global; global.innerHeight = 900;
global.location = { href: "https://channels.weixin.qq.com/web/pages/home", pathname: "/web/pages/home" };
global.navigator = { userAgent: "x" };
global.document = { addEventListener(t, f) { handlers[t] = f; }, querySelector: () => app, querySelectorAll: (q) => (q === "video" ? [video] : []) };
global.setInterval = (f, ms) => { if (ms === 1000) ticks.push(f); };
global.fetch = (u, o) => {
  if (!o || !o.body) return Promise.resolve({ json: () => Promise.resolve({}) });
  // 模拟浏览器的 keepalive 限额：合计超过 64KB 直接拒发（请求一直“在路上”，不释放）
  if (o.keepalive) { if (keepaliveBytes + o.body.length > 65536) return Promise.reject(new TypeError("keepalive quota")); keepaliveBytes += o.body.length; }
  process.stdout.write(o.body + "\n");
  return Promise.resolve({});
};
eval(src);
(async () => {
  window.__sphdl("list", { data: { object: feeds } }, "post:FinderPcFlow"); // 整批列表（很大）
  window.__sphdl("list", { data: { object: feeds.slice(0, 30) } }, "post:FinderCheckPrefetch");
  for (const idx of [5, 6, 7, 12, 30]) { flow.currentFeedIndex = idx; ticks[0](); await new Promise((r) => setTimeout(r, 5)); }
})();
