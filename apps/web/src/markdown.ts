import DOMPurify from "dompurify";
import { marked } from "marked";

marked.setOptions({
  breaks: true,
  gfm: true,
});

export function renderMarkdown(markdown: string): string {
  return DOMPurify.sanitize(marked.parse(markdown) as string, {
    USE_PROFILES: { html: true },
  });
}
