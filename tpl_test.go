package hgmPromptTpl

import (
	"strings"
	"testing"
)

// partPad 是垫在复用文件末尾的填充。建包检查里有一条「只被一个文件引用的 part 必须够大，
// 否则该内联回去」，而下面这些用例关心的是渲染结果不是那条规矩 —— 垫一下，比给每个用例
// 都多编一个入口文件去凑引用数干净。一个汉字 3 字节。
var partPad = strings.Repeat("垫", PartMinBytes/3+1)

func padPart(content string) string {
	return content + partPad
}

func mustNew(t *testing.T, fileMap map[string]string) *Tpl {
	t.Helper()
	byteMap := map[string][]byte{}
	for path, content := range fileMap {
		byteMap[path] = []byte(content)
	}
	tpl, err := NewFromMap(byteMap)
	if err != nil {
		t.Fatalf("NewFromMap 失败: %v", err)
	}
	return tpl
}

func mustGetEp(t *testing.T, tpl *Tpl, epPath string) *Ep {
	t.Helper()
	ep, err := tpl.GetEp(epPath)
	if err != nil {
		t.Fatalf("GetEp %s 失败: %v", epPath, err)
	}
	return ep
}

func mustRender(t *testing.T, tpl *Tpl, epPath string, varMap map[string]string) string {
	t.Helper()
	got, err := mustGetEp(t, tpl, epPath).Render(varMap)
	if err != nil {
		t.Fatalf("Render %s 失败: %v", epPath, err)
	}
	return got
}

// mustParse 建一个只有 a.ep.txt 的模版包，返回建包时的报错。
func mustParse(src string) error {
	_, err := NewFromMap(map[string][]byte{"a.ep.txt": []byte(src)})
	return err
}

func TestRenderVarIncludeAndHereDoc(t *testing.T) {
	tpl := mustNew(t, map[string]string{
		"a.ep.txt":       "头\n{{@include p/one.part.txt}}尾 {{linuxIp}}\n",
		"p/one.part.txt": padPart("口令是 {{adminPassword}}\n{{@include p/two.part.txt}}"),
		"p/two.part.txt": padPart("字面量: {{|END|{{linuxIp}} 和 {{@include p/one.part.txt}}|END|}}\n"),
	})
	ep := mustGetEp(t, tpl, "a.ep.txt")

	// 需要哪些变量是模版说了算的事实，渲染之前就能问出来，而且含 include 进来的那些。
	if got := strings.Join(ep.GetVarNameList(), " "); got != "adminPassword linuxIp" {
		t.Fatalf("需要的变量名不对: %s", got)
	}

	got, err := ep.Render(map[string]string{
		"linuxIp":       "10.10.10.10",
		"adminPassword": "pw123",
	})
	if err != nil {
		t.Fatalf("Render 失败: %v", err)
	}
	want := "头\n口令是 pw123\n字面量: {{linuxIp}} 和 {{@include p/one.part.txt}}\n" +
		partPad + partPad + "尾 10.10.10.10\n"
	if got != want {
		t.Fatalf("渲染结果不对\n得到: %q\n期望: %q", got, want)
	}
}

