import assert from "node:assert/strict";
import { test } from "node:test";
import { markdownToHtml } from "../src/lib/markdown.ts";

test("markdownToHtml renders GFM blocks", () => {
  const html = markdownToHtml("## Done\n\n- one\n- two\n\n```go\nx := 1\n```\n\n| a | b |\n|---|---|\n| 1 | 2 |\n");
  assert.match(html, /<h2>Done<\/h2>/);
  assert.match(html, /<ul>\s*<li>one<\/li>\s*<li>two<\/li>\s*<\/ul>/);
  assert.match(html, /<pre><code class="language-go">x := 1\n<\/code><\/pre>/);
  assert.match(html, /<table>/);
});

test("markdownToHtml renders images as links, never <img>", () => {
  const html = markdownToHtml('![chart](https://example.com/a.png) and ![](https://example.com/"b".png)');
  assert.doesNotMatch(html, /<img/);
  assert.match(html, /<a href="https:\/\/example.com\/a.png">image: chart<\/a>/);
  assert.match(html, /<a href="https:\/\/example.com\/(&quot;|%22)b(&quot;|%22).png">/);
});

test("markdownToHtml renders raw HTML as literal text", () => {
  const html = markdownToHtml('Use <Panel> here.\n\n<script>alert(1)</script>\n\n<img src=x onerror="alert(1)">');
  assert.doesNotMatch(html, /<script|<img|<Panel/);
  assert.match(html, /&lt;Panel&gt;/);
  assert.match(html, /&lt;script&gt;alert\(1\)&lt;\/script&gt;/);
});
