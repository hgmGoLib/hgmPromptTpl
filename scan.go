package hgmPromptTpl

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ScanDir 把一个实体目录读成「相对路径 -> 文件内容」，只收 .ep.txt、.part.txt 和 .raw.txt，
// 别的文件（doc/经验.txt、doc/ai/*.md 之类）直接忽略。
//
// key 一律是 dir 向下的相对路径，分隔符固定 /，与当前系统无关：Windows 上跑出来的 key
// 和 linux 上一模一样，于是 include 里的路径在两边写法相同。
//
// 目录项只支持目录、普通文件、以及指向这两者的 symlink；别的类型（设备、socket 等）
// 直接报错而不是跳过 —— 静默跳过会让人以为文件没被收进来是「后缀不对」，白查一轮。
//
// 跟随目录 symlink 是有意的功能，不是疏忽：提示词包常常要把别处的一份公共段落挂进来。
// 代价是同一个物理文件可以有两个 key（real/x.part.txt 和 link/x.part.txt），于是
// 「一个 part 只有唯一一种写法」这条只在不用 symlink 时成立，第四节那个重复检查也就
// 分辨不出这两个 key 是同一个文件。挂 symlink 的人要自己保证别从两条路径 include 同一份。
func ScanDir(dir string) (map[string][]byte, error) {
	fileMap := map[string][]byte{}
	if err := scanDirInto(dir, "", fileMap); err != nil {
		return nil, err
	}
	return fileMap, nil
}

func scanDirInto(dir string, relPrefix string, fileMap map[string][]byte) error {
	entryList, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("扫描模版目录 %s 失败: %w", dir, err)
	}
	for _, entry := range entryList {
		name := entry.Name()
		fullPath := filepath.Join(dir, name)
		rel := name
		if relPrefix != "" {
			rel = relPrefix + "/" + name
		}
		// 走 Stat 而不是 entry.Type()：symlink 要解引用，指向目录和指向文件都要能用。
		//
		// Stat 故意放在后缀过滤之前，所以一个断链的 note.md（后缀根本不在收录范围里）也会让
		// 整个扫描失败。这是有意的：symlink 断了的时候看不出它本来指向什么，指向目录的话那
		// 底下可能整包提示词都在，按后缀跳过就等于悄悄少读一堆文件，最后表现成 include 找不到
		// 目标，得从那儿反查回来。目录里躺着断链本身就是环境坏了，当场报比往后拖强。
		info, err := os.Stat(fullPath)
		if err != nil {
			return fmt.Errorf("读模版目录项 %s 失败（symlink 断了也会走到这里，"+
				"断链看不出原本指向目录还是文件，指向目录就可能整包提示词都在底下，所以不按后缀跳过）: %w", fullPath, err)
		}
		switch {
		case info.IsDir():
			if err := scanDirInto(fullPath, rel, fileMap); err != nil {
				return err
			}
		case info.Mode().IsRegular():
			if !strings.HasSuffix(name, EpSuffix) && !strings.HasSuffix(name, PartSuffix) && !strings.HasSuffix(name, RawSuffix) {
				continue
			}
			content, err := os.ReadFile(fullPath)
			if err != nil {
				return fmt.Errorf("读模版文件 %s 失败: %w", fullPath, err)
			}
			fileMap[rel] = content
		default:
			return fmt.Errorf("模版目录项 %s 既不是目录也不是普通文件(mode=%s): 扫描只支持目录、普通文件、指向这两者的 symlink",
				fullPath, info.Mode())
		}
	}
	return nil
}
