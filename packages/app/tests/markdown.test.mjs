import assert from "node:assert/strict";
import { test } from "node:test";
import { allowedUri, markdownToHtml } from "../src/lib/markdown.ts";

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

test("markdownToHtml autolinks bare stage deep links", () => {
  const html = markdownToHtml("Next: stage://tasks/12. Also stage-dev://sessions/t-1 and [s](stage://tasks/3).");
  assert.match(html, /<a href="stage:\/\/tasks\/12">stage:\/\/tasks\/12<\/a>\./);
  assert.match(html, /<a href="stage-dev:\/\/sessions\/t-1">/);
  assert.match(html, /<a href="stage:\/\/tasks\/3">s<\/a>/);
});


test("sanitizer enables declared harness links and keeps web and Stage links", () => {
  const base = allowedUri();
  const declared = allowedUri(["t3code", "desktop+links", "app.code"]);
  for (const ok of ["https://x.dev/a", "mailto:a@b.c", "tel:12", "stage://tasks/1", "stage-dev://sessions/x", "/tasks/1", "#top", "issue1/log"]) {
    assert.ok(base.test(ok), ok);
    assert.ok(declared.test(ok), ok);
  }
  for (const app of ["t3code://threads/e/t", "desktop+links://x", "app.code://x"]) {
    assert.equal(base.test(app), false, app);
    assert.equal(declared.test(app), true, app);
  }
  assert.equal(declared.test("appxcode://x"), false);
});

test("sanitizer rejects script URLs even when a scheme is supplied", () => {
  for (const bad of ["javascript:alert(1)", " Javascript:x", "data:text/html,x", "vbscript:x", "mygo://localhost/x", "file:///etc/passwd", "about:blank", "blob:x", "other2:x"]) {
    assert.equal(allowedUri(["javascript", "data", "vbscript", "file", "about", "blob"]).test(bad), false, bad);
  }
});
