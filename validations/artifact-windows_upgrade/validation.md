# artifact-windows_upgrade

## 目标

比较精确 Windows 候选的便携使用、历史升级、降级拒绝及备份恢复。

## 运行

按 tools/windows-portable 的显式参数和测试帮助选择隔离输出，先构建 Web 再 Go；历史 Git 提交来自 historical-baselines.json，未签名包标测试品。不安装，不替换/停止现有实例；结果放仓库外。

## 通过标准

用户事实与 schema 前向迁移一致，降级拒绝且现有库保留，失败窗口可恢复；版本/Git/签名分别说明，不能用构建成功宣称正式发行。
