# Qianshou Agent Instructions

Before changing Qianshou architecture or workflow semantics, read:

- `docs/architecture/control-plane.md`
- every affected role contract under `.agents/skills/`

## Boundaries

- GitHub, Git, test output, PR head SHAs, and Review records are evidence sources. Never replace them with an Agent's completion claim.
- Qianshou's local ledger records handoff state; it does not redefine external facts.
- Keep Developer and Reviewer independent. Reviewer findings return to the implementer; Reviewer does not repair code.
- Never merge, push, publish, deploy, or start an Agent from the dashboard without explicit user action.
- Keep the server bound to `127.0.0.1`. Never expose GitHub credentials or raw environment values to the browser.
- Use test-first development for workflow-state changes.

## 严格度与说明规约

- 说人话：先讲清楚用户要解决什么问题、得到什么结果，再讲技术名词；不要用术语掩盖没想明白的事。
- 坚持 YAGNI（You Aren't Gonna Need It）。别过度设计，别自己加戏：只做当前目标和验收真正需要的内容；未经用户确认，不擅自增加抽象、兼容层、迁移、版本、流程或未来需求。
- 每次都用审视的目光仔细检查用户输入中的潜在问题，犀利地指出问题，并给出用户当前思考框架之外的建议。不要为了顺从而附和；如果用户说得太离谱，就直接骂回来，帮助用户瞬间清醒。批评针对观点、假设和决策，不做人身攻击。
- Issue 正文第一句必须是人话 Goal；不变量引用架构文档、不复制；节按需出现，不按模板凑。
- 一句话检验：先说出这个 Issue 的判断/动作是什么，再问正文里每个部分是让这个判断更不容易错，还是在为别的什么服务——前者留，后者归入它真正服务的 Issue 或删除。
- 判断类代码只有两条不可妥协的错向：查不出必须报错（不得当作"无依赖"）；证据矛盾必须报错（不得静默择一）。语法宽容允许，结论歧义禁止；不声明语法完备。
- 注释不是证据：机制上的安全声明要么有同变更的攻击测试背书，要么删除。
