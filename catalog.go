package ocr

import _ "embed"

// DefaultCatalog 是随源码分发的默认模型清单。
//
// 编进二进制的好处：用户配置文件丢了/删了也总有一个可用的起点。
//
//go:embed config/models.toml
var DefaultCatalog string
