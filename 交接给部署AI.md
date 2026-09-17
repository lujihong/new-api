# 交接给部署 AI：鑫元宝 New API 升级上线

> 2026-09-16 更新：以下为历史升级交接，禁止直接按旧 compose 重建。当前本地路径 `/Users/lujihong/docker/www/鑫元宝/new-api`；线上发布配置 `/data/xybcloud/releases/aicc-20260916/compose.json`。以父目录 `项目管理说明.md` 与 `本轮发布验收记录.md` 的当前状态及现场 inspect 为准。

你要把 **本机已经准备好的代码与脚本** 部署到 **生产服务器**。  
生产站点：**鑫元宝云计算模型服务平台** · 用户访问 **`https://api.xybcloud.com`**。

先读完本文和同目录 **`升级改动必看.md`**。两份都读完再碰服务器。  
**本文不含数据库密码、Redis 密码、Session 密钥。** 这些只在服务器上的 `docker-compose.yml` / 运行中容器环境变量里。禁止写进 Git、禁止贴到聊天。

---

## 0. 你的任务边界

### 你必须做

1. 在**能访问生产 Postgres 的环境**做只读预检和 `pg_dump`。
2. 用 dump 恢复出的 **演练库** 先跑完新版本启动（AutoMigrate）。
3. 演练通过后，在维护窗口对生产：必要时 `ALTER` 额度列 → 本地构建镜像 → 启动 master。
4. 启动后核对 `options.ServerAddress`、站名、type=54 渠道、Seedance 计费表达式。
5. 用 `docker compose -f docker-compose.yml -f docker-compose.site-build.yml` **本地 build**，禁止 `docker pull calciumion/new-api:latest`。

### 你禁止做

- 换 `SQL_DSN`、换库名、指向空库、SQLite 替换 Postgres。
- `DROP` / `TRUNCATE` 任何业务表。
- 用官方仓库的 `docker-compose.yml` 覆盖生产 compose。
- `git cherry-pick aca72e78d` 整提交。
- `SKIP_64BIT_QUOTA_SCHEMA_CHECK=true`。
- `--no-verify`、force push、硬重置生产仓库。
- 把密钥提交进 Git。
- 没 dump、没演练就启动新二进制连生产库。
- `scripts/apply-site-overlay.sh --mode=full` 在补丁 reject 后带着 `.rej` 上线。

### 上一名 AI 没有做、也不能替你做的事

- **没有**对生产或演练库执行迁移（本机连不上 Docker / Postgres）。
- **没有**重建生产容器，因此 compose 里已改的 Cookie 域名 **还没进运行中进程**。
- 后端 4 份 patch 是相对 **rc.24 线** `e2c7aa7b1` 导出的，**不能假设能直接打到 rc.36 / `upstream/main`**。

---

## 1. 仓库与版本（以磁盘为准）

| 项 | 值 |
|---|---|
| 代码目录 | `/Users/lujihong/docker/www/new-api`（服务器上可能是 `/docker/www/new-api` 或同等拷贝） |
| 当前 HEAD（未合并官方前） | `aca72e78d` ≈ `v1.0.0-rc.24-35-gaca72e78d` |
| 官方标签 | `v1.0.0-rc.36` 仅作参考 |
| **必须合并的目标** | `upstream/main`（rc.36 **之后**还有 `4fc9d1f1f` options 主键修复、`9bf328d97` Sora 字段修复） |
| remotes | `origin` = 用户 fork；`upstream` = `https://github.com/QuantumNous/new-api.git` |
| 参考 worktree | `/Users/lujihong/docker/www/new-api-rc36`（标签快照，**不要**把它当生产 compose 来源） |
| 生产库 | PostgreSQL，库名 `newapi`，容器网络 `mynet`，DSN 在生产 compose 的 `SQL_DSN` |
| 日志卷 | `./logs` → 容器 `/app/logs` |
| 数据卷 | `./data` → 容器 `/data` |

本地相对官方 **只有 1 个独有提交** `aca72e78d`。定制已拆到 `site-overlay/`，不要再 cherry-pick 那一颗。

