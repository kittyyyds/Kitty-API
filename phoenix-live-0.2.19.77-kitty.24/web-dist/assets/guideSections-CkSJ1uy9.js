import{d as p,p as g}from"./vendor-misc-CSMekKki.js";const y=48,_={先照着做一遍:"quick-start",注册与登录:"account","充值：先买兑换码，再兑换余额":"recharge","充值：在线充值，或兑换余额":"recharge","API Key：给软件使用的专用密码":"api-key","检查 API Key 能不能用":"first-request",三个可用网址:"domains",五个可用网址:"domains","在 Codex 中使用本站":"codex",自动选择速度最快的网址:"speed-script","使用 goal-workflow 小助手":"goal-workflow",查看余额和使用记录:"usage",错误码含义与处理方案:"error-codes",遇到问题先看这里:"faq",保护账户和密钥:"security",保护登录信息和密钥:"security",联系管理员时要准备什么:"support",教程版本:"version"};function M(t){const n=[],s=/<!--\s*copy-command:([a-z0-9-]+)\s*-->\s*```[^\r\n]*\r?\n([\s\S]*?)\r?\n```/g;for(const r of t.matchAll(s))n.push({id:r[1],content:r[2].trim()});return n}function u(t){const n=/^##[ \t]+(.+?)[ \t]*$/m.exec(t);return n?n[1].trim().replace(/\s+/g," "):""}function f(t,n=[]){const s=new Set(n),r=t.toLowerCase().replace(/[^a-z0-9]+/g,"-").replace(/^-+|-+$/g,"").slice(0,y).replace(/-+$/,"");let e=2166136261;for(const o of t)e^=o.codePointAt(0)||0,e=Math.imul(e,16777619)>>>0;const a=r||`section-${e.toString(36)}`;if(!s.has(a))return a;for(let o=2;;o+=1){const i=`${a}-${o}`;if(!s.has(i))return i}}function B(t){const n=[],s=new Set,r=[];let e=null;const a=(o,i)=>{const d=i.replace(/\s+$/,"");if(!d.trim())return;const c=_[o]&&!s.has(_[o])?_[o]:f(o,s);s.add(c),n.push({slug:c,title:o,content:d})};for(const o of t.split(`
`)){const i=/^##[ \t]+(.+?)[ \t]*$/.exec(o);if(i){e&&a(e.title,e.lines.join(`
`)),e={title:i[1].trim().replace(/\s+/g," "),lines:[o]};continue}e?e.lines.push(o):r.push(o)}return e&&a(e.title,e.lines.join(`
`)),r.join(`
`).trim()&&(s.add("preface"),n.unshift({slug:"preface",title:u(r.join(`
`))||"前言",content:r.join(`
`).replace(/\s+$/,"")})),n}function N(t){return t.map(n=>n.content.replace(/\s+$/,"")).filter(n=>n.trim()!=="").join(`

`)}function $(t){const n=[],s=new Set,r=[];for(const e of t){if(e.content.trim()==="")continue;const a=p.parse(e.content,{gfm:!0,breaks:!1}),o=new DOMParser().parseFromString(g.sanitize(a),"text/html");let i=e.slug||"section",d=2;for(;s.has(i);)i=`${e.slug}-${d}`,d+=1;s.add(i),h(o);const c=o.body.querySelector("h2");if(c){c.id=i,n.push({id:i,label:(c.textContent||"").trim().replace(/\s+/g," ")}),r.push(o.body.innerHTML);continue}const l=o.createElement("section");l.id=i,l.innerHTML=o.body.innerHTML,r.push(l.outerHTML)}return{html:r.join(`
`),sections:n}}function h(t){t.body.querySelectorAll("a[href]").forEach(n=>{const s=n.getAttribute("href")||"";/^https?:\/\//i.test(s)&&(n.target="_blank",n.rel="noopener noreferrer")})}const m=`## 先照着做一遍

第一次使用本站，可以按下面 6 步操作：

1. 打开[注册页面](/register)，准备有效的邀请码或推广码，按提示注册并登录。已有用户直接登录即可。
2. 进入[在线充值](/purchase)，选择金额并完成付款，成功后确认站内余额已增加。
3. 如果你手里有管理员发放的余额兑换码，打开[兑换余额](/redeem)粘贴兑换，成功后确认站内余额已增加。
4. 进入[API Key 管理](/keys)，选择厂商和可用分组，创建一个 API Key。
5. 在 API Key 页面点击“使用密钥”，按所选客户端的说明复制配置。
6. 使用 Codex 后，到[用量记录](/usage)核对用量和实际费用。请求是否完成以客户端最终结果结合记录判断；首次实际模型调用建议用短问题，并预留余额。

这里先记住两个词：

- **兑换码**：管理员或活动发放的一串字符。余额兑换码需要在“兑换余额”页面成功兑换，才会增加本站余额。
- **API Key**：可以把它理解为“软件专用密码”。客户端用它识别你，必须像登录密码一样保管。

> 本教程按 2026-10-31 的站点状态核对。本站提供五个网址入口，共用用户、余额、API Key 和使用记录；不同域名的浏览器登录状态分别保存，换网址时可能需要重新登录。模型、分组和价格以你当前可见的页面及实际记录为准。
`,A=`## 注册与登录

### 注册

1. 打开[注册页面](/register)，填写邮箱和密码。
2. 当前站点要求填写有效的**邀请码或推广码**；没有可用码时，先向邀请人或管理员确认。
3. 按页面要求完成注册，再登录并进入控制台。

本教程核对时，站点开放注册，未开启注册邮箱验证。以后如果页面要求邮箱验证或显示暂停注册，应按当时的页面提示处理。不要连续提交无效的邀请码。

### 登录与找回

- 从[登录页面](/login)使用自己的邮箱和密码登录；客户端使用的 API Key 不是网页登录密码。
- 五个网址共用同一用户，换域名时可能需要重新登录，不需要重复注册。
- 当前未开启邮件自助重置密码。忘记密码时私下联系管理员核实身份，不要把旧密码或验证码发给别人。

### 保护登录信息

- 密码不要和其他网站共用。
- 不要把密码、验证码或完整 API Key 发到群聊或公开帖子。
- 使用公共电脑后退出登录。
- 发现不是自己产生的使用记录时，先停用相关 API Key，再联系管理员。
`,K=`## 充值：在线充值，或兑换余额

本站通过**站内在线充值**增加余额。如果你手里有管理员或活动发放的余额兑换码，也可以到“兑换余额”页面兑换。

### 方式一：在线充值

1. 登录本站，打开[在线充值](/purchase)。
2. 选择充值金额和付款方式，核对页面显示的付款金额与到账余额。
3. 按页面提示完成付款，付款后等待页面显示充值结果。
4. 到账后可在[我的订单](/orders)核对订单状态，并确认站内余额已增加。

站内余额和费用使用美元计价，页面显示 \`$\`。实际付款金额、币种与到账余额以充值页面显示为准，**不能把人民币付款金额直接当成站内余额数值**。

付款后长时间没有到账，不要重复付款，先到订单页核对状态，再联系管理员。

### 方式二：把兑换码换成余额

1. 打开[兑换余额](/redeem)。
2. 完整粘贴余额兑换码，检查没有误复制空格或换行。
3. 提交并查看兑换结果，确认新增金额和当前余额。
4. 成功后可在兑换记录中核对；同一兑换码不能重复领取。

兑换码是尚未使用的凭证，不要截图发给别人，也不要公开展示完整内容。本节介绍余额兑换码。其他类型的码按页面显示领取对应资源，不一定增加余额。

### 充值或兑换不成功怎么办

- **付款后没有到账**：刷新页面，到[我的订单](/orders)核对订单状态；必要时重新登录。
- **兑换码格式错误、无效或已使用**：核对完整兑换码和兑换记录，不要连续反复尝试。
- **兑换显示成功但余额没有变化**：刷新页面并核对兑换类型和记录；必要时重新登录。
- **仍有疑问**：私下联系管理员，提供订单号、付款时间和脱敏后的错误提示。不要发送密码、完整 API Key 或完整兑换码。
`,P=`## API Key：给软件使用的专用密码

Codex 等客户端需要 API Key 连接本站，不能把网页登录密码当成 API Key。

新用户注册后，系统会自动创建 \`GPT-不降智\` 和 \`GPT-福利\` 两个 Key，分别绑定同名分组，可在 [API Key 管理](/keys) 中选择使用。\`GPT-不降智\` 不支持直接调用 Luna；两个 Key 共用你的用户余额，具体价格和限额以各分组设置为准。你也可以按下面的步骤创建其他 Key。

### 创建 API Key

1. 打开[API Key 管理](/keys)，点击“创建密钥”。
2. 填写便于识别的名称，例如“我的电脑”。
3. 选择厂商，再选择该厂商下可用的分组。分组会影响可用模型、价格和限额，创建时需要选定。
4. 按需设置额度和有效期；测试用 Key 建议设置额度。
5. 创建后复制 Key；也可以在列表中使用复制按钮，或打开“使用密钥”查看客户端配置。

列表会遮住部分 Key，但仍可复制完整值，因此要保护浏览器登录状态。不要因为列表显示遮罩就公开截图包含密钥的配置窗口。

### 怎么保管

- 每台电脑或每个软件单独创建一个 API Key，出问题时便于只停用对应的 Key。
- 不要把 API Key 写进公开代码、帖子、截图或聊天记录。
- 不共享、出租或转卖网站登录身份和 Key。
- 不再使用的 Key 应停用或删除；修改额度与有效期前先核对使用记录。
- 经常查看[用量记录](/usage)，发现陌生请求时立即停用相关 Key。

Key 自身额度、用户余额、订阅及分组限制会分别影响请求能否执行。换 Key 不能绕过用户或分组对你的限制。
`,I='## 检查 API Key 能不能用\n\n下面的命令请求模型列表，用于检查服务地址、Key 验证和当前列表访问条件。**返回模型列表不等于已经完成实际模型调用，也不保证列表中的每个模型此刻都能成功使用。**\n\n以 `key66.cc.cd` 为例，把 `<你的 API Key>` 替换为自己的 Key，尖括号也要一起删掉。不要把带真实 Key 的命令或配置发给别人。\n\n### macOS、Linux 或 WSL\n\n```bash\nexport SUB2API_API_KEY="<你的 API Key>"\ncurl --silent --show-error --include https://key66.cc.cd/v1/models \\\n  -H "Authorization: Bearer $SUB2API_API_KEY"\n```\n\n### Windows PowerShell\n\n```powershell\n$env:SUB2API_API_KEY = "<你的 API Key>"\ncurl.exe --silent --show-error --include https://key66.cc.cd/v1/models `\n  -H "Authorization: Bearer $env:SUB2API_API_KEY"\n```\n\n`--include` 会显示 HTTP 状态。请同时检查响应正文：\n\n- `200` 且正文为模型列表：这次列表请求成功。\n- `401`：通常是 Key 缺失、无效或被停用。\n- `403`：可能是 Key 过期、余额不足、用户/IP/订阅等条件不允许；以具体提示为准。\n- `404`：检查服务地址和请求路径，本例的完整路径是 `/v1/models`。\n- `429`：可能是额度耗尽或请求过于频繁，不能只靠一直重试解决。\n\n模型列表请求也可能检查余额、有效期或额度。完整处理方式见[错误码含义与处理方案](#error-codes)。需要验证实际模型调用时，选择这个 Key 可用的模型发送一个短问题，再查看[用量记录](/usage)；实际调用可能产生费用。\n',E="## 五个可用网址\n\n| 网址入口 | Codex/Responses 使用的服务地址 |\n| --- | --- |\n| `mofa.love.gd` | `https://mofa.love.gd/v1` |\n| `mofayaoshipu.cc.cd` | `https://mofayaoshipu.cc.cd/v1` |\n| `key66.cc.cd` | `https://key66.cc.cd/v1` |\n| `key66.vip` | `https://key66.vip/v1` |\n| `mofa.duoduohuiying.cn` | `https://mofa.duoduohuiying.cn/v1` |\n\n“服务地址”也常被软件写成 `Base URL` 或 `base_url`，表示软件要连接的 API 入口。其他客户端按“使用密钥”对应页签填写；例如 Claude Code 的模板使用根地址，不能机械套用本表的 `/v1`。\n\n- 这五个入口在 2026-10-31 核对时均能返回本站健康状态和相同的教程内容；不同网络下的可达性和延迟可能不同。\n- 五个入口共用同一套用户、余额、API Key 和使用记录；浏览器登录状态按域名分别保存，换网址可能需要重新登录。\n- 更换 API 入口时通常可以继续使用原 Key；修改客户端实际生效的服务地址，并检查是否还有单独配置的模型目录地址。\n- 从任意入口打开 `/guide`，教程里的站内链接会留在当前域名。\n- 能打开网页或健康检查成功，不代表某个模型一定可用，也不保证生成速度。\n",b=`## 在 Codex 中使用本站

### 推荐方法：直接复制配置

1. 打开[API Key 管理](/keys)，找到要使用的 Key，点击“使用密钥”。
2. 选择 Codex、自己的操作系统和页面提供的认证模式。
3. 先备份已有配置，再按页面说明处理**所有列出的文件和终端设置**。兼容模式可能同时提供 \`config.toml\` 与 \`auth.json\`；其他认证模式不同，不要混用。
4. 如果选择本地模型目录，按页面说明下载目录文件并放到配置指定的位置；远程目录则使用页面提供的目录地址。
5. 检查模型名属于这个 Key 当前可用的模型目录。页面生成的推荐模型随分组和目录改变，没有对所有用户通用的固定模型。
6. 按页面要求重新启动 Codex。使用 API Key Mode 时，需要完全退出并重启，再新建任务。发送一个短问题后，核对[用量记录](/usage)。

复制内容中可能包含完整 Key 或认证信息，只保存在自己的电脑上。合并配置时保留适合自己的权限设置，不需要把关闭沙箱或关闭审批作为接入本站的前提。

### 手工填写配置

下面是**从终端启动 Codex CLI**的最小接入示例。图形客户端的配置位置及环境变量读取方式应以其说明为准。

用户级配置默认位置：

- Windows：\`%USERPROFILE%\\.codex\\config.toml\`。
- macOS/Linux：\`~/.codex/config.toml\`。
- 设置过 \`CODEX_HOME\`、使用 profile 或命令行覆盖时，先确认真正生效的用户级配置；不要把凭据放进项目仓库的配置。

先备份再合并，不要覆盖整份现有配置。下面的顶层 \`model_provider\`、\`model\` 放在第一个 \`[表]\` 之前，已有同名项和表不要重复添加。把模型占位符替换为**当前 Key 可用的真实模型 ID**：

\`\`\`toml
model_provider = "sub2api"
model = "<当前 API Key 可用的模型 ID>"

[model_providers.sub2api]
name = "Sub2API"
base_url = "https://key66.cc.cd/v1"
env_key = "SUB2API_API_KEY"
wire_api = "responses"
requires_openai_auth = false
supports_websockets = false
\`\`\`

- \`model_provider\` 必须与下面 \`model_providers.sub2api\` 的名称对应，它不是本站 API Key 的分组名称。
- 此例的 \`base_url\` 用于 Responses 接口，结尾包含 \`/v1\`；其他客户端按“使用密钥”对应页签填写。
- \`env_key\` 填环境变量的名称，不填 Key 本身。\`SUB2API_API_KEY\` 是本示例自定义名称，并非 Codex 内置必填名称。
- 此例使用 HTTP/SSE；页面生成的其他接入模式可能不同，不要只替换个别认证字段后混用。

在准备启动 Codex 的 PowerShell 窗口中：

\`\`\`powershell
$env:SUB2API_API_KEY = "<你的 API Key>"
codex
\`\`\`

macOS/Linux 终端中：

\`\`\`bash
export SUB2API_API_KEY="<你的 API Key>"
codex
\`\`\`

环境变量对当前终端及从它启动的 Codex 生效，新开终端需要重新设置。终端历史和配置备份可能留下敏感内容，不要公开分享。

配置字段与覆盖规则参见 [Codex 配置参考](https://learn.chatgpt.com/docs/config-file/config-reference)和[高级配置说明](https://learn.chatgpt.com/docs/config-file/config-advanced)。
`,w=`## 自动选择速度最快的网址

这个工具只适用于 **Windows 默认用户配置**。它比较本站入口的 \`/health\` 健康检查延迟，选择本次合格结果中延迟最低的域名。**它不测试模型生成速度、Token 吞吐或实际模型调用是否成功。**

下载：[自动测速 BAT](/downloads/select-fastest-codex-base-url.bat) · [SHA-256 校验文件](/downloads/select-fastest-codex-base-url.bat.sha256)

SHA-256（可以理解为文件的“指纹”）：\`6cd9e48586f8b766cf3549d420b293bc0da7c875c3c50ac5b03ef2a0e6f7d4f4\`

### 运行前先确认

1. 已成功配置过 Codex，且希望修改的是运行用户的 \`%USERPROFILE%\\.codex\\config.toml\`。
2. 文件有唯一的顶层 \`model_provider\`，对应服务配置表中有唯一的 \`base_url\`。
3. 没有依赖 \`CODEX_HOME\`、WSL 中的配置、独立 profile 或命令行参数来选择另一份生效配置。脚本不会识别这些覆盖，可能改到没有生效的基础配置。
4. 关闭 Codex 和可能修改配置的编辑器，再下载 BAT 与校验文件，放在同一文件夹。
5. 核对指纹后，右键 BAT，选择“以管理员身份运行”，并确认仍使用自己的 Windows 用户。

工具需要管理员权限，因为会修改 Windows 的 \`hosts\` 文件。可以把 hosts 理解成电脑自己的“网址通讯录”。如果实际配置不在上述路径，或脚本提示 TOML 格式不支持，请手工核对配置，不要删除已有设置来迁就脚本。

### 建议先核对文件指纹

在下载文件所在文件夹打开 PowerShell：

\`\`\`powershell
(Get-FileHash .\\select-fastest-codex-base-url.bat -Algorithm SHA256).Hash.ToLower()
Get-Content .\\select-fastest-codex-base-url.bat.sha256
\`\`\`

第一行输出的哈希应等于校验文件开头的哈希，也应与本节一致。不一致时不要运行。脚本不会自动读取这个校验文件，上述检查需要自己完成。

### 工具会做什么

1. 读取并备份完整的 Codex 配置和 Windows hosts 文件。
2. 写入或更新 \`15.204.82.11 mofa.love.gd\` 的 hosts 记录并刷新 DNS；成功后这条记录保留，即使最终选中了其他域名。
3. 测试下面五个网址：
   - \`https://mofa.love.gd\`
   - \`https://key66.vip\`
   - \`https://mofayaoshipu.cc.cd\`
   - \`https://key66.cc.cd\`
   - \`https://mofa.duoduohuiying.cn\`
4. 每个域名先预热一次，再正式测三次；正式三次均成功才参与选择，按三次耗时的中位数排序。
5. 只会更换目标 Codex 配置中 \`base_url\` 的网址域名，保留其协议、端口、路径和其他部分；不会同步修改单独的 \`model_catalog_url\`。
6. 检查写入后的文件和目标域名；这一步不会启动 Codex 或发送模型请求。

### 配置和密钥的处理

工具不要求你输入 Key，也不使用 Key 做健康检查，但它会读取并备份**整个配置文件**。若原配置含有 Key，备份也会含有 Key，必须像原配置一样保管。

工具不会创建或切换 provider，不会修改模型名称或主动删除旧备份。它支持的是有限的简单 TOML 结构，复杂转义、多行字符串或其他配置形式可能被拒绝。

### 怎么看运行结果

输出会列出每个域名的结果、最终选择和两个备份位置。备份位于原文件所在目录，文件名含 \`.codex-speed.\`、时间和 \`.bak\`。

如果五个域名都不合格，Codex 服务地址不会被改写，但脚本此前可能已经调整 hosts，随后会尝试恢复本次改动。**自动恢复可能失败**，尤其是文件已被其他程序修改时；看到 \`Automatic rollback was incomplete\` 要保留备份并核对结果。

### 想恢复原来的设置

1. 找到本次输出对应的配置和 hosts 两个备份，关闭 Codex。
2. 确认运行后没有需要保留的新修改；有疑问时先联系管理员，不要直接覆盖。
3. 使用管理员 PowerShell 将本次备份复制回对应原路径。
4. 执行 \`ipconfig /flushdns\`，再重新打开 Codex，核对实际生效的入口。

不要发送备份正文、完整配置或认证文件给别人；排查时只提供脱敏后的错误、文件名和操作时间。
`,x=`## 使用 goal-workflow 小助手

\`goal-workflow\` 是 Codex 的一个小助手（称为 \`skill\`）。它先读取任务上下文，通过简短问题整理目标、范围、风险与验收方法，再让你决定是否保存并执行。

### 安装

在 **Codex 对话中**输入下面的请求；它不是 PowerShell 命令。也可以点击页面的复制按钮：

<!-- copy-command:skill-install -->
\`\`\`text
安装 skill https://github.com/zuchengchen/goal-workflow/tree/master/skills/goal-workflow
\`\`\`

保留完整仓库路径。安装后检查 Codex 报告的安装位置和验证结果，再输入 \`$goal-workflow\` 确认识别成功；未识别时重新打开 Codex，并检查是否有重复安装。

### 更新

同样在 Codex 对话中输入：

<!-- copy-command:skill-update -->
\`\`\`text
安全更新 skill https://github.com/zuchengchen/goal-workflow/tree/master/skills/goal-workflow：先备份现有 goal-workflow，再重新安装并验证；失败时恢复备份
\`\`\`

这是一项带备份要求的更新请求，不代表 skill 自带保证成功的自动更新器。检查实际安装位置、备份位置及验证结果后再使用新版本。

### 如果提示 Goal 功能未开启

先确认当前客户端支持 Goal，可以查看其 \`/goal\` 帮助或[官方 Goals 说明](https://developers.openai.com/cookbook/examples/codex/using_goals_in_codex)。配置开关不能为不支持此功能的旧客户端增加功能。

对于已支持、但功能被关闭的客户端，在实际生效的用户配置中合并：

\`\`\`toml
[features]
goals = true
\`\`\`

已有 \`[features]\` 时只调整它下面的 \`goals\`，不要重复建表。按客户端要求重新加载配置。字段说明见 [Codex 配置参考](https://learn.chatgpt.com/docs/config-file/config-reference)。

### 使用例子

<!-- copy-command:goal-example -->
\`\`\`text
$goal-workflow 清理内存
\`\`\`

按它的问题说明需求。规划阶段可以只读检查环境，但不会实施目标任务。最后展示完整目标和绝对保存路径，由你一次选择：

- **保存并开始**：保存、精确回读验证后启动；不需要再重复批准开始。
- **仅保存**：保存目标文件，暂不执行。
- **修改或取消**：先调整草案或结束流程。

没有创建 Goal 的工具时，skill 可能提供 \`/goal ...\` 交接命令；只有确认 Goal 真正启动后才执行任务，不能把显示一条命令当作已经启动。具体行为以[维护者的 skill 文件](https://github.com/zuchengchen/goal-workflow/tree/master/skills/goal-workflow)为准。
`,T=`## 查看余额和使用记录

- **查看余额**：登录后在[控制台](/dashboard)查看。页面以 \`$\` 显示美元计价的站内余额。
- **查看某个 API Key**：打开[API Key 用量查询](/key-usage)，输入自己的 Key 查询；不要在公共电脑或不可信网址中填写。
- **查看用量记录**：打开[用量记录](/usage)，按日期、模型和 API Key 核对请求、用量和实际费用；不要把标准费用当作最终扣费。请求是否完成还需结合客户端最终结果判断。
- **查看可用模型**：打开[模型广场](/model-plaza)，按服务商和分组查看模型及价格；公开展示不等于你当前 Key 拥有全部分组权限。

\`Token\` 是模型处理输入、输出时使用的计量单位。费用还可能受缓存、模型价格、分组或用户倍率、高峰因子，以及图片/视频等独立规则影响。不要只按请求次数或一个基础倍率估算最终费用。

第一次配置或更换入口后，可以先检查模型列表，再用短问题验证实际调用。超时、关闭窗口或取消请求时，上游可能已经处理了部分内容，仍可能有费用；重试前先查看用量记录。

同一用户的多个 Key 共用钱包余额，Key 查询中的钱包余额不是每个 Key 各有一份。余额充足不等于所有请求都可执行，Key 额度、有效期、用户限流、分组权限和订阅限制也需要满足。
`,k='## 错误码含义与处理方案\n\n先同时查看 **HTTP 状态、错误文字和请求是否真正完成**。不同入口的错误格式不完全相同：\n\n```json\n{\n  "code": "API_KEY_EXPIRED",\n  "message": "API key 已过期"\n}\n```\n\n上面是站点验证阶段可能返回的格式。进入网关后，客户端也可能收到下面这种格式：\n\n```json\n{\n  "error": {\n    "type": "rate_limit_exceeded",\n    "message": "请求过于频繁，请稍后重试"\n  }\n}\n```\n\n这两段用于说明结构，不代表每个客户端都会显示相同字段或原样文字。大写代号可能只出现在某些入口或记录里；不能仅凭没看到代号就排除该原因。\n\n### 先看三位数\n\n| 状态码 | 常见含义 | 先做什么 |\n| --- | --- | --- |\n| `200` | HTTP 请求已被接受或成功返回 | 检查正文；流式请求还要看最终完成或失败事件 |\n| `400` | 请求内容、参数或认证方式有误 | 核对模型、参数；不要把 Key 放到网址查询参数中 |\n| `401` | Key 缺失、无效、停用，或用户被停用 | 核对 Key 和用户状态，避免反复提交错误凭据 |\n| `403` | Key 过期、余额不足、IP/分组权限或订阅条件不满足等 | 按具体错误检查[控制台](/dashboard)、Key 状态及分组 |\n| `404` | 地址、模型或资源不存在 | 核对完整请求路径和模型名，不能一律靠添加 `/v1` 解决 |\n| `409` | 当前操作与现有状态冲突，例如兑换码已经使用 | 先检查记录和当前状态，不要盲目重复提交 |\n| `413` | 请求内容太大 | 缩短内容或减少一次发送的文件 |\n| `429` | 请求频率、并发或消费额度受限 | 区分短期限流与额度耗尽，见下表 |\n| `499` | 客户端断开、取消或网络中断等 | 先检查结果和用量；已处理的请求仍可能计费 |\n| `500` | 服务内部异常 | 稍后有限次数重试，持续出现则联系管理员 |\n| `502` | 网关或上游请求未成功 | 查看具体错误，不能据此断定本站其他部分一定正常 |\n| `503` | 资源或计费等服务暂不可用 | 等待并检查错误文字，避免连续快速请求 |\n| `504` | 等待上游超时 | 先核对已有结果和用量，再决定是否重试 |\n\n超时、取消或 HTTP 200 的流中出现失败事件，都不能单独证明“没有产生费用”。\n\n### 再看具体原因代号\n\n下列代号用于帮助定位；部分网关会把它们转换为小写分类和错误文字，不一定原样返回。\n\n| 代号或场景 | 含义与处理 |\n| --- | --- |\n| `INSUFFICIENT_BALANCE` | 钱包余额不足，常见为 403；核对余额后按需购买余额兑换码 |\n| `API_KEY_EXPIRED` | Key 已过期，403；在 Key 管理页处理有效期或创建新 Key |\n| `API_KEY_QUOTA_EXHAUSTED` / `insufficient_quota` | Key 总额度用尽，429；Responses 入口可能使用后者作为 type/code，等待下一分钟不会自动补足总额度 |\n| `API_KEY_RATE_5H_EXCEEDED` | Key 的 5 小时消费窗口额度用尽，429 |\n| `API_KEY_RATE_1D_EXCEEDED` | Key 的 24 小时消费窗口额度用尽，429 |\n| `API_KEY_RATE_7D_EXCEEDED` | Key 的 7 天消费窗口额度用尽，429 |\n| `USER_RPM_EXCEEDED` | 用户每分钟请求上限，429；减少并发和频率 |\n| `GROUP_RPM_EXCEEDED` | 当前用户在该分组内的每分钟请求上限，429；不是整个分组是否繁忙的判断 |\n| `USER_PLATFORM_DAILY_QUOTA_EXHAUSTED` | 当前用户在该平台的日额度用尽，429 |\n| `USER_PLATFORM_WEEKLY_QUOTA_EXHAUSTED` | 当前用户在该平台的周额度用尽，429 |\n| `USER_PLATFORM_MONTHLY_QUOTA_EXHAUSTED` | 当前用户在该平台的 30 天窗口额度用尽，429，不一定是自然月 |\n| `SUBSCRIPTION_NOT_FOUND` / `SUBSCRIPTION_INVALID` | 订阅不存在、无效或已过期等，403；检查所选分组和订阅状态 |\n| `USAGE_LIMIT_EXCEEDED` | 订阅的用量限额已达到，可能返回 429；按对应窗口处理 |\n| `BILLING_SERVICE_ERROR` / `billing_service_error` | 计费服务暂不可用，网关可能返回 503 和小写类型；稍后重试 |\n\nKey 的 5 小时、24 小时、7 天窗口应以 Key 页面或用量查询显示的重置倒计时为准，不能保证按你的本地零点重置。用户平台日/周额度按服务端时区计算，当前为 `Asia/Shanghai`；日窗口到次日零点、周窗口到下周一零点，月额度使用 30 天窗口。订阅另有自己的窗口，不能混用这些时间规则。\n\n换 Key 不会消除用户、分组对该用户或平台级别的限制。给钱包充值也不等于重置 Key 总额度、窗口额度或订阅限额。\n\n### 最后看分类名\n\n| `error.type` 分类 | 常见处理方向 |\n| --- | --- |\n| `authentication_error` | 检查 Key 与用户状态 |\n| `permission_error` | 检查 IP、分组和模型权限 |\n| `billing_error` | 检查余额或计费资格 |\n| `invalid_request_error` | 检查请求参数、模型及内容大小 |\n| `not_found_error` | 检查地址和资源是否存在 |\n| `insufficient_quota` | 检查 Key 总额度等消费限制 |\n| `rate_limit_exceeded` / `rate_limit_error` | 结合文字区分频率、并发、排队或消费窗口限额 |\n| `upstream_error` / `provider_error` | 查看上游失败原因，有限次数重试 |\n| `api_error` / `server_error` | 服务错误持续出现时联系管理员 |\n| `overloaded_error` | 降低并发并等待资源恢复 |\n| `billing_service_error` | 等待计费服务恢复 |\n\n### 关于自动重试\n\n如果响应带 `Retry-After`，至少等待指定时间；本站相关限流响应使用秒数。没有提示时逐步延长间隔，并限制重试次数。不同客户端是否自动遵循该提示，需要检查其实际行为。\n\n余额、权限、过期或总额度问题先处理原因，连续重发不会解决。中断或超时后先查看已有结果与[用量记录](/usage)，再决定是否重试，避免重复任务和费用。\n',S=`## 遇到问题先看这里

### 显示 401 或 403

401 通常需要检查 Key 是否完整、有效、启用，以及用户是否被停用。Key 过期通常是 403；403 也可能是余额、IP、权限或订阅问题。先读完整提示，详见[错误码含义与处理方案](#error-codes)。

### 提示余额不足或额度已用完

先分清钱包余额、Key 总额度、Key 窗口额度、平台额度或订阅限额。钱包余额不足时到[在线充值](/purchase)充值；其他限制到对应页面核对。充值或换 Key 不能解决所有限额问题。

### Codex 提示找不到 provider

\`provider\` 是 Codex 的接入服务配置名称，不是本站的 API Key 分组。检查顶层 \`model_provider\` 与 \`[model_providers.<名称>]\` 是否对应，以及是否用了其他 profile。需要重新复制时，先备份，再按“使用密钥”页面合并配置，不要直接覆盖全部设置。

### 某个网址打不开

尝试[五个网址](#domains)中的另一个，并核对客户端实际生效的服务地址。它们共用用户、余额和 Key，但换域名后浏览器可能需要重新登录。网页能打开不代表模型调用必定成功。

### goal-workflow 安装后不能用

确认安装的是完整的 \`tree/master/skills/goal-workflow\` 路径，检查安装位置和是否有重复版本。未识别时重新打开 Codex；能识别 skill 却不能启动 Goal 时，按[Goal 功能说明](#goal-workflow)检查客户端支持情况。

### 测速工具显示全部失败

检查每个域名的错误：网络、DNS、证书、服务状态或某次正式测试失败都可能导致不合格。脚本要求健康检查正式三次全部成功，浏览器首页能打开不能替代这个条件。同时确认是否报告自动恢复不完整，并保留备份。

### 测速工具提示“配置不支持”或改完没有生效

核对默认用户配置路径、顶层 provider、复杂 TOML，以及是否用了 \`CODEX_HOME\`、WSL、profile 或命令行覆盖。脚本不会识别这些生效配置，也不会同步修改单独的模型目录地址。不要为运行脚本删除现有设置，必要时手工修改正确的入口。
`,v=`## 保护登录信息和密钥

- 示例中的 \`<你的 API Key>\` 只是占位文字，使用时换成自己的 Key，不要把真实值发给别人。
- 不把 Key 放在网址查询参数里，本站会拒绝这种认证方式；只在正确的本站页面或自己的客户端中填写。
- 客户端配置、\`auth.json\`、环境变量、终端历史和配置备份都可能包含密钥或登录信息，截图、分享文件前先检查。
- 下载 BAT 后核对 SHA-256；不要运行群聊或陌生网站提供的修改版。
- 测速工具会读取并备份整个 Codex 配置文件，备份可能包含原有凭据。只在自己的电脑上保管，不要上传。
- 不要把关闭沙箱、关闭审批或管理员权限当作使用模型的通用要求。测速 BAT 因修改 Windows hosts 需要管理员权限，普通 API 配置不以此为前提。
- 联系管理员时，不发送密码、完整 API Key、验证码、Cookie 或配置备份正文。
`,C=`## 联系管理员时要准备什么

使用网站当前显示的联系方式，并准备：

- 问题发生的大概时间和时区。
- 当时使用的网址、客户端及版本。
- 完整错误文字、HTTP 状态和请求 ID（如响应中有 \`X-Request-ID\`）。
- 使用的模型和分组，以及是否刚修改过配置。
- 兑换问题对应的订单号、购买时间和脱敏后的兑换记录。
- 测速问题对应的结果、备份文件名，以及是否提示恢复失败。

截图或复制前遮住密码、完整 API Key、完整兑换码和个人信息。不要提交完整 Authorization 请求头、Cookie、\`auth.json\`、配置或备份正文；提供文件名通常就足够定位下一步。
`,O=`## 教程版本

- 教程版本：\`1.8\`
- 更新日期：\`2026-10-31\`
- 事实核对基线：站点版本 \`0.2.19.10\` 的行为及本次发布前配置；本次教程更新不改变业务规则
- 适用对象：第一次使用本站、API Key 或 Codex 的用户
- Windows 测速工具：\`select-fastest-codex-base-url.bat\`
- 工具 SHA-256：\`6cd9e48586f8b766cf3549d420b293bc0da7c875c3c50ac5b03ef2a0e6f7d4f4\`

页面顶部的“在线第 N 版”是线上教程修订号，与这里的教程内容版本不是同一个编号。

站点配置、商店商品、客户端版本和模型可用性会变化。遇到不一致时，先核对当前页面、Key 权限及错误提示，再联系管理员；本教程不承诺固定商店价格、兑换比例或永久模型可用性。
`,R='# OpenAI OAuth Bad Request 排查与恢复\n\nOpenAI OAuth 的 Responses HTTP 请求可能在启用 zstd 请求压缩时收到只有\n`Bad Request` 的 HTTP 400。当前默认发送原始 JSON；此设置同样适用于\nChat/Messages 转换为 Responses 和 WebSocket 回退 HTTP 的路径。\nHTTP 400 也可能来自上下文超限、模型或参数不支持，因此不能仅凭错误码归因。\n\n## 请求压缩\n\n```yaml\ngateway:\n  openai_request_compression_enabled: false\n```\n\n对应环境变量为 `GATEWAY_OPENAI_REQUEST_COMPRESSION_ENABLED=false`。\n默认值为 `false`；无需关闭 `gateway.openai_ws.exact_fidelity_enabled`。\n身份头、TLS、WebSocket 和请求 JSON 内容仍按原有流程处理。配置需要运行包含\n该开关的版本并重启服务后生效。将值改回 `true` 可恢复原有压缩行为；显式开启时\n仍须满足原有 OpenAI OAuth/Responses/exact-fidelity 条件。\n\n关闭压缩会增加上传流量，较大请求可能增加上传耗时。观察实际 HTTP 编码、\n请求长度、最终失败率、首字节耗时和超时率。请求字节大小不等于模型 token 数。\n不要直接删除会话历史，也不要将全部 400 设置为自动重试或切换账号。\n\n通用 400 的结构诊断新增 `http_request_compression_policy`，分别记录\n`disabled`（已关闭）、`zstd`（实际执行压缩）、`ineligible`（不属于压缩适用路径）、\n`existing_encoding`（保留请求已有编码）。缺少此次传输标记时为 `unknown`，不会\n依据历史请求推断。结合 `http_content_encoding`、`http_request_content_length`\n和 `http_protocol` 查看当前失败尝试；诊断只记录固定分类和计数，不保存正文或凭据。\n\n## `gpt-6-astra` 的 `none` 推理参数\n\n上游可能拒绝 `gpt-6-astra` 的 `reasoning.effort=none`。Claude Code 使用的\nMessages 协议中，显式 `thinking.type=disabled` 会转换为 `none`，并优先于\n`output_config.effort`。修复后的兼容入口会对这种显式关闭请求应用已配置的分组\n推理策略，同时保留请求强度 `none` 和实际发送强度 `low` 的分别记录。\n\n只在已经确认受影响、允许该兼容行为的分组中追加以下规则：\n\n```json\n{\n  "from": "none",\n  "to": "low",\n  "match_type": "exact",\n  "model": "gpt-6-astra"\n}\n```\n\n`low` 会启用推理，可能增加耗时和用量。确实要求关闭推理的调用，应选择支持该\n能力的模型。该规则精确匹配转换后发给 OpenAI 的 `model`；若账号模型映射把别名\n解析为 `gpt-6-astra`，也会命中。其他最终模型和已合法的强度保持原有策略；未指定\n强度的 Messages 请求继续使用兼容层默认值。没有该规则时，不会自动将 `none`\n升为 `low`。\n\n管理界面可以编辑分组的推理强度映射，或使用以下管理 API：\n\n1. 根据错误记录中的 `group_id` 确认实际分组和 API Key 当前绑定关系；确认平台\n   是 OpenAI，或复合分组的此次请求实际路由为 OpenAI。不要对所有分组批量修改。\n2. `GET /api/v1/admin/groups/:id`，保存在本机的原始 `reasoning_effort_mappings`，\n   并核对已有精确匹配、前缀、后缀和全局规则。已有同一精确规则时不重复追加；\n   存在不同目标的同一规则时先明确最终策略。\n3. `PUT /api/v1/admin/groups/:id`，仅发送 `reasoning_effort_mappings` 字段。\n   该字段是**整数组替换**，必须在原数组上追加规则；不要用单条规则覆盖其他映射。\n   空数组会清空全部映射，省略该字段则保持原样。写入前再次读取并确认旧规则未被\n   其他管理员更改；此接口没有提供条件更新保证。\n4. 再次 GET 核对映射及其他配置。通过管理 API 更新会使该分组的认证缓存失效，\n   不要直接修改数据库后假设缓存已同步。\n5. 用合成请求验证 `thinking.type=disabled`、同带 `output_config.effort=high`、\n   合法 `high`、未指定强度、其他模型等场景，核对请求与实际推理强度和最终状态。\n\n对于仅带 `thinking.type=disabled` 的请求，需要包含入口绑定修复的新版本；旧版本\n只在存在非空 `output_config.effort` 时绑定策略，单独保存映射不能完成这类修复。\n\n回滚映射时，读取最新规则，仅移除本次新增且内容仍一致的精确规则，保留后来新增的\n其他规则；如果期间没有其他更改，可以通过同一 PUT 接口恢复备份数组。再次 GET\n验证，并使用受影响客户端的合成请求核对结果。\n\n## 验证与发布\n\n本地回归覆盖压缩开关及配置读取、完整转换转发、HTTP 重放长度、明确关闭推理的\n策略绑定、原始强度记录、模型匹配边界和既有模型校验。真实上游对照必须使用合成\n内容，比较同账号、同模型、同 JSON 在压缩与原文传输下的最终结果；只看到 HTTP\n200 还需检查流式终态。\n\n发布遵循仓库 `AGENTS.md`：测试通过后合入 `dev-czc`、`main-czc`，提升 VERSION，\n创建不可变 `czc-*` 标签及 origin GitHub Release，从标签重建前端和 embed 服务端。\n备份生产程序与修改的配置，验收首页、健康检查、版本及 `current_release_url`。\n压缩开关、分组映射和二进制版本分别核对与回滚，避免把配置变更误报成已部署代码。\n',H=Object.assign({"../../../docs/guide/010-quick-start.md":m,"../../../docs/guide/020-account.md":A,"../../../docs/guide/030-recharge.md":K,"../../../docs/guide/040-api-key.md":P,"../../../docs/guide/050-first-request.md":I,"../../../docs/guide/060-domains.md":E,"../../../docs/guide/070-codex.md":b,"../../../docs/guide/080-speed-script.md":w,"../../../docs/guide/090-goal-workflow.md":x,"../../../docs/guide/110-usage.md":T,"../../../docs/guide/120-error-codes.md":k,"../../../docs/guide/130-faq.md":S,"../../../docs/guide/140-security.md":v,"../../../docs/guide/150-support.md":C,"../../../docs/guide/160-version.md":O,"../../../docs/guide/openai-bad-request-recovery.md":R}),D=/(\d+)-([a-z0-9]+(?:-[a-z0-9]+)*)\.md$/;function L(){const t=[];for(const[n,s]of Object.entries(H)){const r=D.exec(n);if(!r)continue;const e=s.replace(/\s+$/,"");e.trim()&&t.push({order:Number(r[1]),chapter:{slug:r[2],title:u(e),content:e}})}return t.sort((n,s)=>n.order-s.order||n.chapter.slug.localeCompare(s.chapter.slug)).map(n=>n.chapter)}const U=L();function q(){return U.map(t=>({...t}))}export{$ as b,f as d,M as e,q as g,N as j,u as r,B as s};
