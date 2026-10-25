<script setup lang="ts">
import { computed } from "vue";

type InlineToken =
  | { type: "text"; text: string }
  | { type: "strong"; text: string }
  | { type: "code"; text: string };

type MarkdownBlock =
  | { type: "heading"; level: number; content: InlineToken[] }
  | { type: "paragraph"; content: InlineToken[] }
  | { type: "ul"; items: InlineToken[][] }
  | { type: "ol"; items: InlineToken[][] }
  | { type: "code"; code: string; language: string };

const props = defineProps<{
  content: string;
}>();

const blocks = computed(() => parseMarkdown(props.content));

function parseMarkdown(markdown: string): MarkdownBlock[] {
  const lines = markdown.replace(/\r\n/g, "\n").split("\n");
  const result: MarkdownBlock[] = [];
  let paragraph: string[] = [];
  let list: { type: "ul" | "ol"; items: string[] } | null = null;
  let codeBlock: { language: string; lines: string[] } | null = null;

  function flushParagraph(): void {
    if (!paragraph.length) {
      return;
    }
    result.push({
      type: "paragraph",
      content: parseInline(paragraph.join("\n"))
    });
    paragraph = [];
  }

  function flushList(): void {
    if (!list) {
      return;
    }
    result.push({
      type: list.type,
      items: list.items.map((item) => parseInline(item))
    });
    list = null;
  }

  for (const line of lines) {
    const fence = line.match(/^```(\S*)\s*$/);
    if (fence) {
      if (codeBlock) {
        result.push({
          type: "code",
          language: codeBlock.language,
          code: codeBlock.lines.join("\n")
        });
        codeBlock = null;
      } else {
        flushParagraph();
        flushList();
        codeBlock = {
          language: fence[1] ?? "",
          lines: []
        };
      }
      continue;
    }

    if (codeBlock) {
      codeBlock.lines.push(line);
      continue;
    }

    if (!line.trim()) {
      flushParagraph();
      flushList();
      continue;
    }

    const heading = line.match(/^(#{1,6})\s+(.+)$/);
    if (heading) {
      flushParagraph();
      flushList();
      result.push({
        type: "heading",
        level: heading[1].length,
        content: parseInline(heading[2])
      });
      continue;
    }

    const unordered = line.match(/^\s*[-*]\s+(.+)$/);
    if (unordered) {
      flushParagraph();
      if (list?.type !== "ul") {
        flushList();
        list = { type: "ul", items: [] };
      }
      list.items.push(unordered[1]);
      continue;
    }

    const ordered = line.match(/^\s*\d+[.)]\s+(.+)$/);
    if (ordered) {
      flushParagraph();
      if (list?.type !== "ol") {
        flushList();
        list = { type: "ol", items: [] };
      }
      list.items.push(ordered[1]);
      continue;
    }

    flushList();
    paragraph.push(line);
  }

  if (codeBlock) {
    result.push({
      type: "code",
      language: codeBlock.language,
      code: codeBlock.lines.join("\n")
    });
  }
  flushParagraph();
  flushList();
  return result;
}

function parseInline(text: string): InlineToken[] {
  const tokens: InlineToken[] = [];
  const pattern = /(`[^`]+`|\*\*[^*]+\*\*)/g;
  let lastIndex = 0;
  let match: RegExpExecArray | null;

  while ((match = pattern.exec(text)) !== null) {
    if (match.index > lastIndex) {
      tokens.push({ type: "text", text: text.slice(lastIndex, match.index) });
    }

    const value = match[0];
    if (value.startsWith("`")) {
      tokens.push({ type: "code", text: value.slice(1, -1) });
    } else {
      tokens.push({ type: "strong", text: value.slice(2, -2) });
    }
    lastIndex = match.index + value.length;
  }

  if (lastIndex < text.length) {
    tokens.push({ type: "text", text: text.slice(lastIndex) });
  }
  return tokens;
}
</script>

<template>
  <div class="markdown-text">
    <template v-for="(block, blockIndex) in blocks" :key="blockIndex">
      <div
        v-if="block.type === 'heading'"
        class="markdown-text__heading"
        :class="`markdown-text__heading--${block.level}`"
      >
        <template v-for="(token, tokenIndex) in block.content" :key="tokenIndex">
          <code v-if="token.type === 'code'" class="markdown-text__inline-code">{{ token.text }}</code>
          <strong v-else-if="token.type === 'strong'" class="markdown-text__strong">{{ token.text }}</strong>
          <template v-else>{{ token.text }}</template>
        </template>
      </div>

      <p v-else-if="block.type === 'paragraph'" class="markdown-text__paragraph">
        <template v-for="(token, tokenIndex) in block.content" :key="tokenIndex">
          <code v-if="token.type === 'code'" class="markdown-text__inline-code">{{ token.text }}</code>
          <strong v-else-if="token.type === 'strong'" class="markdown-text__strong">{{ token.text }}</strong>
          <template v-else>{{ token.text }}</template>
        </template>
      </p>

      <ul v-else-if="block.type === 'ul'" class="markdown-text__list">
        <li v-for="(item, itemIndex) in block.items" :key="itemIndex">
          <template v-for="(token, tokenIndex) in item" :key="tokenIndex">
            <code v-if="token.type === 'code'" class="markdown-text__inline-code">{{ token.text }}</code>
            <strong v-else-if="token.type === 'strong'" class="markdown-text__strong">{{ token.text }}</strong>
            <template v-else>{{ token.text }}</template>
          </template>
        </li>
      </ul>

      <ol v-else-if="block.type === 'ol'" class="markdown-text__list markdown-text__ordered">
        <li v-for="(item, itemIndex) in block.items" :key="itemIndex">
          <template v-for="(token, tokenIndex) in item" :key="tokenIndex">
            <code v-if="token.type === 'code'" class="markdown-text__inline-code">{{ token.text }}</code>
            <strong v-else-if="token.type === 'strong'" class="markdown-text__strong">{{ token.text }}</strong>
            <template v-else>{{ token.text }}</template>
          </template>
        </li>
      </ol>

      <pre v-else class="markdown-text__code"><code>{{ block.code }}</code></pre>
    </template>
  </div>
</template>

<style scoped>
.markdown-text {
  display: grid;
  gap: 0.6rem;
  overflow-wrap: anywhere;
  white-space: normal;
}

.markdown-text__paragraph {
  white-space: pre-wrap;
}

.markdown-text__heading {
  color: inherit;
  font-weight: 900;
  line-height: 1.45;
}

.markdown-text__heading--1,
.markdown-text__heading--2 {
  font-size: 1.05em;
}

.markdown-text__heading--3,
.markdown-text__heading--4,
.markdown-text__heading--5,
.markdown-text__heading--6 {
  font-size: 1em;
}

.markdown-text__strong {
  font-weight: 900;
}

.markdown-text__inline-code {
  border: 1px solid color-mix(in srgb, currentColor 22%, transparent);
  border-radius: 0.4rem;
  background: color-mix(in srgb, currentColor 10%, transparent);
  padding: 0.08rem 0.32rem;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", monospace;
  font-size: 0.92em;
}

.markdown-text__list {
  display: grid;
  gap: 0.35rem;
  padding-left: 1.2rem;
  list-style: disc;
}

.markdown-text__ordered {
  list-style: decimal;
}

.markdown-text__code {
  max-width: 100%;
  overflow-x: auto;
  border: 1px solid color-mix(in srgb, currentColor 18%, transparent);
  border-radius: 0.75rem;
  background: rgba(0, 0, 0, 0.22);
  padding: 0.75rem;
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", monospace;
  font-size: 0.92em;
  line-height: 1.6;
  white-space: pre;
}
</style>
