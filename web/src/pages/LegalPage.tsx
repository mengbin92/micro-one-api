import { useEffect, useState } from 'react';
import { ArrowLeft, Scale, ShieldCheck, TriangleAlert } from 'lucide-react';
import { Link } from 'react-router';
import { apiClient } from '@/lib/api';
import { unwrapApiData } from '@/lib/api-response';
import { t } from '@/lib/i18n';
import { LanguageToggle } from '@/components/LanguageToggle';

interface LegalStatus {
  system_name?: string;
  legal_operator_name?: string;
  legal_operator_address?: string;
  legal_contact_email?: string;
}

interface LegalIdentity {
  systemName: string;
  operatorName: string;
  operatorAddress: string;
  contactEmail: string;
}

const EMPTY_IDENTITY: LegalIdentity = {
  systemName: 'Micro-One API',
  operatorName: '',
  operatorAddress: '',
  contactEmail: '',
};

function useLegalIdentity() {
  const [identity, setIdentity] = useState(EMPTY_IDENTITY);

  useEffect(() => {
    let cancelled = false;
    apiClient.get('/status').then((response) => {
      if (cancelled) return;
      const status = unwrapApiData<LegalStatus>(response.data);
      setIdentity({
        systemName: status.system_name?.trim() || EMPTY_IDENTITY.systemName,
        operatorName: status.legal_operator_name?.trim() || '',
        operatorAddress: status.legal_operator_address?.trim() || '',
        contactEmail: status.legal_contact_email?.trim() || '',
      });
    }).catch(() => {
      // The legal text remains readable when the public status endpoint is unavailable.
    });

    return () => {
      cancelled = true;
    };
  }, []);

  return identity;
}

function LegalSection({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section className="scroll-mt-24 space-y-3">
      <h2 className="text-xl font-semibold tracking-tight text-foreground">{title}</h2>
      <div className="space-y-3 text-sm leading-7 text-muted-foreground sm:text-[15px]">{children}</div>
    </section>
  );
}

function LegalList({ children }: { children: React.ReactNode }) {
  return <ul className="list-disc space-y-2 pl-6 marker:text-muted-foreground/70">{children}</ul>;
}

function OperatorDetails({ identity }: { identity: LegalIdentity }) {
  const incomplete = !identity.operatorName || !identity.operatorAddress || !identity.contactEmail;
  return (
    <>
      {incomplete && (
        <div className="flex gap-3 rounded-xl border border-amber-300/60 bg-amber-50 p-4 text-sm leading-6 text-amber-950 dark:border-amber-400/25 dark:bg-amber-500/10 dark:text-amber-100">
          <TriangleAlert className="mt-0.5 size-5 shrink-0" />
          <div>
            <p className="font-semibold">{t("运营信息尚未完整配置")}</p>
            <p className="mt-1 opacity-80">{t("部署方应在“系统设置”中补齐运营者名称、注册地址和隐私联系邮箱后再开放注册。")}</p>
          </div>
        </div>
      )}
      <dl className="grid gap-3 rounded-xl bg-muted/60 p-4 text-sm sm:grid-cols-[8rem_1fr]">
        <dt className="font-medium text-foreground">{t("服务名称")}</dt>
        <dd className="text-muted-foreground">{identity.systemName}</dd>
        <dt className="font-medium text-foreground">{t("运营者")}</dt>
        <dd className="text-muted-foreground">{identity.operatorName || t("（待部署方配置）")}</dd>
        <dt className="font-medium text-foreground">{t("注册地址")}</dt>
        <dd className="text-muted-foreground">{identity.operatorAddress || t("（待部署方配置）")}</dd>
        <dt className="font-medium text-foreground">{t("联系邮箱")}</dt>
        <dd className="break-all text-muted-foreground">
          {identity.contactEmail ? <a className="text-primary hover:underline" href={`mailto:${identity.contactEmail}`}>{identity.contactEmail}</a> : t("（待部署方配置）")}
        </dd>
      </dl>
    </>
  );
}