---

## 2. 上一名 AI 已经改好的本地交付物

这些在仓库里，部署时带着走：

| 路径 | 作用 |
|---|---|
| `site-overlay/manifest.json` | 覆盖文件分类、补丁列表 |
| `site-overlay/files/` | 品牌、页脚、登录、首页、logo |
| `site-overlay/i18n-delta/` | 仅覆盖「退出 / 视频预览」等 key |
| `site-overlay/patches/001`–`004` | Go 定制（旧基底；合并官方后多半要 rebase） |
| `site-overlay/branding.json` | 站名与 `https://api.xybcloud.com` |
| `site-overlay/PROTECTED_PATHS` | 禁止覆盖的路径 |
| `scripts/apply-site-overlay.sh` | `--mode=branding` 或 `--mode=full` |
| `scripts/refresh-site-overlay.sh` | rebase 成功后把工作区文件写回 overlay |
| `scripts/upgrade-from-upstream.sh` | fetch + merge + **恢复 compose** + overlay + 写 VERSION |
| `scripts/write-version.sh` | `git describe --tags --always` → `VERSION` |
| `scripts/preflight-db.sh` | 打印预检 SQL；`SQL_DSN=... --read-only` 才连库 |
| `scripts/merge_i18n.py` | 增量合并 locale |
| `docker-compose.site-build.yml` | **无密钥**，强制 `image: new-api:xyb-local` + `build: .` |
| `Dockerfile` | 构建时把 `site-overlay/files/web` 叠进前端，空 VERSION 会失败 |
| `升级改动必看.md` | 表结构、AutoMigrate、Seedance 计费、回滚 |

`docker-compose.yml` 本机已把：

- `SESSION_COOKIE_TRUSTED_URL=https://api.xybcloud.com`
- `FRONTEND_BASE_URL=https://api.xybcloud.com`

`NODE_NAME` 已改回 `token-profitly-cn`，避免实例表无故分裂。  
**上线前必须 `docker inspect` 生产容器当前 `NODE_NAME`，以线上为准，不要用猜的。**

主节点会 **忽略** `FRONTEND_BASE_URL`。登录 Cookie 看 `SESSION_COOKIE_TRUSTED_URL`。对外链接看数据库 **`options.ServerAddress`**。

---

## 3. Overlay 怎么用（合并官方之后）

### 第一次合并请用 branding（推荐）

```bash
cd /path/to/new-api
# 工作区除 compose / overlay / 本文档外不要有别的已跟踪脏文件
./scripts/upgrade-from-upstream.sh --target=upstream/main --mode=branding
```

该脚本会：

1. `git fetch upstream --tags`
2. 先把当前工作区的 `docker-compose.yml` 拷到临时文件（含未提交的域名改动）
3. `git merge --no-commit --no-ff upstream/main`
4. **立刻用临时文件覆盖回去**，丢弃官方示例 compose，并 `git add docker-compose.yml` 以免提交时误带上官方文件
5. 拷贝品牌文件 + 合并 i18n
6. **不打** Go 补丁、**不覆盖** rc.36 的定价页 / 任务日志列
7. 写入 `VERSION`

冲突则停。只解决业务文件，compose 永远用我方。

### 什么时候才能 `--mode=full`

仅当：

1. branding 模式已经能 `bun`/`go` 构建；并且
2. 你已经把视频代理、匿名播放、充值等定制 **在新官方文件上重做**；并且
3. `git apply --check site-overlay/patches/*.diff` 全部通过。

否则不要 full。rc.24 的 `model-pricing-sheet.tsx` 盖到 rc.36 会拆掉任务表达式定价 UI，Seedance 将无法正确配置。

补丁打不上时：

1. 停。不要 `--reject` 上线。
2. 以官方新文件为底，手工迁定制。
3. `./scripts/refresh-site-overlay.sh`
4. 用 `git diff upstream/main -- <go 文件>` 重写 `site-overlay/patches/*.diff`

产品取舍必须写进发布记录：

