package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/bluefrog/ocr-manager/internal/paths"
)

// Settings 是用户可以调的全局偏好（`$OCR_HOME/config/settings.json`）。
type Settings struct {
	// DefaultModel 是「识别」页默认选中的模型。
	DefaultModel string `json:"default_model"`
	// ActiveModel 是最后一次成功激活的模型（启动时按 AutoLoad 决定要不要预加载）。
	ActiveModel string `json:"active_model"`
	// HFEndpoint 是 HuggingFace 镜像地址（国内建议 https://hf-mirror.com）。
	HFEndpoint string `json:"hf_endpoint"`
	// AutoLoad 表示启动后自动加载 ActiveModel。
	AutoLoad bool `json:"auto_load"`
	// MaxPages 是多页 PDF 最多识别多少页（0 = 不限）。
	MaxPages int `json:"max_pages"`
	// PDFDPI 是 PDF 转图的分辨率。
	PDFDPI int `json:"pdf_dpi"`
	// MaxSide 是输入图片长边上限（0 = 不缩放）。
	MaxSide int `json:"max_side"`
	// Options 是按模型的参数覆盖（模型清单里的 `[models.options]` 是默认值）。
	Options map[string]map[string]any `json:"options"`
}

func defaultSettings() Settings {
	return Settings{
		AutoLoad: true,
		MaxPages: 20,
		PDFDPI:   200,
		MaxSide:  0,
		Options:  map[string]map[string]any{},
	}
}

func loadSettings(p *paths.Paths) Settings {
	settings := defaultSettings()
	raw, err := os.ReadFile(settingsFile(p))
	if err != nil {
		return settings
	}
	if err := json.Unmarshal(raw, &settings); err != nil {
		return defaultSettings()
	}
	if settings.Options == nil {
		settings.Options = map[string]map[string]any{}
	}
	if settings.PDFDPI <= 0 {
		settings.PDFDPI = 200
	}
	if settings.MaxPages < 0 {
		settings.MaxPages = 0
	}
	applyHubEndpoint(settings.HFEndpoint)
	return settings
}

func settingsFile(p *paths.Paths) string {
	return filepath.Join(p.Root, "config", "settings.json")
}

// Settings 返回当前偏好的副本（调用方可以随便改）。
func (c *Core) Settings() Settings {
	c.mu.Lock()
	defer c.mu.Unlock()
	return cloneSettings(c.settings)
}

// UpdateSettings 合并写入并落盘；只覆盖传了值的字段（零值也照写）。
func (c *Core) UpdateSettings(patch Settings) (Settings, error) {
	c.mu.Lock()
	next := c.settings
	if patch.DefaultModel != "" {
		next.DefaultModel = patch.DefaultModel
	}
	if patch.ActiveModel != "" {
		next.ActiveModel = patch.ActiveModel
	}
	if patch.HFEndpoint != "" || patch.HFEndpoint == "" {
		next.HFEndpoint = strings.TrimSpace(patch.HFEndpoint)
	}
	next.AutoLoad = patch.AutoLoad
	if patch.MaxPages >= 0 {
		next.MaxPages = patch.MaxPages
	}
	if patch.PDFDPI > 0 {
		next.PDFDPI = patch.PDFDPI
	}
	if patch.MaxSide >= 0 {
		next.MaxSide = patch.MaxSide
	}
	if patch.Options != nil {
		next.Options = patch.Options
	}
	c.settings = next
	c.mu.Unlock()

	if err := saveSettings(c.Paths, next); err != nil {
		return next, err
	}
	applyHubEndpoint(next.HFEndpoint)
	return cloneSettings(next), nil
}

// SetActiveModel 记住最后激活的模型。
func (c *Core) SetActiveModel(modelID string) {
	c.mu.Lock()
	next := c.settings
	next.ActiveModel = modelID
	c.settings = next
	c.mu.Unlock()
	_ = saveSettings(c.Paths, next)
}

// modelOptions 是「清单默认值 ← 设置里的覆盖 ← 本次请求」的合并结果。
func (c *Core) modelOptions(modelID string, override map[string]any) map[string]any {
	c.mu.Lock()
	custom := c.settings.Options[modelID]
	settings := c.settings
	c.mu.Unlock()

	out := map[string]any{}
	for key, value := range custom {
		out[key] = value
	}
	for key, value := range override {
		out[key] = value
	}
	// 多页 / 分辨率由服务端统一控制，模型自己的 options 里也允许覆盖
	if _, ok := out["max_pages"]; !ok && settings.MaxPages > 0 {
		out["max_pages"] = settings.MaxPages
	}
	if _, ok := out["pdf_dpi"]; !ok && settings.PDFDPI > 0 {
		out["pdf_dpi"] = settings.PDFDPI
	}
	if _, ok := out["max_side"]; !ok && settings.MaxSide > 0 {
		out["max_side"] = settings.MaxSide
	}
	return out
}

func saveSettings(p *paths.Paths, settings Settings) error {
	file := settingsFile(p)
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	tmp := file + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, file)
}

func cloneSettings(settings Settings) Settings {
	out := settings
	out.Options = map[string]map[string]any{}
	for model, options := range settings.Options {
		copied := map[string]any{}
		for key, value := range options {
			copied[key] = value
		}
		out.Options[model] = copied
	}
	return out
}

// applyHubEndpoint 把镜像地址写进环境变量：worker 是子进程，继承环境最省事。
func applyHubEndpoint(endpoint string) {
	if strings.TrimSpace(endpoint) == "" {
		_ = os.Unsetenv("HF_ENDPOINT")
		return
	}
	_ = os.Setenv("HF_ENDPOINT", strings.TrimSpace(endpoint))
}
