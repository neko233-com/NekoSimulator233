package avd

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// OfficialAppURL only accepts explicit catalog IDs; callers cannot inject an
// arbitrary Android shell argument or third-party installation source.
func OfficialAppURL(app string) (string, error) {
	switch app {
	case "wechat":
		return "https://weixin.qq.com/", nil
	case "douyin":
		return "https://www.douyin.com/downloadpage/app", nil
	default:
		return "", errors.New("不支持的可选应用")
	}
}

// OpenOfficialAppDownload opens a page in the selected Android instance. It
// does not download, install, or assert compatibility of the application.
func (m Manager) OpenOfficialAppDownload(ctx context.Context, instance Instance, app string) error {
	url, err := OfficialAppURL(app)
	if err != nil {
		return err
	}
	out, err := m.adb(ctx, instance.Serial, "shell", "am", "start", "-W", "-a", "android.intent.action.VIEW", "-d", url)
	if err != nil {
		return fmt.Errorf("打开官方页面失败：%w", err)
	}
	if !strings.Contains(string(out), "Status: ok") || strings.Contains(string(out), "Error:") {
		return errors.New("Android 未能打开官方页面；请检查实例网络和浏览器")
	}
	return nil
}