- 官方视频地址带签名 `?access=`；本地曾放行匿名已完成任务。保留匿名 = 在新 `controller/video_proxy.go` + `middleware/auth.go` 重打补丁。
- 充值上限以官方 `MaxWalletQuota` 为准，不要整文件覆盖 `controller/topup.go`。

---

## 4. 服务器上的标准顺序（不许跳）

在服务器仓库根目录操作。假设 compose 与代码在同一目录。

### 4.1 记录现状（不改数据）

```bash
git -C /path/to/new-api rev-parse HEAD
git -C /path/to/new-api describe --tags --always
docker inspect new-api --format '{{.Config.Image}} {{.Created}}'
docker inspect new-api --format '{{range .Config.Env}}{{println .}}{{end}}' \
  | grep -E '^(NODE_NAME|SESSION_COOKIE_|FRONTEND_BASE_URL|TZ)=' 
# 不要 grep SQL_DSN / SESSION_SECRET / REDIS 到聊天
```

记下 `NODE_NAME`，升级后 compose 必须仍是这个值。

### 4.2 全库备份

```bash
mkdir -p /path/to/new-api/backups
# 在服务器用环境变量提供连接，不要把 DSN 写进仓库
pg_dump --format=custom --file="/path/to/new-api/backups/newapi-$(date +%Y%m%d-%H%M%S).dump" \
  --dbname="$SQL_DSN"
ls -lh /path/to/new-api/backups/*.dump
```

确认文件非空，并复制一份离线。`backups/` 已 gitignore。

没有可恢复的 dump，后面全部停止。

### 4.3 只读预检

```bash
SQL_DSN='postgresql://...' ./scripts/preflight-db.sh --read-only
```

必须看：

1. `users.quota/used_quota/aff_quota/aff_history` 是否已是 `bigint`。若是 `integer`，**先 dump，再 ALTER，再启动新进程**（见 `升级改动必看.md` §3.1）。官方 **不会**自动升宽度。
2. `tokens.key` 有无重复。有重复必须先人工处理。
3. `options` 是否有主键、是否有重复 key。
4. `SystemName`、`ServerAddress`、`type=54` 渠道、用户数/令牌数。

把结果存到 `backups/preflight-*.txt`（不要含 DSN）。

额度仍是 `integer` 时，维护窗口执行：

```sql
ALTER TABLE users
  ALTER COLUMN quota TYPE bigint USING quota::bigint,
  ALTER COLUMN used_quota TYPE bigint USING used_quota::bigint,
  ALTER COLUMN aff_quota TYPE bigint USING aff_quota::bigint,
  ALTER COLUMN aff_history TYPE bigint USING aff_history::bigint;
```

然后再查一次四列都是 `bigint`。禁止用 `SKIP_64BIT_QUOTA_SCHEMA_CHECK`。

### 4.4 演练库（强制）

1. 另开一个 Postgres 库（不要用生产 `newapi` 这个名字去做试验）。
2. `pg_restore` 刚才的 dump。
3. 复制一份代码 worktree，compose 的 **DSN 只改成演练库**（改副本，不要改生产 compose 文件直到窗口开始）。
4. 在副本上：

```bash
./scripts/upgrade-from-upstream.sh --mode=branding
./scripts/write-version.sh
docker compose -f docker-compose.yml -f docker-compose.site-build.yml build
# 确认 DSN 已指向演练库之后：
docker compose -f docker-compose.yml -f docker-compose.site-build.yml up -d
```

5. 看日志：`database migration started` 成功；无 32-bit quota / token unique 报错。
6. 冒烟：管理员登录、老用户登录、令牌调用、页脚 ICP、站名、打开定价页。
7. 对照用户数/令牌数；确认新表 `task_plugins` 存在。

演练失败 → **不准**对生产做同样操作。用 dump 证明可以整库回滚。

### 4.5 生产窗口

