package hgmPromptTpl

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
)

// Ep 是一个入口文件展开之后的样子：include 全部摊平，只剩下文本和变量两种东西。
//
// 它是自包含的，不回指 Tpl —— 拿到之后可以单独存着、传出去，调用方不用同时抓着两个对象。
//
// 为什么展开在建包的时候就做完，而不是每次渲染现做：这个引擎没有条件也没有循环，
// 一个入口文件 include 展开成什么形状、用到哪些变量，全由模版本身决定，跟这一次传什么值
// 一点关系都没有。既然是常量就该只算一次。于是 Render 退化成「校验 key 集合 + 顺序写字节」，
// 没有递归、没有 include 查表、没有环检测 —— 那些在 NewFromMap 里全做过了。
type Ep struct {
	path string
	// tokenList 是展开之后的扁平序列，只含 tokenText 和 tokenVar，不再有 tokenInclude。
	//
	// 只摊平 token 本身，不合并相邻文本的字节：token.Text 一直是切进原始文件内容的切片，
	// 合并就得拷贝，内存直接翻倍，也破坏了「字节进字节出」那条零拷贝的路。
	tokenList []token
	// varNameList 是这个入口文件用到的全部变量名，已排序去重。
	varNameList []string
	// varNameMap 是 varNameList 的集合形式，Render 校验 key 用。
	varNameMap map[string]bool
	// textSize 是全部文本 token 的字节数，Render 拿它给 buffer 预分配。
	// 变量值多长不知道，所以这只是个下限，够用了。
	textSize int
}

// GetPath 返回这个入口文件在模版包里的路径。
func (e *Ep) GetPath() string {
	return e.path
}

// GetVarNameList 返回渲染这个入口文件需要准备的全部变量名（已排序去重，含 include 进来的那些）。
//
// 这是变量名单的唯一权威来源：调用方不用再自己维护一份名单跟模版对齐 —— 照着这个填 varMap 就行，
// 多一个少一个 Render 都会报错。
//
// 返回的是拷贝：调用方拿去 append/sort 不该动到包内部的状态。这里付一次拷贝是因为名单就几个
// 字符串，跟 NewFromMap 不拷贝几百 KB 文件内容不是一回事。
func (e *Ep) GetVarNameList() []string {
	return append([]string{}, e.varNameList...)
}

// Render 渲染这个入口文件。
//
// varMap 的 key 集合必须跟 GetVarNameList() 完全一致：少一个报错，多一个也报错。
// 值可以是空串 ——「这个变量这一轮就是空的」是个正常意思，引擎不替调用方判值。
//
// 「多给了也报错」是有意的，它给调用方白送一条 fail-closed。举个真实的例子：一个自动找 bug 的
// 循环要求 linuxIp 和 adminPassword 必须真的出现在提示词里（写死 IP 会让别的实例跑来测同一台
// 机器，写死口令等于把一套环境的口令按到所有环境头上）。假如哪天有人把模版里的 {{linuxIp}}
// 删了改成写死的 IP，渲染出来的提示词依然通顺，光看结果字符串判断不出来 —— 但调用方手写的
// varMap 里还留着 linuxIp 这个 key，于是这里当场报「多给了」。
//
// 代价是一份 varMap 喂不了所有入口文件，得按入口文件各建各的。用法见 example/main.go。
// 注意反过来拿 GetVarNameList() 去驱动建 varMap 就把上面这条保证消掉了 —— 那样 varMap 会跟着
// 模版一起缩。启动自检那种冒烟测试可以这么干，真正的调用点该手写。
func (e *Ep) Render(varMap map[string]string) (string, error) {
	if err := e.checkVarMapKey(varMap); err != nil {
		return "", err
	}
	buf := &bytes.Buffer{}
	buf.Grow(e.textSize)
	// tokenList 里只可能有这两种：include 在建包的时候就展开没了，剩下的形状是确定的。
	// 所以这里不留 default 分支 —— 一条永远走不到的兜底不会让代码更对，只会多一段没测过的路。
	for _, tok := range e.tokenList {
		switch tok.Kind {
		case tokenText:
			buf.Write(tok.Text)
		case tokenVar:
			// 变量值原样写出，不再当模版扫一遍：值是外部拼进来的（比如本轮要处理的文件名列表），
			// 里面万一带 {{ 也必须原样交给 AI，不能被解释成模版。
			buf.WriteString(varMap[tok.Name])
		}
	}
	return buf.String(), nil
}

// MustRender 跟 Render 一样，只是出错直接 panic，panic 出来的就是那个 error 本身。
//
// Render 唯一的失败原因是 varMap 的 key 集合跟模版对不上 —— 值是什么、有多长、内容多脏
// 都不影响成败。也就是说这是代码和模版之间的一处静态矛盾，不是运行时状况：同一个调用点
// 要么永远成功，要么每次都失败。
//
// 什么时候该用它：varMap 的 key 名单是代码里手写死的，也就是 readme.txt 七之一说的那个
// 标准写法。那种调用点的 err 是「起来就炸」的那类，panic 比一路往上传更早暴露。
// 什么时候该用 Render：key 名单是运行时拼出来的（比如按开关增删几个变量），
// 那时候对不上是真会发生的事，得自己处理。
func (e *Ep) MustRender(varMap map[string]string) string {
	text, err := e.Render(varMap)
	if err != nil {
		panic(err)
	}
	return text
}

