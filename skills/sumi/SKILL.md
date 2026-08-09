---
name: sumi
description: 用 sumi CLI 查询、记录、修改和删除个人账单（记账）。当用户说记一笔、花了多少钱、买了什么、这个月花了多少、查账单、删掉那笔、改一下金额、加个分类、收入多少、支出统计时使用。Covers recording expenses/income, querying transactions, monthly/daily/category spending stats, and managing categories.
---

# sumi 记账

通过 `sumi` CLI 操作用户的账单数据。所有命令成功时向 stdout 输出**一个 JSON 文档**，失败时 stdout 为空、错误信息在 stderr、退出码非零。可以直接用 `jq` 或 JSON 解析器处理输出。

## 前置检查

**如果 `sumi` 命令不存在**，先安装它（脚本在本 skill 目录下，可重复执行，已装好会直接跳过）：

```bash
sh scripts/install-cli.sh
```

它会按当前系统架构下载对应的静态二进制装到 `/usr/local/bin`（无权限时退到 `~/.local/bin` 并提示路径）。装完用 `sumi --help` 确认。

只在 `sumi: command not found` 时才需要这一步，不要每次对话都跑。如果它报错，按错误里的提示处理——常见情况是容器里没有 `curl`/`wget`，或者需要用 `SUMI_CLI_URL` 指向内网镜像。

凭证按这个顺序取：**环境变量 > 本地配置文件**（`~/.config/sumi/config.json`，可用 `SUMI_CONFIG` 改路径）。

- `SUMI_API_URL` — 服务地址，默认 `http://localhost:3000`
- `SUMI_API_KEY` — API Key

两者都可以由环境注入；如果环境里没有，就得先在本地登录一次：

```bash
printf '%s' "$PASSWORD" | sumi auth login --email me@example.com --password-stdin
sumi auth key create --name my-agent
```

`--password-stdin` 是你（agent）唯一该用的方式。省略密码参数会让命令弹交互式提示，而在非交互环境下它不会挂住等输入，会直接报 `stdin is not a terminal` 并告诉你该用哪个参数。

`login` 把会话（access + refresh token）写入配置文件（权限 0600），`key create` 生成 API Key 并一并存进去，之后所有 `bill`/`category`/`stats` 命令都不再需要环境变量。access token 过期会自动续期，不用重新登录。

用 `sumi auth status` 查当前用的是哪套凭证、来自环境还是配置文件、以及这个 key 是否真的有效。

**不要代替用户输入密码。** 密码只能由用户自己提供；如果没有可用凭证，把上面的命令给用户让他们自己跑，或者请他们注入 `SUMI_API_KEY`。

API Key 需要这些 scope：`transactions:read` `transactions:write` `transactions:update` `transactions:delete` `categories:read` `categories:write` `stats:read`。如果某个操作返回 `http 403: Forbidden`，说明这个 key 缺对应 scope，不要重试，直接告诉用户需要补哪个 scope。

## 核心概念

**分类是两级的，账单只能挂在二级分类上。** 记账时 `--category` 必须给二级分类名（如 `吃`、`行`、`购物`），不能给一级分类名（如 `必要`、`非必要`）。不确定有哪些分类时先跑 `sumi category list`。

**类型**用 `--type expense`（支出，默认）或 `--type income`（收入）。支出和收入的分类树是分开的，用收入类型配支出分类名会报错。

**金额**是字符串，必须大于 0，不要带货币符号。**日期**是 `YYYY-MM-DD`。

## 命令

### 记一笔

```bash
sumi bill add --amount 25.50 --category 吃 --note 午饭
```

可选：`--type income`、`--date 2026-08-06`、`--currency USD`。

- 不给 `--date` 就记到**今天**，且"今天"由服务端按用户自己的时区判断，不受容器时区影响。用户没说日期时**不要**自己算今天填进去，直接省略 `--date` 最准确。
- 用户说"昨天"、"上周三"这类相对日期时才需要自己算出 `YYYY-MM-DD` 传给 `--date`。注意你所在容器的时区可能不是用户的时区，跨零点时容易差一天；如果不确定，先 `sumi bill add` 不带日期记一笔，从返回的 `occurred_at` 读出用户视角的今天，再据此推算。
- 不给 `--currency` 就用账户默认币种（通常 CNY）。用户没提币种时不要传。

### 一次记多笔（原子）

```bash
sumi bill add-batch --items '[
  {"amount":"12","category":"吃","note":"早饭"},
  {"amount":"30","category":"行","note":"打车","date":"2026-08-06"}
]'
```

字段名和 `bill add` 的 flag 一致：`amount` `category` 必填，`type` `note` `date` `currency` 可选。

**整批是一个事务**：任意一条不合法则整批都不写入，报错会指明是第几条，例如 `items[1]: Category "xxx" not found`。修好那一条后重发整批，不要只发剩下的。上限 1000 条，但手工记账时一次不会超过十几条——大批量数据请用下面的 CSV 导入。

用户一句话里提到多笔时优先用这个，不要循环调用 `bill add`。

### 从 CSV 导入

用户给你一个账单文件（支付宝/微信导出、银行流水、自己整理的表格）时用这个。

**不要自己读全部数据行再逐条记账。** 你只负责看懂表头、决定列映射和分类映射；`sumi bill import` 负责把每一行准确转换并**一次性原子插入**。这样你不会看错金额，也不会出现导一半的情况。

