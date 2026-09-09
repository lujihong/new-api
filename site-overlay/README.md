# 站点 Overlay

生产定制（鑫元宝云计算模型服务平台 · `https://api.xybcloud.com`）与官方 New API 源码分离存放。

- 清单：`manifest.json`
- 精确覆盖的文件：`files/`
- 后端补丁：`patches/`（相对 rc.24 线 `e2c7aa7b1`，合并 `upstream/main` 后必须 rebase）
- 文案增量：`i18n-delta/`（禁止整文件覆盖官方 locale）

部署与升级步骤见仓库根目录：

1. **`交接给部署AI.md`**（给另一名 AI / 运维的操作手册）
2. **`升级改动必看.md`**（数据迁移、禁令、计费、回滚）

应用：

```bash
scripts/apply-site-overlay.sh --mode=branding   # 合并官方后的默认
scripts/apply-site-overlay.sh --mode=full       # 含旧定价页与 Go 补丁；打不上必须停
```
