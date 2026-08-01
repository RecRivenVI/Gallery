// Package version 暴露客户端与服务端共享的产品和协议版本标识。
package version

const (
	ProductName       = "Gallery"
	ProductNameZH     = "画廊"
	ServiceName       = "galleryd"
	DefaultVersion    = "0.2.0-walking-skeleton"
	APIVersion        = "v1"
	DescriptorVersion = 1
)

// Version 是制品的产品版本。开发构建使用 DefaultVersion；发行脚本必须通过 Go linker
// 的 -X 注入本次制品版本，使二进制、发行清单和备份 manifest 使用同一个事实源。
// 它不是 OpenAPI 契约版本，也不是 Web 静态壳版本，三者不得机械绑定。
var Version = DefaultVersion
