import { renderAST, renderDiagnostics, renderSSA, renderTokens, renderTypes } from "/renderers.js";
import { gradePrediction, recordAttempt } from "/model.js";

const STORAGE_KEY = "ssa-study-progress-v1";
const stages = ["tokens", "ast", "types", "ssa"];
const state = {
  lessons: [],
  lesson: null,
  freeLab: false,
  stage: "tokens",
  result: null,
  selectedRange: null,
  selectedAST: -1,
  selectedFunction: "",
  requestSequence: 0,
  timer: 0,
  progress: loadProgress(),
  quizState: { selected: -1, feedback: "", locked: false, correct: false },
};

const ui = {
  lessonList: document.getElementById("lesson-list"),
  progressCount: document.getElementById("progress-count"),
  freeLab: document.getElementById("free-lab"),
  kicker: document.getElementById("lesson-kicker"),
  title: document.getElementById("lesson-title"),
  explanation: document.getElementById("lesson-explanation"),
  editor: document.getElementById("source-editor"),
  mirror: document.querySelector("#source-highlight code"),
  selection: document.getElementById("source-selection"),
  reset: document.getElementById("reset-source"),
  tabs: document.getElementById("stage-tabs"),
  output: document.getElementById("stage-output"),
  diagnostics: document.getElementById("diagnostics"),
  quiz: document.getElementById("quiz-panel"),
  status: document.getElementById("analysis-status"),
  traceStage: document.getElementById("trace-stage-label"),
};

boot();

async function boot() {
  bindEvents();
  try {
    const response = await fetch("/api/lessons");
    if (!response.ok) throw new Error(`lesson request returned ${response.status}`);
    state.lessons = await response.json();
    const first = state.lessons.find((lesson) => !state.progress.completed.includes(lesson.id)) || state.lessons[0];
    loadLesson(first.id);
  } catch (error) {
    setStatus(`Could not load lessons: ${error.message}`, "error");
    ui.output.replaceChildren(emptyMessage("The local lesson catalog did not load."));
  }
}

function bindEvents() {
  ui.editor.addEventListener("input", () => {
    state.selectedRange = null;
    state.selectedAST = -1;
    paintSource();
    scheduleAnalysis();
  });
  ui.editor.addEventListener("scroll", () => {
    const host = ui.mirror.parentElement;
    host.scrollTop = ui.editor.scrollTop;
    host.scrollLeft = ui.editor.scrollLeft;
  });
  ui.tabs.addEventListener("click", (event) => {
    const button = event.target.closest("button[data-stage]");
    if (button) chooseStage(button.dataset.stage);
  });
  ui.reset.addEventListener("click", resetLesson);
  ui.freeLab.addEventListener("click", enterFreeLab);
}

function renderLessonRail() {
  ui.lessonList.replaceChildren();
  for (const lesson of state.lessons) {
    const button = document.createElement("button");
    button.type = "button";
    button.className = "lesson-button";
    if (!state.freeLab && state.lesson?.id === lesson.id) button.setAttribute("aria-current", "step");
    const number = span("lesson-number", String(lesson.number).padStart(2, "0"));
    const name = span("lesson-name", lesson.title);
    const done = span("lesson-check", state.progress.completed.includes(lesson.id) ? "✓" : "");
    done.setAttribute("aria-label", state.progress.completed.includes(lesson.id) ? "Complete" : "Not complete");
    button.append(number, name, done);
    button.addEventListener("click", () => loadLesson(lesson.id));
    ui.lessonList.append(button);
  }
  ui.progressCount.textContent = String(state.progress.completed.length);
  ui.freeLab.setAttribute("aria-pressed", state.freeLab ? "true" : "false");
}

function loadLesson(id) {
  const lesson = state.lessons.find((candidate) => candidate.id === id);
  if (!lesson) return;
  state.lesson = lesson;
  state.freeLab = false;
  state.stage = lesson.stage;
  state.selectedFunction = "";
  const completed = state.progress.completed.includes(lesson.id);
  state.quizState = { selected: completed ? lesson.quiz.answer : -1, feedback: completed ? lesson.quiz.correct : "", locked: completed, correct: completed };
  ui.kicker.textContent = `Lesson ${lesson.number} / ${lesson.kicker}`;
  ui.title.textContent = lesson.title;
  ui.explanation.textContent = lesson.explanation;
  ui.freeLab.setAttribute("aria-pressed", "false");
  setSource(lesson.source);
  renderLessonRail();
  renderQuiz();
  chooseStage(lesson.stage);
}

