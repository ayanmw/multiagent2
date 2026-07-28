// Markdown 渲染工具：用 markdown-it 解析，DOMPurify 清洗后返回安全 HTML。
// 对话助手的回复统一经此渲染，避免原始 HTML/XSS 风险。
import MarkdownIt from 'markdown-it'
import DOMPurify from 'dompurify'

const md = new MarkdownIt({
  html: false, // 不允许内嵌原始 HTML，降低注入面
  linkify: true, // 自动识别链接
  breaks: true, // 单换行视为 <br>
})

// 将 Markdown 文本渲染为清洗后的 HTML 字符串，供 v-html 使用。
export function renderMarkdown(src: string): string {
  const raw = md.render(src ?? '')
  return DOMPurify.sanitize(raw)
}
