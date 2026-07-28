//go:build gallery_e2e_testhooks

package hashjob

import (
	"os"

	"github.com/RecRivenVI/gallery/internal/media"
)

const e2eBlockHashRelativePathEnv = "GALLERY_E2E_BLOCK_HASH_RELATIVE_PATH"

// hashSourceFileWithOptions 只存在于 web-e2e 单独编译的 galleryd。指定的合成文件在
// 实际读取首批字节后等待执行 context 取消，使浏览器能确定性观察并取消同一个活动
// Hash Job；正式构建不包含环境变量分支或阻塞行为。
func hashSourceFileWithOptions(root, relative string, options media.HashOptions) (media.HashResult, error) {
	target := os.Getenv(e2eBlockHashRelativePathEnv)
	if target == "" || relative != target || options.Context == nil {
		return media.HashSourceFileWithOptions(root, relative, options)
	}
	progress := options.Progress
	options.Progress = func(bytes int64) {
		if progress != nil {
			progress(bytes)
		}
		if bytes > 0 {
			<-options.Context.Done()
		}
	}
	return media.HashSourceFileWithOptions(root, relative, options)
}
