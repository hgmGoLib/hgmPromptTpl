// hgmPromptTpl 是给 AI 提示词用的极小模版引擎，只解决一件事：三份提示词之间的信息复用。
// 完整语法和全部报错条件见 doc/完整口径.txt。
package hgmPromptTpl

import (
	"embed"
	"fmt"
	"sort"
	"strings"
)

// EpSuffix 是入口文件后缀：只能被 Render，禁止被 include。
// PartSuffix 是复用文件后缀：只能被 {{@include}}，禁止被 Render。
// RawSuffix 是原样包含文件后缀：只能被 {{@includeRaw}}，整个文件当字面量，一个字节都不解析。
const (
	EpSuffix   = ".ep.txt"
	PartSuffix = ".part.txt"
	RawSuffix  = ".raw.txt"
)

// Tpl 是一个提示词模版包：一组入口文件和复用文件，外加它们的解析和展开结果。
//
// 构造完成之后引擎再也不碰文件系统 API —— 一开始有啥文件，最后就有啥文件。
// 于是 include 里的路径只是一个 map 的 key，路径穿越这类问题从根上就不存在，
// 也不用去追文件系统那套数不清的字符串解析规则。
//
// Tpl 只有一种状态：全部静态检查都过了。检查是 NewFromMap 的一部分，没有「只解析不检查」
// 这个中间态，也就没有「忘了调 Check」这个失败模式。代价是那些检查绕不过去（比如一个
// 600 字节以下的单调用者 part 会让整个包建不出来），理由见 doc/设计决策.md 第二、十节。
type Tpl struct {
	// fileMap 的 key 是模版包根目录向下的相对路径，分隔符固定 /，与当前系统无关。
	fileMap map[string][]byte
	// tokenMap 是 fileMap 逐个解析出来的 token 序列，key 与 fileMap 相同。
	tokenMap map[string][]token
	// epMap 是每个入口文件展开摊平之后的结果，key 是入口文件路径。
	epMap map[string]*Ep
}

// NewFromMap 用一组「相对路径 -> 文件内容」建模版包，顺带跑完全部静态检查。
//
// 建得出来的包就是检查通过的包：语法、include 指向、死文件、单调用者的小 part、
// 一棵展开树里重复 include —— 全在这一步报完，一次全报不是遇到第一个就返回，
// 每条都带「原始文件名:行号」。查了些什么见 doc/完整口径.txt 第六节。
//
// 这里不收变量名单：模版里用到哪些变量是模版自己说了算的事实，不是调用方声明的东西。
// 拿 Ep.GetVarNameList() 问出来，照着填 Ep.Render 的 varMap。
//
// 内容按原始字节收下：不归一 CRLF、不删 BOM、不 TrimSpace —— 文件里是什么字节，
// 渲染出来就是什么字节，这样别的工具看到结果能一眼对回源文件。
//
// 注意 fileMap 里的 []byte 是直接引用不是拷贝：建包之后再去改自己那份切片，会改到模版内部
// 已经解析好的内容，fileMap 和 tokenMap 还会对不上。别这么干。不做防御性拷贝是因为
// 提示词包动辄几百 KB，而正常用法（ScanDir 读出来直接喂进来、或者 embed 的只读字节）
// 根本不会去改它。
func NewFromMap(fileMap map[string][]byte) (*Tpl, error) {
	tpl := &Tpl{
		fileMap:  make(map[string][]byte, len(fileMap)),
		tokenMap: make(map[string][]token, len(fileMap)),
	}
	// 按路径排序校验，报错顺序才是稳定的。理由跟下面解析那个循环一样，见那里的注释。
	for _, path := range getSortedKeyList(fileMap) {
		if err := validateFilePath(path); err != nil {
			return nil, err
		}
	}
	// 按路径排序解析，报错顺序才是稳定的（map 遍历顺序随机，同一批坏文件每次报不同的一条
	// 会让人以为改动生效了）。
	for _, path := range getSortedKeyList(fileMap) {
		tokenList, err := parseOneFile(path, fileMap[path])
		if err != nil {
			return nil, err
		}
		tpl.fileMap[path] = fileMap[path]
		tpl.tokenMap[path] = tokenList
	}
	epMap, err := tpl.runCheck()
	if err != nil {
		return nil, err
	}
	tpl.epMap = epMap
	return tpl, nil
}

// NewFromDir 扫描一个实体目录建模版包，只收 .ep.txt、.part.txt 和 .raw.txt。
func NewFromDir(dir string) (*Tpl, error) {
	fileMap, err := ScanDir(dir)
	if err != nil {
		return nil, err
	}
	return NewFromMap(fileMap)
}