function LegalLayout({
  title,
  summary,
  icon,
  children,
}: {
  title: string;
  summary: string;
  icon: React.ReactNode;
  children: (identity: LegalIdentity) => React.ReactNode;
}) {
  const identity = useLegalIdentity();
  const backTo = localStorage.getItem('token') ? '/dashboard' : '/login';

  return (
    <main className="min-h-screen bg-background text-foreground">
      <header className="sticky top-0 z-10 border-b border-border bg-background/90 backdrop-blur-xl">
        <div className="mx-auto flex h-16 max-w-5xl items-center justify-between px-4 sm:px-6">
          <Link to={backTo} className="inline-flex items-center gap-2 text-sm font-medium text-muted-foreground hover:text-foreground">
            <ArrowLeft className="size-4" />{backTo === '/dashboard' ? t('返回控制台') : t('返回登录页')}
          </Link>
          <div className="flex items-center gap-2">
            <LanguageToggle />
            <Link to="/login" aria-label={t("Micro-One API 首页")} className="flex items-center gap-2">
              <img src="/logo-icon.svg" alt="" className="size-8 rounded-lg" />
              <span className="hidden text-sm font-semibold sm:inline">Micro-One API</span>
            </Link>
          </div>
        </div>
      </header>

      <article className="mx-auto max-w-3xl px-4 py-10 sm:px-6 sm:py-14">
        <div className="mb-10 border-b border-border pb-8">
          <div className="mb-5 grid size-12 place-items-center rounded-2xl bg-primary/10 text-primary">{icon}</div>
          <h1 className="text-3xl font-bold tracking-tight sm:text-4xl">{title}</h1>
          <p className="mt-4 max-w-2xl text-base leading-7 text-muted-foreground">{summary}</p>
          <div className="mt-5 flex flex-wrap gap-x-6 gap-y-1 text-xs text-muted-foreground">
            <span>{t("版本：1.0")}</span>
            <span>{t("发布日期：2026 年 8 月 28 日")}</span>
            <span>{t("生效日期：2026 年 8 月 28 日")}</span>
          </div>
        </div>

        <div className="space-y-10">{children(identity)}</div>

        <footer className="mt-12 flex flex-wrap gap-4 border-t border-border pt-6 text-sm">
          <Link to="/terms" className="text-primary hover:underline">{t("用户协议")}</Link>
          <Link to="/privacy" className="text-primary hover:underline">{t("隐私政策")}</Link>
        </footer>
      </article>
    </main>
  );
}