function enterFreeLab() {
  state.freeLab = true;
  ui.kicker.textContent = "Free lab / arbitrary single file";
  ui.title.textContent = "Ask the pipeline a concrete question";
  ui.explanation.textContent = "Edit the source, then compare stages. Analysis is local and stops before execution; imports use your installed Go toolchain.";
  ui.quiz.replaceChildren();
  renderLessonRail();
  scheduleAnalysis(0);
}

function setSource(source) {
  ui.editor.value = source;
  state.selectedRange = null;
  state.selectedAST = -1;
  paintSource();
  scheduleAnalysis(0);
}

function scheduleAnalysis(delay = 280) {
  window.clearTimeout(state.timer);
  const sequence = ++state.requestSequence;
  state.timer = window.setTimeout(() => analyze(sequence), delay);
}

async function analyze(sequence) {
  setStatus("Analyzing current source…", "busy");
  ui.output.setAttribute("aria-busy", "true");
  try {
    const response = await fetch("/api/analyze", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ source: ui.editor.value }),
    });
    const payload = await response.json();
    if (sequence !== state.requestSequence) return;
    if (!response.ok) throw new Error(payload.error || `analysis returned ${response.status}`);
    state.result = payload;
    renderDiagnostics(ui.diagnostics, payload.diagnostics || []);
    renderStage();
    const suffix = payload.diagnostics?.length ? ` with ${payload.diagnostics.length} diagnostic${payload.diagnostics.length === 1 ? "" : "s"}` : "";
    setStatus(`Analysis ready${suffix}`, payload.diagnostics?.length ? "error" : "ready");
  } catch (error) {
    if (sequence !== state.requestSequence) return;
    setStatus(`Analysis failed: ${error.message}`, "error");
    ui.output.replaceChildren(emptyMessage("The local analyzer did not return a result."));
  } finally {
    if (sequence === state.requestSequence) ui.output.removeAttribute("aria-busy");
  }
}

function chooseStage(stage) {
  if (!stages.includes(stage)) return;
  state.stage = stage;
  ui.traceStage.textContent = stage === "ssa" ? "SSA + CFG" : stage;
  for (const button of ui.tabs.querySelectorAll("button[data-stage]")) {
    button.setAttribute("aria-selected", button.dataset.stage === stage ? "true" : "false");
  }
  for (const item of document.querySelectorAll("[data-pipeline]")) item.classList.toggle("is-active", item.dataset.pipeline === stage);
  renderStage();
}

function renderStage() {
  if (!state.result) {
    ui.output.replaceChildren(emptyMessage("Waiting for the first analysis."));
    return;
  }
  const onSelect = (range, astID = -1) => selectRange(range, astID);
  if (state.stage === "tokens") renderTokens(ui.output, state.result, state.selectedRange, onSelect);
  if (state.stage === "ast") renderAST(ui.output, state.result, state.selectedAST, onSelect);
  if (state.stage === "types") renderTypes(ui.output, state.result, state.selectedAST, onSelect);
  if (state.stage === "ssa") {
    renderSSA(ui.output, state.result, state.selectedAST, onSelect, state.selectedFunction, (name) => {
      state.selectedFunction = name;
      renderStage();
    });
  }
}

function selectRange(range, astID) {
  state.selectedRange = range;
  state.selectedAST = astID;
  paintSource();
  if (range?.line) ui.selection.textContent = `Trace cursor → line ${range.line}:${range.column}, bytes ${range.start}–${range.end}`;
  else ui.selection.textContent = "This item was generated during lowering and has no exact source span.";
  renderStage();
}

function paintSource() {
  const source = ui.editor.value;
  ui.mirror.replaceChildren();
  const range = state.selectedRange;
  if (!range || range.end <= range.start || range.start < 0 || range.end > source.length) {
    ui.mirror.append(document.createTextNode(source + (source.endsWith("\n") ? " " : "\n ")));
    return;
  }
  ui.mirror.append(document.createTextNode(source.slice(0, range.start)));
  const mark = document.createElement("mark");
  mark.textContent = source.slice(range.start, range.end);
  ui.mirror.append(mark, document.createTextNode(source.slice(range.end) + (source.endsWith("\n") ? " " : "\n ")));
}

