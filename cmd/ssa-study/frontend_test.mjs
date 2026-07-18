import assert from "node:assert/strict";
import test from "node:test";

import { cfgEdges, gradePrediction, recordAttempt } from "./web/model.js";

test("gradePrediction gives retry feedback without exposing the answer", () => {
  const quiz = { answer: 1, correct: "yes", incorrect: "try again" };
  assert.deepEqual(gradePrediction(quiz, 0), { correct: false, feedback: "try again" });
  assert.deepEqual(gradePrediction(quiz, 1), { correct: true, feedback: "yes" });
});

test("recordAttempt increments attempts and completes only correct lessons", () => {
  const initial = { version: 1, completed: [], attempts: {} };
  const wrong = recordAttempt(initial, "precedence", false);
  assert.deepEqual(wrong, { version: 1, completed: [], attempts: { precedence: 1 } });
  const right = recordAttempt(wrong, "precedence", true);
  assert.deepEqual(right, { version: 1, completed: ["precedence"], attempts: { precedence: 2 } });
  assert.deepEqual(initial, { version: 1, completed: [], attempts: {} });
});

test("cfgEdges flattens successor relationships with branch labels", () => {
  const edges = cfgEdges([
    { index: 0, succs: [1, 2] },
    { index: 1, succs: [3] },
    { index: 2, succs: [3] },
    { index: 3, succs: [] },
  ]);
  assert.deepEqual(edges, [
    { from: 0, to: 1, label: "true" },
    { from: 0, to: 2, label: "false" },
    { from: 1, to: 3, label: "" },
    { from: 2, to: 3, label: "" },
  ]);
});

test("cfgEdges treats JSON null successor slices as empty", () => {
  assert.deepEqual(cfgEdges([{ index: 0, succs: null }]), []);
});