export function UserAgreementPage() {
  return (
    <LegalLayout
      title={t("Micro-One API 用户协议")}
      summary={t("本协议适用于在中华人民共和国境内向用户提供的 AI API 网关、控制台、账户、计费及相关服务。请在注册或使用服务前完整阅读。")}
      icon={<Scale className="size-6" />}
    >
      {(identity) => (
        <>
          <OperatorDetails identity={identity} />

          <LegalSection title={t("特别提示")}>
            <p>{t("请您重点阅读与服务限制、费用、账号处置、责任限制、未成年人保护及争议解决有关的条款。限制或免除责任的内容仅在法律允许的范围内适用，不排除或限制您依法享有的消费者权利。")}</p>
            <p>{t("当您勾选同意、完成注册、登录或实际使用本服务，即表示您已阅读、理解并同意本协议及《隐私政策》。如您不同意，请勿注册或使用本服务。")}</p>
          </LegalSection>

          <LegalSection title={t("一、协议主体与适用范围")}>
            <p>{t("本协议由您与上述运营者订立。运营者通过")}{identity.systemName}{t("向您提供 AI 模型 API 聚合、请求转发、模型路由、密钥管理、用量统计、钱包计费、订阅及相关控制台功能，具体以实际开放的功能为准。")}</p>
            <p>{t("第三方模型、支付、身份认证等服务由相应第三方独立提供，并适用其依法公示的服务规则。本协议不替代您与第三方之间的协议。")}</p>
          </LegalSection>

          <LegalSection title={t("二、账号注册与使用资格")}>
            <LegalList>
              <li>{t("您应当具有与所实施民事行为相适应的民事行为能力，并提供真实、准确、完整的信息。面向组织使用时，您确认已获得该组织的合法授权。")}</li>
              <li>{t("不满十八周岁的未成年人应在监护人阅读并同意本协议后使用；本服务不面向不满十四周岁的未成年人开放独立注册。")}</li>
              <li>{t("账号仅限您本人或您获授权的组织使用。您应妥善保管密码、会话凭证和 API 密钥，不得出租、出借、出售或以其他方式允许未经授权的主体使用。")}</li>
              <li>{t("发现账号或密钥被盗用、泄露或存在其他安全风险时，您应立即停用相关密钥、修改凭证并联系运营者。")}</li>
            </LegalList>
          </LegalSection>

          <LegalSection title={t("三、服务使用规则")}>
            <p>{t("您应遵守《中华人民共和国民法典》《中华人民共和国网络安全法》《中华人民共和国数据安全法》《中华人民共和国个人信息保护法》及其他适用法律法规，不得利用本服务实施下列行为：")}</p>
            <LegalList>
              <li>{t("制作、复制、发布、传播法律法规禁止的信息，或者从事危害国家安全、公共安全、网络安全及他人合法权益的活动；")}</li>
              <li>{t("未经授权处理国家秘密、工作秘密、商业秘密、个人信息、重要数据或受知识产权保护的内容；")}</li>
              <li>{t("攻击、干扰、破坏本服务或相关网络，规避访问控制、用量限制、安全审计或计费机制；")}</li>
              <li>{t("转售、共享或以异常自动化方式滥用服务资源，实施欺诈、洗钱、侵权、恶意爬取、生成恶意代码或其他违法违规行为；")}</li>
              <li>{t("以任何方式误导他人认为 AI 生成内容由自然人创作，或将未经核验的生成内容用于医疗、法律、金融、公共安全等高风险决策。")}</li>
            </LegalList>
            <p>{t("您对提交的提示词、文件、数据和其他输入内容拥有合法权利或已取得充分授权，并应根据适用法律履行个人信息告知、同意及安全保护义务。")}</p>
          </LegalSection>

          <LegalSection title={t("四、AI 服务特别说明")}>
            <LegalList>
              <li>{t("本服务主要提供技术连接与管理能力，不保证第三方模型持续可用，也不保证生成内容真实、准确、完整、无偏见或适合特定用途。")}</li>
              <li>{t("您应对生成内容进行人工复核，并对使用生成内容作出的决定及其后果负责。不得将生成内容视为专业意见的替代。")}</li>
              <li>{t("请求内容可能被发送至运营者配置的第三方模型服务商。您应避免输入非必要的敏感个人信息、国家秘密、商业秘密或其他受严格保护的数据。")}</li>
              <li><strong className="text-foreground">{t("生成内容标识方式：")}</strong>{t("控制台等可视交互场景应以文字提示说明内容由人工智能生成；为保持 OpenAI、Anthropic 等标准 API 的协议兼容性，API 响应通常不额外插入显式标识。您调用此类 API 即表示申请获得不含额外显式标识的结构化响应，并承诺在对外发布或传播前，按照《人工智能生成合成内容标识办法》等规定主动声明并添加显著标识。运营者在适用法律要求时留存提供对象信息及相关日志不少于六个月。")}</li>
              <li>{t("您不得恶意删除、篡改、伪造、隐匿依法设置的显式或隐式标识，也不得为他人实施上述行为提供工具或服务。")}</li>
            </LegalList>
          </LegalSection>

          <LegalSection title={t("五、费用、充值与订阅")}>
            <LegalList>
              <li>{t("服务价格、计费单位、套餐额度、有效期及支付方式以购买页面在您下单时展示的信息为准。除另有说明外，金额以人民币计价。")}</li>
              <li>{t("模型调用通常按实际请求量、Token 用量、模型单价或套餐规则计费。因网络重试、模型工具调用、缓存读写等产生的用量，以系统依法保存的计量记录为准；您可通过控制台查询。")}</li>
              <li>{t("充值余额、订阅权益、优惠或赠送额度的适用条件以页面说明为准。法律规定或运营者明确承诺可以退款的，按相应规则处理。")}</li>
              <li>{t("价格或计费规则发生调整时，运营者将在生效前以合理方式告知。调整不追溯影响已完成结算的交易，法律法规另有规定的除外。")}</li>
            </LegalList>
          </LegalSection>

          <LegalSection title={t("六、知识产权")}>
            <p>{t("本服务的软件、界面、商标、文档及其他运营者提供内容的知识产权归运营者或相关权利人所有。未经许可，您不得复制、修改、反向工程、出租、出售或制作衍生作品，但开源许可明确允许的除外。")}</p>
            <p>{t("您对合法提交的输入内容保留原有权利。生成内容的权利归属依适用法律、第三方模型规则及您与相关权利人的约定确定；运营者不对生成内容当然主张权利，也不保证其不与他人内容相同或相似。")}</p>
          </LegalSection>

          <LegalSection title={t("七、服务变更、中断与账号处置")}>
            <p>{t("为维护安全、修复故障、升级系统、响应监管要求或第三方服务变化，运营者可以对服务进行维护、调整或暂时中断，并在可行范围内提前通知。")}</p>
            <p>{t("如有合理证据表明您违反本协议或法律法规，或账号存在现实安全风险，运营者可根据风险程度采取警告、限制调用、暂停密钥、冻结账号、停止服务、保存记录并依法报告等措施。除紧急或依法不得通知的情形外，运营者将说明原因并提供申诉渠道。")}</p>
          </LegalSection>

          <LegalSection title={t("八、责任承担与限制")}>
            <p>{t("一方违反本协议或法律法规并给对方造成损失的，应依法承担相应责任。因不可抗力、基础电信故障、第三方服务中断、网络攻击等超出合理控制范围的事件造成服务异常，运营者将在法律要求范围内及时采取补救措施。")}</p>
            <p>{t("任何责任限制均不适用于因故意或重大过失造成的人身损害，不适用于依法应承担的个人信息保护责任、消费者权益保护责任，也不排除法律规定不得限制或免除的其他责任。")}</p>
          </LegalSection>

          <LegalSection title={t("九、协议更新与通知")}>
            <p>{t("因法律法规、监管要求或服务功能变化，运营者可能更新本协议。涉及您重大权益的变更，将通过站内通知、页面提示、电子邮件或其他合理方式告知，并给予合理查看期限。您不同意变更的，可以停止使用并申请注销账号；继续使用表示您接受生效后的协议，但法律另有规定的除外。")}</p>
          </LegalSection>

          <LegalSection title={t("十、终止、注销与数据处理")}>
            <p>{t("您可以通过控制台提供的功能或上述联系邮箱申请注销账号。运营者核验身份并完成未结事项后，将停止提供服务，并按照《隐私政策》及法律规定删除或匿名化处理个人信息；依法需要留存的交易、日志等信息将在法定期限内限制处理。")}</p>
          </LegalSection>

          <LegalSection title={t("十一、法律适用与争议解决")}>
            <p>{t("本协议的订立、履行、解释及争议解决适用中华人民共和国大陆地区法律。发生争议时，双方应先友好协商；协商不成的，任何一方可依法向有管辖权的人民法院提起诉讼。您作为消费者依法享有的投诉、举报和诉讼权利不受影响。")}</p>
          </LegalSection>

          <LegalSection title={t("十二、联系我们")}>
            <p>{t("如您对本协议、费用、账号处置或服务使用有疑问，可通过上述联系邮箱与运营者联系。运营者将在核验必要信息后尽快处理。")}</p>
          </LegalSection>
        </>
      )}
    </LegalLayout>
  );
}

