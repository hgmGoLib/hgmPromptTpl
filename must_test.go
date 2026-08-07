package hgmPromptTpl

import (
	"testing"
)

// 这三个 Must 版只该干一件事：成功时跟原版返回一模一样的东西，失败时把原版那个 error
// 原样 panic 出来。下面每个用例都拿原版的结果对一遍，钉的就是「Must 版不吃信息、不改行为」。

// mustPanicValue 跑 fn，要求它 panic，返回 panic 出来的值。
func mustPanicValue(t *testing.T, name string, fn func()) any {
	t.Helper()
	var got any
	func() {
		defer func() { got = recover() }()
		fn()
	}()
	if got == nil {
		t.Fatalf("%s 该 panic 却没 panic", name)
	}
	return got
}

// mustPanicError 在上面基础上再要求 panic 出来的是个 error 而不是字符串 ——
// 调用方 recover 之后要能直接拿去判断、包装、打印。
func mustPanicError(t *testing.T, name string, fn func()) error {
	t.Helper()
	got := mustPanicValue(t, name, fn)
	err, ok := got.(error)
	if !ok {
		t.Fatalf("%s panic 出来的不是 error，是 %T: %v", name, got, got)
	}
	return err
}

func TestMustNewFromDir(t *testing.T) {
	tpl := MustNewFromDir("example/prompt")
	if got := len(tpl.GetEpList()); got != 2 {
		t.Fatalf("入口文件数不对: %d", got)
	}

	const badDir = "example/没有这个目录"
	got := mustPanicError(t, "MustNewFromDir", func() { MustNewFromDir(badDir) })
	_, want := NewFromDir(badDir)
	if want == nil {
		t.Fatalf("NewFromDir 居然成功了，这个用例的前提没了")
	}
	if got.Error() != want.Error() {
		t.Fatalf("panic 出来的报错跟 NewFromDir 的不一样:\npanic: %v\nNewFromDir: %v", got, want)
	}
}

func TestMustGetEp(t *testing.T) {
	tpl := MustNewFromDir("example/prompt")
	if got := tpl.MustGetEp("找bug.ep.txt").GetPath(); got != "找bug.ep.txt" {
		t.Fatalf("取回来的入口文件不对: %s", got)
	}

	// 复用文件不能被渲染，所以拿它当 epPath 必然失败。
	const partPath = "通用规矩.part.txt"
	got := mustPanicError(t, "MustGetEp", func() { tpl.MustGetEp(partPath) })
	_, want := tpl.GetEp(partPath)
	if want == nil {
		t.Fatalf("GetEp 居然成功了，这个用例的前提没了")
	}
	if got.Error() != want.Error() {
		t.Fatalf("panic 出来的报错跟 GetEp 的不一样:\npanic: %v\nGetEp: %v", got, want)
	}
}

func TestMustRender(t *testing.T) {
	ep := MustNewFromDir("example/prompt").MustGetEp("写周报.ep.txt")
	varMap := map[string]string{
		"fileNameBlock": "* pkg/httpApi/user.go",
		"linuxIp":       "10.10.10.10",
	}
	want, err := ep.Render(varMap)
	if err != nil {
		t.Fatalf("Render 失败: %v", err)
	}
	if got := ep.MustRender(varMap); got != want {
		t.Fatalf("MustRender 的结果跟 Render 不一样:\nMustRender:\n%s\nRender:\n%s", got, want)
	}

	// 少一个 key：Render 唯一的失败方式。
	badVarMap := map[string]string{"linuxIp": "10.10.10.10"}
	gotErr := mustPanicError(t, "MustRender", func() { ep.MustRender(badVarMap) })
	_, wantErr := ep.Render(badVarMap)
	if wantErr == nil {
		t.Fatalf("Render 居然成功了，这个用例的前提没了")
	}
	if gotErr.Error() != wantErr.Error() {
		t.Fatalf("panic 出来的报错跟 Render 的不一样:\npanic: %v\nRender: %v", gotErr, wantErr)
	}
}
