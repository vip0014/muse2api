<p align="center">
  <img src="web/public/logo.png" alt="Muse2API" width="200">
</p>

<p align="center">
  <img alt="Go" src="https://img.shields.io/badge/Go-1.24-00ADD8?style=flat-square">
  <img alt="Docker" src="https://img.shields.io/badge/Docker-Compose-2496ED?style=flat-square">
  <img alt="License" src="https://img.shields.io/badge/License-MIT-green?style=flat-square">
</p>

<h1 align="center">Muse2API</h1>

<p align="center">
  把 Muse 的部分网页能力转成 OpenAI 风格的 API。<br>
  自托管，多账号，带管理后台。
</p>

> 需要自己已经登录的 Muse 账号，通过 Cookie 导入。项目不提供账号，也不会绕过登录、验证码或上游限流。上游风控、账号封禁等风险由使用者自行承担。

## 项目简介

Muse2API 是一个自托管的小网关。它用 Chromium 打开 Muse 页面，把对话、图片、视频和额度读取包装成 OpenAI 风格的接口，方便接到现有客户端或脚本里。

后端使用 Go，管理页面使用 React，最终可以打成一个二进制或 Docker 镜像。项目基于原版 `muse2api` 重写，保留默认端口 `18610`、Bearer Key、`/v1` 路径和 `./data` 数据目录。

## 功能

| 功能 | 说明 |
|---|---|
| 对话 | `/v1/chat/completions`、`/v1/responses`，支持普通响应和 SSE；Responses 目前只覆盖文本 |
| 图片 | 文生图、参考图编辑，同步调用或异步任务 |
| 视频 | 创建任务和查询结果 |
| 账号池 | 多账号轮转、单账号并发上限、有界任务队列 |
| 幂等 | 图片和视频任务支持 `Idempotency-Key`，重试不会重复提交上游 |
| 管理后台 | 账号、任务、媒体、API 调试台、模型能力、设置、日志 |
| 凭据 | 管理员用户名/密码登录，和 API Key 分开；账号 Cookie 使用 AES-256-GCM 加密 |
| 媒体 | 文件落盘，默认需要 Bearer 或管理员会话才能访问 |

`usage` 字段目前固定为 0，工具调用也还没有实现。

## 部署

两种方式选一个：Docker Compose，或者直接在 `/opt` 下从源码构建。两种方式都需要一个已经登录 Muse 的账号。

### 环境要求

- Docker：Linux + Docker Engine 24+ + Docker Compose v2
- 源码：Go 1.24+、Node 24+、Chromium/Chrome，以及 `make`（没有 make 时手动执行构建命令）。Node 只在构建前端时用
- 运行环境必须能启动 Chromium。Docker 镜像已经包含，源码部署需要自己安装

### 获取代码（/opt）

```bash
sudo mkdir -p /opt/muse2api
sudo chown "$USER":"$USER" /opt/muse2api
git clone <repo-url> /opt/muse2api
cd /opt/muse2api
```

下面的命令都假设项目在 `/opt/muse2api`。

### 方式一：Docker Compose

```bash
cp .env.example .env

# 编辑 .env，至少设置这三个：
# MUSE2API_KEY              OpenAI 接口的 Bearer Key
# MUSE2API_ADMIN_PASSWORD   后台管理员密码，至少 8 位
# MUSE2API_CREDENTIAL_KEY   base64 编码的 32 字节密钥
openssl rand -base64 32

# 创建 ./data，并设置容器用户 10001 的权限
sudo make init-data
# 没有 make 时：
# sudo sh deploy/init-data.sh

docker compose up -d --build
docker compose ps
```

默认只监听宿主机 `127.0.0.1:18610`，不要把管理后台直接暴露到公网。

```bash
curl http://127.0.0.1:18610/healthz
```

后台地址是 `http://127.0.0.1:18610/admin`，用 `MUSE2API_ADMIN_USERNAME`（默认 `admin`）和 `MUSE2API_ADMIN_PASSWORD` 登录。

如果宿主机用户不是 10001，在 `.env` 里设置 `MUSE2API_UID` 和 `MUSE2API_GID`，再执行 `sudo make init-data`。SELinux 主机如果遇到权限错误，需要给绑定挂载加 `:Z`。

