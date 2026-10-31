package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// 识别页的输入文件管理：选文件、预览、移除、导出 Excel。

// pickInputs 弹系统文件对话框，把选中的图片 / PDF 加进列表。
func (u *ui) pickInputs() {
	go func() {
		paths, err := pickFiles("选择要识别的图片或 PDF（可多选）", inputFilters)
		u.post(func() {
			u.st.busy = ""
			if err != nil {
				// 这台机器上没有 zenity / kdialog：把「粘贴路径」框露出来（平时不占位）
				u.st.needPaste = errors.Is(err, errNoDialog)
				u.st.fail(err.Error())
			} else if len(paths) > 0 {
				u.addInputPaths(paths)
			}
			u.bump()
		})
	}()
}

// addInputFromText 把「手动添加路径」输入框里的内容加进列表（换行分隔）。
func (u *ui) addInputFromText() {
	raw := strings.TrimSpace(u.pickPath.Get())
	if raw == "" {
		u.post(func() { u.st.fail("先粘贴一个图片或 PDF 的完整路径") })
		return
	}
	u.addInputPaths(splitPaths(raw))
	u.pickPath.Set("")
}

// addInputPaths 把若干路径塞进输入列表：去重、校验、自动选中第一个新增的。
func (u *ui) addInputPaths(paths []string) {
	added := 0
	var firstNew string
	for _, path := range paths {
		file, err := newPickedFile(path)
		if err != nil {
			u.post(func() { u.st.fail(err.Error()) })
			continue
		}
		if u.hasInput(file.Path) {
			continue
		}
		u.st.files = append(u.st.files, file)
		added++
		if firstNew == "" {
			firstNew = file.Path
		}
	}
	if added == 0 {
		u.post(func() {
			if len(paths) > 0 {
				u.st.fail("这些文件已经在列表里了")
			}
			u.bump()
		})
		return
	}
	u.post(func() {
		u.st.say(fmt.Sprintf("已添加 %d 个文件", added))
		u.bump()
	})
	u.selectInput(firstNew)
}

// hasInput 判断路径是否已经在列表里。
func (u *ui) hasInput(path string) bool {
	for _, file := range u.st.files {
		if file.Path == path {
			return true
		}
	}
	return false
}

// selectInput 选中左侧列表里的某个文件，右侧预览跟着刷新。
func (u *ui) selectInput(path string) {
	file, ok := u.inputAt(path)
	u.selFile.Set(path)
	// 选文件就等于要看它：预览自动打开（关掉它靠预览右上角的 ✕）
	u.previewOpen.Set(true)
	// 换文件就回到「适应面板」，别带着上一张的缩放和位移
	u.resetZoom()
	if !ok {
		u.post(func() {
			u.st.previewData, u.st.previewNote = nil, ""
			u.setPreviewSize(nil)
			u.bump()
		})
		return
	}
	// 读图要落盘 I/O，别卡在 UI 线程上
	go func() {
		data, note := readPreview(file)
		u.post(func() {
			u.st.previewData, u.st.previewNote = data, note
			u.setPreviewSize(data)
			u.resetZoom()
			u.bump()
		})
	}()
}

// inputAt 按路径找输入文件。
func (u *ui) inputAt(path string) (pickedFile, bool) {
	for _, file := range u.st.files {
		if file.Path == path {
			return file, true
		}
	}
	return pickedFile{}, false
}

// readPreview 读图片字节给预览用；PDF 只给一句说明（要渲染得靠后端）。
func readPreview(file pickedFile) ([]byte, string) {
	if file.Kind != "image" {
		return nil, "PDF 预览需要先识别；点「开始识别」后在下方结果里看每一页的文本与分块。"
	}
	const limit = 40 << 20
	if file.Size > limit {
		return nil, fmt.Sprintf("图片有 %s，太大就不在预览里加载了（识别照样能做）", fmtBytes(uint64(file.Size)))
	}
	data, err := os.ReadFile(file.Path)
	if err != nil {
		return nil, "读不出这张图：" + err.Error()
	}
	return data, ""
}

// removeInput 从列表里移掉一个文件。
func (u *ui) removeInput(path string) {
	kept := make([]pickedFile, 0, len(u.st.files))
	for _, file := range u.st.files {
		if file.Path != path {
			kept = append(kept, file)
		}
	}
	u.post(func() {
		u.st.files = kept
		if u.selFile.Get() == path {
			next := ""
			if len(kept) > 0 {
				next = kept[0].Path
			}
			u.selFile.Set(next)
			data, note := []byte(nil), ""
			if next != "" {
				if file, ok := u.inputAt(next); ok {
					data, note = readPreview(file)
				}
			}
			u.st.previewData, u.st.previewNote = data, note
			u.setPreviewSize(data)
			u.resetZoom()
		}
		u.bump()
	})
}

// clearInputs 清空输入列表。
func (u *ui) clearInputs() {
	u.post(func() {
		u.st.files = nil
		u.st.previewData, u.st.previewNote = nil, ""
		u.setPreviewSize(nil)
		u.resetZoom()
		u.selFile.Set("")
		u.bump()
	})
}

// exportExcel 把识别页当前结果导出成 Excel。
func (u *ui) exportExcel() {
	_, id := u.currentResult()
	if id == "" {
		u.post(func() { u.st.fail("还没有识别结果可以导出") })
		return
	}
	u.exportExcelFor(id, u.excelFileName(id))
}

// exportExcelFor 把指定产物导出成 Excel：先拉字节，再弹保存对话框。
func (u *ui) exportExcelFor(id, defaultName string) {
	if id == "" {
		u.post(func() { u.st.fail("这条记录没有产物 id，导不了") })
		return
	}
	u.post(func() {
		u.st.busy = "导出 Excel"
		u.bump()
	})
	go func() {
		data, err := u.cl.exportXLSX(u.ctx, id)
		if err != nil {
			u.post(func() {
				u.st.busy = ""
				u.st.fail(err.Error())
				u.bump()
			})
			return
		}
		target, err := pickSaveFile("导出 Excel", defaultName, excelFilter)
		if err != nil {
			u.post(func() {
				u.st.busy = ""
				u.st.fail(err.Error())
				u.bump()
			})
			return
		}
		if target == "" {
			u.post(func() {
				u.st.busy = ""
				u.bump()
			})
			return
		}
		if !strings.EqualFold(filepath.Ext(target), ".xlsx") {
			target += ".xlsx"
		}
		writeErr := os.WriteFile(target, data, 0o644)
		u.post(func() {
			u.st.busy = ""
			if writeErr != nil {
				u.st.fail("写入失败：" + writeErr.Error())
			} else {
				u.st.say("已导出 " + target)
			}
			u.bump()
		})
	}()
}

// excelFileName 给保存对话框一个像样的默认文件名。
func (u *ui) excelFileName(id string) string {
	if len(u.st.files) > 0 {
		stem := strings.TrimSuffix(u.st.files[0].Name, filepath.Ext(u.st.files[0].Name))
		if len(u.st.files) > 1 {
			return fmt.Sprintf("%s-等%d个文件.xlsx", stem, len(u.st.files))
		}
		return stem + ".xlsx"
	}
	return "ocr-" + id + ".xlsx"
}