// {{@includeRaw x.raw.txt}} 把整个文件当字面量塞进来：里面放什么都行，一个字节都不解析。
//
// 这一条是 here doc 之外的另一条路。here doc 要作者挑一个内容里没有的名字（引擎会查），
// .raw.txt 根本没有定界符落在内容里，代价是多一次跳转。两条都留着，写模版的人自己挑。
func TestRenderIncludeRaw(t *testing.T) {
	// 这份 raw 内容把所有会让解析器炸的东西都放了一遍：变量写法、include 写法、here doc 的
	// 开头和收尾、裸的 }}、GitHub Actions 的 ${{ }}。它们全都原样出去。
	raw := "{{linuxIp}} {{@include x.part.txt}}\n" +
		"{{|A|里面还嵌了一层|A|}}\n" +
		"裸的 }} 和 ${{ secrets.KEY }}\n" +
		"int a[2][2] = {{1,2},{3,4}};\n"
	tpl := mustNew(t, map[string]string{
		"a.ep.txt":          "头 {{realVar}}\n{{@includeRaw code/demo.raw.txt}}尾\n",
		"code/demo.raw.txt": raw,
	})
	ep := mustGetEp(t, tpl, "a.ep.txt")
	// raw 文件里那个 {{linuxIp}} 是字面量，不算变量：这个入口文件只需要 realVar。
	if got := strings.Join(ep.GetVarNameList(), " "); got != "realVar" {
		t.Fatalf("需要的变量名不对: %s", got)
	}
	got := mustRender(t, tpl, "a.ep.txt", map[string]string{"realVar": "V"})
	if want := "头 V\n" + raw + "尾\n"; got != want {
		t.Fatalf("渲染结果不对\n得到: %q\n期望: %q", got, want)
	}
}

// raw 文件本身不受 .part.txt 那条「单调用者必须够大」的管：它那一跳买的是「这里面能放什么」
// 这个问题彻底消失，逼它内联等于逼作者回去用 here doc。理由见 doc/设计决策.md 第二节。
func TestTinyRawFileIsOk(t *testing.T) {
	tpl := mustNew(t, map[string]string{
		"a.ep.txt":   "取字段写作 {{@includeRaw go.raw.txt}} 就这样",
		"go.raw.txt": "{{.Field}}",
	})
	if got := mustRender(t, tpl, "a.ep.txt", nil); got != "取字段写作 {{.Field}} 就这样" {
		t.Fatalf("结果不对: %q", got)
	}
}

// 变量的值原样输出，不再被当模版扫一遍：值是外部拼进来的，带 {{ 也必须原样交给 AI。
func TestRenderVarValueIsNotExpandedAgain(t *testing.T) {
	tpl := mustNew(t, map[string]string{"a.ep.txt": "{{fileNameList}}"})
	got := mustRender(t, tpl, "a.ep.txt", map[string]string{"fileNameList": "* {{linuxIp}}"})
	if got != "* {{linuxIp}}" {
		t.Fatalf("变量值被二次展开了: %q", got)
	}
}

// 字节进字节出：CRLF、BOM、首尾空白都不许被引擎改掉。
func TestRenderKeepsRawBytes(t *testing.T) {
	tpl := mustNew(t, map[string]string{
		"a.ep.txt":   "\ufeff  第一行\r\n{{@include x.part.txt}}\r\n  ",
		"x.part.txt": padPart("中间\r\n"),
	})
	got := mustRender(t, tpl, "a.ep.txt", nil)
	want := "\ufeff  第一行\r\n中间\r\n" + partPad + "\r\n  "
	if got != want {
		t.Fatalf("字节被加工过了\n得到: %q\n期望: %q", got, want)
	}
}

// GetVarNameList 是「渲染这个入口文件要准备哪些变量」的唯一权威名单：
// 排序去重、含 include 进来的、各个入口文件互相独立。
func TestGetVarNameList(t *testing.T) {
	tpl := mustNew(t, map[string]string{
		"a.ep.txt":   "{{b}} {{a}} {{b}}\n{{@include x.part.txt}}",
		"b.ep.txt":   "另一个入口只用 {{onlyB}}",
		"x.part.txt": padPart("{{c}} {{a}}"),
	})
	if got := strings.Join(mustGetEp(t, tpl, "a.ep.txt").GetVarNameList(), " "); got != "a b c" {
		t.Fatalf("a.ep.txt 需要的变量名不对: %s", got)
	}
	if got := strings.Join(mustGetEp(t, tpl, "b.ep.txt").GetVarNameList(), " "); got != "onlyB" {
		t.Fatalf("b.ep.txt 需要的变量名不对: %s", got)
	}

	// 一个变量都不用的入口文件返回空名单，不是 nil 也不是别的什么。
	noVarTpl := mustNew(t, map[string]string{"a.ep.txt": "一个变量都没有"})
	if got := mustGetEp(t, noVarTpl, "a.ep.txt").GetVarNameList(); len(got) != 0 {
		t.Fatalf("期望空名单，得到: %v", got)
	}

	// 返回的是拷贝：调用方拿去改不该动到包内部的状态。
	ep := mustGetEp(t, tpl, "a.ep.txt")
	ep.GetVarNameList()[0] = "被改了"
	if got := strings.Join(ep.GetVarNameList(), " "); got != "a b c" {
		t.Fatalf("名单被外面改到了: %s", got)
	}
}