### Docker secrets（可选）

生产环境可以把密钥放到文件里，避免出现在 `docker inspect` 的环境变量中。仓库已经准备好 `deploy/docker-compose.secrets.yml`：

```bash
mkdir -p deploy/secrets
openssl rand -hex 32 > deploy/secrets/muse2api_key
printf '%s' 'a-long-random-admin-password' > deploy/secrets/muse2api_admin_password
openssl rand -base64 32 > deploy/secrets/muse2api_credential_key
chmod 700 deploy/secrets
chmod 0444 deploy/secrets/*

docker compose -f docker-compose.yml -f deploy/docker-compose.secrets.yml up -d --build
```

应用支持 `MUSE2API_KEY_FILE`、`MUSE2API_ADMIN_PASSWORD_FILE`、`MUSE2API_CREDENTIAL_KEY_FILE` 和 `MUSE2API_PROXY_FILE`。文件优先于普通环境变量。

使用 secrets 覆盖后，后续命令也建议设置：

```bash
export COMPOSE_FILE=docker-compose.yml:deploy/docker-compose.secrets.yml
```

### 方式二：从源码运行（/opt）

不想用 Docker 时，直接在刚才的 `/opt/muse2api` 下构建。

```bash
cd /opt/muse2api
cp .env.example .env
# 编辑 .env；如果自动探测不到浏览器，加上 MUSE2API_CHROMIUM=/usr/bin/chromium
make build

mkdir -p data
chmod 700 data
MUSE2API_HOME=/opt/muse2api ./bin/muse2api
```

没有 `make` 时，等价的构建命令是：

```bash
cd web && npm ci && npm run build
cd ..
CGO_ENABLED=0 go build -trimpath -o bin/muse2api ./cmd/muse2api
```

前台运行确认正常后，可以用 systemd 托管。先创建专用用户（已有就跳过），再把数据和 `.env` 交给它：

```bash
sudo useradd --system --home-dir /opt/muse2api --shell /usr/sbin/nologin muse
sudo chmod 755 /opt/muse2api
sudo chown -R muse:muse /opt/muse2api/data
sudo chown muse:muse /opt/muse2api/.env
sudo chmod 600 /opt/muse2api/.env
sudo install -m 644 deploy/muse2api.service /etc/systemd/system/muse2api.service
sudo systemctl daemon-reload
sudo systemctl enable --now muse2api
sudo systemctl status muse2api
```

日志用 `journalctl -u muse2api -f` 查看。源码运行不要执行 Docker 的 `sudo make init-data`，那会把 `data/` 改成 UID 10001；这里的数据目录归运行服务的用户。

默认配置会读取 `/opt/muse2api/.env`。如果服务用户对项目目录没有执行权限，或者 Chromium 不在 `PATH` 里，分别调整目录权限和 `MUSE2API_CHROMIUM`。不想创建 `muse` 用户时，把 `deploy/muse2api.service` 里的 `User`/`Group` 改成现有用户，并让该用户拥有 `data/` 和 `.env`。

### 公网访问

公网部署请放在 HTTPS 反向代理后面。Nginx 模板在 `deploy/nginx-tls.example.conf`，反代后设置：

```dotenv
MUSE2API_TRUST_PROXY=1
MUSE2API_PUBLIC_BASE=https://api.example.com
```

代理要替换 `X-Forwarded-For`，不要追加客户端传来的值。Nginx 示例已经关闭 `proxy_buffering`，否则 SSE 会变成一次性输出。

### 添加账号

进入后台的“账号池”，点“添加账号”，粘贴自己浏览器里登录 Muse 后的 Cookie Header 或 JSON。核心 Cookie 是：

- `hatch_sess`
- `hatch_vml`
- `hatch_native_auth_device`

保存后点“检测”确认会话有效。只导入自己的账号；Cookie 失效后重新登录上游再更新。

