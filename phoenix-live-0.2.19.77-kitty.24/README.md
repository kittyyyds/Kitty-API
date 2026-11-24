# kittyapi.xyz 凤凰城服务器线上版本归档 (`0.2.19.77-kitty.24`)

本目录保存从凤凰城生产服务器（`https://kittyapi.xyz`）提取的当前运行版本 `0.2.19.77-kitty.24`（Tag: `cs-v0.2.19.77-kitty.24`, Commit: `7eb98b08e96d0df96da9e9a948c7165935dea50a`）全部源码级数据库迁移、内置资源、前端编译产物、Caddy 线上热补丁及发布演练脚本。

## 目录结构

- `migrations/`：从线上 `0.2.19.77-kitty.24` 生产二进制 `embed.FS` 原样提取的全部 363 项 SQL 迁移脚本（含 Kitty 定制 `900`–`952` 号段）。
- `resources/`：线上 `release-build/resources`（含 `model-pricing/` 价卡定义与 `content-moderation/` 审查模块源码）。
- `web-dist/`：线上 `0.2.19.77-kitty.24` 内嵌前端编译产物与静态资源（`assets/*`、`index.html`、`prompt_templates/*` 等）。
- `caddy-hotfixes-20261124/`：2026-11-24 现网 Caddy 挂载的两项前端热补丁源码与构建脚本（`release-reminder` 上游版本提醒、`account-model-iq-fix` 账号模型同步与智商摘要 15 秒刷新）。
- `scripts/`：凤凰城服务器 `/opt/kitty-cs-zuchengchen-20261120/releases/0.2.19.77-kitty.24` 及切站目录中的发布、演练、切换、退役表清理与验收脚本（不含任何密钥或运行期凭据）。
- `metadata/`：线上版本发布清单与公开验收回执（`release.json`、`stage.public.json`、`final-release.public.json` 等）。
- `Dockerfile`：线上镜像 `kitty/sub2api-cs-zuchengchen:0.2.19.77-kitty.24` 构建文件。

> 注：仓库根目录完整保留了凤凰城服务器存留的最新全量未编译 Go + Vue 主站源码树（`v0.2.21-custom.1-kitty.36`，Commit `2bc6c301622869f44d9865a5c179f4bb911baf79`，共 7,477 个文件）。