// varMap 的 key 集合必须跟这个入口文件用到的变量完全一致：少一个报错，多一个也报错。
// 值可以是空串。
func TestRenderVarMapKeyMustMatch(t *testing.T) {
	tpl := mustNew(t, map[string]string{
		// linuxIp 在第 2 行用了两次、第 3 行又用了一次：报错里的位置要去重，
		// 同一行报两遍只是噪音。
		"a.ep.txt": "第一行\n{{linuxIp}} 和 {{port}} 再 {{linuxIp}}\n再来一次 {{linuxIp}}\n",
	})
	ep := mustGetEp(t, tpl, "a.ep.txt")

	// 对得上就渲染，值是空串也算填了 ——「这一轮就是空的」是个正常意思。
	got, err := ep.Render(map[string]string{"linuxIp": "", "port": "8080"})
	if err != nil {
		t.Fatalf("Render 失败: %v", err)
	}
	if want := "第一行\n 和 8080 再 \n再来一次 \n"; got != want {
		t.Fatalf("空串值没被原样填进去\n得到: %q\n期望: %q", got, want)
	}

	caseList := []struct {
		name        string
		varMap      map[string]string
		wantSubList []string
	}{
		{
			name:   "少一个：报错要带上模版里用到它的全部位置",
			varMap: map[string]string{"port": "8080"},
			// 同一个变量用了两次，两个位置都要给出来。
			wantSubList: []string{`共 1 处`, `少了 "linuxIp"`, "a.ep.txt:2 a.ep.txt:3"},
		},
		{
			name:        "多一个",
			varMap:      map[string]string{"linuxIp": "x", "port": "8080", "extra": "y"},
			wantSubList: []string{`共 1 处`, `多了 "extra"`, "a.ep.txt 根本没用到它"},
		},
		{
			name:        "缺的和多的一次全报，不是遇到第一个就返回",
			varMap:      map[string]string{"extra": "y"},
			wantSubList: []string{`共 3 处`, `少了 "linuxIp"`, `少了 "port"`, `多了 "extra"`},
		},
		{
			name:   "有变量的入口文件传 nil 也算一个都没给",
			varMap: nil,
			// 老接口里 nil 在内部另有「只走结构不填值」的意思，得专门写代码防着调用方传 nil；
			// 现在 nil 和空 map 都只是「key 集合为空」，没有这个双关了。
			wantSubList: []string{`共 2 处`, `少了 "linuxIp"`, `少了 "port"`},
		},
	}
	for _, c := range caseList {
		t.Run(c.name, func(t *testing.T) {
			_, err := ep.Render(c.varMap)
			if err == nil {
				t.Fatalf("期望报错，实际通过了")
			}
			for _, want := range c.wantSubList {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("报错内容不对\n得到: %v\n期望含: %s", err, want)
				}
			}
		})
	}
}

// 一个变量都不用的入口文件，nil 和空 map 都该通过。
func TestRenderNoVarAcceptsNilAndEmptyMap(t *testing.T) {
	tpl := mustNew(t, map[string]string{"a.ep.txt": "一个变量都没有"})
	ep := mustGetEp(t, tpl, "a.ep.txt")
	for _, varMap := range []map[string]string{nil, {}} {
		got, err := ep.Render(varMap)
		if err != nil {
			t.Fatalf("Render(%v) 失败: %v", varMap, err)
		}
		if got != "一个变量都没有" {
			t.Fatalf("结果不对: %q", got)
		}
	}
	// 但多给一个还是要报，哪怕这个入口文件一个变量都不用。
	_, err := ep.Render(map[string]string{"extra": "y"})
	if err == nil || !strings.Contains(err.Error(), "一个都没有") {
		t.Fatalf("期望报「本文件用到的变量: 一个都没有」，得到: %v", err)
	}
}