1. 再 dump 一次。
2. 停旧容器：**不要删** `data/`、`logs/`。
3. 若 4.3 需要 ALTER，现在做。
4. 在**生产代码目录**合并官方 + branding overlay（或把演练已验证的 commit 同步过来，仍要保护 compose）。
5. `NODE_NAME`、`SQL_DSN`、`SESSION_SECRET`、Redis 与窗口前一致。
6. Cookie 两项为 `https://api.xybcloud.com`。若旧域 `token.profitly.cn` 仍解析到本站，过渡期写成：

   `SESSION_COOKIE_TRUSTED_URL=https://api.xybcloud.com,https://token.profitly.cn`

7. 构建并启动：

```bash
./scripts/write-version.sh
docker compose -f docker-compose.yml -f docker-compose.site-build.yml up -d --build
```

8. 只跑这一实例（master）。等 healthcheck。
9. 后台核对 `ServerAddress` 是否为 `https://api.xybcloud.com`（不是 compose 自动改的，库里没有就要在系统设置里改）。
10. **重配 Seedance / 豆包视频任务计费表达式**。旧 ModelRatio=46 **不会**自动变表达式。type=54 渠道不用改类型，会映射插件 `doubao`。
11. 用测试令牌跑一条短视频，核对扣费。
12. 用 `https://api.xybcloud.com` 登录并 refresh Cookie。

### 4.6 失败回滚

1. 停新容器。
2. 启动窗口前记下的 **旧镜像 ID** + 旧 compose。
3. 若旧进程因新列起不来：用窗口开始的 dump 恢复（会丢掉窗口内新注册/新订单，所以窗口要短）。
4. 不要随便 `DROP options_legacy_*`。

---

## 5. 构建注意

- 构建前必须 `./scripts/write-version.sh`，否则 Dockerfile 会失败（防止再出现 `v0.0.0`）。
- 前端官方用 **bun** + `web/bun.lock`，不要拿 `web/pnpm-lock.yaml` 去 frozen install。
- Dockerfile 会在 `COPY ./web` 之后再 `COPY site-overlay/files/web`，品牌资源以 overlay 为准。
- 后端阶段同样叠 `site-overlay/files/`（`custom_homepage.html`、`public/`）。**Go 补丁不会在 Docker 里自动 apply**，必须在宿主机 `apply-site-overlay` / 手工 rebase 后再 build。

---

## 6. 上线后验收（全勾完才算成功）

- [ ] 日志版本号 = `git describe`，不是 `v0.0.0`
- [ ] `/api/status` 成功
- [ ] 老用户能登录；`https://api.xybcloud.com` 下 Cookie refresh 成功
- [ ] 用户数、令牌数、抽查余额与 dump 一致
- [ ] 页脚：鑫元宝云计算（重庆）有限责任公司、渝ICP备2024036208号
- [ ] `options.ServerAddress` = `https://api.xybcloud.com`
- [ ] `options.SystemName` = 鑫元宝云计算模型服务平台
- [ ] type=54 仍在；测试 Seedance 扣费正确
- [ ] 未误改 `SESSION_SECRET` / DSN / Redis
- [ ] `NODE_NAME` 与窗口前 `docker inspect` 一致
- [ ] dump 已离线保存

---

## 7. 给部署 AI 的回复格式

做完后用这个结构向用户汇报，**不要**粘贴密钥和完整 DSN：

1. 预检：额度列类型、token 重复条数、用户数、令牌数、`ServerAddress` 是否已是 api.xybcloud.com
2. dump 文件名与大小（不含路径中的密码）
3. 演练是否成功、失败日志摘要
4. 生产是否已切换、镜像 tag `new-api:xyb-local`
5. Seedance 表达式是否已重配、测试任务 ID（不要贴 API Key）
6. 未完成项（例如 full overlay 尚未 rebase）

---

## 8. 一句话

本地能改的（overlay、脚本、Dockerfile、域名 compose、文档）已经就绪。  
**数据库迁移只在新版本 master 连上同一条 `SQL_DSN` 时发生。** 那一步必须由你在有 Docker/Postgres 的服务器上，按 dump → 预检 → 演练 → 生产 执行。
