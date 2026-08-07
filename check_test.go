package hgmPromptTpl

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 建包检查跑的是模版自己的问题，跟调用方打算传什么变量无关：这里用了 linuxIp、
// 没有任何地方声明过它，照样该通过。变量名单是从模版算出来的（Ep.GetVarNameList），
// 不是拿来比对的东西。
func TestNewFromMapPass(t *testing.T) {
	bigPart := strings.Repeat("长内容", PartMinBytes) // 一个汉字 3 字节，稳过下限
	tpl := mustNew(t, map[string]string{
		"a.ep.txt":        "{{@include shared.part.txt}}{{@include only.part.txt}}",
		"b.ep.txt":        "{{@include shared.part.txt}} {{linuxIp}}",
		"shared.part.txt": "短的，但被两个入口引用",
		"only.part.txt":   bigPart,
	})
	if got := strings.Join(mustGetEp(t, tpl, "b.ep.txt").GetVarNameList(), " "); got != "linuxIp" {
		t.Fatalf("b.ep.txt 需要的变量名不对: %s", got)
	}
}

func TestNewFromMapCheckErrors(t *testing.T) {
	bigPart := strings.Repeat("长内容", PartMinBytes)
	caseList := []struct {
		name    string
		fileMap map[string]string
		wantSub string
	}{
		{
			name: "没人引用的复用文件一律报错，不看长度",
			fileMap: map[string]string{
				"a.ep.txt":      "正文",
				"dead.part.txt": bigPart,
			},
			wantSub: "从入口文件出发走不到：死文件",
		},
		{
			// 两个入口够不着的复用文件互相 include：引用数各自都能凑到 1，按「有没有人 include」
			// 算就一起溜过去了；而它俩又是个环，按入口展开也永远走不到。必须一起报死文件。
			name: "入口够不着的两个复用文件互相 include",
			fileMap: map[string]string{
				"a.ep.txt":   "入口，谁也不引用",
				"x.part.txt": bigPart + "{{@include y.part.txt}}",
				"y.part.txt": bigPart + "{{@include x.part.txt}}",
			},
			wantSub: "复用文件 x.part.txt 从入口文件出发走不到",
		},
		{
			name: "只被一个文件引用且不到下限，要求内联回去",
			fileMap: map[string]string{
				"a.ep.txt":      "{{@include tiny.part.txt}}",
				"tiny.part.txt": "就一句话",
			},
			wantSub: "单调用者该内联回去",
		},
		{
			name: "同一个文件里引用两遍只算一次引用",
			fileMap: map[string]string{
				"a.ep.txt":      "{{@include tiny.part.txt}}{{@include tiny.part.txt}}",
				"tiny.part.txt": "就一句话",
			},
			wantSub: "只被 a.ep.txt 一个文件引用",
		},
		{
			name: "没人引用的原样包含文件也是死文件",
			fileMap: map[string]string{
				"a.ep.txt":     "正文",
				"dead.raw.txt": "{{.Field}}",
			},
			wantSub: "原样包含文件 dead.raw.txt 从入口文件出发走不到：死文件",
		},
		{
			name: "includeRaw 的目标不存在",
			fileMap: map[string]string{
				"a.ep.txt":    "{{@includeRaw nope.raw.txt}}",
				"has.raw.txt": "{{.Field}}",
			},
			wantSub: "a.ep.txt:1: {{@includeRaw nope.raw.txt}} 找不到这个原样包含文件（现有原样包含文件: has.raw.txt）",
		},
		{
			// 「同一棵展开树里只能出现一次」对 raw 文件一视同仁：同一段内容进最终提示词两遍
			// 一定是写错了，跟它是 part 还是 raw 没关系。
			name: "同一棵展开树里 includeRaw 两次",
			fileMap: map[string]string{
				"a.ep.txt":  "{{@includeRaw x.raw.txt}}{{@includeRaw x.raw.txt}}",
				"x.raw.txt": "{{.Field}}",
			},
			wantSub: "{{@includeRaw x.raw.txt}} 在同一个入口文件的展开里出现了第二次",
		},
		{
			name:    "一个入口文件都没有",
			fileMap: map[string]string{},
			wantSub: "一个 .ep.txt 入口文件都没有",
		},
		{
			name: "include 的复用文件不存在",
			fileMap: map[string]string{
				"a.ep.txt": "{{@include nope.part.txt}}",
			},
			wantSub: "a.ep.txt:1: {{@include nope.part.txt}} 找不到这个复用文件",
		},
		{
			name: "同一棵展开树里重复 include",
			fileMap: map[string]string{
				"a.ep.txt":   "{{@include x.part.txt}}{{@include y.part.txt}}",
				"b.ep.txt":   "{{@include x.part.txt}}{{@include y.part.txt}}",
				"x.part.txt": bigPart,
				"y.part.txt": "{{@include x.part.txt}}" + bigPart,
			},
			wantSub: "出现了第二次",
		},
		{
			// 互相 include 必然表现为同一个 part 被访问第二次，撞的是上面那条，
			// 所以不用单独做环检测。
			name: "两个复用文件互相 include 成环",
			fileMap: map[string]string{
				"a.ep.txt":   "{{@include x.part.txt}}",
				"x.part.txt": bigPart + "{{@include y.part.txt}}",
				"y.part.txt": bigPart + "{{@include x.part.txt}}",
			},
			wantSub: "出现了第二次",
		},
	}
	for _, c := range caseList {
		t.Run(c.name, func(t *testing.T) {
			byteMap := map[string][]byte{}
			for path, content := range c.fileMap {
				byteMap[path] = []byte(content)
			}
			_, err := NewFromMap(byteMap)
			if err == nil {
				t.Fatalf("期望报错，实际通过了")
			}
			if !strings.Contains(err.Error(), c.wantSub) {
				t.Fatalf("报错内容不对\n得到: %v\n期望含: %s", err, c.wantSub)
			}
		})
	}
}