export function PrivacyPolicyPage() {
  return (
    <LegalLayout
      title={t("Micro-One API 隐私政策")}
      summary={t("本政策说明运营者在中国大陆提供服务时如何收集、使用、存储、共享和保护您的个人信息，以及您如何行使个人信息权利。")}
      icon={<ShieldCheck className="size-6" />}
    >
      {(identity) => (
        <>
          <OperatorDetails identity={identity} />

          <LegalSection title={t("引言")}>
            <p>{t("运营者是本服务相关个人信息的个人信息处理者。我们遵循合法、正当、必要、诚信、目的明确、最小必要和公开透明原则处理个人信息。")}</p>
            <p>{t("请特别关注以加粗或单独说明方式提示的敏感个人信息、第三方提供、跨境提供和未成年人信息处理规则。如您代表组织提交他人信息，应确保已取得合法授权并完成必要告知。")}</p>
          </LegalSection>

          <LegalSection title={t("一、我们处理的个人信息及目的")}>
            <div className="overflow-x-auto rounded-xl border border-border">
              <table className="min-w-[680px] w-full border-collapse text-left text-sm">
                <thead className="bg-muted/70 text-foreground">
                  <tr><th className="p-3 font-semibold">{t("使用场景")}</th><th className="p-3 font-semibold">{t("信息种类")}</th><th className="p-3 font-semibold">{t("处理目的与必要性")}</th></tr>
                </thead>
                <tbody className="divide-y divide-border align-top">
                  <tr><td className="p-3">{t("注册与登录")}</td><td className="p-3">{t("用户名、加密保存的密码；您主动填写的显示名称、邮箱；第三方登录提供的账号标识、昵称、头像等授权信息")}</td><td className="p-3">{t("创建账号、身份核验、登录认证、找回或保护账号，履行服务合同")}</td></tr>
                  <tr><td className="p-3">{t("API 与控制台服务")}</td><td className="p-3">{t("用户及密钥标识、选用模型、请求时间、请求状态、Token 用量、缓存用量、响应耗时、流式标记、渠道标识")}</td><td className="p-3">{t("鉴权、路由、计量、账单展示、故障排查和服务质量分析")}</td></tr>
                  <tr><td className="p-3">{t("模型请求处理")}</td><td className="p-3">{t("您提交的提示词、消息、文件、工具参数及模型返回内容")}</td><td className="p-3">{t("完成请求转换并向选定模型服务商转发、接收结果。除实现请求、故障排查或法律另有要求外，运营者不将正文内容作为常规用量日志保存")}</td></tr>
                  <tr><td className="p-3">{t("充值、订阅与账务")}</td><td className="p-3">{t("订单号、用户标识、支付渠道、商品或套餐、金额、币种、订单状态、支付时间、支付机构交易号及必要回调信息")}</td><td className="p-3">{t("创建和核验订单、交付权益、对账、退款、开票及履行财税义务；其中支付账号和交易信息可能属于敏感个人信息")}</td></tr>
                  <tr><td className="p-3">{t("安全与运维")}</td><td className="p-3">{t("IP 地址、访问时间、请求路径、请求 ID、User-Agent、错误与安全事件日志")}</td><td className="p-3">{t("防范攻击、识别异常调用、审计、排障及履行网络安全保护义务")}</td></tr>
                  <tr><td className="p-3">{t("联系与权利请求")}</td><td className="p-3">{t("联系邮箱、问题描述、证明账号归属所必需的信息及沟通记录")}</td><td className="p-3">{t("响应咨询、投诉和个人信息权利请求")}</td></tr>
                </tbody>
              </table>
            </div>
            <p>{t("我们主要基于订立、履行您作为一方当事人的合同所必需，以及履行网络安全、会计、税务等法定义务处理上述信息。对于依法应取得同意、单独同意或书面同意的处理活动，我们会另行征得您的相应同意。")}</p>
          </LegalSection>

          <LegalSection title={t("二、本地存储与类似技术")}>
            <p>{t("控制台使用浏览器 localStorage 保存登录令牌、用户标识、角色及语言、主题等偏好，以维持登录状态和提供界面设置。当前版本不以跨站广告或用户画像为目的使用 Cookie。您可通过退出登录或清除浏览器网站数据移除本地信息；清除登录令牌后需要重新登录。")}</p>
          </LegalSection>

          <LegalSection title={t("三、委托处理、共享与第三方服务")}>
            <p>{t("我们不会出售您的个人信息。为提供服务，信息可能在以下必要范围内由第三方处理：")}</p>
            <LegalList>
              <li><strong className="text-foreground">{t("模型服务商：")}</strong>{t("根据您选择的模型和运营者配置的路由，将提示词、文件、工具参数及必要技术信息发送给相应上游模型服务商；")}</li>
              <li><strong className="text-foreground">{t("支付机构：")}</strong>{t("使用在线支付时，将订单号、金额等必要交易信息提供给支付机构，并接收其支付结果；")}</li>
              <li><strong className="text-foreground">{t("第三方登录服务：")}</strong>{t("当您主动选择 GitHub、Google、飞书、微信或运营者配置的 OIDC 登录时，与相应服务交换认证所需信息；")}</li>
              <li><strong className="text-foreground">{t("基础设施服务商：")}</strong>{t("服务器、数据库、网络、安全防护、邮件或消息通知服务商可能受托处理运行服务所必需的信息。")}</li>
            </LegalList>
            <p>{t("实际启用的第三方由部署方配置，并可能随可用模型或功能变化。运营者将依法与受托方约定处理目的、期限、方式、信息种类和保护措施，并对其处理活动进行监督。向其他个人信息处理者提供个人信息或变更处理目的时，我们将依法另行告知并取得单独同意。")}</p>
          </LegalSection>

          <LegalSection title={t("四、个人信息跨境提供")}>
            <p><strong className="text-foreground">{t("部分境外模型服务商、第三方登录服务或其服务器可能位于中国大陆境外，因此您的模型请求内容、账号授权信息或必要技术数据可能涉及跨境提供。")}</strong></p>
            <p>{t("部署方在启用涉及个人信息出境的模型或登录服务前，必须按照法律要求另行告知境外接收方名称和联系方式、处理目的和方式、信息种类以及您行使权利的方式，并取得您的单独同意；同时根据适用情形完成个人信息出境安全评估、标准合同备案、个人信息保护认证或其他法定程序。")}<strong className="text-foreground">{t("注册时对本政策的一般同意不替代个人信息出境的单独同意。")}</strong>{t("未完成上述条件时，部署方不应启用相应境外服务。")}</p>
          </LegalSection>

          <LegalSection title={t("五、保存地点与期限")}>
            <LegalList>
              <li>{t("在中国大陆境内收集和产生的个人信息原则上存储于中国大陆境内；依法完成跨境提供条件的情形除外。")}</li>
              <li>{t("账号资料在账号存续期间保存；账号注销后，在处理目的已实现且无法律留存要求时删除或匿名化。")}</li>
              <li>{t("交易、会计和税务资料按相关法律规定的期限保存；安全日志按网络安全等法律规定及实现安全目的所需的最短期限保存。")}</li>
              <li>{t("模型请求正文原则上仅在完成转发和返回结果所需期间临时处理；因故障排查、安全事件或您主动提交工单需要保存的，将限制访问并在目的实现后删除。")}</li>
              <li>{t("依法向您提供不含额外显式标识的人工智能生成合成内容时，提供对象信息及相关日志保存期限不少于六个月。")}</li>
            </LegalList>
            <p>{t("法律规定的保存期限尚未届满，或删除在技术上难以实现时，我们将停止除存储和采取必要安全保护措施之外的处理。")}</p>
          </LegalSection>

          <LegalSection title={t("六、您的个人信息权利")}>
            <p>{t("在法律规定范围内，您有权知情、决定、限制或拒绝处理，并可请求查阅、复制、更正、补充、删除个人信息，撤回基于同意作出的授权，要求解释处理规则，以及申请注销账号。符合国家网信部门规定条件的，您还可请求转移个人信息。")}</p>
            <p>{t("您可优先通过控制台管理可直接修改的信息，或发送请求至上述联系邮箱。为保护账号安全，我们可能核验您的身份，并通常在十五个工作日内答复；情况复杂需要延长或法律另有期限要求的，我们会告知原因。拒绝请求时，我们将说明理由，您可向履行个人信息保护职责的部门投诉、举报或依法向人民法院提起诉讼。")}</p>
          </LegalSection>

          <LegalSection title={t("七、账号注销")}>
            <p>{t("您可通过控制台提供的注销功能或上述联系邮箱提交申请。注销前需完成身份核验并处理未支付订单、退款、争议等未结事项。注销完成后，我们将停止提供服务，并依法删除或匿名化您的个人信息；法律法规要求继续保存的除外。")}</p>
          </LegalSection>

          <LegalSection title={t("八、信息安全")}>
            <p>{t("我们根据处理目的、信息种类和风险采取访问控制、身份认证、传输保护、加密或去标识化、日志审计、备份恢复、最小权限和安全事件应急等措施，防止未经授权的访问以及泄露、篡改、丢失。")}</p>
            <p>{t("发生或可能发生个人信息安全事件时，我们将立即采取补救措施，并按照法律要求向主管部门和受影响个人通知事件种类、原因、可能危害、已采取措施、个人可采取的措施及联系方式。")}</p>
          </LegalSection>

          <LegalSection title={t("九、敏感个人信息")}>
            <p>{t("支付交易信息、精确身份凭证以及您主动提交的其他敏感个人信息一旦泄露或被非法使用，可能对人身或财产安全造成危害。我们仅在具有特定目的和充分必要性、采取严格保护措施并依法取得单独同意后处理。请勿在模型请求中输入与使用目的无关的敏感个人信息。")}</p>
          </LegalSection>

          <LegalSection title={t("十、未成年人保护")}>
            <p>{t("本服务不面向不满十四周岁的未成年人开放独立注册。若我们发现未经监护人同意处理了不满十四周岁未成年人的个人信息，将依法尽快删除或采取其他必要措施。十四周岁以上未满十八周岁的未成年人应在监护人指导下使用服务。监护人可通过上述联系邮箱与我们联系。")}</p>
          </LegalSection>

          <LegalSection title={t("十一、政策更新")}>
            <p>{t("我们可能因法律法规、监管要求、服务功能或处理活动变化更新本政策。涉及处理目的、方式、信息种类或您的重大权益发生实质变化时，我们将以显著方式告知；依法需要重新取得同意的，将在变更后的处理开始前征得您的同意。")}</p>
          </LegalSection>

          <LegalSection title={t("十二、联系我们与投诉")}>
            <p>{t("如需行使个人信息权利，或对本政策有疑问、意见或投诉，请通过上述联系邮箱联系运营者。您也有权向国家网信部门、市场监督管理部门等有权机关投诉或举报。")}</p>
          </LegalSection>
        </>
      )}
    </LegalLayout>
  );
}
