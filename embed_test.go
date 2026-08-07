package hgmPromptTpl

import (
	"embed"
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
func TestNewFromEmbedFSSameAsNewFromDir(t *testing.T) {
	fromEmbed, err := NewFromEmbedFS(examplePromptFS, "example/prompt")
	if err != nil {
		t.Fatalf("NewFromEmbedFS 失败: %v", err)
	}
	fromDir, err := NewFromDir("example/prompt")
	if err != nil {
		t.Fatalf("NewFromDir 失败: %v", err)
	}

	if got, want := strings.Join(fromEmbed.GetPartPathList(), " "), strings.Join(fromDir.GetPartPathList(), " "); got != want {
		t.Fatalf("复用文件名单不一样\nembed: %s\ndir  : %s", got, want)
	}
	if got, want := strings.Join(fromEmbed.GetRawPathList(), " "), strings.Join(fromDir.GetRawPathList(), " "); got != want {
		t.Fatalf("原样包含文件名单不一样\nembed: %s\ndir  : %s", got, want)
	}
	embedEpList, dirEpList := fromEmbed.GetEpList(), fromDir.GetEpList()
	if len(embedEpList) != len(dirEpList) {
		t.Fatalf("入口文件数不一样: %d vs %d", len(embedEpList), len(dirEpList))
	}
	for i, embedEp := range embedEpList {
		dirEp := dirEpList[i]
		if embedEp.GetPath() != dirEp.GetPath() {
			t.Fatalf("第 %d 个入口文件路径不一样: %s vs %s", i, embedEp.GetPath(), dirEp.GetPath())
		}
		gotVarNameList := strings.Join(embedEp.GetVarNameList(), " ")
		if wantVarNameList := strings.Join(dirEp.GetVarNameList(), " "); gotVarNameList != wantVarNameList {
			t.Fatalf("%s 的变量名单不一样\nembed: %s\ndir  : %s", embedEp.GetPath(), gotVarNameList, wantVarNameList)
		}
		// 冒烟测试可以照着 GetVarNameList 建 varMap，真正的调用点不行（见 Render 的注释）。
		varMap := map[string]string{}
		for _, name := range embedEp.GetVarNameList() {
			varMap[name] = "值-" + name
		}
		if embedEp.MustRender(varMap) != dirEp.MustRender(varMap) {
			t.Fatalf("%s 渲染出来的字节不一样", embedEp.GetPath())
		}
	}
}

func TestScanEmbedFSSameAsScanDir(t *testing.T) {
	fromEmbed, err := ScanEmbedFS(examplePromptFS, "example/prompt")
	if err != nil {
		t.Fatalf("ScanEmbedFS 失败: %v", err)
	}
	fromDir, err := ScanDir("example/prompt")
	if err != nil {
		t.Fatalf("ScanDir 失败: %v", err)
	}
	gotPathList := strings.Join(getSortedKeyList(fromEmbed), " ")
	wantPathList := strings.Join(getSortedKeyList(fromDir), " ")
	if gotPathList != wantPathList {
		t.Fatalf("扫出来的文件名单跟 ScanDir 不一样\nScanEmbedFS: %s\nScanDir    : %s", gotPathList, wantPathList)
	}
	// 后缀不在收录范围的文件，embed 收进来了但扫描不该收。
	if _, ok := fromEmbed["note.md"]; ok {
		t.Fatalf("note.md 不该被扫进来")
	}
	for path, content := range fromDir {
		if string(fromEmbed[path]) != string(content) {
			t.Fatalf("%s 的内容跟 ScanDir 读出来的不一样", path)
		}
	}
}

// dir 传错那一层的后果：key 会多带一层前缀，于是模版里所有 include 都对不上。
// 这个用例钉的是「dir 这个参数不是摆设」。
func TestScanEmbedFSWrongDirGivesPrefixedKey(t *testing.T) {
	fileMap, err := ScanEmbedFS(examplePromptFS, ".")
	if err != nil {
		t.Fatalf("ScanEmbedFS 失败: %v", err)
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

func TestScanEmbedFSBadDir(t *testing.T) {
	caseList := []struct {
		dir     string
		wantSub string
	}{
		{"./example/prompt", "不是合法的 embed.FS 路径"},
		{"/example/prompt", "不是合法的 embed.FS 路径"},
		{"example/prompt/", "不是合法的 embed.FS 路径"},
		{"", "不是合法的 embed.FS 路径"},
		{"example/没有这个目录", "扫描模版目录"},
	}
	for _, oneCase := range caseList {
		_, err := ScanEmbedFS(examplePromptFS, oneCase.dir)
		if err == nil {
			t.Fatalf("dir=%q 居然扫成功了", oneCase.dir)
		}
		if !strings.Contains(err.Error(), oneCase.wantSub) {
			t.Fatalf("dir=%q 的报错里没有 %q: %v", oneCase.dir, oneCase.wantSub, err)
		}
	}
}

func TestMustNewFromEmbedFS(t *testing.T) {
	tpl := MustNewFromEmbedFS(examplePromptFS, "example/prompt")
	if got := len(tpl.GetEpList()); got != 2 {
		t.Fatalf("入口文件数不对: %d", got)
	}

	const badDir = "example/没有这个目录"
	got := mustPanicError(t, "MustNewFromEmbedFS", func() { MustNewFromEmbedFS(examplePromptFS, badDir) })
	_, want := NewFromEmbedFS(examplePromptFS, badDir)
	if want == nil {
		t.Fatalf("NewFromEmbedFS 居然成功了，这个用例的前提没了")
	}
	if got.Error() != want.Error() {
		t.Fatalf("panic 出来的报错跟 NewFromEmbedFS 的不一样:\npanic: %v\nNewFromEmbedFS: %v", got, want)
	}
}