// 建包检查的意义就在于「跑一遍把能静态发现的都报出来」，不是遇到第一个就返回。
func TestNewFromMapReportsEveryProblemAtOnce(t *testing.T) {
	_, err := NewFromMap(map[string][]byte{
		"a.ep.txt":      []byte("{{@include nope.part.txt}}\n{{@include alsoNope.part.txt}}\n{{@include tiny.part.txt}}"),
		"tiny.part.txt": []byte("单调用者，还没到下限"),
		"dead.part.txt": []byte("没人要我"),
	})
	if err == nil {
		t.Fatalf("期望报错，实际通过了")
	}
	for _, want := range []string{"nope.part.txt", "alsoNope.part.txt", "tiny.part.txt", "dead.part.txt", "共 4 个问题"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("报错里少了 %s:\n%v", want, err)
		}
	}
}

// 拿仓库里真实存在的那个模版包（example/prompt）走一遍 NewFromDir：读磁盘、扫目录、
// 建包检查全套。顺带钉住 README 和 example 里演示的那个包必须一直是能建出来的。
func TestNewFromDirOnExamplePrompt(t *testing.T) {
	tpl, err := NewFromDir("example/prompt")
	if err != nil {
		t.Fatalf("NewFromDir 失败: %v", err)
	}
	pathList := []string{}
	for _, ep := range tpl.GetEpList() {
		pathList = append(pathList, ep.GetPath())
	}
	if got := strings.Join(pathList, " "); got != "写周报.ep.txt 找bug.ep.txt" {
		t.Fatalf("入口文件列表不对: %s", got)
	}
	// 变量名单是从模版本身算出来的，含 include 进来的复用文件里那些。
	ep := mustGetEp(t, tpl, "找bug.ep.txt")
	if got := strings.Join(ep.GetVarNameList(), " "); got != "adminPassword consolePort fileNameBlock linuxIp" {
		t.Fatalf("找bug.ep.txt 需要的变量名不对: %s", got)
	}
}

func TestScanDir(t *testing.T) {
	root := t.TempDir()
	writeFile := func(rel string, content string) {
		t.Helper()
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatalf("建目录失败: %v", err)
		}
		if err := os.WriteFile(full, []byte(content), 0644); err != nil {
			t.Fatalf("写文件失败: %v", err)
		}
	}
	writeFile("a.ep.txt", "入口")
	writeFile("sub/b.part.txt", "子目录里的复用文件")
	writeFile("sub/d.raw.txt", "原样包含文件 {{.Field}}")
	writeFile("经验.txt", "这个该被忽略")
	writeFile("sub/note.md", "这个也该被忽略")
	writeFile("real/c.part.txt", "被 symlink 指到的目录里的文件")

	// symlink 指向目录和指向文件都要能用。
	if err := os.Symlink(filepath.Join(root, "real"), filepath.Join(root, "linkDir")); err != nil {
		t.Skipf("本机建不了 symlink，跳过: %v", err)
	}
	if err := os.Symlink(filepath.Join(root, "a.ep.txt"), filepath.Join(root, "linkFile.ep.txt")); err != nil {
		t.Fatalf("建文件 symlink 失败: %v", err)
	}

	fileMap, err := ScanDir(root)
	if err != nil {
		t.Fatalf("ScanDir 失败: %v", err)
	}
	gotPathList := getSortedKeyList(fileMap)
	want := "a.ep.txt linkDir/c.part.txt linkFile.ep.txt real/c.part.txt sub/b.part.txt sub/d.raw.txt"
	if strings.Join(gotPathList, " ") != want {
		t.Fatalf("扫出来的文件不对\n得到: %s\n期望: %s", strings.Join(gotPathList, " "), want)
	}
	if string(fileMap["sub/b.part.txt"]) != "子目录里的复用文件" {
		t.Fatalf("文件内容不对: %q", fileMap["sub/b.part.txt"])
	}
}