// 多给了好几个的时候报错必须每次一样：map 遍历顺序随机，不排序的话人改一次跑一次看到的是
// 不同的一条，会以为自己的改动生效了。见 doc/设计决策.md 第九节。
func TestRenderVarMapKeyErrorIsStable(t *testing.T) {
	tpl := mustNew(t, map[string]string{"a.ep.txt": "{{a}} {{b}}"})
	ep := mustGetEp(t, tpl, "a.ep.txt")
	varMap := map[string]string{"z": "", "y": "", "x": "", "w": "", "v": ""}
	first := ""
	for i := 0; i < 200; i++ {
		_, err := ep.Render(varMap)
		if err == nil {
			t.Fatalf("期望报错，实际通过了")
		}
		if i == 0 {
			first = err.Error()
			continue
		}
		if err.Error() != first {
			t.Fatalf("同一份 varMap 报了不同的错\n第一次: %s\n第 %d 次: %s", first, i+1, err)
		}
	}
}

func TestGetEpErrors(t *testing.T) {
	tpl := mustNew(t, map[string]string{
		"a.ep.txt":   "{{@include y.part.txt}}",
		"y.part.txt": padPart("y"),
	})
	caseList := []struct {
		epPath  string
		wantSub string
	}{
		{"y.part.txt", "不是入口文件"},
		{"b.ep.txt", "模版包里没有入口文件"},
	}
	for _, c := range caseList {
		t.Run(c.epPath, func(t *testing.T) {
			_, err := tpl.GetEp(c.epPath)
			if err == nil {
				t.Fatalf("期望报错，实际通过了")
			}
			if !strings.Contains(err.Error(), c.wantSub) {
				t.Fatalf("报错内容不对\n得到: %v\n期望含: %s", err, c.wantSub)
			}
		})
	}
}

// GetEpList 按路径排序，是启动自检拿来遍历的东西：新加入口文件不用再去哪里登记一次。
func TestGetEpList(t *testing.T) {
	tpl := mustNew(t, map[string]string{
		"z.ep.txt":     "{{v}}",
		"a.ep.txt":     "无变量",
		"sub/m.ep.txt": "无变量",
	})
	pathList := []string{}
	for _, ep := range tpl.GetEpList() {
		pathList = append(pathList, ep.GetPath())
	}
	if got := strings.Join(pathList, " "); got != "a.ep.txt sub/m.ep.txt z.ep.txt" {
		t.Fatalf("入口文件列表不对: %s", got)
	}
}

