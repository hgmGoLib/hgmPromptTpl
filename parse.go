package hgmPromptTpl

import (
	"bytes"
	"fmt"
	"strings"
)

// tokenKind 是一段解析结果的种类。
type tokenKind int

const (
	// tokenText 是原样输出的普通文本。here doc 解出来的内容也是它。
	tokenText tokenKind = iota
	// tokenVar 是 {{变量名}}。
	tokenVar
	// tokenInclude 是 {{@include 路径}}，目标是 .part.txt，会被当模版继续解析。
	tokenInclude
	// tokenIncludeRaw 是 {{@includeRaw 路径}}，目标是 .raw.txt，整个文件当字面量塞进来。
	tokenIncludeRaw
)

// token 是一段解析结果。
//
// File / Line 记的是「这段东西写在哪个原始文件的第几行」，而不是渲染后的位置。
// 拼完再算行号等于没报 —— 拼出来的是个谁都没见过的 Frankenstein，报「渲染后第 240 行」
// 人是查不下去的。所以在扫描阶段就把来源钉死。
type token struct {
	Kind tokenKind
	Text []byte
	Name string
	Path string
	File string
	Line int
}

// includeDirective 是 include 指令的固定前缀，后面直接跟路径，只准一个空格。
// includeRawDirective 是原样包含指令的前缀，目标文件一个字节都不解析。
//
// 两个前缀不会互相吃掉：@include 后面钉死一个空格，所以 "@includeRaw x" 不是 "@include " 的前缀。
const (
	includeDirective    = "@include "
	includeRawDirective = "@includeRaw "
)

// parseFile 把一个模版文件的原始字节扫成 token 序列。
//
// 这里刻意不用「一串 strings.ReplaceAll + 最后查有没有残留 {{」那套：有了 here doc 之后
// 那套就不成立了 —— here doc 里合法出现的 {{ 会被当成残留误报。只能一趟扫描，边扫边分辨
// here doc / include / 变量 / 普通文本。
func parseFile(file string, src []byte) ([]token, error) {
	tokenList := []token{}
	line := 1      // 扫描位置当前所在行
	textStart := 0 // 当前这段普通文本的起点
	textLine := 1  // 这段普通文本起点所在行
	for i := 0; i < len(src); {
		if src[i] == '\n' {
			line++
			i++
			continue
		}
		if src[i] != '{' || i+1 >= len(src) || src[i+1] != '{' {
			i++
			continue
		}
		// 先查这个 {{ 之前那段普通文本里有没有裸 }}：它在文件里更靠前，先报它读起来才顺。
		if err := checkStrayCloseBrace(file, src[textStart:i], textLine); err != nil {
			return nil, err
		}
		tok, next, err := parseOpen(file, src, i, line)
		if err != nil {
			return nil, err
		}
		if i > textStart {
			tokenList = append(tokenList, token{Kind: tokenText, Text: src[textStart:i], File: file, Line: textLine})
		}
		tokenList = append(tokenList, tok)
		// here doc 可以跨多行，跳过去的这一段里的换行要补进行号。
		line += bytes.Count(src[i:next], []byte("\n"))
		i = next
		textStart = next
		textLine = line
	}
	if err := checkStrayCloseBrace(file, src[textStart:], textLine); err != nil {
		return nil, err
	}
	if len(src) > textStart {
		tokenList = append(tokenList, token{Kind: tokenText, Text: src[textStart:], File: file, Line: textLine})
	}
	return tokenList, nil
}

// checkStrayCloseBrace 抓 here doc 之外的裸 }}。
//
// 规矩：here doc 之外 {{ 和 }} 必须成对；正文里想原样输出 {{ 或 }}，一律用 here doc 包起来
// （或者整段挪进 .raw.txt）。
//
// parseHereDoc 的唯一性检查把「作者写了收尾、引擎却在更早处闭合」这一类结构性地消掉了，
// 所以这条不再是主力，它只剩最后一格要兜：作者压根忘了写收尾，而文件别处某个 here doc 的
// 内容里恰好有一个 |NAME|}} 字面量，被前一个块抓走了。那种情况下被抓走那块的尾巴会漏成普通
// 文本，里面那个 }} 就是裸的，这条抓得住。
//
// 代价：正文里写单个 }} 也得包 here doc。这个代价是认的 —— 正文里出现 }} 的场景（讲模版语法、
// 贴代码）基本都同时含 {{，本来就得包。
func checkStrayCloseBrace(file string, text []byte, textLine int) error {
	at := bytes.Index(text, []byte("}}"))
	if at < 0 {
		return nil
	}
	line := textLine + bytes.Count(text[:at], []byte("\n"))
	lineStart := bytes.LastIndexByte(text[:at], '\n') + 1 // 没有换行时正好是 0
	return fmt.Errorf("%s:%d: 这里有一个没有 {{ 与它配对的 }}：here doc 之外 {{ 和 }} 必须成对，"+
		"正文里要原样输出 {{ 或 }} 请用 here doc 包起来 {{|VUE|...|VUE|}}，整段外来内容也可以挪进一个 %s 文件用 {{%s路径}} 包含进来。"+
		"如果这个 }} 是某个 here doc 的收尾 |NAME|}}，那多半是上面某个 here doc 忘了写收尾，把这个抓走了。这一行是: %s",
		file, line, RawSuffix, includeRawDirective, getBriefHead(string(text[lineStart:])))
}

