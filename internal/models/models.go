// Package models 解析模型清单（`config/models.toml`），并把内置默认清单合并进来。
package models

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/bluefrog/ocr-manager/internal/paths"
)

// 权重来源：决定「下载」按钮要做什么。
const (
	// WeightsHub 表示权重是一个 HuggingFace 仓库，由 worker 的 snapshot_download 拉到本地。
	WeightsHub = "hub"
	// WeightsAuto 表示权重由引擎自己管（onnx 包内置 / 首次推理自动下载），界面只显示状态。
	WeightsAuto = "auto"
	// WeightsNone 表示这个引擎不需要权重（mock / 系统 tesseract）。
	WeightsNone = "none"
)

// ModelSpec 是清单里的一条模型定义。
type ModelSpec struct {
	ID     string `toml:"id" json:"id"`
	Name   string `toml:"name" json:"name"`
	Engine string `toml:"engine" json:"engine"`
	Repo   string `toml:"repo" json:"repo"`
	// Homepage 是这个模型的官方页面（项目主页 / 模型仓库页），UI 上「官网」直接打开。
	// 清单里不写也行：Repo 不为空就按 HuggingFace 仓库页拼出来（见 HomepageURL）。
	Homepage    string   `toml:"homepage" json:"homepage"`
	Description string   `toml:"description" json:"description"`
	Languages   []string `toml:"languages" json:"languages"`
	// Tasks 是这个模型能做的事：text / layout / table / markdown / formula / pdf。
	Tasks []string `toml:"tasks" json:"tasks"`
	// Weights 是权重来源：hub | auto | none（不写就按 repo 猜）。
	Weights     string   `toml:"weights" json:"weights"`
	Extras      []string `toml:"extras" json:"extras"`
	RequiresGPU bool     `toml:"requires_gpu" json:"requires_gpu"`
	// MinVRAMGB 是跑这个模型需要的最小显存（GB），0 表示清单没写要求。
	// 只有在 RequiresGPU 且本机确实有卡时才比，配不上就把具体差值说清楚。
	MinVRAMGB int `toml:"min_vram_gb" json:"min_vram_gb"`
	// MinRAMGB 是最小系统内存（GB），0 表示没写要求。
	MinRAMGB int `toml:"min_ram_gb" json:"min_ram_gb"`
	// Platforms 限定这条模型只在某些系统上出现（`["windows"]`）。空表示不限。
	Platforms []string `toml:"platforms" json:"platforms"`
	// Hidden 表示这条只在清单里存在、不进模型列表（mock 这种自测用的占位引擎）。
	Hidden   bool           `toml:"hidden" json:"hidden"`
	SizeHint string         `toml:"size_hint" json:"size_hint"`
	Tags     []string       `toml:"tags" json:"tags"`
	Featured bool           `toml:"featured" json:"featured"`
	Probe    bool           `toml:"probe" json:"probe"`
	Options  map[string]any `toml:"options" json:"options"`
}

// OptionsJSON 返回透传给 worker 的引擎参数（永不返回 nil）。
func (m ModelSpec) OptionsJSON() map[string]any {
	if m.Options == nil {
		return map[string]any{}
	}
	return m.Options
}

// WeightsMode 决定权重怎么来（清单没写时按 repo 推断）。
func (m ModelSpec) WeightsMode() string {
	switch mode := strings.ToLower(strings.TrimSpace(m.Weights)); mode {
	case WeightsHub, WeightsAuto, WeightsNone:
		return mode
	case "":
		if strings.TrimSpace(m.Repo) != "" {
			return WeightsHub
		}
		return WeightsNone
	default:
		return WeightsNone
	}
}

// NeedsDownload 表示必须在「下载」之后才能激活。
func (m ModelSpec) NeedsDownload() bool { return m.WeightsMode() == WeightsHub }

// HomepageURL 是这个模型的官方页面：清单里写了 homepage 就用它，
// 没写就按 HuggingFace 仓库拼（大多数模型的「官网」就是它在 Hub 上的页面）。
func (m ModelSpec) HomepageURL() string {
	if home := strings.TrimSpace(m.Homepage); home != "" {
		return home
	}
	if repo := strings.TrimSpace(m.Repo); repo != "" {
		return "https://huggingface.co/" + repo
	}
	return ""
}

// RepoCacheDir 是 HuggingFace 缓存目录名，例如 `models--PaddlePaddle--PP-OCRv5_server`。
func (m ModelSpec) RepoCacheDir() string {
	owner, name, ok := strings.Cut(m.Repo, "/")
	if !ok || owner == "" || name == "" {
		return ""
	}
	return fmt.Sprintf("models--%s--%s", owner, name)
}