func TestParseErrors(t *testing.T) {
	caseList := []struct {
		name    string
		src     string
		wantSub string
	}{
		{"变量名带空格", "{{ linuxIp }}", "a.ep.txt:1: {{ linuxIp }} 里的变量名不合法"},
		{"变量名数字开头", "{{1abc}}", "变量名不合法"},
		{"变量名带下划线", "{{linux_ip}}", "变量名不合法"},
		{"变量名空", "{{}}", "变量名不合法"},
		{"未闭合的大括号", "第一行\n{{linuxIp\n", "a.ep.txt:2: 有个 {{ 一直到文件结尾都没等到 }}"},
		{"不认识的指令", "{{@includes x.part.txt}}", "不是认识的指令"},
		{"include 后面没路径", "{{@include }}", "后面没写路径"},
		{"include 路径带空格", "{{@include  x.part.txt}}", "路径前后有多余空格"},
		{"include 用反斜杠", "{{@include p\\x.part.txt}}", "分隔符只支持 /"},
		{"include 绝对路径", "{{@include /x.part.txt}}", "是绝对路径"},
		{"include 走 ..", "{{@include ../x.part.txt}}", "故意不支持相对路径"},
		{"include 走 ./", "{{@include ./x.part.txt}}", "故意不支持相对路径"},
		{"include 入口文件", "{{@include b.ep.txt}}", "入口文件只能被渲染，不能被包含"},
		{"include 后缀不对", "{{@include x.txt}}", "只能包含 .part.txt 文件"},
		{"include 拿去包 raw 文件", "{{@include x.raw.txt}}", "原样包含文件请写 {{@includeRaw x.raw.txt}}"},
		{"includeRaw 拿去包 part 文件", "{{@includeRaw x.part.txt}}", "复用文件请写 {{@include x.part.txt}}"},
		{"includeRaw 入口文件", "{{@includeRaw b.ep.txt}}", "入口文件只能被渲染，不能被包含"},
		{"includeRaw 后缀不对", "{{@includeRaw x.txt}}", "只能包含 .raw.txt 文件"},
		{"includeRaw 后面没路径", "{{@includeRaw }}", "后面没写路径"},
		{"here doc 没第二个竖线", "{{|END", "没等到第二个 |"},
		{"here doc 名字不合法", "{{|1x|abc|1x|}}", "here doc 的名字"},
		{"here doc 没收尾", "行一\n{{|END|abc\n", "a.ep.txt:2: here doc {{|END| 一直到文件结尾都没等到 |END|}}"},
		{"{{ 跨行不许当变量名报错", "讲 Vue {{ 后面一大段\n第二行\n第三行 }}", "这个 {{ 到行尾都没等到 }}"},
	}
	for _, c := range caseList {
		t.Run(c.name, func(t *testing.T) {
			err := mustParse(c.src)
			if err == nil {
				t.Fatalf("期望报错，实际通过了")
			}
			if !strings.Contains(err.Error(), c.wantSub) {
				t.Fatalf("报错内容不对\n得到: %v\n期望含: %s", err, c.wantSub)
			}
		})
	}
}

// here doc 跨多行之后，后面那段文本的行号还要对得上。
func TestLineNumberAfterMultiLineHereDoc(t *testing.T) {
	src := "行一\n{{|END|第二行 {{x}}\n第三行\n第四行|END|}}\n{{badName_}}\n"
	err := mustParse(src)
	if err == nil {
		t.Fatalf("期望报错，实际通过了")
	}
	if !strings.Contains(err.Error(), "a.ep.txt:5:") {
		t.Fatalf("here doc 之后的行号不对: %v", err)
	}
}

// here doc 内容里含有自己的收尾时，扫描会在那里就提前闭合：作者写在真正末尾的那个
// 收尾掉到 here doc 外面变成普通文本，中间本该是字面量的 {{xxx}} 会被真的替换掉。
// 这曾经是这套语法唯一会「渲染出一份错的提示词却一个错都不报」的地方，现在由收尾唯一性堵死。
func TestHereDocEarlyCloseIsCaught(t *testing.T) {
	// 作者想让 here doc 一直管到末尾那个 |END|}}，但内容里提到了收尾写法，扫描在那里就闭合了：
	// 后面的「别搞混」变成普通文本，而里面的 {{name}} 本该原样输出的。
	src := "讲讲语法:\n{{|END|Vue 写法是 {{name}}，本引擎的 here doc 收尾写作 |END|}}，别搞混|END|}}\n"
	err := mustParse(src)
	if err == nil {
		t.Fatalf("here doc 提前闭合没被抓出来")
	}
	for _, want := range []string{"a.ep.txt:2:", "在它开头之后出现了 2 次", "第 2, 2 行", "换成一个内容里不会出现的名字"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("报错内容不对\n得到: %v\n期望含: %s", err, want)
		}
	}
}