// parseOpen 解析 src[i:] 处的一个 {{...}}，返回 token 和它后面第一个字节的下标。
func parseOpen(file string, src []byte, i int, line int) (token, int, error) {
	rest := src[i+2:]
	if len(rest) > 0 && rest[0] == '|' {
		return parseHereDoc(file, src, i, line)
	}
	closeAt := bytes.Index(rest, []byte("}}"))
	if closeAt < 0 {
		return token{}, 0, fmt.Errorf("%s:%d: 有个 {{ 一直到文件结尾都没等到 }}", file, line)
	}
	body := string(rest[:closeAt])
	// 变量名和 include 路径都不可能跨行，所以 body 里出现换行就是「这个 {{ 本来就该是普通文本」，
	// 后面那个 }} 只是碰巧撞上的。这时候不能按变量名报错：body 可能是几十行正文，
	// 整段贴进报错里没人看得下去。
	if strings.Contains(body, "\n") {
		return token{}, 0, fmt.Errorf("%s:%d: 这个 {{ 到行尾都没等到 }}（往后第 %d 行才有一个 }}）："+
			"{{...}} 不能跨行。正文里要写字面量 {{ 请用 here doc 包起来 {{|END|...|END|}}。"+
			"这个 {{ 后面开头是: %s", file, line, strings.Count(body, "\n"), getBriefHead(body))
	}
	next := i + 2 + closeAt + 2
	if strings.HasPrefix(body, "@") {
		switch {
		case strings.HasPrefix(body, includeRawDirective):
			path := strings.TrimPrefix(body, includeRawDirective)
			if err := validateIncludePath(file, line, includeRawDirective, path, RawSuffix); err != nil {
				return token{}, 0, err
			}
			return token{Kind: tokenIncludeRaw, Path: path, File: file, Line: line}, next, nil
		case strings.HasPrefix(body, includeDirective):
			path := strings.TrimPrefix(body, includeDirective)
			if err := validateIncludePath(file, line, includeDirective, path, PartSuffix); err != nil {
				return token{}, 0, err
			}
			return token{Kind: tokenInclude, Path: path, File: file, Line: line}, next, nil
		default:
			return token{}, 0, fmt.Errorf("%s:%d: {{%s}} 不是认识的指令，@ 开头的只支持 {{%s路径}} 和 {{%s路径}}",
				file, line, body, includeDirective, includeRawDirective)
		}
	}
	if !isName(body) {
		return token{}, 0, fmt.Errorf("%s:%d: {{%s}} 里的变量名不合法：只允许英文字母开头、后面接英文字母或数字，"+
			"而且前后不许有空格（{{ x }} 这种带空格的不认，请写成 {{x}}）", file, line, body)
	}
	return token{Kind: tokenVar, Name: body, File: file, Line: line}, next, nil
}

