package web

import (
	"bytes"
	"net/http"
	"net/url"
	"strings"
)

// maxRewriteBody 限制改写缓冲区大小，超过后放弃改写直接透传，
// 避免把视频流之类的大响应缓存在内存里。
const maxRewriteBody = 4 << 20

// pathRewriteWriter 把上游返回内容里的绝对路径补上门户的路径前缀，
// 例如 <link href="/styles.css"> -> <link href="/demo/styles.css">。
type pathRewriteWriter struct {
	http.ResponseWriter
	prefix  string
	secure  bool
	buf     bytes.Buffer
	rewrite bool
	safe    bool
	tooBig  bool
	wrote   bool
}

func (w *pathRewriteWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// unwrapWriter 剥掉代理层的改写包装，拿到原始的 ResponseWriter。
// 错误页必须直接写，否则页面里的站内链接会被补上应用前缀。
func unwrapWriter(w http.ResponseWriter) http.ResponseWriter {
	for {
		u, ok := w.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			return w
		}
		w = u.Unwrap()
	}
}

func (w *pathRewriteWriter) WriteHeader(code int) {
	if w.wrote {
		return
	}
	w.wrote = true

	h := w.Header()
	if loc := h.Get("Location"); loc != "" {
		h.Set("Location", w.rewriteLocation(loc))
	}
	if cookies := h.Values("Set-Cookie"); len(cookies) > 0 {
		h.Del("Set-Cookie")
		for _, c := range cookies {
			h.Add("Set-Cookie", rewriteSetCookie(c, w.prefix, w.secure))
		}
	}
	if rewritableContentType(h.Get("Content-Type")) {
		w.rewrite = true
		// 改写后长度会变，交给 Go 用 chunked 输出。
		h.Del("Content-Length")
		// 上游常给静态资源和页面发长期缓存头（Next.js 甚至是 immutable），
		// 但改写后的内容一旦被缓存，后续调整规则就再也刷不掉了。
		h.Set("Cache-Control", "no-store")
		h.Del("ETag")
		h.Del("Last-Modified")
		h.Del("Expires")
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *pathRewriteWriter) Write(p []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	if !w.rewrite || w.tooBig {
		return w.ResponseWriter.Write(p)
	}
	w.buf.Write(p)
	if w.buf.Len() > maxRewriteBody {
		w.tooBig = true
		out := w.buf.Bytes()
		w.buf.Reset()
		if _, err := w.ResponseWriter.Write(out); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}

// Flush 只对流式响应（视频、SSE 等不需要改写的内容）立即下发。
// 需要改写时必须等整段内容缓冲完，否则会在 chunk 边界被截断，
// 出现前半段改了、后半段没改的情况。
func (w *pathRewriteWriter) Flush() {
	if w.rewrite {
		return
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *pathRewriteWriter) finish() {
	if w.rewrite && !w.tooBig && w.buf.Len() > 0 {
		body := prefixAbsolutePaths(w.buf.Bytes(), w.prefix, w.safe)
		_, _ = w.ResponseWriter.Write(allowSameOriginInSandbox(body))
		w.buf.Reset()
	}
}

// returnParams 是各个应用用来记录「登录后回跳地址」的查询参数名。
var returnParams = []string{"next", "redirect", "redirect_url", "redirect_uri", "return", "returnUrl", "return_url", "rd", "continue", "from"}

func (w *pathRewriteWriter) rewriteLocation(loc string) string {
	if !strings.HasPrefix(loc, "/") || strings.HasPrefix(loc, "//") {
		return loc
	}
	path, rawQuery, hasQuery := strings.Cut(loc, "?")
	if !strings.HasPrefix(path, w.prefix) {
		if path == "/" {
			path = w.prefix + "/"
		} else {
			path = w.prefix + path
		}
	}
	if !hasQuery || rawQuery == "" {
		return path
	}
	// 登录跳转常带 ?next=/xxx，这个值也是上游视角的裸路径，
	// 不补前缀的话登录成功后会跳出应用（例如回到门户首页）。
	vals, err := url.ParseQuery(rawQuery)
	if err != nil {
		return path + "?" + rawQuery
	}
	changed := false
	for _, k := range returnParams {
		v := vals.Get(k)
		if v == "" || !strings.HasPrefix(v, "/") || strings.HasPrefix(v, "//") || strings.HasPrefix(v, w.prefix) {
			continue
		}
		if v == "/" {
			vals.Set(k, w.prefix+"/")
		} else {
			vals.Set(k, w.prefix+v)
		}
		changed = true
	}
	if !changed {
		return path + "?" + rawQuery
	}
	return path + "?" + vals.Encode()
}

func rewritableContentType(ct string) bool {
	ct = strings.ToLower(ct)
	switch {
	case strings.HasPrefix(ct, "text/html"),
		strings.HasPrefix(ct, "text/css"),
		strings.HasPrefix(ct, "text/javascript"),
		strings.HasPrefix(ct, "application/javascript"),
		// Next.js App Router 的客户端导航数据
		strings.HasPrefix(ct, "text/x-component"),
		strings.HasPrefix(ct, "application/json"):
		return true
	}
	return false
}

// allowSameOriginInSandbox 给 iframe 的 sandbox 属性补上 allow-same-origin。
//
// 不带 allow-same-origin 的沙箱 iframe 是一个不透明源，它内部发起的请求
// 会被当成跨站，浏览器不会带上 SameSite=Lax 的会话 cookie，
// 于是这些请求在门户侧就是未登录状态，会被 303 回登录页
// （典型表现：AstrBot 的表情包管理器 iframe 里显示的是门户登录页）。
func allowSameOriginInSandbox(body []byte) []byte {
	const key = "sandbox"
	const add = "allow-same-origin "
	if !bytes.Contains(body, []byte(key)) {
		return body
	}
	out := make([]byte, 0, len(body)+32)
	last := 0
	for i := 0; i+len(key) <= len(body); i++ {
		if !bytes.HasPrefix(body[i:], []byte(key)) {
			continue
		}
		j := i + len(key)
		for j < len(body) && isSpaceByte(body[j]) {
			j++
		}
		if j >= len(body) || (body[j] != '=' && body[j] != ':') {
			continue
		}
		j++
		for j < len(body) && isSpaceByte(body[j]) {
			j++
		}
		if j >= len(body) || !isQuoteByte(body[j]) {
			continue
		}
		quote := body[j]
		k := j + 1
		for k < len(body) && k-j < 256 && body[k] != quote {
			k++
		}
		if k >= len(body) || body[k] != quote {
			continue
		}
		val := body[j+1 : k]
		if bytes.Contains(val, []byte("allow-same-origin")) || !bytes.Contains(val, []byte("allow-scripts")) {
			i = k
			continue
		}
		out = append(out, body[last:j+1]...)
		out = append(out, add...)
		last = j + 1
		i = k
	}
	return append(out, body[last:]...)
}

// prefixAbsolutePaths 给以 / 开头的字符串路径补上门户前缀，覆盖：
//
//	href="/x"  src='/x'  url(/x)  url("/x")
//	fetch("/api/x")  axios.get('/api/x')  `...${x}`
//
// 协议相对地址（//cdn...）和已经带前缀的地址会跳过。
func prefixAbsolutePaths(body []byte, prefix string, safe bool) []byte {
	if prefix == "" || len(body) == 0 {
		return body
	}
	prefixBytes := []byte(prefix)
	urlMarker := []byte("url(")
	out := make([]byte, 0, len(body)+256)
	last := 0

	// 单趟扫描。不能对每个引号都重新 Index 一次剩余内容，
	// 那样在几 MB 的 JS 上是 O(n²)，会把响应卡到几十秒。
	for i := 0; i < len(body); {
		markerLen := 0
		isQuote := false
		switch body[i] {
		case '"', '\'', '`':
			// 被反斜杠转义的引号（例如 JS 里的 \"）不是字符串边界，跳过。
			if i > 0 && body[i-1] == '\\' {
				i++
				continue
			}
			markerLen = 1
			isQuote = true
		case '&':
			// HTML 实体引号 &#39;（单引号）和 &quot;（双引号），
			// 一些后端模板引擎会把 JS 字符串里的引号转义成实体，
			// 导致我们识别不到字符串边界、漏掉里面的路径改写。
			if bytes.HasPrefix(body[i:], []byte("&#39;")) || bytes.HasPrefix(body[i:], []byte("&#39;")) || bytes.HasPrefix(body[i:], []byte("&quot;")) {
				if bytes.HasPrefix(body[i:], []byte("&quot;")) {
					markerLen = 6
				} else {
					markerLen = 5
				}
				isQuote = true
			} else {
				i++
				continue
			}
		case 'u':
			if bytes.HasPrefix(body[i:], urlMarker) {
				markerLen = len(urlMarker)
			} else {
				i++
				continue
			}
		default:
			i++
			continue
		}

		end := i + markerLen
		rest := body[end:]

		// 拼接用的片段（例如 "/api/tasks/".concat(id, "/cover")）不能加前缀，
		// 否则会拼成 /api/tasks/1/viggle/cover。
		if isQuote && isFragmentContext(body, i) {
			i = end
			continue
		}

		// 后面不是绝对路径、是协议相对地址、是自闭合标签的 "/>"、
		// 或已经带前缀，就原样保留。
		if len(rest) == 0 || rest[0] != '/' ||
			(len(rest) > 1 && (rest[1] == '/' || rest[1] == '>')) ||
			bytes.HasPrefix(rest, prefixBytes) {
			i = end
			continue
		}

		// 单独的 "/" 语义模糊：路由默认值、补前导斜杠、路径拼接片段都很常见，
		// 只有 href="/" 这种首页链接、Vite 拼资源基址的 return"/"+dep，
		// 以及 useState("/") 这种保存跳转目标的初始值需要补前缀。
		if len(rest) > 1 && isQuoteByte(rest[1]) &&
			!isBaseJoin(body, i, rest) && !isHrefValue(body, i) &&
			!isRedirectDefault(body, i) && !isBaseURLAssign(body, i) {
			i = end
			continue
		}

		// 安全模式：只改资源/接口路径，避免动到前端路由（Vue Router 的
		// path:"/login"、hash="/auth/login" 这类字符串），它们必须保持原样。
		if safe && !isResourcePath(rest) && !isBaseJoin(body, i, rest) && !isBaseURLAssign(body, i) {
			i = end
			continue
		}

		out = append(out, body[last:end]...)
		out = append(out, prefix...)
		last = end
		i = end
	}
	return append(out, body[last:]...)
}

// isBaseJoin 判断是不是 Vite 用来拼资源基址的 return"/"+dep，
// 形如 function(e){return"/"+e}。
// 其它 "/"+x 多是补前导斜杠（Vue Router 的 o="/"+o），绝对不能加前缀。
func isBaseJoin(body []byte, pos int, rest []byte) bool {
	k := 2
	for k < len(rest) && isSpaceByte(rest[k]) {
		k++
	}
	if k >= len(rest) || rest[k] != '+' {
		return false
	}
	before := bytes.TrimRight(body[:pos], " \t\r\n")
	return bytes.HasSuffix(before, []byte("return"))
}

// isHrefValue 判断引号前是不是 href= / workspace: 这类站内链接字段，
// 这时单独的 "/" 就是应用首页，必须带上门户前缀。
func isHrefValue(body []byte, pos int) bool {
	i := pos - 1
	for i >= 0 && isSpaceByte(body[i]) {
		i--
	}
	if i < 0 || (body[i] != '=' && body[i] != ':') {
		return false
	}
	i--
	for i >= 0 && isSpaceByte(body[i]) {
		i--
	}
	end := i + 1
	for i >= 0 && isIdentByte(body[i]) {
		i--
	}
	switch string(body[i+1 : end]) {
	case "href", "to", "home", "workspace":
		return true
	}
	return false
}

// isBaseURLAssign 判断这个 "/" 是不是应用基址变量的赋值，
// 例如 window.__ENV__QlBaseUrl="/"（青龙面板）。
// 这类变量会被前端拼在 API、资源和路由前面，必须换成门户下的路径前缀，
// 否则请求会打到门户根路径上。
func isBaseURLAssign(body []byte, pos int) bool {
	i := pos - 1
	for i >= 0 && isSpaceByte(body[i]) {
		i--
	}
	if i < 0 || (body[i] != '=' && body[i] != ':') {
		return false
	}
	i--
	for i >= 0 && isSpaceByte(body[i]) {
		i--
	}
	end := i + 1
	for i >= 0 && isIdentByte(body[i]) {
		i--
	}
	return strings.HasSuffix(strings.ToLower(string(body[i+1:end])), "baseurl")
}

// isRedirectDefault 判断这个 "/" 是不是组件里保存跳转目标的初始值，
// 例如 const [next, setNext] = useState("/")，登录成功后 location.replace(next)。
func isRedirectDefault(body []byte, pos int) bool {
	start := pos - 24
	if start < 0 {
		start = 0
	}
	ctx := bytes.TrimRight(body[start:pos], " ")
	for len(ctx) > 0 && (ctx[len(ctx)-1] == '(' || ctx[len(ctx)-1] == ')') {
		ctx = bytes.TrimRight(ctx[:len(ctx)-1], " ")
	}
	return bytes.HasSuffix(ctx, []byte("useState"))
}

// isFragmentContext 判断某个字符串是不是拼接用的路径片段：
// 前面是 +，或者位于 .concat( / .join( 的某个参数位置。
func isFragmentContext(body []byte, pos int) bool {
	i := pos - 1
	for i >= 0 && isSpaceByte(body[i]) {
		i--
	}
	if i < 0 {
		return false
	}
	if body[i] == '+' {
		return true
	}

	depth := 0
	for j := i; j >= 0; j-- {
		switch body[j] {
		case ')', ']', '}':
			depth++
		case '(':
			if depth > 0 {
				depth--
				continue
			}
			k := j - 1
			for k >= 0 && isSpaceByte(body[k]) {
				k--
			}
			end := k + 1
			for k >= 0 && isIdentByte(body[k]) {
				k--
			}
			name := string(body[k+1 : end])
			return name == "concat" || name == "join"
		case '[', '{':
			if depth > 0 {
				depth--
				continue
			}
			return false
		case ';', '\n', '\r':
			return false
		}
	}
	return false
}

func isQuoteByte(c byte) bool {
	return c == '"' || c == '\'' || c == '`'
}

func isIdentByte(c byte) bool {
	return c == '_' || c == '$' ||
		(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9')
}

// resourcePrefixes 是安全模式下可以安全加前缀的路径开头。
var resourcePrefixes = []string{
	"/api/", "/assets/", "/static/", "/_next/", "/__next",
	"/socket.io/", "/uploads/", "/media/", "/files/",
	"/fonts/", "/images/", "/img/", "/favicon", "/js/", "/css/",
}

func isResourcePath(rest []byte) bool {
	for _, p := range resourcePrefixes {
		if bytes.HasPrefix(rest, []byte(p)) {
			return true
		}
	}
	return false
}

func isSpaceByte(c byte) bool {
	return c == ' ' || c == '\t' || c == '\r' || c == '\n'
}

// rewriteSetCookie 修正上游下发的 cookie：
//
//	去掉 Domain        —— 上游的域名和门户域名不一致，浏览器会直接丢弃
//	去掉 Secure        —— 客户端走 http 时带 Secure 的 cookie 也会被丢弃
//	SameSite=None -> Lax —— None 必须配合 Secure，否则浏览器拒收
//	Path=/ -> Path=/{应用}/ —— 避免和其他应用、门户互相覆盖
func rewriteSetCookie(cookie, prefix string, secure bool) string {
	attrs := strings.Split(cookie, ";")
	parts := make([]string, 0, len(attrs))

	for i, a := range attrs {
		t := strings.TrimSpace(a)
		if t == "" {
			continue
		}
		if i == 0 {
			parts = append(parts, t)
			continue
		}

		low := strings.ToLower(t)
		switch {
		case strings.HasPrefix(low, "domain="):
			continue
		case low == "secure":
			if !secure {
				continue
			}
		case strings.HasPrefix(low, "samesite=none"):
			if secure {
				parts = append(parts, t)
			} else {
				parts = append(parts, "SameSite=Lax")
			}
			continue
		case strings.HasPrefix(low, "path="):
			p := t[len("path="):]
			if !strings.HasPrefix(p, prefix) {
				if p == "/" {
					p = prefix + "/"
				} else if strings.HasPrefix(p, "/") {
					p = prefix + p
				}
			}
			parts = append(parts, "Path="+p)
			continue
		}
		parts = append(parts, t)
	}
	return strings.Join(parts, "; ")
}
