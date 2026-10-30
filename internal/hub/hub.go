// Package hub 探测 HuggingFace 仓库元数据（文件清单、大小、license、gated）。
//
// 官方站经常连不通，所以支持 `HF_ENDPOINT` 换镜像；探测失败不算错误 ——
// 界面退化成「只看清单里的 size_hint」，不影响下载以外的任何操作。
package hub

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// RepoFile 是仓库里的一个文件。
type RepoFile struct {
	Name  string `json:"name"`
	Bytes uint64 `json:"bytes"`
}

// RepoInfo 是一次探测结果。
type RepoInfo struct {
	Repo         string     `json:"repo"`
	Exists       bool       `json:"exists"`
	Reachable    bool       `json:"reachable"`
	Endpoint     string     `json:"endpoint"`
	PipelineTag  *string    `json:"pipeline_tag"`
	LibraryName  *string    `json:"library_name"`
	Tags         []string   `json:"tags"`
	Files        []RepoFile `json:"files"`
	TotalBytes   uint64     `json:"total_bytes"`
	Downloads    uint64     `json:"downloads"`
	Likes        uint64     `json:"likes"`
	LastModified *string    `json:"last_modified"`
	Gated        bool       `json:"gated"`
	Private      bool       `json:"private"`
	Error        *string    `json:"error"`
}

// Client 带内存缓存的探测客户端。
type Client struct {
	http *http.Client
	ttl  time.Duration

	mu    sync.Mutex
	cache map[string]entry
}

type entry struct {
	info  RepoInfo
	at    time.Time
	fresh bool // 上次是成功探测（失败结果缓存时间短一些）
}

// New 建一个客户端（5 秒超时，缓存 10 分钟）。
func New() *Client {
	return &Client{
		http:  &http.Client{Timeout: 8 * time.Second},
		ttl:   10 * time.Minute,
		cache: map[string]entry{},
	}
}

// Endpoint 是当前使用的 Hub 地址（默认官方站，可用 HF_ENDPOINT 覆盖）。
func Endpoint() string {
	if value := strings.TrimSpace(os.Getenv("HF_ENDPOINT")); value != "" {
		return strings.TrimSuffix(value, "/")
	}
	return "https://huggingface.co"
}

// Info 返回仓库元数据；refresh=true 跳过缓存。
func (c *Client) Info(repo string, refresh bool) *RepoInfo {
	repo = strings.TrimSpace(strings.Trim(repo, "/"))
	if repo == "" {
		return nil
	}

	c.mu.Lock()
	if cached, ok := c.cache[repo]; ok && !refresh {
		limit := c.ttl
		if !cached.fresh {
			limit = 30 * time.Second
		}
		if time.Since(cached.at) < limit {
			info := cached.info
			c.mu.Unlock()
			return &info
		}
	}
	c.mu.Unlock()

	var info RepoInfo
	if truthy(os.Getenv("OCR_NO_PREWARM")) || truthy(os.Getenv("OCR_HF_OFFLINE")) {
		info = RepoInfo{Repo: repo, Endpoint: Endpoint(), Error: strPtr("离线模式：跳过 Hub 探测")}
	} else {
		info = c.fetch(repo)
	}

	c.mu.Lock()
	c.cache[repo] = entry{info: info, at: time.Now(), fresh: info.Exists}
	c.mu.Unlock()
	return &info
}

func (c *Client) fetch(repo string) RepoInfo {
	endpoint := Endpoint()
	info := RepoInfo{Repo: repo, Endpoint: endpoint}

	target := fmt.Sprintf("%s/api/models/%s?blobs=true", endpoint, url.PathEscape(repo))
	request, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		info.Error = strPtr(err.Error())
		return info
	}
	request.Header.Set("Accept", "application/json")
	if token := strings.TrimSpace(os.Getenv("HF_TOKEN")); token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}

	response, err := c.http.Do(request)
	if err != nil {
		info.Error = strPtr("连不上 " + endpoint + "：" + err.Error())
		return info
	}
	defer response.Body.Close()
	info.Reachable = true

	switch response.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		info.Error = strPtr(fmt.Sprintf("仓库需要授权（HTTP %d）：先在网页上同意条款，再设置 HF_TOKEN", response.StatusCode))
		return info
	case http.StatusNotFound:
		info.Exists = false
		info.Error = strPtr("仓库不存在：" + repo)
		return info
	default:
		info.Error = strPtr(fmt.Sprintf("Hub 返回 HTTP %d", response.StatusCode))
		return info
	}

	var payload struct {
		PipelineTag  string   `json:"pipeline_tag"`
		LibraryName  string   `json:"library_name"`
		Tags         []string `json:"tags"`
		Downloads    uint64   `json:"downloads"`
		Likes        uint64   `json:"likes"`
		LastModified string   `json:"lastModified"`
		Gated        any      `json:"gated"`
		Private      bool     `json:"private"`
		Siblings     []struct {
			Name string `json:"rfilename"`
			Size uint64 `json:"size"`
			LFS  *struct {
				Size uint64 `json:"size"`
			} `json:"lfs"`
		} `json:"siblings"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		info.Error = strPtr("解析 Hub 响应失败：" + err.Error())
		return info
	}

	info.Exists = true
	info.PipelineTag = optional(payload.PipelineTag)
	info.LibraryName = optional(payload.LibraryName)
	info.Tags = payload.Tags
	if info.Tags == nil {
		info.Tags = []string{}
	}
	info.Downloads = payload.Downloads
	info.Likes = payload.Likes
	info.LastModified = optional(payload.LastModified)
	info.Private = payload.Private
	if flag, ok := payload.Gated.(bool); ok {
		info.Gated = flag
	} else if payload.Gated != nil {
		info.Gated = true
	}

	for _, sibling := range payload.Siblings {
		size := sibling.Size
		if sibling.LFS != nil && sibling.LFS.Size > size {
			size = sibling.LFS.Size
		}
		info.Files = append(info.Files, RepoFile{Name: sibling.Name, Bytes: size})
		info.TotalBytes += size
	}
	if info.Files == nil {
		info.Files = []RepoFile{}
	}
	return info
}

// License 从 tags 里挑出 license 标签（`license:apache-2.0`）。
func (i RepoInfo) License() string {
	for _, tag := range i.Tags {
		if value, ok := strings.CutPrefix(tag, "license:"); ok {
			return value
		}
	}
	return ""
}

func optional(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return &value
}

func strPtr(value string) *string { return &value }

func truthy(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
