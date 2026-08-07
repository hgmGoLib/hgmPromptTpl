package hgmPromptTpl

import (
	"embed"
	"os"
	"strings"
	"testing"
)

// 拿仓库里真实存在的那个模版包走 embed 这条路：编译期收进二进制，运行时一个文件都不读。
// 收的是整个目录（note.md 也在里面），扫描该忽略的照样忽略。
//
//go:embed example/prompt
var examplePromptFS embed.FS

// embed 这条路和磁盘那条路必须得出一模一样的包：同样的文件名单、同样的变量名单、
// 同样的渲染字节。不然「本地 go run 是对的、编译出来的二进制是另一份」就成立了。
func TestNewFromFSSameAsNewFromDir(t *testing.T) {
	fromFS, err := NewFromFS(examplePromptFS, "example/prompt")
	if err != nil {
		t.Fatalf("NewFromFS 失败: %v", err)
	}
	fromDir, err := NewFromDir("example/prompt")
	if err != nil {
		t.Fatalf("NewFromDir 失败: %v", err)
	}
	assertSameTpl(t, fromFS, fromDir)
}

// fs.FS 不是只有 embed.FS：os.DirFS 也该走得通，本包一个字都不用改。
func TestNewFromFSOnOsDirFS(t *testing.T) {
	fromFS, err := NewFromFS(os.DirFS("."), "example/prompt")
	if err != nil {
		t.Fatalf("NewFromFS(os.DirFS) 失败: %v", err)
	}
	fromDir, err := NewFromDir("example/prompt")
	if err != nil {
		t.Fatalf("NewFromDir 失败: %v", err)
	}
	assertSameTpl(t, fromFS, fromDir)
}

func TestScanFSSameAsScanDir(t *testing.T) {
	fromFS, err := ScanFS(examplePromptFS, "example/prompt")
	if err != nil {
		t.Fatalf("ScanFS 失败: %v", err)
	}
	fromDir, err := ScanDir("example/prompt")
	if err != nil {
		t.Fatalf("ScanDir 失败: %v", err)
	}
	gotPathList := strings.Join(getSortedKeyList(fromFS), " ")
	wantPathList := strings.Join(getSortedKeyList(fromDir), " ")
	if gotPathList != wantPathList {
		t.Fatalf("扫出来的文件名单跟 ScanDir 不一样\nScanFS : %s\nScanDir: %s", gotPathList, wantPathList)
	}
	// 后缀不在收录范围的文件，embed 收进来了但扫描不该收。
	if _, ok := fromFS["note.md"]; ok {
		t.Fatalf("note.md 不该被扫进来")
	}
	for path, content := range fromDir {
		if string(fromFS[path]) != string(content) {
			t.Fatalf("%s 的内容跟 ScanDir 读出来的不一样", path)
		}
	}
}

// dir 传错那一层的后果：key 会多带一层前缀，于是模版里所有 include 都对不上。
// 这个用例钉的是「dir 这个参数不是摆设」，报错要能让人一眼看出是前缀问题。
func TestScanFSWrongDirGivesPrefixedKey(t *testing.T) {
	fileMap, err := ScanFS(examplePromptFS, ".")
	if err != nil {
		t.Fatalf("ScanFS 失败: %v", err)
	}
	if _, ok := fileMap["example/prompt/找bug.ep.txt"]; !ok {
		t.Fatalf("dir 传 \".\" 时 key 该带全前缀，实际扫出来: %s", strings.Join(getSortedKeyList(fileMap), " "))
	}
	// 带前缀的 key 本身是合法路径，所以建包不会在路径校验那儿拦下来，
	// 而是走到 include 找不到目标 —— 报错里就是那个多出来的前缀。
	_, err = NewFromMap(fileMap)
	if err == nil {
		t.Fatalf("dir 传错了居然建得出包")
	}
	if !strings.Contains(err.Error(), "通用规矩.part.txt") {
		t.Fatalf("报错该点出对不上的那个 include 路径: %v", err)
	}
}

