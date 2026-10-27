# Muse2API Cookie 导出插件

一个 Chrome / Edge Manifest V3 扩展，用来读取 `muse.ai` 的登录 Cookie，并生成 Muse2API 账号池可以直接使用的格式。

## 功能

- 读取 `muse.ai` 及子域下的登录 Cookie
- 检查三个核心凭据是否齐全：
  - `hatch_sess`
  - `hatch_vml`
  - `hatch_native_auth_device`
- 支持三种导出格式：
  - **Cookie Header**：`name=value; name2=value2`，直接粘贴到“账号池 → 添加账号”
  - **JSON**：`{"name":"value"}`，同样可以粘贴到同一个输入框
  - **批量行**：`标签|Cookie Header`，用于 `/admin/accounts` 的 `batch` 字段
- 所有数据只在本机读取和复制，不会发往任何服务器

## 安装

1. 打开 Chrome 的 `chrome://extensions` 或 Edge 的 `edge://extensions`
2. 打开右上角的“开发者模式”
3. 点击“加载已解压的扩展程序”
4. 选择本仓库的 `browser-extension/` 目录
5. 把扩展固定到工具栏

## 使用

1. 在浏览器里登录 https://muse.ai
2. 点击扩展图标
3. 填写账号标签（可选，批量行会用到）
4. 选择导出格式，点击“复制”
5. 打开 Muse2API 后台 → 账号池 → 添加账号，粘贴到“Cookie Header 或 JSON”输入框
6. 保存后点“检测”，确认会话有效

如果显示“缺少核心项”，通常是没有真正登录、登录已过期，或者浏览器拦截了第三方 Cookie。重新登录 Muse 后再点“重新读取”。

## 安全说明

- 扩展只申请 `muse.ai` 的 Cookie 读取权限，不包含任何上传或远程请求逻辑
- Cookie 等同于账号登录凭据，请不要截图、分享或提交到公开仓库
- 如果账号出现异常，先在 Muse 退出登录，再更新账号池里的凭据