// 同一个文件里两块 here doc 复用同一个名字：现在报错。
//
// 以前放行（TestHereDocSameNameReusedIsOk），因为当时那条残留终结符检查是启发式的，扫描区间
// 限到「下一个 {{| 之前」，同名复用天然落在区间外。现在换成「开头和收尾在文件里各只有一个」
// 这条硬规则，同名复用就是它的直接代价 —— 换来的是提前闭合结构上不可能，见 doc/设计决策.md 第一节。
func TestHereDocSameNameReusedIsRejected(t *testing.T) {
	err := mustParse("{{|END|字面量 {{a}}|END|}} 中间 {{|END|字面量 {{b}}|END|}}")
	if err == nil {
		t.Fatalf("同名复用没被抓出来")
	}
	for _, want := range []string{"开头 {{|END| 在这个文件里出现了 2 次", "只准开一块", "换个名字"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("报错内容不对\n得到: %v\n期望含: %s", err, want)
		}
	}
	// 换成两个名字就对了。here doc 里的 {{a}} {{b}} 是字面量，所以一个变量都不需要。
	tpl := mustNew(t, map[string]string{
		"a.ep.txt": "{{|VUE|字面量 {{a}}|VUE|}} 中间 {{|GOTPL|字面量 {{b}}|GOTPL|}}",
	})
	if got := mustRender(t, tpl, "a.ep.txt", nil); got != "字面量 {{a}} 中间 字面量 {{b}}" {
		t.Fatalf("结果不对: %q", got)
	}
}

// 只想输出两个字节 }}：内容以 }} 开头，开头 {{|A| 的尾巴会跟它拼成一个 |A|}}。
// 收尾唯一性必须从内容起点数起，从文件头数就成了 2 个，把这个正确写法误报掉。
func TestHereDocCanHoldLoneCloseBrace(t *testing.T) {
	tpl := mustNew(t, map[string]string{"a.ep.txt": "收尾写作 {{|A|}}|A|}} 就这样"})
	if got := mustRender(t, tpl, "a.ep.txt", nil); got != "收尾写作 }} 就这样" {
		t.Fatalf("结果不对: %q", got)
	}
}

// here doc 内容里一个 {{ 都没有不再报错。
//
// 那条检查（「它白写了」）以前兼着兜提前闭合，现在唯一性把那件事接管了，它就只剩风格判断，
// 而且是错的风格判断：它挡住了「我就想输出 }}」，也挡住了「这段外来内容今天没花括号但我不想
// 它以后被解释」。.raw.txt 那边显然不会要求内容里必须有 {{，这边也不该要求。
func TestHereDocWithoutBraceIsOk(t *testing.T) {
	tpl := mustNew(t, map[string]string{"a.ep.txt": "{{|X|一句普通话|X|}}"})
	if got := mustRender(t, tpl, "a.ep.txt", nil); got != "一句普通话" {
		t.Fatalf("结果不对: %q", got)
	}
}

// {{ 跨行的报错不许把整段正文贴进来：body 可能是几十行，贴出来没人看得下去。
func TestUnclosedBraceErrorIsShort(t *testing.T) {
	src := "讲 Vue 模版 {{ 这里是一大段说明\n" + strings.Repeat("正文行\n", 30) + "结尾 }} 完"
	err := mustParse(src)
	if err == nil {
		t.Fatalf("期望报错，实际通过了")
	}
	if len(err.Error()) > 300 {
		t.Fatalf("报错信息 %d 字节，太长了（整段正文被贴进来了）:\n%v", len(err.Error()), err)
	}
	if strings.Contains(err.Error(), "正文行") {
		t.Fatalf("报错里贴了正文:\n%v", err)
	}
}

// here doc 之外的裸 }} 报错：想原样输出 }} 就得包起来。
// 这条不是为了对称，是最后一格兜底，只剩「忘写收尾」那一类要它管，
// 见 TestHereDocMissingTerminatorStolenFromLiteral。
func TestLoneCloseBraceIsError(t *testing.T) {
	err := mustParse("结束符 }} 得包起来")
	if err == nil {
		t.Fatalf("裸 }} 没被抓出来")
	}
	for _, want := range []string{"a.ep.txt:1:", "没有 {{ 与它配对的 }}", "结束符 }} 得包起来"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("报错内容不对\n得到: %v\n期望含: %s", err, want)
		}
	}
}

