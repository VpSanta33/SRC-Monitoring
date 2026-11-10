# SRC Observatory

**无需登录、无需独立服务器的 SRC 公开资产监控站点。**

- 在线访问：[GitHub Pages](https://vpsanta33.github.io/SRC-Monitoring/)
- 采集与发布：[GitHub Actions](https://github.com/VpSanta33/SRC-Monitoring/actions/workflows/pages.yml)
- 监控状态：[monitor-state 分支](https://github.com/VpSanta33/SRC-Monitoring/tree/monitor-state)

React + TypeScript 展示 HackerOne、Bugcrowd 的公开项目与资产范围；GitHub Actions 定时运行 Go 采集器，完成快照比对并发布静态 JSON。关注和已读状态保存在浏览器，不需要账号、Cookie 会话或后端 API。

## 功能

- 项目目录、资产清单、范围内 / 范围外筛选、资产类型筛选、分页搜索。
- 项目与资产的新增、修改、移除记录，以及变更前后内容。
- 项目详情与资产说明按需加载，保留平台原始规则链接。
- 本地关注列表、已读标记和按当前筛选导出 CSV。
- 数据源状态、最后成功采集时间、数据发布时间和最近 50 次运行记录。
- 默认每小时第 **17、47 分钟（UTC）** 执行；页面“刷新数据”仅读取最新发布结果。
- 抓取失败、无效快照和数量异常下降时，保留最后有效数据；Actions 报错并发布来源异常状态。

## 架构

```text
GitHub Actions（定时 / 手动 / main 推送）
  │
  ├─ 恢复 monitor-state 分支的 JSONL 基线
  ├─ Go 采集器：下载公开范围 → 归一化 → 快照比对
  │    └─ 临时 SQLite 工作库，仅在本次任务中使用
  ├─ 更新 monitor-state：基线、变更历史、运行状态
  ├─ 导出公开 JSON + 构建 React
  └─ GitHub Pages 发布
        └─ 浏览器：静态读取、筛选、CSV 导出
             └─ localStorage：关注 / 已读
```

**没有常驻 Go 服务，没有登录系统，也不需要配置平台 API Token 或个人 GitHub Token。** 工作流使用仓库内置的短期 `GITHUB_TOKEN` 保存状态并发布 Pages，该令牌不会进入前端或监控数据。

## 数据来源

首版读取社区维护的 [bounty-targets-data](https://github.com/arkadiyt/bounty-targets-data)：

- [HackerOne 公开范围](https://github.com/arkadiyt/bounty-targets-data/blob/main/data/hackerone_data.json)
- [Bugcrowd 公开范围](https://github.com/arkadiyt/bounty-targets-data/blob/main/data/bugcrowd_data.json)

这不是平台官方实时接口，不保证覆盖所有项目。镜像可能延迟或不完整；所有测试范围均应以项目原始规则为准。采集器只读取范围数据，不扫描或探测目标。

“成功同步”表示采集器成功读取或确认镜像的时间，“数据发布于”表示静态数据生成时间；后者不代表上游来源已经更新。概览的 24 小时变更数截至该次数据生成时间。

同一项目中按平台、项目标识、原始类型和去除首尾空白后的资产标识去重。保留 URL 大小写、通配符和尾斜杠。原始条目同时出现于范围内外时，保留全部说明，优先标为范围外并显示冲突提示。

## 在 GitHub 上部署

1. 将代码推送到 `main`。
2. 在 **Settings → Pages → Build and deployment → Source** 选择 **GitHub Actions**。
3. 在 Actions 打开 **Collect scopes and deploy Pages**，选择 **Run workflow**。
4. 工作流会生成 `monitor-state` 分支并发布 Pages。访问 `https://<用户名>.github.io/<仓库名>/`。

当前仓库已包含完整工作流。Fork 后，需要在自己的仓库启用 Actions 与 Pages，并在 `monitor.config.json` 修改 `repository` 字段；定时工作流在 Fork 中默认不自动启用。

前端基础路径由 `actions/configure-pages` 的 `base_path` 自动传给 Vite，同时支持项目子路径与根路径。不会请求 `/api`，也不会把静态数据错误加载到域名根目录。

### 手动采集

在 Actions 的工作流页面选择 **Run workflow**。这需要仓库写权限；普通访客无需登录即可浏览站点。网页上不会要求访客输入 GitHub Token。

### 修改计划或数据源

编辑 `monitor.config.json`：

```json
{
  "repository": "VpSanta33/SRC-Monitoring",
  "workflow": "pages.yml",
  "schedule": "17,47 * * * *",
  "timezone": "UTC",
  "enabled": {"hackerone": true, "bugcrowd": true}
}
```

修改周期时，同时更新 `.github/workflows/pages.yml` 中的 `schedule.cron`。`scripts/check-config.mjs` 会检查二者是否一致。数据源开关只需修改 `enabled`，关闭后保留既有数据。

GitHub 定时任务可能排队延迟，并非严格实时服务。公开仓库长期没有活动时，GitHub 可能停用定时工作流；可在 Actions 中重新启用。参见 [GitHub schedule 文档](https://docs.github.com/en/actions/reference/workflows-and-actions/events-that-trigger-workflows#schedule)。

## 数据持久化与故障恢复

- `monitor-state` 分支是持续监控的正式基线，不依赖可能过期的 Actions cache 或 artifact。
- 状态按稳定顺序分为 JSONL 文件，并记录 SHA-256 与条数。损坏、缺文件或读取分支失败时中止任务，**不会静默重新建立基线**。
- 每次采集使用临时 SQLite，完成后将状态导回 JSONL；不会将数据库部署到 Pages。
- 首次采集只建立基线，重复快照不产生重复变更；支持 ETag 304。
- 项目或资产总数下降超过 30% 时拒绝整个来源快照。确认真实大规模变更后，应先保留状态分支历史，再调整 `ApplySnapshot` 中的保护阈值并测试。
- 各来源独立更新。部分来源失败时，成功来源可继续更新，失败来源保留旧资产；页面显示错误，发布后的工作流仍标记失败供维护者检查。
- 保存完整变更历史；网页只发布最近 **5,000** 条。旧记录仍可从状态分支读取。工作流状态保存最近 100 次运行，网页展示最近 50 次。
- 数据文件名包含内容摘要，清单在完整导出后才写入，避免页面混用不同批次的数据。
- 不要删除或强制重写 `monitor-state` 来“重新部署”。修复 UI 只需推送 `main`。
- 关注与已读使用稳定的数据集标识作为命名空间，跨发布版本保留；清除浏览器网站数据会重置，跨设备不会自动同步。

仓库主分支只维护源码与配置。所有状态数据都是公开范围信息，检查点与静态导出都排除个人关注和已读标记。

## 本地开发

需要 Go 1.24+、Node.js 24 和 npm：

```sh
make setup
make collect  # 下载公开镜像，生成 .state/state 与 web/public/data
make run      # 启动 Vite；无需启动 Go HTTP 服务
```

访问终端打印的 Vite 地址。以后可用 `make collect` 更新数据；只想根据现有基线重新导出时使用 `make export`。

复现 GitHub Pages 的子路径：

```sh
VITE_BASE_PATH=/SRC-Monitoring/ npm --prefix web run build
VITE_BASE_PATH=/SRC-Monitoring/ npm --prefix web run preview
```

浏览 `http://localhost:4173/SRC-Monitoring/`。`.state/`、原来的本地数据库、二进制、生成数据和构建输出均不会提交到 `main`。

若希望本地继续线上已有的监控基线，先在干净的 `.state/` 目录执行 `bash scripts/prepare-state.sh`，再采集。脚本会拒绝覆盖未提交的本地状态；GitHub Actions 每次使用干净 runner，不存在此冲突。

## 项目结构

```text
cmd/collect/main.go             单次采集 / 离线导出 CLI
internal/monitor/model.go      平台适配、校验、去重
internal/monitor/store.go      临时 SQLite 与快照比对
internal/monitor/sync.go       HTTP 抓取、ETag、重试
internal/monitor/checkpoint.go JSONL 基线恢复与持久化
internal/monitor/export.go     静态站数据导出
web/src/data.ts                静态数据客户端、筛选、本地偏好、CSV
web/src/App.tsx                React 界面
scripts/                      状态分支与配置校验脚本
.github/workflows/pages.yml    定时采集和 Pages 发布
.github/workflows/ci.yml       PR 只读测试，不发布、不写状态
```

## 验证

```sh
make test
```

测试覆盖基线幂等性、增删改、范围冲突、异常下降保护、跨执行恢复、检查点损坏拒绝、来源失败保留数据、静态导出字段隔离、Pages 子路径、按需详情、本地状态持久化和 CSV 转义。