func TestScanFSBadDir(t *testing.T) {
	caseList := []struct {
		dir     string
		wantSub string
	}{
		{"./example/prompt", "不是合法的 fs.FS 路径"},
		{"/example/prompt", "不是合法的 fs.FS 路径"},
		{"example/prompt/", "不是合法的 fs.FS 路径"},
		{"", "不是合法的 fs.FS 路径"},
		{"example/没有这个目录", "扫描模版目录"},
	}
	for _, oneCase := range caseList {
		_, err := ScanFS(examplePromptFS, oneCase.dir)
		if err == nil {
			t.Fatalf("dir=%q 居然扫成功了", oneCase.dir)
		}
		if !strings.Contains(err.Error(), oneCase.wantSub) {
			t.Fatalf("dir=%q 的报错里没有 %q: %v", oneCase.dir, oneCase.wantSub, err)
		}
	}
}

func TestMustNewFromFS(t *testing.T) {
	tpl := MustNewFromFS(examplePromptFS, "example/prompt")
	if got := len(tpl.GetEpList()); got != 2 {
		t.Fatalf("入口文件数不对: %d", got)
	}

	const badDir = "example/没有这个目录"
	got := mustPanicError(t, "MustNewFromFS", func() { MustNewFromFS(examplePromptFS, badDir) })
	_, want := NewFromFS(examplePromptFS, badDir)
	if want == nil {
		t.Fatalf("NewFromFS 居然成功了，这个用例的前提没了")
	}
	if got.Error() != want.Error() {
		t.Fatalf("panic 出来的报错跟 NewFromFS 的不一样:\npanic: %v\nNewFromFS: %v", got, want)
	}
}

// assertSameTpl 要求两个包在外部看得见的地方完全一致：入口文件名单、复用文件名单、
// 每个入口文件的变量名单和渲染结果。
func assertSameTpl(t *testing.T, got *Tpl, want *Tpl) {
	t.Helper()
	if g, w := strings.Join(got.GetPartPathList(), " "), strings.Join(want.GetPartPathList(), " "); g != w {
		t.Fatalf("复用文件名单不一样\n得到: %s\n期望: %s", g, w)
	}
	if g, w := strings.Join(got.GetRawPathList(), " "), strings.Join(want.GetRawPathList(), " "); g != w {
		t.Fatalf("原样包含文件名单不一样\n得到: %s\n期望: %s", g, w)
	}
	gotEpList, wantEpList := got.GetEpList(), want.GetEpList()
	if len(gotEpList) != len(wantEpList) {
		t.Fatalf("入口文件数不一样: %d vs %d", len(gotEpList), len(wantEpList))
	}
	for i, gotEp := range gotEpList {
		wantEp := wantEpList[i]
		if gotEp.GetPath() != wantEp.GetPath() {
			t.Fatalf("第 %d 个入口文件路径不一样: %s vs %s", i, gotEp.GetPath(), wantEp.GetPath())
		}
		gotVarNameList := strings.Join(gotEp.GetVarNameList(), " ")
		if wantVarNameList := strings.Join(wantEp.GetVarNameList(), " "); gotVarNameList != wantVarNameList {
			t.Fatalf("%s 的变量名单不一样\n得到: %s\n期望: %s", gotEp.GetPath(), gotVarNameList, wantVarNameList)
		}
		// 冒烟测试可以照着 GetVarNameList 建 varMap，真正的调用点不行（见 Render 的注释）。
		varMap := map[string]string{}
		for _, name := range gotEp.GetVarNameList() {
			varMap[name] = "值-" + name
		}
		if gotEp.MustRender(varMap) != wantEp.MustRender(varMap) {
			t.Fatalf("%s 渲染出来的字节不一样", gotEp.GetPath())
		}
	}
}
