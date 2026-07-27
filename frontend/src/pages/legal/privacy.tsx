/**
 * 隐私政策页面 — 公开访问，无需登录
 */
import { ArrowLeft } from "lucide-react";
import { Link } from "react-router-dom";

export function PrivacyPage() {
  return (
    <div className="min-h-screen bg-background">
      <header className="border-b">
        <div className="mx-auto flex max-w-3xl items-center gap-3 px-6 py-4">
          <Link
            to="/write"
            className="flex h-8 w-8 items-center justify-center rounded-lg hover:bg-accent transition-ui"
          >
            <ArrowLeft className="h-4 w-4" />
          </Link>
          <div className="flex h-8 w-8 items-center justify-center rounded-xl bg-brand-gradient shadow-sm">
            <span className="text-sm font-bold text-white">笔</span>
          </div>
          <span className="text-sm font-medium">隐私政策</span>
        </div>
      </header>

      <article className="prose-article mx-auto max-w-3xl px-6 py-12">
        <h1>隐私政策</h1>
        <p className="text-sm text-muted-foreground">最后更新：2025 年 7 月 21 日</p>

        <h2>1. 信息收集</h2>
        <p>本服务收集以下类型的信息：</p>
        <ul>
          <li><strong>账号信息</strong>：注册时提供的用户名和加密后的密码。</li>
          <li><strong>写作内容</strong>：你输入的写作素材和 AI 生成的文章内容。</li>
          <li><strong>使用记录</strong>：会话历史、反馈数据、写作偏好（记忆功能）。</li>
          <li><strong>技术信息</strong>：浏览器类型、访问时间等用于服务运行的必要日志。</li>
        </ul>

        <h2>2. 信息使用</h2>
        <p>收集的信息用于：</p>
        <ul>
          <li>提供和改进写作辅助功能；</li>
          <li>保存你的写作历史和个性化偏好；</li>
          <li>维护账号安全和服务稳定性；</li>
          <li>分析服务使用情况以优化产品体验。</li>
        </ul>

        <h2>3. 信息存储与安全</h2>
        <p>
          你的密码使用 bcrypt 算法加密存储，写作数据存储在服务端数据库中。我们采取合理的技术和管理措施保护你的信息安全，但无法保证绝对安全。
        </p>

        <h2>4. 信息共享</h2>
        <p>
          本服务不会将你的个人信息出售或出租给第三方。在以下情况下可能共享信息：
        </p>
        <ul>
          <li>获得你的明确同意；</li>
          <li>法律法规要求或政府主管部门强制要求；</li>
          <li>为维护本服务的合法权益（如防止欺诈）。</li>
        </ul>

        <h2>5. AI 处理说明</h2>
        <p>
          本服务使用大语言模型处理你的写作输入。输入内容将发送至 AI 模型进行推理生成，模型服务商可能短暂缓存请求用于服务运行，但不会用于训练其模型。
        </p>

        <h2>6. Cookie 与本地存储</h2>
        <p>
          本服务使用 localStorage 存储登录令牌（JWT）和用户偏好设置（如主题模式）。这些数据仅存储在你的浏览器本地，不会跨设备同步。
        </p>

        <h2>7. 你的权利</h2>
        <p>你有权：</p>
        <ul>
          <li>访问和查看你的个人信息；</li>
          <li>删除你的写作历史和会话记录；</li>
          <li>注销账号并清除关联数据；</li>
          <li>退出登录以清除本地存储的令牌。</li>
        </ul>

        <h2>8. 未成年人保护</h2>
        <p>
          本服务不面向 13 岁以下的未成年人。如发现未成年人未经监护人同意使用本服务，我们将采取措施删除相关账号信息。
        </p>

        <h2>9. 政策更新</h2>
        <p>
          本政策可能不时更新，更新后将在本页面公布。继续使用本服务即视为你同意修改后的隐私政策。
        </p>

        <h2>10. 联系我们</h2>
        <p>如对本隐私政策有任何疑问，请通过产品内反馈渠道与我们联系。</p>
      </article>
    </div>
  );
}
