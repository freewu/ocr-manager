package main

import _ "embed"

// iconPNG 是应用图标（`python ../scripts/make_icon.py` 从 logo.png 生成）。
// 打包时 NSGo 用它给 exe / .app 做图标，运行时再拿来画侧栏的品牌图。
//
//go:embed assets/Icon.png
var iconPNG []byte
