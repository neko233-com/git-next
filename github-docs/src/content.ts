/**
 * 文档的全部文字与命令都集中在这里。
 * 页面组件只负责显示，不在别处散落文案。
 */

export type StepItem = {
  text: string;
  command?: string;
  note?: string;
};

export type FaqItem = {
  question: string;
  answer: string;
  command?: string;
};

export type CalloutTone = "info" | "warn" | "danger";

export type Block =
  | { kind: "heading"; anchor: string; text: string }
  | { kind: "paragraph"; text: string }
  | { kind: "steps"; items: StepItem[] }
  | { kind: "code"; code: string; caption?: string }
  | { kind: "callout"; tone: CalloutTone; title: string; text: string }
  | { kind: "list"; items: string[]; ordered?: boolean }
  | { kind: "checklist"; items: string[] }
  | { kind: "faq"; items: FaqItem[] }
  | { kind: "cheatsheet" }
  | { kind: "unsupported" };

export type Section = {
  id: string;
  group: string;
  nav: string;
  title: string;
  kicker: string;
  summary: string;
  keywords: string[];
  blocks: Block[];
};

export type CheatRow = {
  command: string;
  when: string;
  note: string;
};

export type UnsupportedRow = {
  command: string;
  why: string;
  instead: string;
};

/** 常用命令速查表。测试会检查这里不含当前实现不支持的命令。 */
export const commandCheatSheet: CheatRow[] = [
  {
    command: "git status",
    when: "看看改了哪些文件、现在在哪个版本",
    note: "拿不准时先运行它，没有副作用",
  },
  {
    command: 'git add "文件名"',
    when: "把指定的文件放进待保存清单",
    note: "路径里有空格要加英文双引号",
  },
  {
    command: "git add -A",
    when: "把当前文件夹里所有改动放进清单",
    note: "先用 git status 确认没有多余文件",
  },
  {
    command: 'git commit -m "说明"',
    when: "把清单里的改动保存成一条记录",
    note: "说明写清楚改了什么，别只写“更新”",
  },
  {
    command: "git switch main",
    when: "切回正式版本（一般叫 main）",
    note: "切换前先保存手上的修改",
  },
  {
    command: "git switch -c 分支名",
    when: "新建并切到一个平行版本，用来试改",
    note: "分支名用英文，好认即可",
  },
  {
    command: "git clone 仓库地址",
    when: "第一次把团队项目复制到本机",
    note: "一台电脑上同一个项目只需要做一次",
  },
  {
    command: "git pull --ff-only",
    when: "拿同事的最新版本到本机",
    note: "当前实现需要 --ff-only；自己有提交时会拒绝",
  },
  {
    command: "git push",
    when: "把保存好的记录发给团队",
    note: "第一次推送用 git push -u origin main",
  },
  {
    command: "git log --oneline",
    when: "看看已经保存了哪些记录",
    note: "每行一条，最上面是最新的一条",
  },
  {
    command: 'git restore "文件名"',
    when: "把还没保存的改动撤掉",
    note: "会丢掉改动，运行前确认文件名",
  },
  {
    command: 'git restore --source HEAD~1 "文件名"',
    when: "从上一版把误删的文件拿回来",
    note: "拿回来后要再保存一次",
  },
];

/** 网上常见、但当前实现没有的命令，以及对应的替代做法。 */
export const unsupportedCommands: UnsupportedRow[] = [
  {
    command: "git commit -a",
    why: "当前实现没有这条命令，直接输入会报错。",
    instead: '先运行 git add -A，再运行 git commit -m "说明"。',
  },
  {
    command: "git rev-parse --abbrev-ref",
    why: "当前实现没有这个写法，不能靠它取当前版本名。",
    instead: "用 git status -b 看当前版本名（这是给人看的提示）。",
  },
];

