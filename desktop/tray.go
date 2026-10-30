package main

// trayHome 是托盘菜单「打开数据目录」要打开的目录。
// 由 UI 在连上后端后填（数据根在 info.Dirs["root"]），没连上就返回空，
// 菜单里干脆不显示这一项。
var trayHome = func() string { return "" }

// trayHomeDir 读当前的数据目录。
func trayHomeDir() string { return trayHome() }