// NewFromEmbedFS 从一个 //go:embed 进来的 embed.FS 里读模版包，跟 NewFromDir 的区别只有
// 「文件从哪来」：
//
//	//go:embed prompt
//	var promptFS embed.FS
//
//	tpl, err := hgmPromptTpl.NewFromEmbedFS(promptFS, "prompt")
//
// 为什么要有这条路：embed 之后模版包是二进制的一部分，跟着程序走 —— 部署时不用另外带一个目录，
// 也就不会出现「线上那份 prompt 目录被人顺手改了、跟代码里的 varMap 对不上」这种事；而且路径不再
// 跟进程的当前工作目录有关（NewFromDir("example/prompt") 换个目录起进程就找不着了，
// //go:embed 的路径是相对源文件的，编译期就定死）。代价是改提示词得重新编译。
//
// dir 是模版包在 fsys 里的哪个子目录，路径按 embed.FS 那一套：分隔符固定 /，不许 ./、../、
// 开头的 /、结尾的 /，fsys 的根目录写 "."，不合法当场报错。
//
// dir 这个参数是必须的，不是图方便：上面那个 //go:embed 收进来的路径带着 prompt/ 这一层，
// dir 传 "." 的话包里的文件名就成了 prompt/找bug.ep.txt。而文件名就是 include 里写的那个路径
// （永远是模版包根目录向下的相对路径），多这一层前缀等于模版里所有 include 全对不上 ——
// 报出来是「include 的目标文件不存在」，还得从那儿反查回来。传 "prompt" 才对。
//
// 另外 //go:embed 自己有条规矩跟本引擎无关但会咬人：//go:embed prompt 这种写法收不到以 .
// 或 _ 开头的文件和目录。模版文件别那么起名，真要收就写 //go:embed prompt/*。
//
// 建包检查照样在这一步全跑完，跟 NewFromDir 一个字都不差。
func NewFromEmbedFS(fsys embed.FS, dir string) (*Tpl, error) {
	fileMap, err := scanEmbedFS(fsys, dir)
	if err != nil {
		return nil, err
	}
	return NewFromMap(fileMap)
}

// MustNewFromEmbedFS 跟 NewFromEmbedFS 一样，只是出错直接 panic，panic 出来的就是那个 error 本身。
//
// 这个是几个 Must 版里最该用的一个：embed 进来的模版包是本程序自己的一部分，dir 又是代码里
// 写死的字符串 —— 建不出来纯粹是发布前就该发现的问题，而且必然每次启动都失败。
func MustNewFromEmbedFS(fsys embed.FS, dir string) *Tpl {
	tpl, err := NewFromEmbedFS(fsys, dir)
	if err != nil {
		panic(err)
	}
	return tpl
}

// MustNewFromDir 跟 NewFromDir 一样，只是出错直接 panic，panic 出来的就是那个 error 本身
// （报错内容一个字都不变，recover 之后能直接拿去判断和打印）。
//
// 什么时候该用它：dir 是代码里写死的、模版包属于本程序自己的一部分 —— 那种场合建不出来
// 就是发布前该发现的问题，err 只有一条处理方式（打出来退出），写成 if err != nil { log.Fatal }
// 跟 panic 是一回事，少一层缩进而已。
// 什么时候该用 NewFromDir：dir 是运行时才知道的（命令行参数、配置、用户上传的目录），
// 那种情况下建不出来是个正常的运行时状况，得自己处理。
func MustNewFromDir(dir string) *Tpl {
	tpl, err := NewFromDir(dir)
	if err != nil {
		panic(err)
	}
	return tpl
}

// GetEp 按路径取一个入口文件。拿到的 Ep 是自包含的，可以单独存着传出去。
func (t *Tpl) GetEp(epPath string) (*Ep, error) {
	if ep, ok := t.epMap[epPath]; ok {
		return ep, nil
	}
	if _, ok := t.tokenMap[epPath]; ok {
		return nil, fmt.Errorf("%s 不是入口文件：只有 %s 能被渲染，%s 只能被 include",
			epPath, EpSuffix, PartSuffix)
	}
	return nil, fmt.Errorf("模版包里没有入口文件 %s（现有入口文件: %s）",
		epPath, strings.Join(t.getEpPathList(), " "))
}

