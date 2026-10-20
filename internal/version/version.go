// Package version 暴露客户端与服务端共享的产品和协议版本标识。
package version

const (
	ProductName       = "Gallery"
	ProductNameZH     = "画廊"
	ServiceName       = "galleryd"
	APIVersion        = "v1"
	DescriptorVersion = 1
)

// Version 由构建从 Git 注入；未注入的源码构建只标为 dev，不冒充发行号。
var Version = "dev"
