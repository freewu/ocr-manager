# desktop/embedded

这个目录是打包用的「内置后端」插槽，平时只有本文件。

`scripts/desktop.py build`（也就是 `just build` / `just desktop-build`）会把
`bin/ocr-server` 复制进来，并写一个 `target.txt` 记下它是什么平台的：

```
embedded/ocr-server.exe    # 后端（Windows 带 .exe，别的平台不带）
embedded/target.txt        # 例如 windows/amd64
```

`desktop/embedded.go` 用 `//go:embed embedded` 把这两个文件编进桌面壳，于是
发布物只有一个 `ocr-manager.exe`：旁边没有 `ocr-server` 时，就把内置的那份
解包到 `$OCR_HOME/runtime/ocr-server-<哈希>.exe` 再跑。

开发时不用管它：`bin/ocr-server` 优先于内置那份，`just desktop` / `just dev`
改动直接生效。往仓库里提交 `ocr-server` 是没意义的（`.gitignore` 已经挡住）。
