/**
 * 使用条款页面 — 公开访问，无需登录
 */
import { ArrowLeft } from "lucide-react";
import { Link } from "react-router-dom";
import { BrandIcon } from "@/components/brand-icon";

export function TermsPage() {
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
          <BrandIcon size="sm" />
          <span className="text-sm font-medium">使用条款</span>
        </div>
      </header>

      <article className="prose-article mx-auto max-w-3xl px-6 py-12">
        <h1>使用条款</h1>
        <p className="text-sm text-muted-foreground">最后更新：2026 年 8 月 23 日</p>

        <h2>1. 服务说明</h2>
        <p>
          笔润智谈（以下简称"本服务"）是一款基于人工智能技术的写作辅助工具，由本团队运营和维护。使用本服务即表示你同意本条款的所有内容。
        </p>

        <h2>2. 账号注册</h2>
        <p>
          你需要注册账号才能使用完整功能。注册时应提供真实、准确的信息，并对账号及密码的安全负责。因账号信息泄露造成的损失由你自行承担。
        </p>

        <h2>3. 使用规范</h2>
        <p>使用本服务时，你不得：</p>
        <ul>
          <li>利用本服务从事任何违反法律法规的活动；</li>
          <li>上传或传播侵犯他人知识产权的内容；</li>
          <li>尝试破坏系统安全或干扰其他用户的使用；</li>
          <li>以自动化方式大量调用接口，影响服务稳定性。</li>
        </ul>

        <h2>4. 内容权利</h2>
        <p>
          你通过本服务生成的写作内容归你所有。本服务不会在未经你同意的情况下将你的内容用于其他商业用途。
        </p>

        <h2>5. 服务变更与终止</h2>
        <p>
          本团队保留随时修改、暂停或终止部分或全部服务的权利。如你违反本条款，本团队有权限制或终止你的账号访问。
        </p>

        <h2>6. 免责声明</h2>
        <p>
          本服务基于 AI 模型生成内容，可能存在不准确或不恰当之处。你应对生成内容进行独立判断，本团队不对生成内容的准确性、完整性承担责任。
        </p>

        <h2>7. 条款修改</h2>
        <p>
          本条款可能不时更新，更新后将在本页面公布。继续使用本服务即视为你同意修改后的条款。
        </p>

        <h2>8. 联系我们</h2>
        <p>如对本条款有任何疑问，请通过以下方式与我们联系：</p>
        <ul>
          <li><strong>邮箱</strong>：luminbuddy@ericdocmic.top</li>
          <li><strong>产品内反馈</strong>：通过个人中心的反馈渠道提交意见。</li>
        </ul>
      </article>
    </div>
  );
}
