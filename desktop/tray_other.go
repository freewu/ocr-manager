//go:build !windows

package main

// 非 Windows 平台暂时没有系统托盘：NSGo 没有托盘 API，Linux / macOS 各自要
// libappindicator / NSStatusBar 才做得到，先留空实现，界面行为保持一致。

// startTray 是空操作（Windows 之外没有托盘）。
func startTray() {}

// stopTray 是空操作（没有托盘可摘）。
func stopTray() {}