// checkVarMapKey 校验 varMap 的 key 集合跟本入口文件用到的变量完全一致。
//
// 缺的和多的一次全报出来，不是遇到第一个就返回 —— 一条一条报会让人改一次跑一次。
// 缺的那些还带上模版里用到它的位置，报错要能直接跳过去看。
func (e *Ep) checkVarMapKey(varMap map[string]string) error {
	msgList := []string{}
	for _, name := range e.varNameList {
		if _, ok := varMap[name]; ok {
			continue
		}
		msgList = append(msgList, fmt.Sprintf("少了 %q，模版里用到它的地方: %s",
			name, strings.Join(e.getVarUsePosList(name), " ")))
	}
	// map 遍历顺序是随机的，多给了好几个的时候不排序报错顺序就是飘的，见 doc/设计决策.md 第九节。
	extraNameList := []string{}
	for name := range varMap {
		if !e.varNameMap[name] {
			extraNameList = append(extraNameList, name)
		}
	}
	sort.Strings(extraNameList)
	for _, name := range extraNameList {
		msgList = append(msgList, fmt.Sprintf("多了 %q，%s 根本没用到它", name, e.path))
	}
	if len(msgList) == 0 {
		return nil
	}
	return fmt.Errorf("渲染 %s 的变量表对不上，共 %d 处（varMap 的 key 集合必须跟这个入口文件"+
		"用到的变量完全一致，值可以是空串；本文件用到的变量: %s）:\n  %s",
		e.path, len(msgList), e.getVarNameListText(), strings.Join(msgList, "\n  "))
}

// getVarNameListText 把变量名单拼成报错里那一小段，一个变量都没用到的时候要说人话。
func (e *Ep) getVarNameListText() string {
	if len(e.varNameList) == 0 {
		return "一个都没有"
	}
	return strings.Join(e.varNameList, " ")
}

// getVarUsePosList 返回模版里用到某个变量的全部位置，形如「找bug.ep.txt:12」，
// 按它们在展开结果里出现的先后排，同一个位置只出现一次。
//
// 只在报错的时候才走，所以现算不预存。
func (e *Ep) getVarUsePosList(name string) []string {
	posList := []string{}
	seenMap := map[string]bool{}
	for _, tok := range e.tokenList {
		if tok.Kind != tokenVar || tok.Name != name {
			continue
		}
		pos := fmt.Sprintf("%s:%d", tok.File, tok.Line)
		if seenMap[pos] {
			continue
		}
		seenMap[pos] = true
		posList = append(posList, pos)
	}
	return posList
}

// expandEp 把一个入口文件顺着 include 全部摊平成一个 Ep。NewFromMap 里对每个入口文件跑一遍。
func (t *Tpl) expandEp(epPath string) (*Ep, error) {
	ep := &Ep{path: epPath, varNameMap: map[string]bool{}}
	if err := t.appendExpandedToken(ep, epPath, map[string]bool{}); err != nil {
		return nil, err
	}
	for name := range ep.varNameMap {
		ep.varNameList = append(ep.varNameList, name)
	}
	sort.Strings(ep.varNameList)
	return ep, nil
}

// appendExpandedToken 把一个文件的 token 追加进 ep，遇到 include 就递归展开进去。
//
// seenMap 的作用域是「一个入口文件展开后的整棵树」，进去了就不再拿出来。同一段规矩在最终
// 提示词里出现两遍，AI 只会更困惑，这一定是写错了。顺带把成环也吃掉了：互相 include 必然
// 表现为同一个 part 被访问第二次，直接撞这条报错，所以不用单独做环检测，也不需要深度上限
// （每个 part 每棵树最多出现一次，深度天然有界）。
func (t *Tpl) appendExpandedToken(ep *Ep, filePath string, seenMap map[string]bool) error {
	for _, tok := range t.tokenMap[filePath] {
		switch tok.Kind {
		case tokenText:
			ep.tokenList = append(ep.tokenList, tok)
			ep.textSize += len(tok.Text)
		case tokenVar:
			ep.tokenList = append(ep.tokenList, tok)
			ep.varNameMap[tok.Name] = true
		case tokenInclude, tokenIncludeRaw:
			directive, kindName, existList := includeDirective, "复用文件", t.GetPartPathList()
			if tok.Kind == tokenIncludeRaw {
				directive, kindName, existList = includeRawDirective, "原样包含文件", t.GetRawPathList()
			}
			if _, ok := t.tokenMap[tok.Path]; !ok {
				return fmt.Errorf("%s:%d: {{%s%s}} 找不到这个%s（现有%s: %s）",
					tok.File, tok.Line, directive, tok.Path, kindName, kindName, strings.Join(existList, " "))
			}
			if seenMap[tok.Path] {
				return fmt.Errorf("%s:%d: {{%s%s}} 在同一个入口文件的展开里出现了第二次："+
					"同一段内容不该进最终提示词两遍；两个复用文件互相 include 成环也会撞这条",
					tok.File, tok.Line, directive, tok.Path)
			}
			seenMap[tok.Path] = true
			// .raw.txt 的 token 序列就是一个 tokenText（parseOneFile 里给的），所以这一层递归
			// 对它只是走个形式，进去就出来了；不为它单开一条路是为了让「同一棵树只准出现一次」
			// 和「找不到目标」这两条对两种包含一视同仁。
			if err := t.appendExpandedToken(ep, tok.Path, seenMap); err != nil {
				return err
			}
		}
	}
	return nil
}