// 包进 here doc 就是合法的。
func TestCloseBraceInsideHereDocIsOk(t *testing.T) {
	tpl := mustNew(t, map[string]string{"a.ep.txt": "{{|END|Vue 写法 {{name}} 收尾 }}|END|}}"})
	got := mustRender(t, tpl, "a.ep.txt", nil)
	if got != "Vue 写法 {{name}} 收尾 }}" {
		t.Fatalf("结果不对: %q", got)
	}
}

// 裸 }} 的行号要指到它自己那一行，不是这段文本的起点。
func TestStrayCloseBraceLineNumber(t *testing.T) {
	err := mustParse("{{|END|字面量 {{x}}|END|}}\n第二行\n第三行 }} 在这\n")
	if err == nil {
		t.Fatalf("裸 }} 没被抓出来")
	}
	if !strings.Contains(err.Error(), "a.ep.txt:3:") {
		t.Fatalf("行号不对: %v", err)
	}
}

// 老的残留终结符检查（启发式）在这里瞎：中间夹一个别名 here doc，它的扫描区间就被切断，
// 作者真正的 |A|}} 落在区间外看不见，只能靠「裸 }}」兜。收尾唯一性没有区间，直接抓。
func TestHereDocEarlyCloseWithOtherHereDocBetween(t *testing.T) {
	src := "{{|A|Vue 写法 {{name}}, 收尾写 |A|}} 这里开始泄漏 {{linuxIp}}\n" +
		"{{|B|另一个例子 {{z}}|B|}}\n" +
		"作者以为管到这里 |A|}}\n"
	err := mustParse(src)
	if err == nil {
		t.Fatalf("here doc 提前闭合没被抓出来（泄漏区的 {{linuxIp}} 会被真替换）")
	}
	for _, want := range []string{"收尾 |A|}} 在它开头之后出现了 2 次", "第 1, 3 行"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("报错内容不对\n得到: %v\n期望含: %s", err, want)
		}
	}
}

// 老的三条检查（残留终结符 / 内容里没有 {{ / 裸 }}）合起来剩的那个洞：作者写在末尾的 |A|}}
// 掉进了后面另一个 here doc 的内容里，成了字面量而不是普通文本，「裸 }}」也看不见它。
// 那时候这份模版建得出来，还渲染出一份错的提示词（第三行的 {{linuxIp}} 被真替换），退出码 0。
//
// 收尾唯一性堵死了它：|A|}} 在文件里有两个，不管第二个落在哪儿都算。
func TestHereDocEarlyCloseIntoAnotherHereDocIsCaught(t *testing.T) {
	src := "教你写本引擎的模版:\n" +
		"{{|A|变量写作 {{name}}, here doc 收尾写作 |A|}}\n" +
		"正文里的 {{linuxIp}} 会被替换成真值\n" +
		"{{|B|要原样输出就包起来: {{name}} 收尾写 |A|}} 这样|B|}}\n"
	err := mustParse(src)
	if err == nil {
		t.Fatalf("这个洞没堵上：第三行的 {{linuxIp}} 会被真替换，一个错都不报")
	}
	for _, want := range []string{"a.ep.txt:2:", "收尾 |A|}} 在它开头之后出现了 2 次", "第 2, 4 行"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("报错内容不对\n得到: %v\n期望含: %s", err, want)
		}
	}
	// 按报错说的把块改名就全对了：B 内容里那个 |A|}} 只是字面量，没有对应的开头，碍不着谁。
	tpl := mustNew(t, map[string]string{
		"a.ep.txt": "教你写本引擎的模版:\n" +
			"{{|VUE|变量写作 {{name}}, here doc 收尾写作 |A|}}|VUE|}}\n" +
			"正文里的 {{linuxIp}} 会被替换成真值\n" +
			"{{|B|要原样输出就包起来: {{name}} 收尾写 |A|}} 这样|B|}}\n",
	})
	got := mustRender(t, tpl, "a.ep.txt", map[string]string{"linuxIp": "10.10.10.10"})
	if !strings.Contains(got, "正文里的 10.10.10.10 会被替换成真值") {
		t.Fatalf("改名之后的渲染结果不对:\n%s", got)
	}
	if !strings.Contains(got, "变量写作 {{name}}, here doc 收尾写作 |A|}}") {
		t.Fatalf("VUE 块里的内容没被当字面量:\n%s", got)
	}
}

