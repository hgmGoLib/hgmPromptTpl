package hgmPromptTpl

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// PartMinBytes 是「只被 1 个文件引用」的复用文件允许存在的最小字节数。
//
// 只被 1 个地方引用 = 单调用者，本该内联回调用者，多一层跳转只是降低可读性。
// 但整段大文本内联回去会把入口文件重新撑成之前那样，所以给一个体量豁免：够大的段落
// 就算只有一个调用者也允许单独放着。600 字节全中文约 200 个汉字。
const PartMinBytes = 600

// runCheck 是整个模版包的编译检查，NewFromMap 的最后一步：跑一遍就把所有能静态发现的问题
// 一次全报出来，不用等到真去渲染某一个入口文件才炸。检查通过之后顺手把每个入口文件展开成 Ep 返回。
//
// 这里不查变量名：模版用了哪些变量是模版说了算的事实，没有一份「合法名单」可以拿来比对。
// 「调用方能不能填上这些变量」是调用方那头的事，拿 Ep.GetVarNameList() 自己判，
// 填错了 Ep.Render 会报。
//
// 检查项（每一项都会把所有问题收齐，不是遇到第一个就返回）:
//  1. 模版包里至少要有一个入口文件
//  2. include / includeRaw 的目标文件都存在
//  3. 复用文件的引用数（只算从入口文件能走到的那些文件发出的 include）：
//     0 个引用一律报错；1 个引用且内容小于 PartMinBytes 也报错。
//     .raw.txt 只查 0 个引用那一半，理由见下面那段注释
//  4. 每个入口文件展开一遍：同一个被包含文件在一棵展开树里不能出现两次（成环也撞这条）
func (t *Tpl) runCheck() (map[string]*Ep, error) {
	msgList := []string{}
	seenMsgMap := map[string]bool{}
	addMsg := func(format string, args ...any) {
		msg := fmt.Sprintf(format, args...)
		if seenMsgMap[msg] {
			return
		}
		seenMsgMap[msg] = true
		msgList = append(msgList, msg)
	}

	epPathList := t.getEpPathList()
	if len(epPathList) == 0 {
		addMsg("模版包里一个 %s 入口文件都没有：多半是根目录填错了", EpSuffix)
	}

	// reachableMap 是「从入口文件出发顺着 include 能走到的全部文件」。
	// 引用数只认这个集合里发出的 include：两个谁也够不着的复用文件互相 include，引用数各自
	// 都能凑到 1，就一起从下面的死文件检查底下溜过去了；而它俩又是个环，第 4 项按入口展开
	// 也永远走不到它们。于是仓库里躺着一对自循环的孤儿，检查说没问题。从入口扫就没这个盲区。
	reachableMap := t.getReachableFileMap(epPathList)

	// refMap[被引用的 part] = 引用它的文件集合。同一个文件里写两遍只算一次引用，
	// 「引用数」的口径是「引用它的不同可达文件数」。
	refMap := map[string]map[string]bool{}
	for _, filePath := range getSortedKeyList(t.fileMap) {
		for _, tok := range t.tokenMap[filePath] {
			if tok.Kind != tokenInclude && tok.Kind != tokenIncludeRaw {
				continue
			}
			if _, ok := t.tokenMap[tok.Path]; !ok {
				if tok.Kind == tokenIncludeRaw {
					addMsg("%s:%d: {{%s%s}} 找不到这个原样包含文件（现有原样包含文件: %s）",
						tok.File, tok.Line, includeRawDirective, tok.Path, strings.Join(t.GetRawPathList(), " "))
				} else {
					addMsg("%s:%d: {{%s%s}} 找不到这个复用文件（现有复用文件: %s）",
						tok.File, tok.Line, includeDirective, tok.Path, strings.Join(t.GetPartPathList(), " "))
				}
				continue
			}
			if !reachableMap[filePath] {
				continue
			}
			if refMap[tok.Path] == nil {
				refMap[tok.Path] = map[string]bool{}
			}
			refMap[tok.Path][filePath] = true
		}
	}

	for _, partPath := range t.GetPartPathList() {
		refFileList := getSortedSetKeyList(refMap[partPath])
		switch {
		case len(refFileList) == 0:
			addMsg("复用文件 %s 从入口文件出发走不到：死文件，要么接上要么删掉"+
				"（只有同样走不到的复用文件在 include 它也算走不到，两个孤儿互相 include 照样是死的）", partPath)
		case len(refFileList) == 1 && len(t.fileMap[partPath]) < PartMinBytes:
			addMsg("复用文件 %s 只被 %s 一个文件引用，内容才 %d 字节（下限 %d）："+
				"单调用者该内联回去，把内容搬回 %s 并删掉这个文件",
				partPath, refFileList[0], len(t.fileMap[partPath]), PartMinBytes, refFileList[0])
		}
	}

	// .raw.txt 只查死文件，不受 PartMinBytes 那条管。
	//
	// PartMinBytes 的道理是「单调用者多一层跳转只是降低可读性」，前提是内联回去不花代价 ——
	// 对 .part.txt 成立（把文字搬回去就行）。对 .raw.txt 不成立：那一跳买的是「这里面能放什么」
	// 这个问题彻底消失，逼它内联等于逼作者回去用 here doc 那条路。两条路并存、由写模版的人挑，
	// 是有意的，所以不给 .raw.txt 加体量门槛。死文件那条照管：没人引用的 .raw.txt 就是垃圾。
	for _, rawPath := range t.GetRawPathList() {
		if len(refMap[rawPath]) == 0 {
			addMsg("原样包含文件 %s 从入口文件出发走不到：死文件，要么接上要么删掉", rawPath)
		}
	}

	// 展开每个入口文件。这一步既是检查（专门抓「同一棵树里重复 include」和「互相 include 成环」），
	// 也是产出：展开结果就是渲染时要用的 Ep。展开不了的入口文件不进 epMap，反正下面会整体报错。
	epMap := map[string]*Ep{}
	for _, epPath := range epPathList {
		ep, err := t.expandEp(epPath)
		if err != nil {
			addMsg("%s", err)
			continue
		}
		epMap[epPath] = ep
	}

	if len(msgList) > 0 {
		return nil, errors.New("模版包检查不通过，共 " + fmt.Sprint(len(msgList)) + " 个问题:\n  " + strings.Join(msgList, "\n  "))
	}
	return epMap, nil
}

// getReachableFileMap 从入口文件出发顺着 include 走一遍，返回走得到的全部文件（含入口文件自己）。
//
// 这里带 visited 集合，所以互相 include 成环也只是各访问一次就停，不会转不出来；
// 走不通的 include（目标文件不存在）跳过，那种问题由上面的存在性检查单独报。
func (t *Tpl) getReachableFileMap(epPathList []string) map[string]bool {
	reachableMap := map[string]bool{}
	var walk func(filePath string)
	walk = func(filePath string) {
		if reachableMap[filePath] {
			return
		}
		reachableMap[filePath] = true
		for _, tok := range t.tokenMap[filePath] {
			if tok.Kind != tokenInclude && tok.Kind != tokenIncludeRaw {
				continue
			}
			if _, ok := t.tokenMap[tok.Path]; ok {
				walk(tok.Path)
			}
		}
	}
	for _, epPath := range epPathList {
		walk(epPath)
	}
	return reachableMap
}

func getSortedSetKeyList(set map[string]bool) []string {
	keyList := make([]string, 0, len(set))
	for key := range set {
		keyList = append(keyList, key)
	}
	sort.Strings(keyList)
	return keyList
}