export const sections: Section[] = [
  {
    id: "download",
    group: "官网",
    nav: "下载中心",
    title: "熟悉的 Git，为游戏项目重新打造",
    kicker: "官网 · 下载中心",
    summary:
      "git-next 是原生 Go 客户端与 git-terminal 的组合，CGO_ENABLED=0，不依赖 C 工具链；从安装到日常提交都按美术、运营也能上手的方式设计。",
    keywords: ["下载", "安装包", "MSI", "tar.gz", "Windows", "macOS", "Linux", "官网"],
    blocks: [
      { kind: "heading", anchor: "platforms", text: "下载安装包" },
      { kind: "heading", anchor: "go-preview", text: "Go-only 预览包" },
      { kind: "heading", anchor: "before-install", text: "安装前先看" },
      { kind: "heading", anchor: "facts", text: "项目现状" },
      { kind: "heading", anchor: "roles", text: "按角色开始" },
    ],
  },
  {
    id: "home",
    group: "开始",
    nav: "首页",
    title: "git-next 是什么，能帮你做什么",
    kicker: "先看这里",
    summary:
      "一句话：它是一个帮你“记住文件每次改动”的工具。不用背命令，照着本手册复制、粘贴、按回车，就能安全地保存文件和拿到最新版本。",
    keywords: ["介绍", "是什么", "入门", "美术", "运营", "团队"],
    blocks: [
      { kind: "heading", anchor: "what", text: "用一句大白话说" },
      {
        kind: "paragraph",
        text: "git-next 是一个管理项目文件历史的工具。它把每一次保存的改动记下来，需要的时候可以把文件恢复到以前的样子，也可以把你的改动交给同事。它打开后就是一个可以输入文字的黑色窗口，这叫“终端”；你输入一行命令、按回车，它就替你做事。",
      },
      {
        kind: "callout",
        tone: "info",
        title: "不用背命令",
        text: "本手册每一步都给了可以直接复制的命令。点代码块右上角的“复制”，再到窗口里粘贴、按回车即可。复制时不会带上引号以外的多余字符。",
      },
      { kind: "heading", anchor: "why", text: "它到底帮你解决什么" },
      {
        kind: "list",
        ordered: true,
        items: [
          "改错了能找回来：昨天还对的贴图今天被覆盖了，可以从记录里拿回旧版本。",
          "拿到最新文件：同事更新了配置表，你一条命令就能把最新版本取到本机。",
          "放心试新东西：想试一版新文案又不想弄乱正式版，可以开一条“平行版本”（Git 里叫“分支”），试完再决定要不要留下。",
          "大文件也能管：贴图、模型、音频会用专门的方式保存，不会让项目文件夹越来越难打开。",
        ],
      },
      { kind: "heading", anchor: "ask-dev", text: "什么时候请找开发" },
      {
        kind: "callout",
        tone: "warn",
        title: "遇到这些情况先停下来",
        text: "看到不认识的英文报错；提示“冲突（conflict）”；要改别人的文件；要删掉历史记录；要换电脑或重装系统。这些情况请截图问开发，不要自己反复尝试。",
      },
      {
        kind: "paragraph",
        text: "找开发时尽量给三样东西：你输入的命令、窗口里显示的文字（截图就行）、你当时在哪个文件夹、改的是哪个文件。这样对方不用反复追问。",
      },
    ],
  },
  {
    id: "quickstart",
    group: "开始",
    nav: "3 分钟上手",
    title: "3 分钟上手",
    kicker: "跟着做一遍",
    summary:
      "打开 Git Terminal，进入项目文件夹，看一眼状态，再保存一次修改。做完这四步就算入门了。",
    keywords: ["安装", "打开", "进入文件夹", "第一次", "status", "commit"],
    blocks: [
      { kind: "heading", anchor: "installed", text: "第 0 步：确认已经安装" },
      {
        kind: "paragraph",
        text: "在 Windows 开始菜单里能找到“Git Terminal”，说明已经装好。如果找不到，请把截图发给开发或运维，不要自己下载来路不明的安装包。",
      },
      {
        kind: "callout",
        tone: "info",
        title: "更快的方式",
        text: "在 Windows 文件夹空白处点右键，选“Open Git-Terminal Here”，窗口会直接开在当前文件夹，就不用手动输入路径了。",
      },
      { kind: "heading", anchor: "open", text: "第 1 步：打开窗口并进入项目文件夹" },
      {
        kind: "steps",
        items: [
          {
            text: "从开始菜单打开“Git Terminal”。你会看到一个可以输入文字的黑色窗口。",
          },
          {
            text: "输入下面的命令，把 D:\\Code\\my-project 换成你自己的项目路径，然后按回车。路径里有空格时，要用英文双引号把整个路径括起来。",
            command: 'cd "D:\\Code\\my-project"',
            note: "不知道路径怎么写？在文件夹地址栏复制，再粘进来。",
          },
          {
            text: "确认当前位置对不对，窗口里会显示你所在的文件夹。",
            command: "pwd",
          },
        ],
      },
      { kind: "heading", anchor: "status", text: "第 2 步：看看有什么变化" },
      {
        kind: "steps",
        items: [
          {
            text: "输入下面的命令，看看工具记下了哪些改动。",
            command: "git status",
          },
          {
            text: "如果显示 nothing to commit, working tree clean，说明没有新改动。这是正常结果，不是报错。",
          },
        ],
      },
      { kind: "heading", anchor: "save", text: "第 3 步：保存一次修改" },
      {
        kind: "paragraph",
        text: "“保存一次修改”在 Git 里叫“提交（commit）”。它不是覆盖文件，而是新增一条记录，所以以后还能找回来。",
      },
      {
        kind: "steps",
        items: [
          {
            text: "把要保存的文件放进“待保存清单”（Git 里叫“暂存区”，可以理解成购物车）。把 Assets/Art/hero.psd 换成你改过的文件。",
            command: 'git add "Assets/Art/hero.psd"',
          },
          {
            text: "再看一遍清单，确认里面只有你想保存的文件。",
            command: "git status",
          },
          {
            text: "写清楚这次改了什么，然后保存。引号里的文字请写成别人也看得懂的话。",
            command: 'git commit -m "替换主角立绘配色"',
          },
          {
            text: "确认刚才的操作已经变成一条记录。",
            command: "git log --oneline",
          },
        ],
      },
      {
        kind: "callout",
        tone: "warn",
        title: "提交前先看一眼",
        text: "保存前先运行 git status，确认清单里没有同事的文件、临时文件或缓存文件。",
      },
      { kind: "heading", anchor: "all", text: "想一次保存全部改动" },
      {
        kind: "paragraph",
        text: "如果你确定当前文件夹里所有改动都要保存，可以用下面两条命令代替上面第 3 步的前两条。",
      },
      {
        kind: "code",
        caption: "全部保存的两条命令",
        code: 'git add -A\ngit commit -m "说明这次改了什么"',
      },
      {
        kind: "callout",
        tone: "danger",
        title: "注意一个常见误区",
        text: "当前实现没有 git commit -a 这条命令。想达到同样效果，请用上面的 git add -A 加 git commit -m。",
      },
    ],
  },
  {
    id: "artists",
    group: "按角色",
    nav: "给美术",
    title: "给美术：贴图、模型和音频怎么安全提交",
    kicker: "美术同学看这里",
    summary: "大文件不用怕。关键是让工具用专门的方式管它们，并在提交前照着清单检查一遍。",
    keywords: ["美术", "贴图", "模型", "音频", "大文件", "LFS", "meta", "提交"],
    blocks: [
      { kind: "heading", anchor: "big", text: "大文件为什么要特别处理" },
      {
        kind: "paragraph",
        text: "普通文件会把每次改动都记下来；一张几百兆的贴图如果每改一次就存一份，项目会变得非常庞大。git-next 内置了大文件管理（叫 LFS），项目里只存一个很小的“指针”，真正的文件放在旁边，你不需要另外安装任何东西。",
      },
      { kind: "heading", anchor: "first", text: "第一次参与项目时" },
      {
        kind: "steps",
        items: [
          {
            text: "进入项目文件夹，把路径换成你自己的。",
            command: 'cd "D:\\Code\\my-project"',
          },
          {
            text: "看看当前状态，确认没有别人的改动混进来。",
            command: "git status",
          },
          {
            text: "如果项目还没设置大文件规则，请让开发设置。不要自己改规则文件 .gitattributes，它会影响所有人。",
          },
        ],
      },
      {
        kind: "callout",
        tone: "info",
        title: "已经设置好的项目",
        text: "你直接改文件、按下面的流程保存就行，不需要重复设置。",
      },
      { kind: "heading", anchor: "submit", text: "日常提交一张贴图" },
      {
        kind: "steps",
        items: [
          { text: "确认你改了哪几个文件。", command: "git status" },
          {
            text: "只把你要交的文件放进清单，把文件名换成你自己的。",
            command: 'git add "Assets/Art/hero.psd"',
          },
          {
            text: "再看一遍清单，确认没有多放文件。",
            command: "git status",
          },
          {
            text: "写清楚改了什么，保存。",
            command: 'git commit -m "更新主角立绘配色"',
          },
          {
            text: "交给团队。第一次推送用下面这条，以后用 git push。",
            command: "git push -u origin main",
          },
        ],
      },
      {
        kind: "callout",
        tone: "warn",
        title: "推送失败时",
        text: "先看窗口里的提示，不要强行覆盖。常见原因和办法见“常见问题”。",
      },
      { kind: "heading", anchor: "dont", text: "哪些文件不要乱改" },
      {
        kind: "list",
        items: [
          ".git 文件夹：这是工具自己用的记录，手动改会让历史损坏。",
          ".gitattributes 和 .gitignore：规则文件，改了会影响所有人，先问开发。",
          ".meta 文件（Unity）：它记录贴图和预设的配对信息，改名或丢失会让资源在编辑器里“消失”，移动资源时要连它一起移动。",
          "别人正在编辑的文件：先在群里说一声，避免两个人同时改同一个文件。",
          "缓存和生成的文件夹（Library、Temp、Obj、Binaries、Intermediate、Saved）：这些是编辑器生成的，不需要提交。",
        ],
      },
      { kind: "heading", anchor: "missing", text: "文件不见了、名字变了怎么办" },
      {
        kind: "paragraph",
        text: "先别慌，按下面的顺序排查，大多数情况都能找回来。",
      },
      {
        kind: "list",
        ordered: true,
        items: [
          "看是不是切到了别的版本：运行 git status，看提示里的版本名对不对。",
          "看是不是被规则排除了：文件还在硬盘上但没出现在列表里，问开发确认规则。",
          "文件确实被删了：可以从上一条记录里拿回来，方法见下面的命令。",
        ],
      },
      {
        kind: "code",
        caption: "从上一版拿回误删的文件，把文件名换成你自己的",
        code: 'git restore --source HEAD~1 "Assets/Art/hero.psd"',
      },
      {
        kind: "paragraph",
        text: "拿回来以后，再按上面的日常提交流程保存一次。",
      },
      {
        kind: "callout",
        tone: "danger",
        title: "不要自己尝试",
        text: "不要运行带 --hard 的命令，它会丢掉你还没保存的修改。拿不准就截图问开发。",
      },
      { kind: "heading", anchor: "checklist", text: "提交前自检清单" },
      {
        kind: "checklist",
        items: [
          "我运行了 git status，清单里只有我要交的文件。",
          "文件名和路径没有乱码、没有多套一层文件夹。",
          "大文件已经按项目规则处理，不是直接扔进去的单个超大文件。",
          "Unity 的 .meta 文件跟着资源一起改了。",
          "提交说明写清楚了“改了什么”，不是只写“更新”。",
          "我没有提交缓存、临时文件或别人的文件。",
        ],
      },
    ],
  },
  {
    id: "ops",
    group: "按角色",
    nav: "给运营",
    title: "给运营：拿最新版本、试改和回退",
    kicker: "运营同学看这里",
    summary:
      "每天开工先拿最新版本；要试新东西就开一条平行版本；改坏了能退回来；最后把改动交给开发。",
    keywords: ["运营", "配置", "拿版本", "试改", "回退", "分支", "push"],
    blocks: [
      { kind: "heading", anchor: "daily", text: "每天开工第一件事：拿最新版本" },
      {
        kind: "steps",
        items: [
          {
            text: "进入项目文件夹，把路径换成你自己的。",
            command: 'cd "D:\\Code\\my-project"',
          },
          {
            text: "确认自己没有还没保存的修改。",
            command: "git status",
          },
          {
            text: "把同事的最新改动取到本机。这条命令只在能直接前进时才更新，不会把你的改动弄乱。",
            command: "git pull --ff-only",
          },
        ],
      },
      {
        kind: "callout",
        tone: "info",
        title: "如果提示你还有自己的改动",
        text: "先把改动按“给美术”章节的流程保存，或者截图问开发，不要删除文件。",
      },
      { kind: "heading", anchor: "try", text: "临时试改：开一条平行版本" },
      {
        kind: "paragraph",
        text: "想试一版新文案或新配置，又不想影响正式版本，就开一条“分支”。可以先把它理解成从当前版本复制出来的一条平行线，你在这条线上随便改。",
      },
      {
        kind: "steps",
        items: [
          {
            text: "起一个好认的名字，创建并切过去。",
            command: "git switch -c try-new-copy",
          },
          { text: "改你的文件，然后把改动放进清单。", command: "git add -A" },
          {
            text: "写清楚改了什么，保存。",
            command: 'git commit -m "试写一版新活动文案"',
          },
        ],
      },
      { kind: "heading", anchor: "back", text: "试完了：回到正式版本" },
      {
        kind: "steps",
        items: [
          {
            text: "切回正式版本（一般叫 main）。切换前先保存刚才的试改。",
            command: "git switch main",
          },
          {
            text: "再拿一次最新版本。",
            command: "git pull --ff-only",
          },
        ],
      },
      {
        kind: "callout",
        tone: "warn",
        title: "切换时被拒绝",
        text: "如果提示“本地修改会被覆盖”，说明你有没保存的东西。先按上面的流程保存，不要强行切换。",
      },
      { kind: "heading", anchor: "undo", text: "改坏了怎么回退" },
      {
        kind: "list",
        items: [
          "只是改乱了文件、还没保存：用下面这条命令把这个文件恢复到上一次保存的样子，文件名换成你自己的。",
          "已经保存了又想撤销：不要自己处理，截图发给开发。",
        ],
      },
      {
        kind: "code",
        caption: "撤掉还没保存的改动",
        code: 'git restore "config/event.json"',
      },
      {
        kind: "callout",
        tone: "danger",
        title: "记住这条",
        text: "任何带 --hard 的命令都可能永久丢掉没保存的修改，不要自己运行。",
      },
      { kind: "heading", anchor: "handoff", text: "把改动交给开发" },
      {
        kind: "steps",
        items: [
          { text: "确认你改了哪些文件。", command: "git status" },
          {
            text: "把改动放进清单，保存到你自己的分支（名字和前面一致）。",
            command: "git add -A",
          },
          {
            text: "写下这次改了什么。",
            command: 'git commit -m "更新活动配置与文案"',
          },
          {
            text: "把分支推送到团队服务器。",
            command: "git push -u origin try-new-copy",
          },
          {
            text: "在群里告诉开发：分支名、改了哪些文件、希望他检查什么。",
          },
        ],
      },
      { kind: "heading", anchor: "handoff-check", text: "交给开发前的检查单" },
      {
        kind: "checklist",
        items: [
          "我运行了 git status，没有遗漏的文件。",
          "每一次保存都写了清楚的说明。",
          "我没有把账号密码、密钥或玩家隐私数据写进文件。",
          "我告诉了开发分支名和要检查的内容。",
          "推送后窗口显示成功，没有红色报错。",
        ],
      },
    ],
  },
  {
    id: "cheatsheet",
    group: "随手查",
    nav: "命令速查",
    title: "常用命令速查表",
    kicker: "复制到窗口里用",
    summary: "只列日常工作真的会用到的命令。点代码块右上角的“复制”即可。",
    keywords: ["命令", "速查", "status", "add", "commit", "switch", "clone", "pull", "push"],
    blocks: [
      { kind: "heading", anchor: "daily", text: "日常最常用" },
      { kind: "cheatsheet" },
      { kind: "heading", anchor: "missing", text: "当前实现没有的命令" },
      {
        kind: "paragraph",
        text: "下面两条写法在网上很常见，但当前实现并不支持。照着“替代做法”写，不要直接复制它们。",
      },
      { kind: "unsupported" },
      {
        kind: "callout",
        tone: "info",
        title: "不确定某条命令能不能用",
        text: "运行 git doctor 看当前版本的提示，或者直接问开发。当前实现不会悄悄把不认识的命令当成别的命令执行。",
      },
    ],
  },
  {
    id: "faq",
    group: "随手查",
    nav: "常见问题",
    title: "常见问题",
    kicker: "出问题时翻这里",
    summary: "中文乱码、路径空格、推送失败、权限、文件锁定、大文件、网络中断，都在这里。",
    keywords: ["乱码", "中文", "空格", "推送失败", "权限", "锁定", "大文件", "网络", "报错"],
    blocks: [
      { kind: "heading", anchor: "how", text: "点开问题看解决办法" },
      {
        kind: "faq",
        items: [
          {
            question: "中文显示成乱码，或者文件名变成问号怎么办？",
            answer:
              "不要改文件名。请先确认你用的是 Git Terminal，它支持中文输入和显示。想固定界面语言，可以运行切换语言的命令。路径里有中文时，用英文双引号把整个路径括起来。",
            command: "git language set chinese",
          },
          {
            question: "路径里有空格，命令总是报错？",
            answer: "用英文双引号把整段路径或文件名括起来，不要漏掉收尾的引号。",
            command: 'cd "D:\\Code\\My Project"',
          },
          {
            question: "推送（push）失败，提示 non-fast-forward？",
            answer:
              "说明服务器上有你还没拿到的新记录。先拿最新版本，再推送一次；不要强行覆盖。如果仍然失败，把窗口文字截图发给开发。",
            command: "git pull --ff-only\ngit push",
          },
          {
            question: "提示没有权限，或者要我登录？",
            answer:
              "先让开发确认你的账号有没有这个项目的权限；再按窗口弹窗的提示完成登录。不要把账号和密码写进项目文件里。",
          },
          {
            question: "文件被占用、提示锁定或打不开？",
            answer:
              "先关掉正在使用这个资源的编辑器或软件再重试。Unity／UE 里锁定的资源要在编辑器内解锁。不要手动删除锁文件，先截图问开发。",
          },
          {
            question: "大文件传不上去，或者一直卡住？",
            answer:
              "先确认项目已经设置大文件规则（问开发，不要自己改规则文件）。单个超大文件可能需要拆分。传输时请耐心等待，不要反复中断；失败后把提示截图发出来。",
          },
          {
            question: "网络断了，命令卡住或者失败了？",
            answer:
              "先确认网络恢复，再重新运行同一条命令。拿最新版本和推送都可以安全地重试。多次失败时记下大概时间并截图。",
          },
          {
            question: "提示 not a Git repository？",
            answer:
              "说明你不在项目文件夹里，或者站在了项目文件夹的外面。先用 cd 进入项目文件夹再运行。",
            command: 'cd "D:\\Code\\my-project"\ngit status',
          },
          {
            question: "提示 nothing to commit, working tree clean，是我操作错了吗？",
            answer: "没有。它的意思是“没有新改动”，属于正常结果，可以直接继续下一步。",
          },
          {
            question: "文件明明改了，git status 里却看不到？",
            answer:
              "这个文件可能被规则排除了，常见于缓存和编辑器生成的文件。真正要提交的文件如果也看不到，请把规则文件和截图发给开发确认。",
          },
        ],
      },
    ],
  },
  {
    id: "language-help",
    group: "随手查",
    nav: "语言与帮助",
    title: "切换语言与寻求帮助",
    kicker: "让界面说中文",
    summary: "软件默认跟随系统语言，可以固定成中文或英文。遇到问题时也请按这里的方式反馈。",
    keywords: ["语言", "中文", "英文", "切换", "反馈", "截图", "帮助"],
    blocks: [
      { kind: "heading", anchor: "language", text: "切换软件界面语言" },
      {
        kind: "paragraph",
        text: "默认跟随系统语言。想固定成中文或英文，运行下面的命令，只需要设置一次。",
      },
      {
        kind: "code",
        caption: "第一行看当前语言，后面三行分别设置中文、英文、跟随系统",
        code: "git language get\ngit language set chinese\ngit language set english\ngit language set auto",
      },
      {
        kind: "paragraph",
        text: "只想这一次用英文看提示，可以在命令中间加一个参数：git --language english status。给其他程序读的机器格式不会随语言变化。",
      },
      { kind: "heading", anchor: "terminal-language", text: "Git Terminal 自己的语言" },
      {
        kind: "code",
        caption: "窗口本身也可以单独设置语言",
        code: "git-terminal language get\ngit-terminal language set chinese\ngit-terminal language set english\ngit-terminal language set auto",
      },
      { kind: "heading", anchor: "feedback", text: "截图反馈时请提供什么" },
      {
        kind: "paragraph",
        text: "遇到问题发给开发时，尽量让一张截图包含下面几项，这样对方不用反复追问。",
      },
      {
        kind: "checklist",
        items: [
          "窗口里完整的提示文字，不要只截最后一行。",
          "你输入的命令，能从窗口里一起截到最好。",
          "当前所在的文件夹路径，窗口底部会显示。",
          "问题发生的大概时间，以及你当时正在做什么。",
        ],
      },
      {
        kind: "callout",
        tone: "info",
        title: "不需要记录日志",
        text: "Git Terminal 不会把命令和输出写进应用日志。截图就够了，不需要你另外整理终端内容。",
      },
      { kind: "heading", anchor: "selfcheck", text: "先自己做的两个检查" },
      {
        kind: "list",
        ordered: true,
        items: [
          "运行 git status，看有没有还没保存的修改。",
          "运行 git doctor，看当前版本有没有明确提示。",
        ],
      },
    ],
  },
  {
    id: "limits",
    group: "了解边界",
    nav: "版本与边界",
    title: "版本与边界：请这样使用 git-next",
    kicker: "重要，请读一遍",
    summary:
      "git-next 能覆盖日常工作，但还不是“什么都会、可以完全替代 Git”的工具。请按本页的边界使用。",
    keywords: ["版本", "边界", "Go", "CGO", "L0", "兼容", "限制"],
    blocks: [
      { kind: "heading", anchor: "current", text: "当前是什么" },
      {
        kind: "list",
        items: [
          "当前独立客户端用 Go 语言实现，构建时关闭了 CGO（CGO_ENABLED=0）；正式安装包里带的是固定版本的原生引擎和自有运行时。",
          "常用命令已通过 L0 验收（39 项）。当前正式版本是 1.0.4，以窗口里显示的版本为准。",
          "新项目默认分支名是 main，默认使用 LF 换行规则；大文件管理已内置，不用另外安装。",
        ],
      },
      { kind: "heading", anchor: "boundary", text: "不要把它当成唯一的 Git 安装" },
      {
        kind: "callout",
        tone: "danger",
        title: "请留着原来的 Git",
        text: "当前实现的完整兼容标记 complete_git_parity 仍为 false，也就是说它还没有覆盖 Git 的全部功能。请不要卸载系统里原有的 Git，也不要把它当成唯一可用的 Git。本手册没写的命令，先问开发。",
      },
      { kind: "heading", anchor: "notyet", text: "明确还没做好的部分" },
      {
        kind: "list",
        items: [
          "没有交互式的历史改写（rebase）、stash、cherry-pick、revert。",
          "git pull 必须使用 --ff-only；真的需要合并时请让开发处理。",
          "切换分支要求工作区干净；有没保存的修改时会明确拒绝，而不是帮你丢掉。",
          "推送只覆盖常见情况，不是所有写法都支持。",
          "大文件的传输和锁定协议只实现了一部分。",
          "hooks、签名、子模块的完整兼容仍在进行中；SHA-256 格式也尚未完整支持。",
        ],
      },
      { kind: "heading", anchor: "whenlimit", text: "碰到边界怎么做" },
      {
        kind: "paragraph",
        text: "本手册没提到的命令、看不懂的报错、涉及删除历史或服务器设置的操作，请直接把截图发给开发。这是正常使用方式，不是你操作错了。",
      },
    ],
  },
];