// 「忘了写收尾」是唯一性堵不住的那一类：文件里那个 |A|}} 只有一个，只不过它是别人内容里的
// 字面量。这时候被抓走那块的尾巴会漏成普通文本，靠「裸 }}」兜住。这是最后一格。
func TestHereDocMissingTerminatorStolenFromLiteral(t *testing.T) {
	src := "{{|A| 作者忘了给 A 写收尾 {{x}}\n" +
		"{{|B| 举例: 收尾写作 |A|}} 这样 |B|}}\n"
	err := mustParse(src)
	if err == nil {
		t.Fatalf("忘写收尾没被抓出来")
	}
	if !strings.Contains(err.Error(), "没有 {{ 与它配对的 }}") {
		t.Fatalf("报错内容不对: %v", err)
	}
}

// here doc 名字非法时不许把整段正文贴进报错：名字只在本行里找第二个 |。
func TestHereDocBadNameErrorIsShort(t *testing.T) {
	src := "{{|管道 开头\n" + strings.Repeat("正文行\n", 40) + "| 表格里的竖线 |\n"
	err := mustParse(src)
	if err == nil {
		t.Fatalf("期望报错，实际通过了")
	}
	if len(err.Error()) > 300 {
		t.Fatalf("报错信息 %d 字节，太长了（整段正文被贴进来了）:\n%v", len(err.Error()), err)
	}
	if strings.Contains(err.Error(), "正文行") {
		t.Fatalf("报错里贴了正文:\n%v", err)
	}
}

// 多个坏路径时报的必须是固定那一条：map 遍历顺序随机，不排序的话每次报不同的一条，
// 人会以为自己的改动生效了。
func TestNewFromMapPathErrorIsStable(t *testing.T) {
	badMap := map[string][]byte{
		"/abs.ep.txt":  []byte("x"),
		"a\\b.ep.txt":  []byte("x"),
		"../up.ep.txt": []byte("x"),
		" sp.ep.txt":   []byte("x"),
		"bad.md":       []byte("x"),
	}
	first := ""
	for i := 0; i < 200; i++ {
		_, err := NewFromMap(badMap)
		if err == nil {
			t.Fatalf("期望报错，实际通过了")
		}
		if i == 0 {
			first = err.Error()
			continue
		}
		if err.Error() != first {
			t.Fatalf("同一批坏路径报了不同的错\n第一次: %s\n第 %d 次: %s", first, i+1, err)
		}
	}
}

func TestNewFromMapPathErrors(t *testing.T) {
	caseList := []struct {
		path    string
		wantSub string
	}{
		{"x.txt", "后缀不对"},
		{"/x.ep.txt", "是绝对路径"},
		{"p\\x.ep.txt", "路径分隔符只支持 /"},
		{"../x.ep.txt", "故意不支持相对路径写法"},
		{"./x.ep.txt", "故意不支持相对路径写法"},
		{"p//x.ep.txt", "有空目录名"},
		{" x.ep.txt", "前后有空白字符"},
	}
	for _, c := range caseList {
		t.Run(c.path, func(t *testing.T) {
			_, err := NewFromMap(map[string][]byte{c.path: []byte("x")})
			if err == nil {
				t.Fatalf("期望报错，实际通过了")
			}
			if !strings.Contains(err.Error(), c.wantSub) {
				t.Fatalf("报错内容不对\n得到: %v\n期望含: %s", err, c.wantSub)
			}
		})
	}
}
