# 准备本机环境

Windows x64 使用 go.mod 的 Go、web/package.json 的 Node/npm 和 PowerShell。版本只从声明取值，不在文档定义第二份。

Go 缺失时，从官方 go.dev 下载固定版本 Windows amd64 免安装包到工作区 Scratches/Toolchains，核对官方 SHA-256；通过 -GoExecutable 显式传入，不修改系统 PATH/环境变量或注册表。

可在被忽略的 tools/build/local.json 中填写 goExecutable 与 outputDirectory；共享仓库不包含本机路径。构建传参优先于本地文件，未提供 Go 路径时使用命令查找。测试直接调用选定的可执行文件。

隔离 AppDirs、合成 Source、输出根和真实 Source 的当前授权应分别记录在本地；真实 Source 不作为默认测试配置。操作范围遵守项目 X-02、X-09。