// MustGetEp 跟 GetEp 一样，只是出错直接 panic，panic 出来的就是那个 error 本身。
//
// 什么时候该用它：epPath 是代码里写死的常量。建包那一步已经把模版包本身检查完了，
// 到这里剩下的唯一失败原因就是这个字符串跟模版包对不上 —— 那是代码写错，不是运行时状况，
// 而且它必然每次都失败，起来就炸比藏在 err 里强。
// 什么时候该用 GetEp：epPath 来自配置、命令行或者别的外部输入。
func (t *Tpl) MustGetEp(epPath string) *Ep {
	ep, err := t.GetEp(epPath)
	if err != nil {
		panic(err)
	}
	return ep
}

// GetEpList 返回本包里全部入口文件（按路径排序）。
// 启动自检要把它们全过一遍，新加的入口文件不用再去哪里登记一次。
func (t *Tpl) GetEpList() []*Ep {
	epList := []*Ep{}
	for _, path := range t.getEpPathList() {
		epList = append(epList, t.epMap[path])
	}
	return epList
}

// GetPartPathList 返回本包里全部复用文件路径（已排序）。
func (t *Tpl) GetPartPathList() []string {
	return t.getPathListBySuffix(PartSuffix)
}

// GetRawPathList 返回本包里全部原样包含文件路径（已排序）。
func (t *Tpl) GetRawPathList() []string {
	return t.getPathListBySuffix(RawSuffix)
}

// parseOneFile 按后缀决定一个文件怎么变成 token 序列。
//
// .raw.txt 走的是「整个文件一个 tokenText，一个字节都不看」这条路 —— 这就是它存在的全部理由：
// here doc 的正确性依赖作者挑一个内容里没有的名字（引擎会查，但那毕竟是一条要理解的规矩），
// 而 .raw.txt 根本没有定界符落在内容里，于是「这里面能放什么」这个问题不存在，答案永远是「随便」。
// 代价是多一次跳转：读模版的人得翻到另一个文件才知道那儿是什么。两条路都留着，写模版的人自己挑。
func parseOneFile(path string, src []byte) ([]token, error) {
	if strings.HasSuffix(path, RawSuffix) {
		if len(src) == 0 {
			return []token{}, nil
		}
		return []token{{Kind: tokenText, Text: src, File: path, Line: 1}}, nil
	}
	return parseFile(path, src)
}

// getEpPathList 返回全部入口文件路径（已排序）。它从 fileMap 算而不是从 epMap 算，
// 因为 runCheck 建 epMap 的时候要用它。
func (t *Tpl) getEpPathList() []string {
	return t.getPathListBySuffix(EpSuffix)
}

func (t *Tpl) getPathListBySuffix(suffix string) []string {
	pathList := []string{}
	for path := range t.fileMap {
		if strings.HasSuffix(path, suffix) {
			pathList = append(pathList, path)
		}
	}
	sort.Strings(pathList)
	return pathList
}

// validateFilePath 校验模版包内的文件路径。NewFromMap 收的是调用方给的 map，
// 里面的 key 没经过 ScanDir 归一，所以要在这里一次性挡住所有花样。
func validateFilePath(path string) error {
	if path == "" {
		return fmt.Errorf("模版文件路径不能是空串")
	}
	if strings.TrimSpace(path) != path {
		return fmt.Errorf("模版文件路径 %q 前后有空白字符", path)
	}
	if strings.Contains(path, "\\") {
		return fmt.Errorf("模版文件路径 %q 含 \\：路径分隔符只支持 /，不管当前系统是 linux 还是 windows", path)
	}
	if strings.HasPrefix(path, "/") {
		return fmt.Errorf("模版文件路径 %q 是绝对路径：只允许模版包根目录向下的相对路径", path)
	}
	for _, seg := range strings.Split(path, "/") {
		switch seg {
		case "":
			return fmt.Errorf("模版文件路径 %q 里有空目录名", path)
		case ".", "..":
			return fmt.Errorf("模版文件路径 %q 里有 %q：故意不支持相对路径写法", path, seg)
		}
	}
	if !strings.HasSuffix(path, EpSuffix) && !strings.HasSuffix(path, PartSuffix) && !strings.HasSuffix(path, RawSuffix) {
		return fmt.Errorf("模版文件路径 %q 后缀不对：只支持 %s、%s、%s", path, EpSuffix, PartSuffix, RawSuffix)
	}
	return nil
}

func getSortedKeyList(fileMap map[string][]byte) []string {
	keyList := make([]string, 0, len(fileMap))
	for key := range fileMap {
		keyList = append(keyList, key)
	}
	sort.Strings(keyList)
	return keyList
}