// parseHereDoc 解析 {{|END|任意内容|END|}}，中间的东西一律当字面量：
// 里面的 {{xxx}} 不替换，{{@include}} 也不展开。
//
// 「作者自己挑一个内容里没有的定界符」这套跟 Rust 的 r#"..."#、C++ 的 R"delim(...)delim"、
// shell 的 <<'EOF'、MIME 的 boundary 是同一套。这套本身是能做对的（内容有限，名字无限，
// 拿「比内容里最长的那串字母数字再长一个」当名字就一定不撞），问题从来不是「没有正确用法」，
// 而是「执行规矩的是人，执行错了没人拦」——MIME 那边直接写成「这是发送方的责任」，
// Rust / C# 也只说「不够就多加几个 # / 引号」，都没人查。而这里一旦撞上，剩下的残文是一份
// 合法提示词（Rust / C++ 那边编译不过，当场就知道），本该是字面量的 {{xxx}} 会被真替换掉。
//
// 所以这里把「够长够独特」换成一条机器查得动的等价性质：**这个名字的开头和收尾在文件里各只有一个**。
// 于是提前闭合从「尽量抓」变成「结构上不可能」：设开头在 p，引擎找到的收尾在 q，作者想要的在 q'，
// 若 q ≠ q' 则文件里至少有两个 |NAME|}}，唯一性当场报错；反过来只要过了检查，q 就必然等于 q'。
//
// 这条换掉了原先的两条启发式（「闭合后到下一个 {{| 之前不许再有 |NAME|}}」和「内容里必须含 {{」），
// 它俩都有盲区，理由和被换掉的过程见 doc/设计决策.md 第一节。
func parseHereDoc(file string, src []byte, i int, line int) (token, int, error) {
	rest := src[i+3:] // 跳过 {{|
	// 名字只在本行里找第二个 |。合法名字是「字母开头的字母数字」，本来就跨不了行；不限行的话，
	// 正文里写个 {{|管道 会一路扫到几十行之后表格里的那个 |，于是「名字」变成一大段正文，
	// 报错里 %q 一贴就是几千字节没人看得下去 —— 跟 {{...}} 跨行那条是同一个坑。
	nameZone := rest
	if nl := bytes.IndexByte(nameZone, '\n'); nl >= 0 {
		nameZone = nameZone[:nl]
	}
	nameEnd := bytes.IndexByte(nameZone, '|')
	if nameEnd < 0 {
		return token{}, 0, fmt.Errorf("%s:%d: here doc 的开头 {{| 后面到行尾都没等到第二个 |，"+
			"正确写法是 {{|END|内容|END|}}。这个 {{| 后面开头是: %s", file, line, getBriefHead(string(nameZone)))
	}
	name := string(nameZone[:nameEnd])
	if !isName(name) {
		return token{}, 0, fmt.Errorf("%s:%d: here doc 的名字 %q 不合法：只允许英文字母开头、后面接英文字母或数字",
			file, line, getBriefHead(name))
	}
	contentStart := i + 3 + nameEnd + 1
	opener := "{{|" + name + "|"
	terminator := "|" + name + "|}}"

	// 开头唯一：一个名字在一个文件里只准开一块 here doc。
	// 这一半不是提前闭合所必需的（收尾唯一就够了），它管的是另外两件事：
	// 「同名开两块」时报得准（否则第二块只会得到一句莫名其妙的「没等到收尾」），
	// 以及不让别处的字面量里出现一个正在用的开头 —— 那种写法读的人分不清哪个是真的。
	if n := bytes.Count(src, []byte(opener)); n > 1 {
		return token{}, 0, fmt.Errorf("%s:%d: here doc 的开头 %s 在这个文件里出现了 %d 次（第 %s 行）："+
			"一个名字在一个文件里只准开一块 here doc，再开一块请换个名字。"+
			"名字按内容取（VUE / GOTPL / YAML 这种），别都叫 END",
			file, line, opener, n, joinLineList(findOccurLineList(src, 0, opener)))
	}

	// 收尾唯一：从本块开头之后数起，|NAME|}} 只准有一个。这一条就是「提前闭合结构上不可能」的全部依据。
	//
	// 必须从 contentStart 数，不能从文件头数：内容以 }} 开头的时候（{{|A|}}|A|}}，就为了输出
	// 两个字节 }}），开头 {{|A| 的尾巴跟内容开头的 }} 会拼成一个 |A|}}，从头数就是 2 个，误报。
	switch n := bytes.Count(src[contentStart:], []byte(terminator)); {
	case n == 0:
		return token{}, 0, fmt.Errorf("%s:%d: here doc %s 一直到文件结尾都没等到 %s", file, line, opener, terminator)
	case n > 1:
		return token{}, 0, fmt.Errorf("%s:%d: here doc %s 的收尾 %s 在它开头之后出现了 %d 次（第 %s 行）："+
			"引擎在第一个那里就闭合，后面本该是字面量的内容会被当模版解释，渲染出一份错的提示词。"+
			"一个 here doc 的内容里不许出现它自己的 %s —— 把 %s 换成一个内容里不会出现的名字"+
			"（名字按内容取，VUE / GOTPL / YAML 这种），或者整段挪进一个 %s 文件用 {{%s路径}} 包含进来",
			file, line, opener, terminator, n,
			joinLineList(findOccurLineList(src, contentStart, terminator)),
			terminator, name, RawSuffix, includeRawDirective)
	}

	closeAt := bytes.Index(src[contentStart:], []byte(terminator))
	content := src[contentStart : contentStart+closeAt]
	end := contentStart + closeAt + len(terminator)
	return token{Kind: tokenText, Text: content, File: file, Line: line}, end, nil
}