export const navGroups: { name: string; ids: string[] }[] = [
  { name: "官网", ids: ["download"] },
  { name: "开始", ids: ["home", "quickstart"] },
  { name: "按角色", ids: ["artists", "ops"] },
  { name: "随手查", ids: ["cheatsheet", "faq", "language-help"] },
  { name: "了解边界", ids: ["limits"] },
];

export function findSection(id: string): Section {
  return sections.find((section) => section.id === id) ?? sections[0];
}

export function sectionHeadings(section: Section): { anchor: string; text: string }[] {
  return section.blocks.flatMap((block) =>
    block.kind === "heading" ? [{ anchor: block.anchor, text: block.text }] : [],
  );
}

function collectText(blocks: Block[]): string {
  return blocks
    .map((block) => {
      switch (block.kind) {
        case "heading":
        case "paragraph":
          return block.text;
        case "steps":
          return block.items
            .map((item) => `${item.text} ${item.command ?? ""} ${item.note ?? ""}`)
            .join(" ");
        case "code":
          return `${block.caption ?? ""} ${block.code}`;
        case "callout":
          return `${block.title} ${block.text}`;
        case "list":
        case "checklist":
          return block.items.join(" ");
        case "faq":
          return block.items
            .map((item) => `${item.question} ${item.answer} ${item.command ?? ""}`)
            .join(" ");
        case "cheatsheet":
          return commandCheatSheet.map((row) => `${row.command} ${row.when}`).join(" ");
        case "unsupported":
          return unsupportedCommands
            .map((row) => `${row.command} ${row.why} ${row.instead}`)
            .join(" ");
      }
    })
    .join(" ");
}

export function searchSections(query: string): Section[] {
  const needle = query.trim().toLocaleLowerCase();
  if (!needle) return [];
  return sections.filter((section) => {
    const haystack = [
      section.nav,
      section.title,
      section.summary,
      ...section.keywords,
      collectText(section.blocks),
    ]
      .join(" ")
      .toLocaleLowerCase();
    return haystack.includes(needle);
  });
}