function renderQuiz() {
  ui.quiz.replaceChildren();
  if (!state.lesson || state.freeLab) return;
  const quiz = state.lesson.quiz;
  const heading = document.createElement("div");
  heading.className = "quiz-heading";
  const text = document.createElement("div");
  const kicker = document.createElement("p");
  kicker.className = "eyebrow";
  kicker.textContent = "Prediction checkpoint";
  const title = document.createElement("h2");
  title.id = "quiz-heading";
  title.textContent = quiz.prompt;
  text.append(kicker, title);
  const attempts = document.createElement("span");
  attempts.className = "attempt-count";
  const count = state.progress.attempts[state.lesson.id] || 0;
  attempts.textContent = `${count} attempt${count === 1 ? "" : "s"}`;
  heading.append(text, attempts);

  const fieldset = document.createElement("fieldset");
  fieldset.className = "quiz-options";
  fieldset.disabled = state.quizState.locked;
  const legend = document.createElement("legend");
  legend.className = "sr-only";
  legend.textContent = quiz.prompt;
  fieldset.append(legend);
  quiz.options.forEach((option, index) => {
    const label = document.createElement("label");
    label.className = "quiz-option";
    if (state.quizState.locked && index === quiz.answer) label.classList.add("is-answer");
    const input = document.createElement("input");
    input.type = "radio";
    input.name = `quiz-${state.lesson.id}`;
    input.value = String(index);
    input.checked = state.quizState.selected === index;
    input.addEventListener("change", () => { state.quizState.selected = index; });
    const marker = span("option-marker", String.fromCharCode(65 + index));
    label.append(input, marker, document.createTextNode(option));
    fieldset.append(label);
  });

  const actions = document.createElement("div");
  actions.className = "quiz-actions";
  const check = document.createElement("button");
  check.type = "button";
  check.className = "primary-button";
  check.textContent = state.quizState.correct ? "Prediction confirmed" : "Check prediction";
  check.disabled = state.quizState.locked;
  check.addEventListener("click", checkPrediction);
  const reveal = document.createElement("button");
  reveal.type = "button";
  reveal.className = "secondary-button";
  reveal.textContent = "Show explanation";
  reveal.disabled = state.quizState.locked;
  reveal.addEventListener("click", revealPrediction);
  actions.append(check, reveal);

  const feedback = document.createElement("p");
  feedback.className = state.quizState.correct ? "quiz-feedback is-correct" : "quiz-feedback";
  feedback.setAttribute("role", "status");
  feedback.textContent = state.quizState.feedback || "Choose first. Prediction makes the transformation easier to remember.";
  ui.quiz.append(heading, fieldset, actions, feedback);
}

function checkPrediction() {
  if (!state.lesson || state.quizState.selected < 0) {
    state.quizState.feedback = "Choose one answer before checking the prediction.";
    renderQuiz();
    return;
  }
  const grade = gradePrediction(state.lesson.quiz, state.quizState.selected);
  state.progress = recordAttempt(state.progress, state.lesson.id, grade.correct);
  state.quizState.feedback = grade.feedback;
  state.quizState.correct = grade.correct;
  state.quizState.locked = grade.correct;
  saveProgress();
  renderLessonRail();
  renderQuiz();
}

function revealPrediction() {
  if (!state.lesson) return;
  state.quizState.selected = state.lesson.quiz.answer;
  state.quizState.feedback = `${state.lesson.quiz.correct} The answer is now revealed; reset the lesson to retry from a blank checkpoint.`;
  state.quizState.locked = true;
  renderQuiz();
}

function resetLesson() {
  if (!state.lesson) return;
  state.progress = {
    version: 1,
    completed: state.progress.completed.filter((id) => id !== state.lesson.id),
    attempts: { ...state.progress.attempts },
  };
  delete state.progress.attempts[state.lesson.id];
  state.quizState = { selected: -1, feedback: "", locked: false, correct: false };
  saveProgress();
  renderLessonRail();
  renderQuiz();
  setSource(state.lesson.source);
}

function saveProgress() {
  localStorage.setItem(STORAGE_KEY, JSON.stringify(state.progress));
}

function setStatus(message, mode) {
  ui.status.textContent = message;
  ui.status.className = `analysis-status is-${mode}`;
}

function loadProgress() {
  try {
    const parsed = JSON.parse(localStorage.getItem(STORAGE_KEY));
    if (parsed?.version === 1 && Array.isArray(parsed.completed)) return parsed;
  } catch (_) {
    // Ignore malformed local-only progress and start cleanly.
  }
  return { version: 1, completed: [], attempts: {} };
}

function emptyMessage(text) {
  const node = document.createElement("div");
  node.className = "empty-state";
  node.textContent = text;
  return node;
}

function span(className, text) {
  const node = document.createElement("span");
  node.className = className;
  node.textContent = text;
  return node;
}