// ExternalCommand 是外部工具的入口命令（`options.command` 的第一个词），
// 例如 surya 的 `surya_ocr`。用来判断本机到底装没装这个 CLI。
func (m ModelSpec) ExternalCommand() string {
	raw, _ := m.Options["command"].(string)
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

// Supports 判断这个模型是否声明了某项能力。
func (m ModelSpec) Supports(task string) bool {
	for _, item := range m.Tasks {
		if item == task {
			return true
		}
	}
	return false
}

type catalogFile struct {
	Models []ModelSpec `toml:"models"`
}

// Catalog 是合并后的模型清单。
type Catalog struct {
	models  []ModelSpec
	sources []string
}

// Load 按「内置默认 → 仓库内 config/models.toml → $OCR_MODELS_FILE → $OCR_HOME/config/models.toml」
// 的顺序合并，后加载的同 id 条目覆盖先前的（顺序按首次出现）。
func Load(p *paths.Paths, builtin string) (*Catalog, error) {
	byID := map[string]ModelSpec{}
	var order []string
	var sources []string

	ingest := func(raw, origin string) error {
		var parsed catalogFile
		if _, err := toml.Decode(raw, &parsed); err != nil {
			return fmt.Errorf("解析模型清单失败: %s: %w", origin, err)
		}
		sources = append(sources, origin)
		for _, spec := range parsed.Models {
			if strings.TrimSpace(spec.ID) == "" {
				continue
			}
			if _, seen := byID[spec.ID]; !seen {
				order = append(order, spec.ID)
			}
			byID[spec.ID] = spec
		}
		return nil
	}

	if err := ingest(builtin, "builtin"); err != nil {
		return nil, err
	}

	candidates := []string{p.DevConfigFile(), strings.TrimSpace(os.Getenv("OCR_MODELS_FILE")), p.ConfigFile()}
	for _, file := range candidates {
		if file == "" {
			continue
		}
		raw, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		if err := ingest(string(raw), file); err != nil {
			return nil, err
		}
	}

	out := make([]ModelSpec, 0, len(order))
	for _, id := range order {
		spec, ok := byID[id]
		if !ok {
			continue
		}
		if spec.Languages == nil {
			spec.Languages = []string{}
		}
		if spec.Tasks == nil {
			spec.Tasks = []string{}
		}
		if spec.Extras == nil {
			spec.Extras = []string{}
		}
		if spec.Platforms == nil {
			spec.Platforms = []string{}
		}
		if spec.Tags == nil {
			spec.Tags = []string{}
		}
		// 官网地址在加载阶段就补齐：这样不管清单里写不写、
		// /api/models 出去的每条记录都能直接拿去打开。
		spec.Homepage = spec.HomepageURL()
		if !PlatformAllowed(spec) {
			continue
		}
		out = append(out, spec)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("模型清单为空")
	}
	return &Catalog{models: out, sources: sources}, nil
}

// Platform 是当前系统名，测试里可以临时改（默认 runtime.GOOS）。
var Platform = runtime.GOOS

// PlatformAllowed 判断一条模型在当前系统上要不要出现在列表里。
func PlatformAllowed(spec ModelSpec) bool {
	if len(spec.Platforms) == 0 {
		return true
	}
	for _, name := range spec.Platforms {
		if strings.EqualFold(strings.TrimSpace(name), Platform) {
			return true
		}
	}
	return false
}

// All 返回全部模型（顺序稳定）。
func (c *Catalog) All() []ModelSpec { return c.models }

// Visible 返回要在 UI 列表里出现的模型（过滤掉 Hidden）。
func (c *Catalog) Visible() []ModelSpec {
	out := make([]ModelSpec, 0, len(c.models))
	for _, spec := range c.models {
		if spec.Hidden {
			continue
		}
		out = append(out, spec)
	}
	return out
}

// Get 按 id 查模型（Hidden 的也能查到，方便 CLI 调试）。
func (c *Catalog) Get(id string) (ModelSpec, bool) {
	for _, spec := range c.models {
		if spec.ID == id {
			return spec, true
		}
	}
	return ModelSpec{}, false
}

// Sources 返回清单来源（按加载顺序）。
func (c *Catalog) Sources() []string { return c.sources }

// SortedIDs 是给测试与 CLI 用的稳定 id 列表。
func (c *Catalog) SortedIDs() []string {
	ids := make([]string, 0, len(c.models))
	for _, spec := range c.models {
		ids = append(ids, spec.ID)
	}
	sort.Strings(ids)
	return ids
}

// ---------------------------------------------------------------- 用户目录副本

// seedStamp 是写用户副本时打的那行摘要头：`# 摘要: <body 的 sha256 前 16 位>`。
//
// 有它才能分清「这份副本还是我们当初写的」和「用户自己改过」——
// 前者可以跟着内置清单升级，后者必须原样留着（手改的是用户的资产）。
const seedStamp = "# 摘要: "

func userCopyDigest(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:8])
}

// lookupUserCopy 决定用户副本要不要更新，返回新的文件内容（空表示不动）。
func lookupUserCopy(existing, builtin string) (string, bool) {
	if existing == "" {
		return seedStamp + userCopyDigest(builtin) + "\n" + builtin, true
	}
	firstLine, rest, _ := strings.Cut(existing, "\n")
	stamp, ok := strings.CutPrefix(firstLine, seedStamp)
	if !ok || strings.TrimSpace(stamp) != userCopyDigest(rest) {
		return "", false
	}
	if rest == builtin {
		return "", false
	}
	return seedStamp + userCopyDigest(builtin) + "\n" + builtin, true
}

// SyncUserCopy 让用户目录里的清单副本跟上内置清单（首次运行会创建）。
//
// 只覆盖「没被改过」的副本：摘要对得上 = 还是我们当初写的，可以放心换新；
// 用户手改过的文件保持原样，宁可让他的改动继续生效。
func SyncUserCopy(path, builtin string) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	next, write := lookupUserCopy(string(raw), builtin)
	if !write {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(next), 0o644)
}
