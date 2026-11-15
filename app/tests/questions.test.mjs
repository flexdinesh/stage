import assert from "node:assert/strict";
import { test } from "node:test";
import {
  answerText,
  answeredCount,
  buildAnswers,
  initialDraft,
  isSelected,
  optionAnswer,
  resolveAnswer,
  setCustom,
  toggleOption,
} from "../src/lib/questions.ts";

const single = {
  id: "scope",
  header: "Scope",
  question: "Which scope?",
  options: [
    { label: " Server ", description: "Go only" },
    { label: "Web", description: "UI", value: "web-value" },
  ],
};
const multi = {
  id: "extras",
  header: "Extras",
  question: "Extras?",
  multiSelect: true,
  options: [
    { label: "Docs", description: "" },
    { label: "Tests", description: "" },
  ],
};
const strict = { id: "strict", header: "", question: "Yes?", allowCustomAnswer: false, options: [{ label: "Yes", description: "" }] };
const prefilled = { id: "notes", header: "Notes", question: "Anything else?", initialAnswer: "Ship behind a flag", options: [] };

test("option answers use the value, else the trimmed label", () => {
  assert.equal(optionAnswer(single.options[0]), "Server");
  assert.equal(optionAnswer(single.options[1]), "web-value");
  assert.equal(optionAnswer({ label: "X", description: "", value: null }), "X");
});

test("single select picks one option and clears custom text", () => {
  let d = setCustom(single, initialDraft(single), "my own");
  assert.equal(resolveAnswer(single, d), "my own");
  d = toggleOption(single, d, "web-value");
  assert.deepEqual(d, { selected: ["web-value"], custom: "" });
  assert.equal(resolveAnswer(single, d), "web-value");
  d = toggleOption(single, d, "Server");
  assert.equal(resolveAnswer(single, d), "Server");
  assert.equal(isSelected(single, d, "web-value"), false);
  assert.deepEqual(toggleOption(single, d, "unknown"), d);
});

test("custom text wins over a selection and clears it", () => {
  let d = toggleOption(single, initialDraft(single), "Server");
  d = setCustom(single, d, "  typed  ");
  assert.deepEqual(d.selected, []);
  assert.equal(isSelected(single, d, "Server"), false);
  assert.equal(resolveAnswer(single, d), "typed");
  // Blank custom text keeps the selection.
  d = setCustom(single, toggleOption(single, initialDraft(single), "Server"), "   ");
  assert.equal(resolveAnswer(single, d), "Server");
});

test("multi select answers an array in selection order", () => {
  let d = toggleOption(multi, initialDraft(multi), "Tests");
  d = toggleOption(multi, d, "Docs");
  assert.deepEqual(resolveAnswer(multi, d), ["Tests", "Docs"]);
  d = toggleOption(multi, d, "Tests");
  assert.deepEqual(resolveAnswer(multi, d), ["Docs"]);
  d = toggleOption(multi, d, "Docs");
  assert.equal(resolveAnswer(multi, d), null);
  assert.equal(resolveAnswer(multi, setCustom(multi, d, "neither")), "neither");
});

test("custom answers are refused when the question disallows them", () => {
  const d = setCustom(strict, initialDraft(strict), "maybe");
  assert.equal(d.custom, "");
  assert.equal(resolveAnswer(strict, d), null);
  assert.equal(resolveAnswer(strict, toggleOption(strict, d, "Yes")), "Yes");
});

test("initialAnswer prefills the custom answer and is sent exactly", () => {
  const d = initialDraft(prefilled);
  assert.equal(d.custom, "Ship behind a flag");
  assert.equal(resolveAnswer(prefilled, d), "Ship behind a flag");
  assert.equal(resolveAnswer(prefilled, setCustom(prefilled, d, " keep spaces ")), " keep spaces ");
});

test("buildAnswers needs every question answered", () => {
  const qs = [single, multi, strict];
  const drafts = { scope: toggleOption(single, initialDraft(single), "Server") };
  assert.equal(buildAnswers(qs, drafts), null);
  assert.equal(answeredCount(qs, drafts), 1);
  drafts.extras = toggleOption(multi, initialDraft(multi), "Docs");
  drafts.strict = toggleOption(strict, initialDraft(strict), "Yes");
  assert.deepEqual(buildAnswers(qs, drafts), { scope: "Server", extras: ["Docs"], strict: "Yes" });
  assert.equal(answeredCount(qs, drafts), 3);
  // A prefilled question counts as answered without a draft.
  assert.deepEqual(buildAnswers([prefilled], {}), { notes: "Ship behind a flag" });
});

test("answerText shows labels for options and the text for custom answers", () => {
  assert.equal(answerText(single, "web-value"), "Web");
  assert.equal(answerText(multi, ["Tests", "Docs"]), "Tests, Docs");
  assert.equal(answerText(single, "my own"), "my own");
});