流程固定四步：

```bash
# 1. 只看前 20 行搞清结构（不要读整个文件）
head -20 账单.csv

# 2. 看看用户有哪些分类，好决定怎么映射
sumi category list

# 3. dry-run 确认映射对不对（不写任何数据）
sumi bill import --file 账单.csv \
  --skip-rows 3 \
  --map "date=交易时间,amount=金额,note=商品说明,type=收/支,category=交易分类" \
  --category-map "餐饮美食=吃,交通出行=行,休闲娱乐=娱乐" \
  --dry-run

# 4. 预览没问题再真导
sumi bill import --file 账单.csv --skip-rows 3 --map "..." --category-map "..."
```

参数说明：

- `--map` 把列指到字段上，值可以是**表头名**也可以是**第几列**（如 `amount=3`）。可用字段:`date` `amount` `category` `type` `note` `currency`。不给 `--map` 时按 `date/amount/category/type/note/currency` 这些英文表头名找。
- `--category-map` 把来源分类改写成用户已有的二级分类名。**这一步最需要你判断**——"餐饮美食"该落到"吃"、"滴滴出行"该落到"行"，这种语义映射只有你能做对。先跑 `sumi category list` 看用户实际有哪些分类，不要凭空猜一个不存在的名字。
- `--skip-rows N` 丢掉表头之前的说明行（平台导出通常有 3~4 行）。
- `--default-category` 给没有分类列的文件兜底；`--default-type` 在没有收支列时指定全是支出还是收入。
- `--skip-invalid` 跳过转换不了的行并在结果里列出来。**默认不跳**：任何一行不合法则整个文件都不导入。
- `--encoding gbk` 一般不用给，GBK 和 BOM 会自动识别。

行为要点:

- **一个文件一个事务**，上限 1000 行。超了会让你拆分文件。
- 金额为 0 的行自动跳过（平台账单用它表示不计收支的条目）。
- 负数金额会取绝对值，方向由 `type` 决定。
- 没有日期列时，该行记为**用户时区的今天**。
- `dry_run` 的输出里有 `preview`（前 5 条转换结果）和 `would_import`，拿它跟用户确认再真导。

出错时看 `invalid_rows` 或错误信息里的行号，它指的是**原始文件的行号**，可以直接让用户去看那一行。

**导入前先跟用户确认**要导入的条数和分类映射方案，尤其是文件行数多的时候。导入是批量写入，搞错了要一条条删。

### 查账单

```bash
sumi bill list                          # 最近 20 条，含支出和收入
sumi bill list --keyword 午饭            # 按备注模糊搜索
sumi bill list --category 吃 --limit 50
sumi bill list --type income
sumi bill list --from 2026-08-01 --to 2026-09-01
```

`--to` 是**开区间**（不含当天），查整个 8 月要写 `--from 2026-08-01 --to 2026-09-01`。

`--category` 接受名字。如果这个名字在支出和收入里都存在（比如 `其他`），命令会要求你加 `--type`，按用户意图补上即可。

### 改一笔

```bash
sumi bill update 42 --amount 31.00
sumi bill update 42 --note 改成晚饭 --category 行
```

只传要改的字段，没传的字段保持原值。先用 `bill list` 或 `bill get <id>` 拿到 id。

### 删一笔

```bash
sumi bill del 42
```

**删除前必须先确认。** 流程是：用 `bill list` 找到候选，把金额、日期、备注告诉用户，得到明确确认后再删。用户说"删掉那笔午饭"时不要直接猜哪条——如果 `list` 返回多条，列出来让用户选。

### 分类

```bash
sumi category list                                # 支出分类树
sumi category list --type income
sumi category add --name 宠物 --parent 非必要        # 在一级分类下建二级分类
```

`--parent` 必须是已存在的**一级**分类名。同类型下二级分类名不能重复，重名会返回 409。新建后可以立刻用 `--category 宠物` 记账。

只有在用户明确要求新分类时才建；否则把账记到语义最接近的已有分类。

### 统计

```bash
sumi stats monthly                  # 本月收入/支出/净额，按币种分组
sumi stats monthly --month 2026-07
sumi stats daily --month 2026-08    # 按天
sumi stats category                 # 按分类，默认支出
sumi stats category --type income
```

不给 `--month` 就是当前月。

## 出错时怎么办

错误信息在 stderr，多数已经包含下一步动作，照做即可：

- `Category "X" not found for this type` — 跑 `sumi category list` 看有哪些名字，选最接近的；确实需要新分类时问用户是否创建。
- `category "X" exists for both expense and income; add --type` — 补 `--type`。
- `http 403: Forbidden` — API Key 缺 scope，告诉用户，不要重试。
- `no API key: set SUMI_API_KEY, or run ...` — 没有可用凭证，见上面的前置检查；把登录命令交给用户执行。
- `not logged in` — `auth key` 系列命令需要会话，请用户先 `sumi auth login`。
- `cannot reach sumi api at ...` — 服务没起或地址不对，把地址报给用户。

不要因为报错就换一条命令硬试。先读错误信息，它通常直接说明了缺什么。

## 回话方式

用户要的是结果，不是 JSON。记完一笔就说清金额、分类、日期；查询就把关键几笔用自然语言列出来，金额带上币种。除非用户要求，不要把原始 JSON 贴给他们。