也可以用仓库自带的浏览器插件导出：在 Chrome / Edge 的扩展页开启开发者模式，加载 `browser-extension/` 目录，登录 muse.ai 后点击扩展图标即可复制 Cookie Header 或 JSON。也可以直接下载 [ZIP 包](https://github.com/vip0014/muse2api/releases/download/browser-extension-v1.0.0/muse2api-cookie-exporter-1.0.0.zip)，解压后加载。安装与使用说明见 [browser-extension/README.md](browser-extension/README.md)。

### 验证 API

```bash
curl http://127.0.0.1:18610/v1/models \
  -H "Authorization: Bearer $MUSE2API_KEY"
```

## 配置

必填项：

| 变量 | 说明 |
|---|---|
| `MUSE2API_KEY` | `/v1/*` 的 Bearer Key。启动后也可以在后台轮换，轮换后写入 `data/api_key.secure` |
| `MUSE2API_ADMIN_PASSWORD` | 后台管理员密码，至少 8 位 |
| `MUSE2API_CREDENTIAL_KEY` | 32 字节随机值的 base64，用于加密账号 Cookie。丢失后旧账号无法解密 |

常用可选项：

| 变量 | 默认 | 说明 |
|---|---|---|
| `MUSE2API_ADMIN_USERNAME` | `admin` | 后台管理员用户名 |
| `MUSE2API_BIND` | `127.0.0.1` | Compose 绑定地址 |
| `MUSE2API_UID` / `MUSE2API_GID` | `10001` | 容器运行用户，需和 `data/` 所有者一致 |
| `MUSE2API_MAX_UPLOAD_BYTES` | `16777216` | 上传限制；改大时要同步修改 Nginx 的 `client_max_body_size` |
| `MUSE2API_BROWSER` | `1` | 设为 `0` 后不启动 Chromium，对话、生成和额度都不可用 |
| `MUSE2API_LOG_LEVEL` | `info` | 日志级别 |

其余变量、超时、并发、队列和目录配置都在 `.env.example` 里。`.env.example` 的占位值会被程序拒绝，不要直接拿来启动。

## 使用

### 对话

Base URL 是 `http://127.0.0.1:18610/v1`，请求头带上 `Authorization: Bearer $MUSE2API_KEY`。模型名使用 `muse-spark`、`muse-image`、`muse-video`。

```bash
curl http://127.0.0.1:18610/v1/chat/completions \
  -H "Authorization: Bearer $MUSE2API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"model":"muse-spark","messages":[{"role":"user","content":"你好"}],"stream":false}'
```

### 图片

```bash
curl http://127.0.0.1:18610/v1/images/tasks \
  -H "Authorization: Bearer $MUSE2API_KEY" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: my-image-1" \
  -d '{"prompt":"海边的灯塔","response_format":"url"}'
```

然后用 `GET /v1/images/tasks/{id}` 查询。同步接口用 `POST /v1/images/generations`，参考图编辑用 `POST /v1/images/edits`。

### 视频

`POST /v1/videos` 创建任务，`GET /v1/videos/{id}` 查询。`/v1/videos/generations` 是兼容路径，行为相同。

媒体 URL 默认也要求认证。浏览器里已经登录后台时可以直接预览；外部客户端下载时带上 Bearer。只有显式设置 `MUSE2API_PUBLIC_MEDIA=1` 才会恢复匿名访问。

### API 路径

- `GET /v1/models`
- `POST /v1/chat/completions`
- `POST /v1/responses`
- `POST /v1/images/generations`
- `POST /v1/images/edits`
- `POST /v1/images/tasks`
- `GET /v1/images/tasks/{id}`
- `POST /v1/videos` 或 `POST /v1/videos/generations`
- `GET /v1/videos/{id}` 或 `GET /v1/videos/generations/{id}`
- `GET /v1/media/{name}`

## 注意事项

- 这不是纯 HTTP 协议实现。对话、生图、视频和额度都依赖 Chromium 打开 Muse 页面，页面结构改版后选择器可能需要跟着修。
- `gpt-4o`、`claude-sonnet-4` 会落到 `muse-spark`，`dall-e-3` 落到 `muse-image`，`sora` 落到 `muse-video`。这些只是兼容别名，不代表对应的官方模型能力。
- 2026-10-26 用真实 Free 账号做过一轮端到端验收。其他套餐、地区和上游改版仍需要自己验证。
- 浏览器页面操作目前是全局串行的，本地任务并发不等于上游并行生成。
- 视频时长最终由上游决定，请求里的 `duration` 不是精确保证。
- 服务重启时，遗留的排队/运行中任务会标成 `unknown`，不会自动重放，避免重复扣费。管理员会话保存在内存，重启后需要重新登录。
- 后台的备份恢复要求重启服务，当前进程不会热重载恢复后的数据。
- 程序兼容旧版 `accounts.json/tasks.json`；首次启动会把旧账号加密迁移。升级前先备份 `data/` 和加密 Key。
- 不要把 CDP 端口 `19210` 发布到宿主机。`data/chromium` 里的 profile 和生成的媒体都应当按敏感数据保护。

## 常见问题

**登录后台后看不到账号，或者检测返回 401？**  
Cookie 失效了，重新从自己的浏览器登录 Muse，再更新账号。

**接口返回 503，提示能力不可用？**  
先确认 `MUSE2API_BROWSER=1`，再看容器日志里的 Chromium/CDP 错误。`MUSE2API_BROWSER=0` 是主动降级，不是 bug。

**Nginx 后面的 SSE 不实时？**  
确认 `proxy_buffering off`，并保留后端返回的 `X-Accel-Buffering: no`。

**容器报 `permission denied`？**  
`data/` 的所有者要和容器用户一致。执行 `sudo make init-data`，或者设置 `MUSE2API_UID/GID`。

**重启后任务变成 `unknown`？**  
服务无法确认上游是否已经完成，不会自动重试。按任务 ID 人工核对，避免重复扣费。

## 日常维护

### 备份

Docker 部署：

```bash
make backup
```

源码 + systemd 部署：

```bash
sudo env MUSE2API_SYSTEMD_UNIT=muse2api make backup
```

脚本会短暂停止服务，打包 `data/`，默认跳过可重建的 `data/chromium` 和 `data/downloads`。归档默认放在项目下的 `backups/`。

### 恢复

Docker：

```bash
docker compose stop muse2api
mv data "data.before-restore-$(date -u +%Y%m%dT%H%M%SZ)"
tar -xzf backups/muse2api-data-YYYYMMDDTHHMMSSZ.tar.gz -C .
sudo chown -R 10001:10001 data   # 改过 MUSE2API_UID/GID 时用对应值
docker compose up -d
```

源码 + systemd：

```bash
sudo systemctl stop muse2api
cd /opt/muse2api
sudo mv data "data.before-restore-$(date -u +%Y%m%dT%H%M%SZ)"
sudo tar -xzf backups/muse2api-data-YYYYMMDDTHHMMSSZ.tar.gz -C .
sudo chown -R muse:muse data
sudo systemctl start muse2api
```

不要直接覆盖现有 `data`；归档里包含账号密文和媒体，应该按敏感文件保存。

### 升级

Docker：

```bash
make backup
docker compose build --pull
docker compose up -d
```

源码 + systemd：

```bash
sudo env MUSE2API_SYSTEMD_UNIT=muse2api make backup
git pull
make build
sudo systemctl restart muse2api
```

## 本地开发

需要 Go 1.24+、Node 24+ 和本机 Chromium/Chrome。

```bash
make test
make build
cp .env.example .env
go run ./cmd/muse2api
```

前端开发可以用 `cd web && npm run dev -- --port 7100`，Vite 会代理到 `127.0.0.1:18610`。

## 安全与合规

- 只导入自己的账号。不要批量注册、转卖额度，也不要用本项目绕过上游限制。
- 管理员密码、API Key 和 `MUSE2API_CREDENTIAL_KEY` 都要单独备份。加密 Key 丢失后，旧账号 Cookie 无法恢复。
- 生产环境使用 HTTPS。CDP 端口 `19210` 只在容器内监听，不要映射到宿主机。
- `data/` 包含账号密文、媒体和浏览器 profile，不要提交到 Git，也不要放进公开备份。

## 许可

项目使用 MIT 许可证，见 `LICENSE`。上游项目是 [czg86389-hub/muse2api](https://github.com/czg86389-hub/muse2api)，同样使用 MIT 许可证，原项目版权归其作者所有。

Docker 镜像包含 Alpine、Chromium、Noto CJK 字体等第三方组件，它们分别遵循自己的许可证。