// findOccurLineList 返回 src[from:] 里 sub 每次出现所在的行号（按整个 src 从 1 起算）。
// 只在报错的时候才走，所以怎么慢都无所谓；判断走的是 bytes.Count。
func findOccurLineList(src []byte, from int, sub string) []int {
	lineList := []int{}
	for at := from; ; {
		idx := bytes.Index(src[at:], []byte(sub))
		if idx < 0 {
			return lineList
		}
		abs := at + idx
		lineList = append(lineList, 1+bytes.Count(src[:abs], []byte("\n")))
		at = abs + len(sub)
	}
}

func joinLineList(lineList []int) string {
	textList := make([]string, 0, len(lineList))
	for _, line := range lineList {
		textList = append(textList, fmt.Sprint(line))
	}
	return strings.Join(textList, ", ")
}

// briefHeadRuneCount 是报错里引用一段原文时最多带几个字符。
const briefHeadRuneCount = 30

// getBriefHead 取一段文本开头的一小截用于报错：只取第一行，并且按字符截断（不切坏 UTF-8）。
func getBriefHead(s string) string {
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		s = s[:idx]
	}
	runeList := []rune(s)
	if len(runeList) <= briefHeadRuneCount {
		return s
	}
	return string(runeList[:briefHeadRuneCount]) + "……"
}

// validateIncludePath 校验 @include / @includeRaw 后面的路径。wantSuffix 是这条指令唯一认的后缀。
//
// 这里故意不 trim、不认 \、不认相对路径写法：路径就是 map 的 key，多一种等价写法就多一种
// 「同一个 part 被当成两个」的可能，第 5 条那个重复检查就会漏。
func validateIncludePath(file string, line int, directive string, path string, wantSuffix string) error {
	switch {
	case path == "":
		return fmt.Errorf("%s:%d: {{%s}} 后面没写路径", file, line, directive)
	case strings.TrimSpace(path) != path:
		return fmt.Errorf("%s:%d: {{%s%s}} 的路径前后有多余空格", file, line, directive, path)
	case strings.Contains(path, "\\"):
		return fmt.Errorf("%s:%d: {{%s%s}} 的路径含 \\：分隔符只支持 /，不管当前系统是 linux 还是 windows", file, line, directive, path)
	case strings.HasPrefix(path, "/"):
		return fmt.Errorf("%s:%d: {{%s%s}} 是绝对路径：只允许模版包根目录向下的相对路径", file, line, directive, path)
	}
	for _, seg := range strings.Split(path, "/") {
		switch seg {
		case "":
			return fmt.Errorf("%s:%d: {{%s%s}} 的路径里有空目录名", file, line, directive, path)
		case ".", "..":
			return fmt.Errorf("%s:%d: {{%s%s}} 的路径里有 %q：故意不支持相对路径，写法永远是从模版包根目录向下",
				file, line, directive, path, seg)
		}
	}
	if strings.HasSuffix(path, wantSuffix) {
		return nil
	}
	// 后缀不对。这里分情况给具体建议而不是只说「后缀不对」：写错的人多半是拿混了两条指令，
	// 直接告诉他该用哪条，比让他回去翻 doc/完整口径.txt 快。
	switch {
	case strings.HasSuffix(path, EpSuffix):
		return fmt.Errorf("%s:%d: {{%s%s}} 想包含一个入口文件：入口文件只能被渲染，不能被包含", file, line, directive, path)
	case strings.HasSuffix(path, PartSuffix):
		return fmt.Errorf("%s:%d: {{%s%s}} 的后缀不对：%s 只认 %s，复用文件请写 {{%s%s}}",
			file, line, directive, path, directive, wantSuffix, includeDirective, path)
	case strings.HasSuffix(path, RawSuffix):
		return fmt.Errorf("%s:%d: {{%s%s}} 的后缀不对：%s 只认 %s，原样包含文件请写 {{%s%s}}",
			file, line, directive, path, directive, wantSuffix, includeRawDirective, path)
	}
	return fmt.Errorf("%s:%d: {{%s%s}} 的后缀不对：%s 只能包含 %s 文件", file, line, directive, path, directive, wantSuffix)
}

// isName 判断变量名 / here doc 名字是否合法：英文字母开头，后面接英文字母或数字。
// 变量不许以 @ 开头这条由这个字符集顺带保证了。
func isName(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
		case c >= '0' && c <= '9':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}
